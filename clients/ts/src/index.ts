// dopdb-client — WASM-backed API runtime + remote caller for the dopdb framework.
//
// Two independent halves:
//   - WASM runtime  (wasm.ts):   define APIs from typed JS/TS functions, run/serve them.
//   - Remote caller (client.ts): call a remote dopdb server's /api/<name> route.

export {
  loadDopdb,
  defineApi,
  Dopdb,
  type ApiHandler,
  type TypedCaller,
  type LoadOptions,
} from "./wasm.js";

export {
  createApi,
  configure,
  RequestOptions,
  Opt,
  type TokenGetter,
  DataClient,
  collection,
} from "./client.js";
