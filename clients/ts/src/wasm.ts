// dopdb WASM runtime for TypeScript / JavaScript.
//
// The dopdb api core is compiled to WebAssembly (from Go) and loaded here. You
// register an API by passing a typed function — its name (or fn.name) becomes
// the route /api/<name>:
//
//   import { loadDopdb } from "dopdb-client";
//   const db = await loadDopdb();
//
//   interface GreetIn { name: string }
//   interface GreetOut { msg: string }
//
//   const greet = db.api((input: GreetIn): GreetOut => ({ msg: "hi " + input.name }));
//   await greet({ name: "Ada" });            // -> { msg: "hi Ada" }  (in-process)
//
//   // serve every registered endpoint at /api/<name>:
//   import { createServer } from "node:http";
//   createServer(db.nodeListener).listen(8080);
//
// Both synchronous and async (Promise-returning) handlers are supported.

/** The raw object the WASM module installs on globalThis as `dopdb`. */
interface DopdbRaw {
  createApi(name: string, handler: (input: any) => any): string | Error;
  callApi(name: string, input?: any): Promise<any>;
  removeApi(name: string): void;
  apiNames(): string[];
  sanitizeFilter(filter: any): any | Error;
  version: string;
}

/** A handler is a (possibly async) function from a typed input to a typed output. */
export type ApiHandler<I = any, O = any> = (input: I) => O | Promise<O>;

/** The value returned by `db.api(...)`: a typed in-process caller carrying its route name. */
export type TypedCaller<I = any, O = any> = ((input: I) => Promise<O>) & {
  /** The registered route name (the `<name>` in /api/<name>). */
  readonly apiName: string;
};

export interface LoadOptions {
  /** Browser: URL to dopdb.wasm. Node: defaults to the bundled file. */
  wasmUrl?: string;
  /** Browser: URL to Go's wasm_exec.js. Node: defaults to the bundled file. */
  wasmExecUrl?: string;
  /** Provide the wasm bytes directly (any environment). */
  wasmBytes?: ArrayBuffer | Uint8Array;
  /** Force re-instantiation even if already loaded. */
  reload?: boolean;
}

function isNode(): boolean {
  return typeof process !== "undefined" && !!(process as any).versions?.node;
}

function errMsg(e: any): string {
  return String(e && e.message ? e.message : e);
}

let _instance: Dopdb | null = null;

/**
 * Load (once) the dopdb WASM engine and return a Dopdb runtime. Subsequent calls
 * return the same instance unless `{ reload: true }` is passed.
 */
export async function loadDopdb(opts: LoadOptions = {}): Promise<Dopdb> {
  if (_instance && !opts.reload) return _instance;
  await ensureGo(opts);
  const Go = (globalThis as any).Go;
  const go = new Go();
  const bytes = await resolveWasmBytes(opts);
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);

  // main() sets globalThis.dopdb, calls __dopdbReady, then blocks on select{}.
  const ready = new Promise<void>((resolve) => {
    (globalThis as any).__dopdbReady = resolve;
  });
  void go.run(instance); // fire-and-forget; do not await (main never returns)
  await ready;

  const raw = (globalThis as any).dopdb as DopdbRaw | undefined;
  if (!raw) throw new Error("dopdb: WASM module did not install the global `dopdb`");
  _instance = new Dopdb(raw);
  return _instance;
}

async function ensureGo(opts: LoadOptions): Promise<void> {
  if (typeof (globalThis as any).Go !== "undefined") return;
  if (isNode()) {
    const url = opts.wasmExecUrl ?? new URL("../wasm/wasm_exec.js", import.meta.url).href;
    await import(/* @vite-ignore */ url);
  } else {
    if (!opts.wasmExecUrl) {
      throw new Error("dopdb: in the browser, pass wasmExecUrl (URL of Go's wasm_exec.js)");
    }
    await loadBrowserScript(opts.wasmExecUrl);
  }
  if (typeof (globalThis as any).Go === "undefined") {
    throw new Error("dopdb: wasm_exec.js did not define globalThis.Go");
  }
}

function loadBrowserScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const doc: any = (globalThis as any).document;
    if (!doc) {
      reject(new Error("dopdb: no document to inject wasm_exec.js; provide globalThis.Go yourself"));
      return;
    }
    const s = doc.createElement("script");
    s.src = src;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error("dopdb: failed to load wasm_exec.js from " + src));
    doc.head.appendChild(s);
  });
}

async function resolveWasmBytes(opts: LoadOptions): Promise<BufferSource> {
  if (opts.wasmBytes) return opts.wasmBytes as BufferSource;
  const url = opts.wasmUrl ?? new URL("../wasm/dopdb.wasm", import.meta.url).href;
  if (isNode() && url.startsWith("file:")) {
    const { readFile } = await import("node:fs/promises");
    const { fileURLToPath } = await import("node:url");
    return await readFile(fileURLToPath(url)); // Buffer is a Uint8Array
  }
  const res = await fetch(url);
  return await res.arrayBuffer();
}

/** The dopdb runtime: register typed endpoints and serve them at /api/<name>. */
export class Dopdb {
  constructor(private raw: DopdbRaw) {}

  /** WASM build version string. */
  get version(): string {
    return this.raw.version;
  }

  /** Names of all registered endpoints (the route keys). */
  names(): string[] {
    return this.raw.apiNames();
  }

  /**
   * Register a typed handler as an API. The route is /api/<name>, where name is
   * the explicit string or the function's name (the "type name"). Returns a
   * typed in-process caller carrying `.apiName`.
   */
  api<I = any, O = any>(handler: ApiHandler<I, O>): TypedCaller<I, O>;
  api<I = any, O = any>(name: string, handler: ApiHandler<I, O>): TypedCaller<I, O>;
  api<I = any, O = any>(a: string | ApiHandler<I, O>, b?: ApiHandler<I, O>): TypedCaller<I, O> {
    let name: string;
    let fn: ApiHandler<I, O>;
    if (typeof a === "string") {
      name = a;
      fn = b as ApiHandler<I, O>;
    } else {
      fn = a;
      name = a.name; // function name = the "type name"
    }
    if (!name) {
      throw new Error(
        "dopdb: cannot infer an API name from an anonymous function — use db.api('name', fn)"
      );
    }
    if (typeof fn !== "function") {
      throw new Error("dopdb: db.api requires a handler function");
    }
    const res = this.raw.createApi(name, fn as (input: any) => any);
    if (res instanceof Error) throw res;
    const registered = res as string;

    const caller = ((input: I) => this.call<O>(registered, input)) as TypedCaller<I, O>;
    Object.defineProperty(caller, "apiName", { value: registered, enumerable: true });
    return caller;
  }

  /** Invoke a registered endpoint by name (runs the pipeline; resolves the output). */
  call<O = any>(name: string, input?: any): Promise<O> {
    return this.raw.callApi(name, input) as Promise<O>;
  }

  /** Validate a Mongo-style query filter against the operator allowlist. Throws on a forbidden op. */
  sanitizeFilter<T = any>(filter: T): T {
    const r = this.raw.sanitizeFilter(filter);
    if (r instanceof Error) throw r;
    return r as T;
  }

  /** Unregister an endpoint (e.g. for hot reload). */
  remove(name: string): void {
    this.raw.removeApi(name);
  }

  // --------------------------------------------------------------------------
  // server adapters — serve registered endpoints at /api/<name> using this
  // in-wasm engine. Query string and JSON body are merged into the input.
  // (JWT @-binding, permissions and data commands live in the Go httpserve.)
  // --------------------------------------------------------------------------

  /** Node http(s) request listener: `createServer(db.nodeListener)`. */
  nodeListener = async (req: any, res: any): Promise<void> => {
    try {
      const host = (req.headers && req.headers.host) || "localhost";
      const url = new URL(req.url || "/", `http://${host}`);
      const name = matchApiName(url.pathname);
      if (name === null) {
        res.statusCode = 404;
        res.end("dopdb: not an /api/<name> route");
        return;
      }
      const input: Record<string, any> = {};
      url.searchParams.forEach((v, k) => {
        input[k] = v;
      });
      const method = (req.method || "GET").toUpperCase();
      if (method !== "GET" && method !== "HEAD") {
        const body = await readNodeBody(req);
        if (body) {
          try {
            Object.assign(input, JSON.parse(body));
          } catch {
            res.statusCode = 400;
            res.end(JSON.stringify({ error: "invalid JSON body" }));
            return;
          }
        }
      }
      const out = await this.call(name, input);
      res.statusCode = 200;
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify(out ?? null));
    } catch (e: any) {
      res.statusCode = 400;
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ error: errMsg(e) }));
    }
  };

  /** Fetch-style handler for Deno / Bun / Cloudflare Workers: `serve(db.fetchHandler)`. */
  fetchHandler = async (request: Request): Promise<Response> => {
    const url = new URL(request.url);
    const name = matchApiName(url.pathname);
    if (name === null) {
      return new Response("dopdb: not an /api/<name> route", { status: 404 });
    }
    const input: Record<string, any> = {};
    url.searchParams.forEach((v, k) => {
      input[k] = v;
    });
    if (request.method !== "GET" && request.method !== "HEAD") {
      const text = await request.text();
      if (text) {
        try {
          Object.assign(input, JSON.parse(text));
        } catch {
          return new Response(JSON.stringify({ error: "invalid JSON body" }), {
            status: 400,
            headers: { "Content-Type": "application/json" },
          });
        }
      }
    }
    try {
      const out = await this.call(name, input);
      return new Response(JSON.stringify(out ?? null), {
        headers: { "Content-Type": "application/json" },
      });
    } catch (e: any) {
      return new Response(JSON.stringify({ error: errMsg(e) }), {
        status: 400,
        headers: { "Content-Type": "application/json" },
      });
    }
  };
}

/** Return the <name> in a /api/<name> path, or null if the path is not an API route. */
function matchApiName(pathname: string): string | null {
  const i = pathname.indexOf("/api/");
  if (i < 0) return null;
  let name = pathname.slice(i + "/api/".length).replace(/\/+$/, "");
  try {
    name = decodeURIComponent(name);
  } catch {
    /* keep raw */
  }
  return name || null;
}

function readNodeBody(req: any): Promise<string> {
  return new Promise((resolve, reject) => {
    let data = "";
    req.on("data", (c: any) => {
      data += c;
    });
    req.on("end", () => resolve(data));
    req.on("error", reject);
  });
}

/** Convenience: load the engine and register a handler in one call. */
export async function defineApi<I = any, O = any>(
  handler: ApiHandler<I, O>
): Promise<TypedCaller<I, O>>;
export async function defineApi<I = any, O = any>(
  name: string,
  handler: ApiHandler<I, O>
): Promise<TypedCaller<I, O>>;
export async function defineApi<I = any, O = any>(
  a: string | ApiHandler<I, O>,
  b?: ApiHandler<I, O>
): Promise<TypedCaller<I, O>> {
  const db = await loadDopdb();
  return typeof a === "string" ? db.api<I, O>(a, b as ApiHandler<I, O>) : db.api<I, O>(a);
}
