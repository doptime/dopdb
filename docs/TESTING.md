# Test layout and running (standard suite)

## Go test files

| File | Coverage | Needs a server |
|---|---|---|
| `codec_test.go` | the CBOR storage format: round-trip, json-tag field names, deterministic encoding, unique-slot stability | no |
| `query_test.go` | **the filter dialect**: every operator, dot paths, array semantics, type ordering, sort, skip/limit, projection | no |
| `sql_test.go` | **the SQL front end**: compilation of every supported form, agreement with the query engine, rejection of everything unsupported | no |
| `dopdb_test.go` | root integration: Collection methods, unique-index enforcement, atomic increment, namespace isolation, watch | **yes** |
| `api/api_test.go` | functional API pipeline (decode→validate→func) | no |
| `config/config_test.go` | config load/parse/validate | no |
| `httpserve/helpers_test.go` | shared test helpers (`setupHandler`/`tokenFor`/`do`/`decodeObj`) | — |
| `httpserve/api_dispatch_test.go` | HTTP routing/dispatch, command vocabulary, error shape | no |
| `httpserve/permission_persist_test.go` | permission `SaveJSON`/`LoadJSON` | no |
| `httpserve/httpon_test.go` | the HttpOn bitmask is the sole gate (zero Grant) | **yes** |
| `httpserve/integration_test.go` | end-to-end CRUD / owner isolation / multi-datasource / FIND shaping / 409 on a unique violation | **yes** |
| `httpserve/conformance_test.go` | **Go↔TS consistency**: starts a TS subprocess, drives both, diffs status+code+body per command (incl. all String/List/Set/ZSet + HScan/HRandField) | **yes** |

`query_test.go` and `codec_test.go` are new with the KVRocks migration, and they matter: MongoDB used to *be* the reference implementation of the filter dialect, and now dopdb is. They run on a bare checkout with no server.

## TypeScript test files (`ts/test/`)

`schema` `client` `permission` `sanitize` `prepare` `indexes` `config` `hardening` `browser-safety` `spec-export` `next-handler` `types.test-d` `watch-reconnect` — no server needed.

`query` `codec` `sql` — new, the twins of the Go files above; no server needed.

`server` `watch-e2e` — need a real server and auto-skip without `DOPDB_TEST_KVROCKS_URI`. `server.test.ts` used to run against a ~180-line in-memory fake Mongo; that fake is gone, because on KVRocks the commands **are** Redis commands (LPUSH, ZADD, HSCAN, WATCH/MULTI, pub/sub) and a hand-rolled fake of those would be testing the fake, not the engine.

## Running

```bash
# a server for the integration tests: KVRocks, or any Redis-protocol server
# (dopdb uses only the common command set)
kvrocks -c kvrocks.conf          # or: redis-server --port 6666
export DOPDB_TEST_KVROCKS_URI="redis://127.0.0.1:6666"

# Go
go test ./...                    # without the env var, the integration cases skip
make test-kvrocks                # integration only, verbose

# TS
( cd ts && npm test )

# Go↔TS conformance (starts a TS subprocess)
# if node is not on PATH: export DOPDB_TS_NODE=/path/to/node
go test ./httpserve -run Conformance -v
```

## Conventions

- Tests that need a real server are **always** gated by `DOPDB_TEST_KVROCKS_URI` (else `t.Skip` / TS `{ skip }`); they must not hard-fail without one.
- Each test takes a throwaway **key namespace** and deletes it on cleanup, so a shared server is safe and two suites can run against one instance at the same time (the conformance test relies on exactly this: the Go and TS servers get separate namespaces).
- **Two-engine consistency is recognized only via `conformance_test.go`**: the same set of requests is sent to both Go and TS, and the diff must be empty. A single-engine integration test **cannot** count as consistency evidence.
- Tests must not alter the system-under-test's gates/guards to pass; the key numbers are taken from real stdout.
