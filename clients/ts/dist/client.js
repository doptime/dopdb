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
/** Per-request options for the remote caller. */
export class RequestOptions {
    /** Server origin; "" means same origin as the page. Trailing slashes trimmed. */
    baseUrl = "";
    /** Extra query-string params (e.g. { ds: "default" }). */
    params = {};
    /** Extra HTTP headers. */
    headers = {};
    /** Returns the bearer token (static or dynamic, sync or async). */
    getToken = () => null;
    /** Return a shallow clone with overrides applied (does not mutate this). */
    with(patch) {
        const o = new RequestOptions();
        o.baseUrl = (patch.baseUrl ?? this.baseUrl).replace(/\/+$/, "");
        o.params = { ...this.params, ...(patch.params || {}) };
        o.headers = { ...this.headers, ...(patch.headers || {}) };
        o.getToken = patch.getToken ?? this.getToken;
        return o;
    }
    /** Select the server data source / database (?ds=). */
    withDataSource(name) {
        return this.with({ params: { ds: name } });
    }
}
/** The shared default options, mutated by configure(). */
export const Opt = new RequestOptions();
/** Set global defaults for the remote caller. */
export function configure(opts) {
    if (opts.baseUrl !== undefined)
        Opt.baseUrl = opts.baseUrl.replace(/\/+$/, "");
    if (opts.headers)
        Opt.headers = { ...Opt.headers, ...opts.headers };
    if (opts.getToken !== undefined) {
        Opt.getToken = opts.getToken;
    }
    else if (opts.token !== undefined) {
        const t = opts.token;
        Opt.getToken = () => t;
    }
}
function buildUrl(opt, path) {
    const qs = new URLSearchParams(opt.params).toString();
    return `${opt.baseUrl}${path}${qs ? "?" + qs : ""}`;
}
async function authHeaders(opt) {
    const h = { ...opt.headers };
    const token = await opt.getToken();
    if (token)
        h["Authorization"] = token.startsWith("Bearer ") ? token : `Bearer ${token}`;
    return h;
}
async function parse(res) {
    const ct = res.headers.get("content-type") || "";
    const value = ct.includes("application/json") ? await res.json() : await res.text();
    if (!res.ok) {
        const msg = typeof value === "object" && value && "error" in value ? value.error : value;
        throw new Error(`dopdb: ${res.status} ${res.statusText}: ${msg}`);
    }
    return value;
}
/**
 * Create a typed remote caller for the endpoint served at /api/<name>. The
 * returned function POSTs its argument as JSON and resolves the JSON response.
 */
export function createApi(name, options = Opt) {
    if (!name)
        throw new Error("dopdb: API name cannot be empty");
    return async (data = {}, opt = options) => {
        const headers = await authHeaders(opt);
        headers["Content-Type"] = "application/json";
        const res = await fetch(buildUrl(opt, `/api/${encodeURIComponent(name)}`), {
            method: "POST",
            headers,
            body: JSON.stringify(data),
        });
        return (await parse(res));
    };
}
//# sourceMappingURL=client.js.map