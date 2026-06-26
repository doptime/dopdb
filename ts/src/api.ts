// api.ts — typed function endpoints.
//
// defineApi returns a callable handle (T2.1): you invoke it directly to run the
// endpoint in-process, and `.remove()` unregisters it. There is no separate
// callApi / removeApi — a server API that needs another API just calls its
// handle like any function (point 4). The route is /api/<name>.
//
// This module imports nothing from node or mongodb, so the client can do
//   import type { api } from "../server/api-defs";
// and pull only the input/output TYPES across — the handler code never ships.

import { ValidationError, type FieldIssue } from "./errors.js";

/** The phantom-typed view the client consumes (types only). */
export interface Endpoint<I, O> {
  readonly __in: I;
  readonly __out: O;
  readonly name: string;
}

export type ApiMap = Record<string, Endpoint<any, any>>;

/** The full server-side endpoint: a callable handle carrying its types. */
export type ServerEndpoint<I, O> = ((input: I) => Promise<O>) & {
  readonly __in: I;
  readonly __out: O;
  readonly name: string;
  /** Unregister the endpoint (hot reload / teardown). */
  remove(): void;
};

export interface ApiContext {
  /** Verified JWT claims for the current call (empty for in-process calls). */
  claims: Record<string, unknown>;
}

export interface DefineApiOptions<I> {
  /** Override the auto-derived name (default: the handler's function name). */
  name?: string;
  /** Optional input validation run before the handler. Throw to reject. */
  validate?: (input: I) => void;
}

type Runner = (input: unknown, ctx: ApiContext) => Promise<unknown>;

interface Registered {
  name: string;
  run: Runner;
}

const registry = new Map<string, Registered>();

function clean(name: string): string {
  return name.trim().toLowerCase();
}

/**
 * Register a typed endpoint. The handler may be sync or async and may take an
 * optional second `ctx` argument (claims). Returns a callable handle.
 *
 *   const greet = defineApi(function greet(in: { name: string }) {
 *     return { msg: "hi " + in.name };
 *   });
 *   await greet({ name: "Ada" });   // in-process call, typed
 */
export function defineApi<I, O>(
  handler: (input: I, ctx: ApiContext) => O | Promise<O>,
  opts: DefineApiOptions<I> = {},
): ServerEndpoint<I, O> {
  const name = clean(opts.name ?? handler.name);
  if (!name) {
    throw new Error("dopdb: cannot infer an API name from an anonymous handler — pass { name }");
  }
  if (registry.has(name)) {
    throw new Error(`dopdb: endpoint "${name}" already defined`);
  }

  const run: Runner = async (input, ctx) => {
    const typed = input as I;
    if (opts.validate) opts.validate(typed);
    return handler(typed, ctx);
  };
  registry.set(name, { name, run });

  const handle = ((input: I) => run(input, { claims: {} })) as ServerEndpoint<I, O>;
  Object.defineProperty(handle, "name", { value: name, enumerable: true });
  Object.defineProperty(handle, "remove", { value: () => registry.delete(name) });
  return handle;
}

/** Server use: dispatch an incoming request to a registered endpoint by name. */
export async function runEndpoint(name: string, input: unknown, ctx: ApiContext): Promise<unknown> {
  const ep = registry.get(clean(name));
  if (!ep) {
    const e = new ValidationError([] as FieldIssue[], `endpoint not found: ${name}`);
    (e as { status: number }).status = 404;
    (e as { code: string }).code = "not_found";
    throw e;
  }
  return ep.run(input, ctx);
}

/** Names of all registered endpoints. */
export function apiNames(): string[] {
  return [...registry.keys()];
}

/** Test/teardown helper. */
export function _clearApiRegistry(): void {
  registry.clear();
}
