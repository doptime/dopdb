# MIGRATION · MongoDB → KVRocks (CBOR storage)

Everything below is verified against a live Redis-protocol server, both engines,
plus the Go↔TS conformance harness:

```
go vet ./...                                  ok
gofmt -l .                                    clean
go test ./...            (no server)          ok — integration cases skip
go test ./...            (live server)        ok — 4/4 packages, conformance included
tsc -p tsconfig.json --noEmit                 ok
npm test                 (no server)          71 pass, 25 skip
npm test                 (live server)        96 pass, 0 fail
```

---

## 1 · What was replaced

| Concern | Before | After |
|---|---|---|
| Store | MongoDB (`go.mongodb.org/mongo-driver/v2`, `mongodb` npm) | KVRocks (`github.com/redis/go-redis/v9`, `ioredis`) |
| Value format | BSON, field names from `bson:"..."` | **CBOR** (canonical), field names from `json:"..."` |
| Query | server-side, Mongo query planner | **in-process** (`query.go` / `src/query.ts`), same dialect |
| Logical database | Mongo database name (`db`) | **key namespace prefix** (`namespace`) |
| Config section | `[[mongo]]` | `[[kvrocks]]` |
| Test env var | `DOPDB_TEST_MONGO_URI` (replica set for watch) | `DOPDB_TEST_KVROCKS_URI` (plain instance) |

The **wire protocol, schema, `@`-binding, owner-scope and permission model are
unchanged.** No URL, no command name, no JSON body shape moved.

## 2 · Key layout

```
<ns>:<coll>                  HASH   Hash collection: field = document id, value = CBOR document
<ns>:<coll>:<key>            native STRING / LIST / SET / ZSET
<ns>:<coll>:__owner          HASH   key -> owner (row isolation for the non-Hash types)
<ns>:<coll>:__uniq:<field>   HASH   unique-index claims
<ns>:<coll>:__events         channel for watch
```

This is redisdb's own layout, which is why the Hash family is back on
`HGET`/`HSET`/`HDEL`/`HSCAN`/`HRANDFIELD` and List/Set/ZSet are the real Redis
types rather than array surgery on a document.

## 3 · What got better

- **Every L\*/S\*/Z\* command is one atomic Redis command.** On Mongo they were
  read-modify-write; `LREM`, `LINSERT` and `ZINCRBY` could lose a concurrent
  update. They cannot now.
- **ZSet ordering is the server's index**, not a derived order re-sorted on every
  read. Visible sequences are identical (Redis orders by score then member).
- **String TTL is native** (`SET ... EX`) instead of a swept TTL index with
  ~60 s granularity.
- **No replica set** and no `notify-keyspace-events` needed for `watch`.

## 4 · What got worse — the four capability losses

These are real. They are documented where they bite (`docs/01-data.md`,
`docs/02-http.md`, `docs/REDISDB-COMPAT.md`, `docs/RUNBOOK.md`) and asserted in
tests, not quietly dropped.

1. **`FIND` scans.** KVRocks has no query language. `FIND`/`COUNT`/`FINDONE`
   walk the collection hash and filter in-process. Same dialect, different cost
   model: **O(collection), not O(result)**.
2. **Indexes are gone except `unique`.** `unique` is enforced by dopdb via a
   claim hash and now surfaces as **409 Conflict** (`ErrDuplicate`). It is
   *sparse*: a missing value claims nothing, where a Mongo unique index would
   have rejected a second null. `1`/`-1`/`text`/`2dsphere` tags are accepted and
   **inert**.
3. **No per-document TTL.** A Hash collection is one Redis key, so a document
   inside it cannot expire on its own. `EnsureTTL` is a documented no-op; a
   `.ttl(...)` schema declaration is recorded and inert.
4. **`watch` cannot replay.** Redis pub/sub is fire-and-forget: no resume token,
   **no SSE `id:` line**, `Last-Event-ID` deliberately ignored, reconnect starts
   fresh. Only writes made *through dopdb* are visible.

The TS test that used to assert *"watch resumes from Last-Event-ID, replaying
changes missed while disconnected"* was **rewritten to assert the new behaviour**
rather than deleted — see `ts/test/server.test.ts`.

## 5 · Behaviour deliberately preserved

- **`LPUSH` with several items** lands them head-first *in the order given*
  (`[a,b]` onto `[c]` → `[a,b,c]`), matching the Mongo build. Raw Redis `LPUSH`
  would give `[b,a,c]`; dopdb reverses before pushing so existing callers and the
  TS engine keep agreeing.
- **`HIncrBy` keeps an integral result an integer**, exactly as Mongo's `$inc`
  kept `int + int` an `int`. CBOR is typed: writing `25.0` where the struct field
  is an `int` would make the document undecodable on the next read.
- **`HSCAN` cursor semantics** are now the server's real cursor, so a page may be
  short or empty with a non-zero cursor — only cursor `0` means done. Both engines
  behave identically; the conformance suite covers it.

## 6 · Three bugs found and fixed while migrating

1. **CBOR is strongly typed.** `HIncrBy` producing `25.0` made the next `HGet`
   fail to decode into an `int` field. Fixed by storing integral results as
   integers (see above).
2. **`WATCH` granularity is the KEY, and a Hash collection is one key.** Any write
   to any document in the collection aborts a concurrent compare-and-set. Fixed
   with 64 jittered-backoff retries; correctness was never at risk (an aborted
   transaction does not write), but throughput was. Documented as the price of the
   one-hash-per-collection layout.
3. **Default connection pool is too small.** go-redis defaults to 10×CPU; on a
   1-CPU container, 20 concurrent `HIncrBy` calls exhausted the pool and *lost 8
   increments* as `connection pool timeout`. Fixed with a `minPoolSize = 64` floor
   in `ConnectDatasources`; an explicit `?pool_size=` in the URL still wins.

## 7 · Test-suite changes worth knowing

- **New, no server needed:** `query_test.go` + `ts/test/query.test.ts` (the filter
  dialect), `codec_test.go` + `ts/test/codec.test.ts` (the storage format). These
  matter because MongoDB *used to be* the reference implementation of the dialect
  and now dopdb is.
- **Removed:** the ~180-line in-memory fake Mongo in `ts/test/server.test.ts`. On
  KVRocks the commands *are* Redis commands (LPUSH, ZADD, HSCAN, WATCH/MULTI,
  pub/sub); hand-rolling a fake of those would test the fake, not the engine.
  `server.test.ts` now gates on `DOPDB_TEST_KVROCKS_URI`, and the two new unit
  suites keep `npm test` meaningful on a bare checkout.
- **Namespaces make a shared server safe:** every test takes a throwaway key
  namespace and deletes it on cleanup. The conformance harness relies on exactly
  this — the Go and TS servers get separate namespaces on one instance.

## 8 · Migrating an existing deployment

Mechanical:

- `[[mongo]]` → `[[kvrocks]]`; `uri` becomes a `redis://` URL (a leftover
  `mongodb://` is now rejected **at config load**, not at dial time); `db` →
  `namespace`; new optional `password` / `password_env` (KVRocks namespace token).
- Drop `bson:"..."` struct tags. CBOR takes names from `json:"..."`.
- `ds.Add(name, client.Database(db))` → `ds.Add(name, redisClient, namespace)`;
  `DatasourceConfig{Name,URI,DB}` → `{Name,URI,Namespace,Password}`.
- TS: `serve({ mongo: {uri, db} })` → `serve({ kvrocks: {uri, namespace} })`;
  `serverDb(schema, mongoDb)` → `serverDb(schema, backend)`.

**Data:** there is no in-place path. BSON documents and CBOR values in a Redis
hash share no format — export and re-import through the dopdb API (read with the
old build, write with the new one).

## 9 · Files

**Go — new:** `kvrocks.go` `codec.go` `query.go` `index.go` `keyspace.go`
`query_test.go` `codec_test.go`
**Go — deleted:** `mongo.go`
**Go — rewritten:** `string.go` `list.go` `set.go` `zset.go` `types.go`
`config/config.go` `dopdb_test.go` `config/config_test.go`
`httpserve/integration_test.go`
**Go — edited:** `dopdb.go` `http_accessor.go` `sanitize.go` `go.mod` `go.sum`
`httpserve/{bootstrap,serve,helpers_test,httpon_test,conformance_test}.go`

**TS — new:** `src/kvrocks.ts` `src/codec.ts` `src/query.ts` `test/query.test.ts`
`test/codec.test.ts`
**TS — rewritten:** `src/config.ts` `test/config.test.ts` `test/indexes.test.ts`
**TS — edited:** `src/server.ts` `package.json` `conformance/server.ts` `README.md`
`test/{server,hardening,next-handler,watch-e2e,browser-safety}.test.ts`
`examples/next-minimal/app/api/[...slug]/route.ts`

**Docs/config:** `README.md` `AGENTS.md` `Makefile` `config.toml.example`
`docs/{00-overview,01-data,02-http,03-config,04-typescript,REDISDB-COMPAT,RUNBOOK,TESTING}.md`

**Untouched on purpose** (nothing in them touched the database):
`modifiers.go` `perms.go` `api/` `httpserve/{context,jwt,permission}.go`
`ts/src/{schema,client,api,errors,permission,sanitize,index}.ts` and their tests.
