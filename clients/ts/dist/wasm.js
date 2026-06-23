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
function isNode() {
    return typeof process !== "undefined" && !!process.versions?.node;
}
function errMsg(e) {
    return String(e && e.message ? e.message : e);
}
let _instance = null;
/**
 * Load (once) the dopdb WASM engine and return a Dopdb runtime. Subsequent calls
 * return the same instance unless `{ reload: true }` is passed.
 */
export async function loadDopdb(opts = {}) {
    if (_instance && !opts.reload)
        return _instance;
    await ensureGo(opts);
    const Go = globalThis.Go;
    const go = new Go();
    const bytes = await resolveWasmBytes(opts);
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
    // main() sets globalThis.dopdb, calls __dopdbReady, then blocks on select{}.
    const ready = new Promise((resolve) => {
        globalThis.__dopdbReady = resolve;
    });
    void go.run(instance); // fire-and-forget; do not await (main never returns)
    await ready;
    const raw = globalThis.dopdb;
    if (!raw)
        throw new Error("dopdb: WASM module did not install the global `dopdb`");
    _instance = new Dopdb(raw);
    return _instance;
}
async function ensureGo(opts) {
    if (typeof globalThis.Go !== "undefined")
        return;
    if (isNode()) {
        const url = opts.wasmExecUrl ?? new URL("../wasm/wasm_exec.js", import.meta.url).href;
        await import(/* @vite-ignore */ url);
    }
    else {
        if (!opts.wasmExecUrl) {
            throw new Error("dopdb: in the browser, pass wasmExecUrl (URL of Go's wasm_exec.js)");
        }
        await loadBrowserScript(opts.wasmExecUrl);
    }
    if (typeof globalThis.Go === "undefined") {
        throw new Error("dopdb: wasm_exec.js did not define globalThis.Go");
    }
}
function loadBrowserScript(src) {
    return new Promise((resolve, reject) => {
        const doc = globalThis.document;
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
async function resolveWasmBytes(opts) {
    if (opts.wasmBytes)
        return opts.wasmBytes;
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
    raw;
    constructor(raw) {
        this.raw = raw;
    }
    /** WASM build version string. */
    get version() {
        return this.raw.version;
    }
    /** Names of all registered endpoints (the route keys). */
    names() {
        return this.raw.apiNames();
    }
    api(a, b) {
        let name;
        let fn;
        if (typeof a === "string") {
            name = a;
            fn = b;
        }
        else {
            fn = a;
            name = a.name; // function name = the "type name"
        }
        if (!name) {
            throw new Error("dopdb: cannot infer an API name from an anonymous function — use db.api('name', fn)");
        }
        if (typeof fn !== "function") {
            throw new Error("dopdb: db.api requires a handler function");
        }
        const res = this.raw.createApi(name, fn);
        if (res instanceof Error)
            throw res;
        const registered = res;
        const caller = ((input) => this.call(registered, input));
        Object.defineProperty(caller, "apiName", { value: registered, enumerable: true });
        return caller;
    }
    /** Invoke a registered endpoint by name (runs the pipeline; resolves the output). */
    call(name, input) {
        return this.raw.callApi(name, input);
    }
    /** Validate a Mongo-style query filter against the operator allowlist. Throws on a forbidden op. */
    sanitizeFilter(filter) {
        const r = this.raw.sanitizeFilter(filter);
        if (r instanceof Error)
            throw r;
        return r;
    }
    /** Unregister an endpoint (e.g. for hot reload). */
    remove(name) {
        this.raw.removeApi(name);
    }
    // --------------------------------------------------------------------------
    // server adapters — serve registered endpoints at /api/<name> using this
    // in-wasm engine. Query string and JSON body are merged into the input.
    // (JWT @-binding, permissions and data commands live in the Go httpserve.)
    // --------------------------------------------------------------------------
    /** Node http(s) request listener: `createServer(db.nodeListener)`. */
    nodeListener = async (req, res) => {
        try {
            const host = (req.headers && req.headers.host) || "localhost";
            const url = new URL(req.url || "/", `http://${host}`);
            const name = matchApiName(url.pathname);
            if (name === null) {
                res.statusCode = 404;
                res.end("dopdb: not an /api/<name> route");
                return;
            }
            const input = {};
            url.searchParams.forEach((v, k) => {
                input[k] = v;
            });
            const method = (req.method || "GET").toUpperCase();
            if (method !== "GET" && method !== "HEAD") {
                const body = await readNodeBody(req);
                if (body) {
                    try {
                        Object.assign(input, JSON.parse(body));
                    }
                    catch {
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
        }
        catch (e) {
            res.statusCode = 400;
            res.setHeader("Content-Type", "application/json");
            res.end(JSON.stringify({ error: errMsg(e) }));
        }
    };
    /** Fetch-style handler for Deno / Bun / Cloudflare Workers: `serve(db.fetchHandler)`. */
    fetchHandler = async (request) => {
        const url = new URL(request.url);
        const name = matchApiName(url.pathname);
        if (name === null) {
            return new Response("dopdb: not an /api/<name> route", { status: 404 });
        }
        const input = {};
        url.searchParams.forEach((v, k) => {
            input[k] = v;
        });
        if (request.method !== "GET" && request.method !== "HEAD") {
            const text = await request.text();
            if (text) {
                try {
                    Object.assign(input, JSON.parse(text));
                }
                catch {
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
        }
        catch (e) {
            return new Response(JSON.stringify({ error: errMsg(e) }), {
                status: 400,
                headers: { "Content-Type": "application/json" },
            });
        }
    };
}
/** Return the <name> in a /api/<name> path, or null if the path is not an API route. */
function matchApiName(pathname) {
    const i = pathname.indexOf("/api/");
    if (i < 0)
        return null;
    let name = pathname.slice(i + "/api/".length).replace(/\/+$/, "");
    try {
        name = decodeURIComponent(name);
    }
    catch {
        /* keep raw */
    }
    return name || null;
}
function readNodeBody(req) {
    return new Promise((resolve, reject) => {
        let data = "";
        req.on("data", (c) => {
            data += c;
        });
        req.on("end", () => resolve(data));
        req.on("error", reject);
    });
}
export async function defineApi(a, b) {
    const db = await loadDopdb();
    return typeof a === "string" ? db.api(a, b) : db.api(a);
}
//# sourceMappingURL=wasm.js.map