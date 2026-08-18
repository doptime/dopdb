# RUNBOOK · dopdb build / test / run / migrate

## Prerequisites

- **Go** ≥ 1.22 (modules `github.com/redis/go-redis/v9`, `github.com/fxamacker/cbor/v2`).
- **Node** ≥ 20 (the TS implementation; `ioredis` ^5 is a peer dependency, `cbor-x` a dependency). For the conformance test's TS subprocess, **Node ≥ 20.6** (the `--import tsx` loader); older defaults: run via the local `node_modules/.bin/tsx`, or set `DOPDB_TS_NODE` to a newer node.
- **KVRocks**: any recent version. Every integration test runs against a plain instance — there is no replica-set requirement any more, because `watch` no longer uses change streams. Any Redis-protocol server works for the test suite, since dopdb uses only the common command set.

## Go: build and test

```bash
make build         # go build ./...
make vet           # go vet ./...
make fmt-check     # gofmt check
make test          # go test ./... (integration tests auto-skip when DOPDB_TEST_KVROCKS_URI is unset)
make test-kvrocks  # DOPDB_TEST_KVROCKS_URI=redis://... runs integration tests
```

- **Unit tests** (`query_test.go`, `codec_test.go`, `api/`, `config/`, `httpserve/` api-dispatch/permission): no database needed. The query-engine and codec tests are the ones pinning the filter dialect and the storage format, now that dopdb owns both.
- **Integration tests** (root `dopdb_test.go`, `httpserve/integration_test.go`): need `DOPDB_TEST_KVROCKS_URI`; each uses a throwaway key namespace and deletes it at the end, so a shared server is safe.
- **Conformance** (`httpserve/conformance_test.go`): needs `DOPDB_TEST_KVROCKS_URI` and starts a TS subprocess; the two engines get separate namespaces on the same server. Covers every command including all String/List/Set/ZSet.

```bash
DOPDB_TEST_KVROCKS_URI="redis://localhost:6666" make test-kvrocks
```

## TypeScript: build and test

```bash
make ts            # cd ts && npm install && npm run build
make ts-typecheck  # tsc -p tsconfig.json --noEmit (strict)
make ts-test       # node --import tsx --test test/*.test.ts
```

Most TS tests need no server: `query.test.ts` and `codec.test.ts` cover the dialect and the storage format outright, and `config.test.ts` only parses TOML. `server.test.ts` and `watch-e2e.test.ts` drive a real server and skip without `DOPDB_TEST_KVROCKS_URI` — the in-memory fake Mongo they used to share is gone, since faking `LPUSH`/`ZADD`/`HSCAN`/`WATCH`/pub-sub by hand would test the fake rather than the engine.

## Run (Go)

```toml
# config.toml (secrets resolved from env)
[http]
addr = ":8080"
jwt_secret_env = "DOPTIME_JWT_SECRET"
cors_origins = ["https://app.example.com"]
[[kvrocks]]
name = "default"
uri_env = "DOPTIME_KVROCKS_URI"
password_env = "DOPTIME_KVROCKS_PASSWORD"
namespace = "appdb"
```

```bash
export DOPTIME_JWT_SECRET="...HS256 secret or RS256 PEM..."
export DOPTIME_KVROCKS_URI="redis://host:6666"
export DOPTIME_KVROCKS_PASSWORD="...namespace token..."
./your-server   # internally: config.Load → httpserve.Serve(cfg)
```

Example requests:

```bash
TOKEN="Bearer <jwt>"
# write (default source)
curl -XPOST "localhost:8080/api/hset/users?f=u1" -H "Authorization: $TOKEN" -d '{"name":"Ada","email":"ada@x.io","age":30}'
# read (specific source)
curl "localhost:8080/api/hget/users?ds=analytics&f=u1" -H "Authorization: $TOKEN"
# my own record
curl "localhost:8080/api/hget/profiles?f=@uid" -H "Authorization: $TOKEN"
# query (filter in the body)
curl -XPOST "localhost:8080/api/find/users?limit=20" -H "Authorization: $TOKEN" -d '{"age":{"$gte":18}}'
# list push / range
curl -XPOST "localhost:8080/api/rpush/queue?f=jobs" -H "Authorization: $TOKEN" -d '{"item":"job1"}'
curl "localhost:8080/api/lrange/queue?f=jobs&start=0&stop=-1" -H "Authorization: $TOKEN"
# live subscribe (SSE). Note: no resume — a reconnect starts fresh.
curl -N "localhost:8080/api/watch/users" -H "Authorization: $TOKEN"
```

## Migrating from the MongoDB build

The wire protocol, the schema, the `@`-binding and the permission model are **unchanged**. What moves is the storage layer and four capabilities that MongoDB provided and KVRocks does not.

**Mechanical changes**

- **Config**: `[[mongo]]` → `[[kvrocks]]`; `uri` becomes a `redis://` URL (a leftover `mongodb://` is now rejected at load time, not at dial time); `db` → `namespace` (a key prefix); new optional `password` / `password_env`.
- **Struct tags**: drop `bson:"..."`. CBOR takes the field name from `json:"..."`, so the stored names and the wire names are the same by construction.
- **Datasources**: `ds.Add(name, client.Database(db))` → `ds.Add(name, redisClient, namespace)`; `DatasourceConfig{Name,URI,DB}` → `{Name,URI,Namespace,Password}`.
- **TypeScript**: `serve({ mongo: {uri, db} })` → `serve({ kvrocks: {uri, namespace} })`; `serverDb(schema, mongoDb)` → `serverDb(schema, backend)`; the `mongodb` peer dependency becomes `ioredis`.
- **Test env**: `DOPDB_TEST_MONGO_URI` → `DOPDB_TEST_KVROCKS_URI`, and no replica set is needed.

**Behaviour you must plan around**

- **`FIND` scans.** There is no server-side query. `FIND`/`COUNT`/`FINDONE` walk the whole collection and filter in-process, so cost is O(collection). The filter dialect is byte-for-byte the same; only the cost model changed. Collections you query by content should stay small, or be reachable by key.
- **Indexes are gone except `unique`.** `1`/`-1`/`text`/`2dsphere` tags are accepted and inert. `unique` is enforced by dopdb (sparse: a missing value claims nothing) and now surfaces as **409 Conflict** (`ErrDuplicate`) rather than a driver duplicate-key error.
- **Per-document TTL is gone.** A Hash collection is one Redis key. Per-key TTL survives, natively, for the String family (`?expiration=`).
- **`watch` cannot replay.** Events come from dopdb's own pub/sub channel, so no replica set and no `notify-keyspace-events` are required — but there is no resume token, the server sends no SSE `id:` line, a reconnect starts fresh, and only writes made *through dopdb* are visible.

**Data migration**: there is no in-place path. BSON documents in Mongo and CBOR values in a Redis hash share no format, so export and re-import through the dopdb API (read with the old build, write with the new one).

## Earlier migrations (still relevant)

- **URL**: data commands moved from the old `CMD-KEY` segment to `/api/<cmd>/<coll>`; the data source moved from a path segment to `?ds=<name>`.
- **Store/Codec**: removed; if you relied on `memstore`/`mongostore`/`WithStore`, switch to plain `New[...]` + `SetDatasources`/`ConnectDatasources`.
- **Permissions**: `auto_auth` removed; expose a collection with `.HttpOn(...)` (default off). The legacy `Permissions` map still works as a runtime override.
- **WASM**: retired; TS is a standalone equivalent — use `dopdb/client` and `dopdb/server`.
- **API hooks**: `ParamEnhancer/ResultSaver/ResponseModifier` removed; only `Validate` remains.
