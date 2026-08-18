# 00 · Overview (architecture · trade-offs · package map)

## What it is

dopdb is a merge-rewrite of `doptime` + `redisdb` + `doptime-client`, with the backend on **KVRocks** (Apache Kvrocks — a RocksDB-backed store speaking the Redis protocol). Core thesis: **one schema produces the types, validation, typed client, and server at once** — no codegen, no writing it twice on front and back.

## Two equivalent implementations

dopdb is not "a Go server + a TS client" — it is **two equivalent, complete implementations** sharing one wire protocol and every feature:

- **Go** (root package `dopdb` + `api/` + `httpserve/` + `config/`): the server, bound directly to a Redis-protocol client.
- **TypeScript** (`ts/`): runs the same server on Node, and provides a typed browser client.

The two engines share more than a protocol: `kvrocks.go`/`kvrocks.ts`, `codec.go`/`codec.ts` and `query.go`/`query.ts` are deliberate twins — same key layout, same CBOR value format, same filter dialect — so a divergence shows up in the cross-engine conformance suite immediately.

Same URL scheme (`/api/<cmd>/<coll>` + `?ds=`), same command vocabulary, same `@`-binding / row-level isolation / permission model. The two engines may be mixed.

## Key trade-offs

| Trade-off | Note |
|---|---|
| Direct to KVRocks, no Store abstraction | the `Store`/`Codec` interfaces and `memstore`/`mongostore` are gone; the root package speaks the Redis protocol directly — Redis data structures used as Redis data structures |
| CBOR is the value format | dopdb owns the on-disk format now (KVRocks stores opaque bytes). Field names come from the `json:"..."` tags, so what is stored and what crosses HTTP carry identical names |
| Multiple data sources + `?ds=` | config may hold several `[[kvrocks]]`; a request selects one with `?ds=<name>` (default `default`); the source is not in the path. A datasource's `namespace` is a key prefix — KVRocks has no per-database collections |
| Closed command vocabulary | see `02-http`; covers Hash + String/List/Set/ZSet |
| Redis-compatible data structures | Hash is one Redis hash per collection; String/List/Set/ZSet are the native Redis types, so L\*/S\*/Z\* are the real commands. Blocking ops (BLPop/BRPop/BRPopLPush) are intentionally not implemented (see `REDISDB-COMPAT`) |
| **FIND scans** | KVRocks cannot query by content. `FIND`/`COUNT`/`FINDONE` walk the collection hash and filter in-process (`query.go`/`query.ts`). Cost is O(collection), not O(result) — this is the single biggest thing to know before designing a collection |
| SQL | since the query is ours now, `SELECT` is offered as a front end over the same engine — read-only, Hash-only, no JOIN. It costs exactly what `FIND` costs. See `05-sql` |
| **Indexes are mostly gone** | only `unique` survives, enforced by dopdb itself via a side hash. `1`/`-1`/`text`/`2dsphere` tags are accepted and inert. A per-document TTL cannot be expressed at all (a Hash collection is one Redis key); per-key TTL is native for the String family |
| `@`-binding | the server injects identity/context; client `@`-keys are stripped (anti-forgery) |
| Row-level isolation | owner-scope: whole-collection reads are forced to AND `{owner: me}`. For the non-Hash types (no document to hold an owner field) the same predicate is enforced against the collection's owner index |
| Expose + authorize in one line | `HttpOn(...)` registers a collection to HTTP and declares the allowed commands (bitmask); default off until called |
| JWT | HS256 + RS256; `none` rejected |
| **watch** | dopdb's own pub/sub channel → SSE. No replica set needed and no `notify-keyspace-events` required — but **no replay**: a reconnect starts fresh, and only writes made through dopdb are seen |
| Minimal functional API | `decode → Validate → Func`, no hook chain |
| WASM retired | TS is a standalone equivalent implementation, no WASM bridge |

## Package map (Go)

```
dopdb.go          generic Collection[K,V]: native trusted API (HGet/HSet/Find/HIncrBy/HScan/HRandField/...)
keyspace.go       the shared handle behind the four native Redis collection types
string.go         StringCollection[K]: String type (STR* commands), native per-key TTL
list.go           ListCollection[K,E]: List type (L*/R* commands) — a real Redis list
set.go            SetCollection[K]: Set type (S* commands) — a real Redis set
zset.go           ZSetCollection[K]: ZSet type (Z* commands) — a real Redis sorted set
types.go          M / FindOpt / SortKey / IndexSpec / ErrNoDoc / ErrForbidden / ErrDuplicate
kvrocks.go        kvBackend (key layout, CRUD, WATCH/MULTI writes, scan, pub-sub watch) + Datasources registry
codec.go          the CBOR storage codec (canonical/deterministic)
query.go          the in-process query engine: filter evaluation, sort, skip/limit, projection
sql.go            the SQL front end: parses SELECT into the shape query.go evaluates (Hash only)
index.go          unique-index enforcement (the only index kind KVRocks can honour)
http_accessor.go  HttpAccessor: type-erasure bridge (box V as any for HTTP dispatch) + owner-scope policy
perms.go          Perm bitmask (uint64; one bit per command) + groups + HttpOn gate
modifiers.go      write modifiers (timestamps, @-bound fields)
sanitize.go       filter sanitization (the accepted operator allowlist)
api/api.go        functional API endpoint registration + dispatch
httpserve/        context (routing+JWT+@-parse) / serve (dispatch+watch) / permission / jwt / bootstrap (Serve)
config/config.go  TOML loading (multiple [[kvrocks]]; secrets from env, never in files)
```

> Deleted packages/files: `store.go`, `memstore/`, `mongostore/`, `mongo.go`, `wasm/`, the old WASM `clients/`.

## Two faces (Go and TS alike)

- **Trusted face**: the server reads/writes internally — no scope, no JWT. Go uses `Collection` native methods; TS uses `serverDb(schema, backend)`.
- **Controlled face**: the outward HTTP boundary enforces JWT `@`-binding, owner-scope, and permissions. Go uses `httpserve`; TS uses `serve(cfg)`.

Read on: `01-data` (data layer), `02-http` (HTTP/security), `03-config` (configuration), `04-typescript` (TS equivalent), `05-sql` (SQL over Hash collections), `REDISDB-COMPAT` (Redis-compatible data structures), `RUNBOOK`.
