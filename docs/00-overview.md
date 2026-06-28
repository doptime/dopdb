# 00 · Overview (architecture · trade-offs · package map)

## What it is

dopdb is a merge-rewrite of `doptime` + `redisdb` + `doptime-client`, with the backend swapped to MongoDB. Core thesis: **one schema produces the types, validation, typed client, and server at once** — no codegen, no writing it twice on front and back.

## Two equivalent implementations

dopdb is not "a Go server + a TS client" — it is **two equivalent, complete implementations** sharing one wire protocol and every feature:

- **Go** (root package `dopdb` + `api/` + `httpserve/` + `config/`): the server, bound directly to the MongoDB driver.
- **TypeScript** (`ts/`): runs the same server on Node, and provides a typed browser client.

Same URL scheme (`/api/<cmd>/<coll>` + `?ds=`), same command vocabulary, same `@`-binding / row-level isolation / permission model. The two engines may be mixed.

## Key trade-offs

| Trade-off | Note |
|---|---|
| Direct to Mongo, no Store abstraction | the `Store`/`Codec` interfaces and `memstore`/`mongostore` are gone; the root package uses driver v2 directly — Mongo used as Mongo |
| Multiple data sources + `?ds=` | config may hold several `[[mongo]]`; a request selects one with `?ds=<name>` (default `default`); the source is not in the path |
| Closed command vocabulary | see `02-http`; covers Hash + String/List/Set/ZSet |
| Redis-compatible data structures | beyond Hash, dopdb implements String/List/Set/ZSet on Mongo; blocking ops (BLPop/BRPop/BRPopLPush) are intentionally not implemented (see `REDISDB-COMPAT`) |
| `@`-binding | the server injects identity/context; client `@`-keys are stripped (anti-forgery) |
| Row-level isolation | owner-scope: whole-collection reads are forced to AND `{owner: me}` |
| Expose + authorize in one line | `HttpOn(...)` registers a collection to HTTP and declares the allowed commands (bitmask); default off until called |
| JWT | HS256 + RS256; `none` rejected |
| watch | change stream → SSE (needs a replica set) |
| Minimal functional API | `decode → Validate → Func`, no hook chain |
| WASM retired | TS is a standalone equivalent implementation, no WASM bridge |

## Package map (Go)

```
dopdb.go          generic Collection[K,V]: native trusted API (HGet/HSet/Find/HIncrBy/HScan/HRandField/...)
string.go         StringCollection[K]: String type (STR* commands) + TTL
list.go           ListCollection[K,E]: List type (L*/R* commands, atomic pop)
set.go            SetCollection[K]: Set type (S* commands)
zset.go           ZSetCollection[K]: ZSet type (Z* commands)
types.go          M / FindOpt / SortKey / IndexSpec / ErrNoDoc / ErrForbidden
mongo.go          mongoBackend (CRUD/indexes/watch/sample/scan via driver v2) + Datasources registry
http_accessor.go  HttpAccessor: type-erasure bridge (box V as any for HTTP dispatch) + owner-scope policy
perms.go          Perm bitmask (uint64; one bit per command) + groups + HttpOn gate
modifiers.go      write modifiers (timestamps, @-bound fields)
sanitize.go       filter sanitization (reject $-operators injected as keys)
api/api.go        functional API endpoint registration + dispatch
httpserve/        context (routing+JWT+@-parse) / serve (dispatch+watch) / permission / jwt / bootstrap (Serve)
config/config.go  TOML loading (multiple [[mongo]]; secrets from env, never in files)
```

> Deleted packages/files: `store.go`, `memstore/`, `mongostore/` (inlined into `mongo.go`), `wasm/`, the old WASM `clients/`.

## Two faces (Go and TS alike)

- **Trusted face**: the server reads/writes internally — no scope, no JWT. Go uses `Collection` native methods; TS uses `serverDb(schema, db)`.
- **Controlled face**: the outward HTTP boundary enforces JWT `@`-binding, owner-scope, and permissions. Go uses `httpserve`; TS uses `serve(cfg)`.

Read on: `01-data` (data layer), `02-http` (HTTP/security), `03-config` (configuration), `04-typescript` (TS equivalent), `REDISDB-COMPAT` (Redis-compatible data structures), `RUNBOOK`.
