// Remote caller for a dopdb HTTP server. This is the front-end side: it does not
// use WASM, it just POSTs to the server's /api/<name> route.
//
//   import { configure, createApi } from "dopdb-client";
//   configure({ baseUrl: "https://api.example.com", getToken: () => localStorage.token });
//
//   interface GreetIn { name: string }
//   interface GreetOut { msg: string }
//   const greet = createApi<GreetIn, GreetOut>("greet");
//   const out = await greet({ name: "Ada" });   // POST {baseUrl}/api/greet

export type TokenGetter = () => string | null | undefined | Promise<string | null | undefined>;

/** Per-request options for the remote caller. */
export class RequestOptions {
  /** Server origin; "" means same origin as the page. Trailing slashes trimmed. */
  baseUrl = "";
  /** Extra query-string params (e.g. { ds: "default" }). */
  params: Record<string, string> = {};
  /** Extra HTTP headers. */
  headers: Record<string, string> = {};
  /** Returns the bearer token (static or dynamic, sync or async). */
  getToken: TokenGetter = () => null;

  /** Return a shallow clone with overrides applied (does not mutate this). */
  with(patch: Partial<RequestOptions>): RequestOptions {
    const o = new RequestOptions();
    o.baseUrl = (patch.baseUrl ?? this.baseUrl).replace(/\/+$/, "");
    o.params = { ...this.params, ...(patch.params || {}) };
    o.headers = { ...this.headers, ...(patch.headers || {}) };
    o.getToken = patch.getToken ?? this.getToken;
    return o;
  }

  /** Select the server data source / database (?ds=). */
  withDataSource(name: string): RequestOptions {
    return this.with({ params: { ds: name } });
  }
}

/** The shared default options, mutated by configure(). */
export const Opt = new RequestOptions();

/** Set global defaults for the remote caller. */
export function configure(opts: {
  baseUrl?: string;
  token?: string | null;
  getToken?: TokenGetter;
  headers?: Record<string, string>;
}): void {
  if (opts.baseUrl !== undefined) Opt.baseUrl = opts.baseUrl.replace(/\/+$/, "");
  if (opts.headers) Opt.headers = { ...Opt.headers, ...opts.headers };
  if (opts.getToken !== undefined) {
    Opt.getToken = opts.getToken;
  } else if (opts.token !== undefined) {
    const t = opts.token;
    Opt.getToken = () => t;
  }
}

function buildUrl(opt: RequestOptions, path: string): string {
  const qs = new URLSearchParams(opt.params).toString();
  return `${opt.baseUrl}${path}${qs ? "?" + qs : ""}`;
}

async function authHeaders(opt: RequestOptions): Promise<Record<string, string>> {
  const h: Record<string, string> = { ...opt.headers };
  const token = await opt.getToken();
  if (token) h["Authorization"] = token.startsWith("Bearer ") ? token : `Bearer ${token}`;
  return h;
}

async function parse(res: Response): Promise<any> {
  const ct = res.headers.get("content-type") || "";
  const value = ct.includes("application/json") ? await res.json() : await res.text();
  if (!res.ok) {
    const msg = typeof value === "object" && value && "error" in value ? (value as any).error : value;
    throw new Error(`dopdb: ${res.status} ${res.statusText}: ${msg}`);
  }
  return value;
}

/**
 * Create a typed remote caller for the endpoint served at /api/<name>. The
 * returned function POSTs its argument as JSON and resolves the JSON response.
 */
export function createApi<I = any, O = any>(name: string, options: RequestOptions = Opt) {
  if (!name) throw new Error("dopdb: API name cannot be empty");
  return async (data: I = {} as I, opt: RequestOptions = options): Promise<O> => {
    const headers = await authHeaders(opt);
    headers["Content-Type"] = "application/json";
    const res = await fetch(buildUrl(opt, `/api/${encodeURIComponent(name)}`), {
      method: "POST",
      headers,
      body: JSON.stringify(data),
    });
    return (await parse(res)) as O;
  };
}

// ----------------------------------------------------------------------------
// Data-command client — talks to /CMD-<coll> routes (HGET, HSET, FIND, …).
// ----------------------------------------------------------------------------

/** A client for dopdb data commands on one collection. */
export class DataClient {
  readonly coll: string;
  readonly opt: RequestOptions;

  constructor(coll: string, opt: RequestOptions) {
    this.coll = coll;
    this.opt = opt;
  }

  /** HGET-<coll>?f=<field> */
  async hget(field: string): Promise<unknown> {
    const headers = await authHeaders(this.opt);
    const res = await fetch(buildUrl(this.opt, `/HGET-${encodeURIComponent(this.coll)}?f=${encodeURIComponent(field)}`), {
      method: "GET",
      headers,
    });
    return parse(res);
  }

  /** HSET-<coll>?f=<field> body JSON */
  async hset(field: string, value: unknown): Promise<void> {
    const headers = await authHeaders(this.opt);
    headers["Content-Type"] = "application/json";
    const res = await fetch(buildUrl(this.opt, `/HSET-${encodeURIComponent(this.coll)}?f=${encodeURIComponent(field)}`), {
      method: "POST",
      headers,
      body: JSON.stringify(value),
    });
    await parse(res);
  }

  /** HSETNX-<coll>?f=<field> body JSON */
  async hsetnx(field: string, value: unknown): Promise<unknown> {
    const headers = await authHeaders(this.opt);
    headers["Content-Type"] = "application/json";
    const res = await fetch(buildUrl(this.opt, `/HSETNX-${encodeURIComponent(this.coll)}?f=${encodeURIComponent(field)}`), {
      method: "POST",
      headers,
      body: JSON.stringify(value),
    });
    return parse(res);
  }

  /** HDEL-<coll>?f=<field> */
  async hdel(field: string): Promise<void> {
    const headers = await authHeaders(this.opt);
    headers["Content-Type"] = "application/json";
    const res = await fetch(buildUrl(this.opt, `/HDEL-${encodeURIComponent(this.coll)}?f=${encodeURIComponent(field)}`), {
      method: "POST",
      headers,
    });
    await parse(res);
  }

  /** HEXISTS-<coll>?f=<field> */
  async hexists(field: string): Promise<unknown> {
    const headers = await authHeaders(this.opt);
    const res = await fetch(buildUrl(this.opt, `/HEXISTS-${encodeURIComponent(this.coll)}?f=${encodeURIComponent(field)}`), {
      method: "GET",
      headers,
    });
    return parse(res);
  }

  /** HGETALL-<coll> */
  async hgetall(): Promise<unknown> {
    const headers = await authHeaders(this.opt);
    const res = await fetch(buildUrl(this.opt, `/HGETALL-${encodeURIComponent(this.coll)}`), {
      method: "GET",
      headers,
    });
    return parse(res);
  }

  /** HKEYS-<coll> */
  async hkeys(): Promise<unknown> {
    const headers = await authHeaders(this.opt);
    const res = await fetch(buildUrl(this.opt, `/HKEYS-${encodeURIComponent(this.coll)}`), {
      method: "GET",
      headers,
    });
    return parse(res);
  }

  /** HLEN-<coll> */
  async hlen(): Promise<unknown> {
    const headers = await authHeaders(this.opt);
    const res = await fetch(buildUrl(this.opt, `/HLEN-${encodeURIComponent(this.coll)}`), {
      method: "GET",
      headers,
    });
    return parse(res);
  }

  /** FIND-<coll> body JSON(filter) */
  async find(filter: unknown): Promise<unknown> {
    const headers = await authHeaders(this.opt);
    headers["Content-Type"] = "application/json";
    const res = await fetch(buildUrl(this.opt, `/FIND-${encodeURIComponent(this.coll)}`), {
      method: "POST",
      headers,
      body: JSON.stringify(filter),
    });
    return parse(res);
  }
}

/** Create a data-command client for the named collection. */
export function collection(coll: string, options: RequestOptions = Opt): DataClient {
  return new DataClient(coll, options);
}
