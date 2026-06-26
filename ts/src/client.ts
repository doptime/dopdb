// client.ts — the browser half. No WASM. Builds a typed `db` proxy from the one
// schema and a typed API caller from the server's api type. All it does under
// the hood is fetch.
//
//   import { db as schema } from "./shared/schema";          // the single source
//   import { clientDb } from "dopdb/client";
//   const db = clientDb(schema, { baseUrl: "https://api.example.com",
//                                 getToken: () => localStorage.token });
//   const u = await db.User.hget("u1");        // u: User | null   (typed)
//   await db.Order.hset(oid, { total: 12 });   // owner is @-bound server-side
//
//   import type { api } from "../server/api";  // type-only: handlers never ship
//   import { apiClient } from "dopdb/client";
//   const call = apiClient<typeof api>({ baseUrl });
//   const out = await call("greet", { name: "Ada" });   // typed in & out

import { type Collection, type Infer, type InferInput } from "./schema.js";
import { sanitizeFilter, type Filter } from "./sanitize.js";
import { errorFromResponse } from "./errors.js";
import { type Endpoint, type ApiMap } from "./api.js";

export type TokenGetter = () => string | null | undefined | Promise<string | null | undefined>;

export interface ClientOptions {
  /** Server origin; "" means same origin. Trailing slashes are trimmed. */
  baseUrl?: string;
  /** Bearer token provider (static or dynamic, sync or async). */
  getToken?: TokenGetter;
  /** Extra headers merged into every request. */
  headers?: Record<string, string>;
  /** Route prefix the server is mounted at (default "/api"). Set this to match a
   * server mounted at a different path, e.g. a Next.js route at app/db/[...slug]. */
  apiBase?: string;
}

export interface FindOpt {
  limit?: number;
  skip?: number;
  sort?: Record<string, 1 | -1>;
  projection?: Record<string, 0 | 1>;
}

export type WatchOp = "insert" | "update" | "replace" | "delete";

/** A change event delivered to a `watch` handler. For deletes `doc` is null;
 * for owner-scoped collections deletes are not delivered (no fullDocument to
 * scope on) — see the README. */
export interface WatchEvent<T> {
  type: WatchOp;
  key: string;
  doc: T | null;
}

export type WatchHandler<T> = (event: WatchEvent<T>) => void;
export type Unsubscribe = () => void;

export interface WatchOptions {
  /** Abort the subscription externally (in addition to the returned unsub). */
  signal?: AbortSignal;
  /** Auto-reconnect on disconnect, resuming from the last change (default true).
   * Permanent failures (401/403/404) stop the loop regardless. */
  reconnect?: boolean;
  /** Reconnect backoff. Defaults: base 500ms, max 30s, with jitter. */
  backoff?: { baseMs?: number; maxMs?: number };
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => {
      clearTimeout(t);
      resolve();
    }, { once: true });
  });
}

function norm(o: ClientOptions): Required<Omit<ClientOptions, "getToken">> & { getToken: TokenGetter } {
  return {
    baseUrl: (o.baseUrl ?? "").replace(/\/+$/, ""),
    getToken: o.getToken ?? (() => null),
    headers: o.headers ?? {},
    apiBase: (o.apiBase ?? "/api").replace(/\/+$/, ""),
  };
}

async function authHeaders(o: ReturnType<typeof norm>, json: boolean): Promise<Record<string, string>> {
  const h: Record<string, string> = { ...o.headers };
  if (json) h["Content-Type"] = "application/json";
  const t = await o.getToken();
  if (t) h["Authorization"] = t.startsWith("Bearer ") ? t : `Bearer ${t}`;
  return h;
}

async function parse(res: Response): Promise<unknown> {
  const ct = res.headers.get("content-type") || "";
  const body = ct.includes("application/json") ? await res.json() : await res.text();
  if (!res.ok) throw errorFromResponse(res.status, body);
  return body;
}

// Data commands live at /api/<cmd>/<coll>. The datasource is selected with the
// ?ds=<name> query parameter (never a path segment). The closed command
// vocabulary keeps this unambiguous against the function-API route /api/<name>.
function cmdPath(apiBase: string, cmd: string, coll: string): string {
  return `${apiBase}/${cmd}/${encodeURIComponent(coll)}`;
}

/** The typed surface of one collection on the client. Method names stay
 * redisdb-compatible (point 3 / point 8); `get`/`set` are convenience aliases. */
export interface DbApi<C> {
  hget(key: string): Promise<Infer<C> | null>;
  hset(key: string, value: InferInput<C>): Promise<void>;
  hsetnx(key: string, value: InferInput<C>): Promise<boolean>;
  hdel(...keys: string[]): Promise<void>;
  /** Delete one or more records by key (DEL; same effect as hdel). */
  del(...keys: string[]): Promise<void>;
  hexists(key: string): Promise<boolean>;
  hgetall(): Promise<Record<string, Infer<C>>>;
  hkeys(): Promise<string[]>;
  hvals(): Promise<Infer<C>[]>;
  hlen(): Promise<number>;
  hincrby(key: string, field: string, n: number): Promise<void>;
  /** Atomically add a floating-point amount to a numeric field. */
  hincrbyfloat(key: string, field: string, n: number): Promise<void>;
  /** Set many key→value pairs in one round-trip (point 13: hmset, not batch). */
  hmset(entries: Record<string, InferInput<C>>): Promise<void>;
  /** Get many keys at once; result is aligned to the input order (null = miss). */
  hmget(keys: string[]): Promise<(Infer<C> | null)[]>;
  /** Count documents matching a filter (owner-scoped on the server). */
  count(filter?: Filter): Promise<number>;
  find(filter?: Filter, opt?: FindOpt): Promise<Infer<C>[]>;
  findone(filter?: Filter): Promise<Infer<C> | null>;
  /** Subscribe to live changes (point 5: Mongo change streams). Owner-scoped on
   * the server. Returns an unsubscribe function. */
  watch(onEvent: WatchHandler<Infer<C>>, opts?: WatchOptions): Promise<Unsubscribe>;
  /** alias of hget */
  get(key: string): Promise<Infer<C> | null>;
  /** alias of hset */
  set(key: string, value: InferInput<C>): Promise<void>;
  /** Upsert a value, deriving the key from its `_id` field (doptime Save). */
  save(value: InferInput<C>): Promise<void>;
}

export type Db<M extends Record<string, Collection<any>>> = { [K in keyof M]: DbApi<M[K]> };

function makeDbApi<C extends Collection<any>>(key: string, c: C, o: ReturnType<typeof norm>): DbApi<C> {
  const coll = c.opts.name ?? key; // .named() renames the public API name + storage
  const db = c.opts.db ?? "default";
  const dsq = db && db !== "default" ? `ds=${encodeURIComponent(db)}` : "";
  const url = (cmd: string, qs = "") => {
    const full = [dsq, qs].filter(Boolean).join("&");
    return o.baseUrl + cmdPath(o.apiBase, cmd, coll) + (full ? `?${full}` : "");
  };
  const q = (params: Record<string, string>) => new URLSearchParams(params).toString();

  const getJSON = async (cmd: string, qs = "") => {
    const res = await fetch(url(cmd, qs), { method: "GET", headers: await authHeaders(o, false) });
    return parse(res);
  };
  const postJSON = async (cmd: string, qs: string, body: unknown) => {
    const res = await fetch(url(cmd, qs), {
      method: "POST",
      headers: await authHeaders(o, true),
      body: JSON.stringify(body ?? {}),
    });
    return parse(res);
  };

  const hget: DbApi<C>["hget"] = async (key) => {
    try {
      return (await getJSON("hget", q({ f: key }))) as Infer<C>;
    } catch (e: any) {
      if (e?.status === 404) return null;
      throw e;
    }
  };
  const hset: DbApi<C>["hset"] = async (key, value) => {
    await postJSON("hset", q({ f: key }), value);
  };

  return {
    hget,
    hset,
    get: hget,
    set: hset,
    hsetnx: async (key, value) => {
      const r = (await postJSON("hsetnx", q({ f: key }), value)) as { inserted?: boolean };
      return !!r?.inserted;
    },
    hdel: async (...keys) => {
      await postJSON("hdel", keys.map((k) => `f=${encodeURIComponent(k)}`).join("&"), {});
    },
    del: async (...keys) => {
      if (keys.length === 0) return;
      await postJSON("del", keys.map((k) => `f=${encodeURIComponent(k)}`).join("&"), {});
    },
    save: async (value) => {
      const k = (value as Record<string, unknown>)._id;
      if (k === undefined || k === null) throw new Error("save: value has no _id");
      await postJSON("hset", q({ f: String(k) }), value);
    },
    hexists: async (key) => {
      const r = (await getJSON("hexists", q({ f: key }))) as { exists?: boolean };
      return !!r?.exists;
    },
    hgetall: async () => (await getJSON("hgetall")) as Record<string, Infer<C>>,
    hkeys: async () => (await getJSON("hkeys")) as string[],
    hvals: async () => (await getJSON("hvals")) as Infer<C>[],
    hlen: async () => {
      const r = (await getJSON("hlen")) as { len?: number };
      return r?.len ?? 0;
    },
    hincrby: async (key, field, n) => {
      await postJSON("hincrby", q({ f: key, field, n: String(n) }), {});
    },
    hincrbyfloat: async (key, field, n) => {
      await postJSON("hincrbyfloat", q({ f: key, field, n: String(n) }), {});
    },
    hmset: async (entries) => {
      await postJSON("hmset", "", entries);
    },
    hmget: async (keys) => {
      if (keys.length === 0) return [];
      const qs = keys.map((k) => `f=${encodeURIComponent(k)}`).join("&");
      return (await getJSON("hmget", qs)) as (Infer<C> | null)[];
    },
    count: async (filter = {}) => {
      sanitizeFilter(filter);
      const r = (await postJSON("count", "", filter)) as { count?: number };
      return r?.count ?? 0;
    },
    find: async (filter = {}, opt = {}) => {
      sanitizeFilter(filter); // fail fast before the round-trip
      const params: Record<string, string> = {};
      if (opt.limit != null) params.limit = String(opt.limit);
      if (opt.skip != null) params.skip = String(opt.skip);
      if (opt.sort) params.s = JSON.stringify(opt.sort);
      if (opt.projection) params.p = JSON.stringify(opt.projection);
      return (await postJSON("find", q(params), filter)) as Infer<C>[];
    },
    findone: async (filter = {}) => {
      sanitizeFilter(filter);
      try {
        return (await postJSON("findone", "", filter)) as Infer<C>;
      } catch (e: any) {
        if (e?.status === 404) return null;
        throw e;
      }
    },
    watch: async (onEvent, opts) => {
      const ctrl = new AbortController();
      if (opts?.signal) opts.signal.addEventListener("abort", () => ctrl.abort(), { once: true });
      const reconnect = opts?.reconnect !== false;
      const baseMs = opts?.backoff?.baseMs ?? 500;
      const maxMs = opts?.backoff?.maxMs ?? 30000;

      let lastId: string | null = null;
      let firstResolve: (() => void) | null = null;
      let firstReject: ((e: unknown) => void) | null = null;
      const first = new Promise<void>((res, rej) => {
        firstResolve = res;
        firstReject = rej;
      });
      const settleOpen = () => {
        firstResolve?.();
        firstResolve = null;
        firstReject = null;
      };
      const failOpen = (e: unknown) => {
        firstReject?.(e);
        firstResolve = null;
        firstReject = null;
      };

      const handleFrame = (frame: string): void => {
        let event = "message";
        let data = "";
        for (const line of frame.split("\n")) {
          if (line.startsWith("id:")) lastId = line.slice(3).trim();
          else if (line.startsWith("event:")) event = line.slice(6).trim();
          else if (line.startsWith("data:")) data += line.slice(5).trim();
        }
        if (event === "error") {
          lastId = null; // resume token rejected by the server → reconnect fresh
          return;
        }
        if (!data) return;
        try {
          onEvent(JSON.parse(data) as WatchEvent<Infer<C>>);
        } catch {
          /* ignore malformed frame */
        }
      };

      let attempt = 0;
      void (async () => {
        while (!ctrl.signal.aborted) {
          let connected = false;
          try {
            const headers = await authHeaders(o, false);
            if (lastId) headers["Last-Event-ID"] = lastId;
            const res = await fetch(url("watch"), { method: "GET", headers, signal: ctrl.signal });
            if (res.status === 401 || res.status === 403 || res.status === 404) {
              const ct = res.headers.get("content-type") || "";
              failOpen(errorFromResponse(res.status, ct.includes("json") ? await res.json() : await res.text()));
              return; // permanent
            }
            if (!res.ok || !res.body) throw new Error(`watch failed: ${res.status}`);
            connected = true;
            attempt = 0;
            settleOpen();
            const reader = res.body.getReader();
            const dec = new TextDecoder();
            let buf = "";
            for (;;) {
              const { done, value } = await reader.read();
              if (done) break;
              buf += dec.decode(value, { stream: true });
              let i: number;
              while ((i = buf.indexOf("\n\n")) >= 0) {
                handleFrame(buf.slice(0, i));
                buf = buf.slice(i + 2);
              }
            }
          } catch {
            /* network error or aborted */
          }
          if (ctrl.signal.aborted || !reconnect) break;
          attempt = connected ? 1 : attempt + 1;
          const delay = Math.min(maxMs, baseMs * 2 ** Math.min(attempt, 6)) + Math.random() * 200;
          await sleep(delay, ctrl.signal);
        }
        // if we never connected and aren't retrying, unblock the awaiter
        settleOpen();
      })();

      await first;
      return () => ctrl.abort();
    },
  };
}

/** Build a typed db client from the one schema map. The map keys are the
 * collection names; nothing is generated or redefined. */
export function clientDb<M extends Record<string, Collection<any>>>(schema: M, options: ClientOptions = {}): Db<M> {
  const o = norm(options);
  const out = {} as Db<M>;
  for (const key of Object.keys(schema) as (keyof M & string)[]) {
    (out as any)[key] = makeDbApi(key, schema[key], o);
  }
  return out;
}

// ----------------------------------------------------------------------------
// Typed API caller (tRPC-style) — uses the server's `api` type, no codegen
// ----------------------------------------------------------------------------

type In<E> = E extends Endpoint<infer I, any> ? I : never;
type Out<E> = E extends Endpoint<any, infer O> ? O : never;

export type ApiClient<A extends ApiMap> = <K extends keyof A & string>(name: K, input: In<A[K]>) => Promise<Out<A[K]>>;

/** Build a typed caller for a server's api object: `apiClient<typeof api>({...})`.
 * Import the api type with `import type` so handler code is never bundled. */
export function apiClient<A extends ApiMap>(options: ClientOptions = {}): ApiClient<A> {
  const o = norm(options);
  return async (name, input) => {
    const res = await fetch(o.baseUrl + `${o.apiBase}/${encodeURIComponent(name)}`, {
      method: "POST",
      headers: await authHeaders(o, true),
      body: JSON.stringify(input ?? {}),
    });
    return (await parse(res)) as any;
  };
}
