# Test layout and running (standard suite)

The tests accumulated across rounds have converged to the standard project test files below (the superseded `httpserve/interop_test.go` — single-engine Go, replaced by `conformance_test.go` — has been removed).

## Go test files

| File | Coverage | Needs Mongo |
|---|---|---|
| `dopdb_test.go` | root unit: Collection methods, key codec, encoding | no |
| `api/api_test.go` | functional API pipeline (decode→validate→func) | no |
| `config/config_test.go` | config load/parse | no |
| `httpserve/helpers_test.go` | shared test helpers (`setupHandler`/`tokenFor`/`do`/`decodeObj`) | — |
| `httpserve/api_dispatch_test.go` | HTTP routing/dispatch, command vocabulary, error shape | no |
| `httpserve/httpon_test.go` | the HttpOn bitmask is the sole gate (zero Grant) | yes |
| `httpserve/permission_persist_test.go` | permission `SaveJSON`/`LoadJSON` | no |
| `httpserve/integration_test.go` | real-Mongo end-to-end CRUD/atomic/owner isolation | **yes** |
| `httpserve/conformance_test.go` | **Go↔TS consistency**: starts a TS subprocess, drives both, diffs status+code+body per command (incl. all String/List/Set/ZSet + HScan/HRandField) | **yes** |

## TypeScript test files (`ts/test/`)

`schema` `client` `server` `permission` `sanitize` `prepare` `indexes` `config` `hardening` `browser-safety` `spec-export` `next-handler` `types.test-d` `watch-e2e` `watch-reconnect`. Of these, `watch-e2e` needs a real Mongo (replica set) and auto-skips without `DOPDB_TEST_MONGO_URI`; the rest use in-memory doubles.

## Running

```bash
# TS: full suite (no-Mongo cases auto-skip)
( cd ts && npm test )

# Go: the parts that need no Mongo (unit + routing + config + permission persistence)
go test ./... -short    # or without -short but without DOPDB_TEST_MONGO_URI: the Mongo cases skip

# Go: the integration + conformance that need a real Mongo (replica set)
export DOPDB_TEST_MONGO_URI="mongodb://127.0.0.1:27017/?replicaSet=rs0"
# if node is not at a default PATH location (conformance starts a TS subprocess): export DOPDB_TS_NODE=/path/to/node
go test ./httpserve -run Integration -v
go test ./httpserve -run Conformance -v
go test ./httpserve -run IntegrationWatch -v
```

## Conventions

- Tests that need a real Mongo are **always** gated by `DOPDB_TEST_MONGO_URI` (else `t.Skip` / TS `{ skip }`); they must not hard-fail in a Mongo-less environment.
- **Two-engine consistency is recognized only via `conformance_test.go`**: the same set of requests is sent to both Go and TS, and the diff must be empty. A single-engine integration test (like the old interop) **cannot** count as consistency evidence.
- Tests must not alter the system-under-test's gates/guards to pass; the key numbers are taken from real stdout.
