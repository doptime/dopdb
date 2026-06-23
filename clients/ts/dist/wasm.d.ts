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
/**
 * Load (once) the dopdb WASM engine and return a Dopdb runtime. Subsequent calls
 * return the same instance unless `{ reload: true }` is passed.
 */
export declare function loadDopdb(opts?: LoadOptions): Promise<Dopdb>;
/** The dopdb runtime: register typed endpoints and serve them at /api/<name>. */
export declare class Dopdb {
    private raw;
    constructor(raw: DopdbRaw);
    /** WASM build version string. */
    get version(): string;
    /** Names of all registered endpoints (the route keys). */
    names(): string[];
    /**
     * Register a typed handler as an API. The route is /api/<name>, where name is
     * the explicit string or the function's name (the "type name"). Returns a
     * typed in-process caller carrying `.apiName`.
     */
    api<I = any, O = any>(handler: ApiHandler<I, O>): TypedCaller<I, O>;
    api<I = any, O = any>(name: string, handler: ApiHandler<I, O>): TypedCaller<I, O>;
    /** Invoke a registered endpoint by name (runs the pipeline; resolves the output). */
    call<O = any>(name: string, input?: any): Promise<O>;
    /** Validate a Mongo-style query filter against the operator allowlist. Throws on a forbidden op. */
    sanitizeFilter<T = any>(filter: T): T;
    /** Unregister an endpoint (e.g. for hot reload). */
    remove(name: string): void;
    /** Node http(s) request listener: `createServer(db.nodeListener)`. */
    nodeListener: (req: any, res: any) => Promise<void>;
    /** Fetch-style handler for Deno / Bun / Cloudflare Workers: `serve(db.fetchHandler)`. */
    fetchHandler: (request: Request) => Promise<Response>;
}
/** Convenience: load the engine and register a handler in one call. */
export declare function defineApi<I = any, O = any>(handler: ApiHandler<I, O>): Promise<TypedCaller<I, O>>;
export declare function defineApi<I = any, O = any>(name: string, handler: ApiHandler<I, O>): Promise<TypedCaller<I, O>>;
export {};
//# sourceMappingURL=wasm.d.ts.map