export type TokenGetter = () => string | null | undefined | Promise<string | null | undefined>;
/** Per-request options for the remote caller. */
export declare class RequestOptions {
    /** Server origin; "" means same origin as the page. Trailing slashes trimmed. */
    baseUrl: string;
    /** Extra query-string params (e.g. { ds: "default" }). */
    params: Record<string, string>;
    /** Extra HTTP headers. */
    headers: Record<string, string>;
    /** Returns the bearer token (static or dynamic, sync or async). */
    getToken: TokenGetter;
    /** Return a shallow clone with overrides applied (does not mutate this). */
    with(patch: Partial<RequestOptions>): RequestOptions;
    /** Select the server data source / database (?ds=). */
    withDataSource(name: string): RequestOptions;
}
/** The shared default options, mutated by configure(). */
export declare const Opt: RequestOptions;
/** Set global defaults for the remote caller. */
export declare function configure(opts: {
    baseUrl?: string;
    token?: string | null;
    getToken?: TokenGetter;
    headers?: Record<string, string>;
}): void;
/**
 * Create a typed remote caller for the endpoint served at /api/<name>. The
 * returned function POSTs its argument as JSON and resolves the JSON response.
 */
export declare function createApi<I = any, O = any>(name: string, options?: RequestOptions): (data?: I, opt?: RequestOptions) => Promise<O>;
//# sourceMappingURL=client.d.ts.map