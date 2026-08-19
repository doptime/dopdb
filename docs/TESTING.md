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
| `httpserve/conformance_test.go` | **Go↔TS consistency**: starts a TS subprocess, drives both, diffs status+code+body | **yes** |
| `httpserve/jwt_algconfusion_test.go` | JWT algorithm pinning, alg-confusion rejection, exp fail-closed | no |
| `topn_test.go` | bounded result retention returns exactly what a full sort would | no |
| `bench_test.go` / `mem_test.go` | query-engine benchmarks and the retained-memory bound | **yes** |

`query_test.go` and `codec_test.go` are new with the KVRocks migration, and they matter: MongoDB used to *be* the reference implementation of the filter dialect, and now dopdb is. They run on a bare checkout with no server.
## TypeScript test files (`ts/test/`)

`schema` `client` `permission` `sanitize` `prepare` `indexes` `config` `hardening` `browser-safety` `spec-export` `next-handler` `types.test-d` `watch-reconnect` — no server needed.

`query` `codec` `sql` `topn` `jwt` `exports` — the twins of the Go files above; no server needed.

`server` `watch-e2e` — need a real server and auto-skip without `DOPDB_TEST_KVROCKS_URI`. `server.test.ts` used to run against a ~180-line in-memory fake Mongo; that fake is gone, because on KVRocks the commands **are** Redis commands (LPUSH, ZADD, HSCAN, WATCH/MULTI, pub/sub) and a hand-rolled fake of those would be testing the fake, not the engine.

## Stress / load harness (`cmd/stress` + `ts/stress`)

Throughput and latency live in their own harness, separate from the conformance
suite: a single binary, `bin/stress`, with two subcommands.

| Piece | Role |
|---|---|
| `cmd/stress/serve.go` | boots a real dopdb **Go** server on `--addr` (default `:8091`) against `--kvrocks`, owner-scoped `notes` + `kv`/`queue`/`tags`/`board` collections, all `.HttpOn()`, then seeds `--users x --docs-per-user` notes and prints `DOPDB_GO_READY` |
| `ts/stress/server.ts` | the exact TS mirror: same schema/seed/JWT, `DOPDB_TS_READY` marker (run with `ts/node_modules/.bin/tsx ts/stress/server.ts` from `ts/`; `bench/run-all.sh` does this for you — avoid `node --import tsx`, which breaks on Node < 20) |
| `cmd/stress/load.go` | the **engine-neutral** client: mints a real HS256 token per user, drives N concurrent workers over HTTP for a fixed wall time, and writes a per-run JSON report (total ops, ops/s, errors, per-op p50/p90/p99) |
| `bench/run.sh` | runs one full scenario matrix (9 scenarios × 3 concurrency levels) against a single running server |
| `bench/run-all.sh` | the whole comparison: flush the namespace, start the Go server, matrix, start the TS server, matrix — results in `bench/results/{go,ts}_<scenario>_c<conc>.json` |

Scenarios: `crud` (hget/hset/hsetnx/hexists mix on a user's own rows),
`find` (owner-scoped FIND with a selective filter), `fanout` (unscoped
collection-wide reads), `list` (lpush/rpush/lrange), `zset` (zadd/zrange),
`set` (sadd/smembers), `str` (strset/strget), `incr` (HINCRBY on a dedicated
counter row), and `mix` (all of the above in one window).

```bash
# KVRocks (or any Redis-protocol server) on :6666, then:
make stress-all        # ~5 min per engine, 9×3 runs each -> bench/results/
```

Both engines are driven by the same client over the same HTTP contract, so the
reports are directly comparable. **The load harness measures performance; it
does not prove equivalence** — that remains `conformance_test.go`'s job.

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
### What conformance covers

**What the harness covers today** (`TestConformance*`): the Hash family
(hget/hset/hsetnx/hdel/del/hexists/hgetall/hkeys/hvals/hlen/hmget/count/find/
findone/hscan/hscannovalues/hrandfield), the full String/List/Set/ZSet command
sets, SQL, sort+projection shaping, owner-scope, method enforcement (writes are
POST-only on both engines), the error-status classes (400/401/403/404/405), and
the response *shapes* that differ structurally (hgetall is a map, hvals an array).

**What it does not cover yet**: `watch` event streams, TTL expiry, `?ds=`
selection, and 409/413 differentials. Those are named here rather than implied to
be covered — the claim "every command is covered" was in four documents while the
suite had sixteen cases, and the two defects found in the last audit (hgetall's
response shape, hash commands against a non-hash collection) both sat squarely in
the uncovered set. That is not a coincidence: an overstated coverage claim is
worse than an honest gap, because it stops people looking.

Both engines are driven through their real permission gate. The harness used to
pass `permit: () => true` to the TypeScript server, which made it allow
everything — so every `403` the Go engine produced was an unreachable difference,
and the gate was the one thing the consistency suite could never compare.

- **Two-engine consistency is recognized only via `conformance_test.go`**: the same set of requests is sent to both Go and TS, and the diff must be empty. A single-engine integration test **cannot** count as consistency evidence.
- Tests must not alter the system-under-test's gates/guards to pass; the key numbers are taken from real stdout.
