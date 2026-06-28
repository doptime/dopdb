# RUNBOOK · dopdb build / test / run / migrate

## Prerequisites

- **Go** ≥ 1.22 (driver `go.mongodb.org/mongo-driver/v2`).
- **Node** ≥ 20 (the TS implementation; `mongodb` ^6 is a peer dependency). For the conformance test's TS subprocess, **Node ≥ 20.6** (the `--import tsx` loader); older defaults: run via the local `node_modules/.bin/tsx`, or set `DOPDB_TS_NODE` to a newer node.
- **MongoDB**: a plain instance runs most integration tests; **`watch` (change streams) needs a replica set**.

## Go: build and test

```bash
make build         # go build ./... (root binds the driver; one network fetch of the driver module)
make vet           # go vet ./...
make fmt-check     # gofmt check
make test          # go test ./... (integration tests auto-skip when DOPDB_TEST_MONGO_URI is unset)
make test-mongo    # DOPDB_TEST_MONGO_URI=mongodb://... runs integration tests (watch needs a replica set)
```

- **Unit tests** (`api/`, `config/`, `httpserve/` api-dispatch/permission): no database needed.
- **Integration tests** (root `dopdb_test.go`, `httpserve/integration_test.go`): need `DOPDB_TEST_MONGO_URI`, each uses a throwaway database and drops it at the end.
- **Conformance** (`httpserve/conformance_test.go`): needs `DOPDB_TEST_MONGO_URI` and starts a TS subprocess; covers every command including all String/List/Set/ZSet.

```bash
# e.g. local single-node replica set for watch
DOPDB_TEST_MONGO_URI="mongodb://localhost:27017/?replicaSet=rs0" make test-mongo
```

## TypeScript: build and test

```bash
make ts            # cd ts && npm install && npm run build
make ts-typecheck  # tsc -p tsconfig.json --noEmit (strict)
make ts-test       # node --import tsx --test test/*.test.ts
```

Most TS unit tests need no real DB (`server.test.ts` uses an injected in-memory fake Mongo; `config.test.ts` only parses TOML).

## Run (Go)

```toml
# config.toml (secrets resolved from env)
[http]
addr = ":8080"
jwt_secret_env = "DOPTIME_JWT_SECRET"
cors_origins = ["https://app.example.com"]
[[mongo]]
name = "default"
uri_env = "DOPTIME_MONGO_URI"
db = "appdb"
```

```bash
export DOPTIME_JWT_SECRET="...HS256 secret or RS256 PEM..."
export DOPTIME_MONGO_URI="mongodb://user:pw@host:27017/?authSource=admin"
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
# live subscribe (SSE)
curl -N "localhost:8080/api/watch/users" -H "Authorization: $TOKEN"
```

## Migrating from older versions (highlights)

- **URL**: data commands move from the old `CMD-KEY` segment to `/api/<cmd>/<coll>`; the data source moves from a path segment to `?ds=<name>`.
- **Store/Codec**: removed; if you relied on `memstore`/`mongostore`/`WithStore`, switch to plain `New[...]` + `SetDatasources`/`ConnectDatasources`.
- **Permissions**: `auto_auth` removed; expose a collection with `.HttpOn(...)` (default off). The legacy `Permissions` map still works as a runtime override.
- **WASM**: retired; TS is a standalone equivalent — use `dopdb/client` and `dopdb/server`.
- **API hooks**: `ParamEnhancer/ResultSaver/ResponseModifier` removed; only `Validate` remains.

> Note: the Go code binds driver v2 — run `go build ./...` locally to confirm API details; the TS side passes strict type-checking and all unit tests.
