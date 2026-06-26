// dopdb — one framework, one source of truth.
//
// Entry points (see package.json "exports"):
//   import { collection, f, type Infer } from "dopdb";          // shared schema
//   import { clientDb, apiClient }       from "dopdb/client";    // browser (fetch)
//   import { serve, serverDb, defineApi } from "dopdb/server";   // node + mongo
//
// The root + /client entries import nothing from node or mongodb, so the schema
// and the client are safe to bundle into a browser app. Only /server pulls the
// driver in.

// ---- shared: the single source of truth ----
export {
  f,
  collection,
  Collection,
  Field,
  validate,
  prepareWrite,
  specOf,
  buildSpec,
  nanoid,
  type Shape,
  type CollectionOpts,
  type Infer,
  type InferInput,
  type FieldKind,
  type FieldRules,
  type FieldSpec,
  type IndexSpec,
  type CollectionSpec,
  type SchemaSpec,
  type WriteSide,
  type ValidateOpts,
} from "./schema.js";

export {
  sanitizeFilter,
  type Filter,
} from "./sanitize.js";

export {
  DopdbError,
  ValidationError,
  UnauthorizedError,
  ForbiddenError,
  NotFoundError,
  ConflictError,
  errorFromResponse,
  type FieldIssue,
} from "./errors.js";

// ---- API endpoint types (shared) + the in-process handle factory ----
export {
  defineApi,
  runEndpoint,
  apiNames,
  type Endpoint,
  type ServerEndpoint,
  type ApiMap,
  type ApiContext,
  type DefineApiOptions,
} from "./api.js";

// ---- client (fetch); safe in the browser ----
export {
  clientDb,
  apiClient,
  type Db,
  type DbApi,
  type ClientOptions,
  type ApiClient,
  type FindOpt,
  type TokenGetter,
  type WatchEvent,
  type WatchHandler,
  type WatchOp,
  type Unsubscribe,
  type WatchOptions,
} from "./client.js";
