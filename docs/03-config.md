# 03 · Configuration (TOML · env-var secrets · wiring)

`config/` (Go) and `ts/src/config.ts` (TS) read the same TOML schema. Both pull in **no third-party dependency** (a tiny parser covering exactly this schema), and resolve secrets and connection strings from **environment variables** — never plaintext from a file.

## Schema

```toml
[http]
addr           = ":8080"
jwt_secret_env = "DOPTIME_JWT_SECRET"    # name of the env var holding the HS256 secret / RS256 PEM public key
cors_origins   = ["https://app.example.com"]

# multiple [[kvrocks]] allowed; exactly one must be name = "default"
[[kvrocks]]
name         = "default"
uri_env      = "DOPTIME_KVROCKS_URI"         # name of the env var holding the connection URL (may contain credentials)
uri          = "redis://localhost:6666"      # literal fallback (dev only); env wins
password_env = "DOPTIME_KVROCKS_PASSWORD"    # name of the env var holding the auth token
namespace    = "appdb"

[[kvrocks]]
name      = "analytics"
uri       = "redis://localhost:6666"
namespace = "analytics"
```

> `auto_auth` has been **removed**. A collection is reachable over HTTP only after it calls `.HttpOn(...)` (default off); see `02-http`.

## Why `namespace` and not `db`

KVRocks speaks the Redis protocol and has no databases-containing-collections. A datasource's logical database is therefore a **key prefix** applied to every key dopdb writes:

```
appdb:notes            the "notes" Hash collection
appdb:sessions:abc     one entry of the "sessions" String/List/Set/ZSet collection
```

Two datasources pointed at the same server with different namespaces are fully isolated — that is what `?ds=` selects between. If the server also uses KVRocks' **native** namespace tokens, `password` selects that namespace and `namespace` still partitions keys inside it; the two mechanisms compose.

## Parsing and validation

- `jwt_secret`: resolved from the env var named by `jwt_secret_env`; empty → error.
- Each `[[kvrocks]]`'s `uri`: the env var named by `uri_env` wins, else the literal `uri`.
- Each `[[kvrocks]]`'s `password`: the env var named by `password_env` wins, else the literal `password`.
- Validation: at least one `[[kvrocks]]`; a `name="default"` must exist; names unique; each source needs a `uri` that starts with `redis://` or `rediss://`, and a `namespace`.
  - A leftover `mongodb://` URI is rejected **at load time** rather than failing later at dial time.
- `Warnings()`: lists non-fatal risks (a literal uri containing credentials, a literal password) to print at startup.

## Wiring (Go)

```go
cfg, err := config.Load("config.toml")
if err != nil { log.Fatal(err) }
for _, w := range cfg.Warnings() { log.Println("warn:", w) }

// one-line serve: connect all sources → SetDatasources → Handler → CORS → listen
log.Fatal(httpserve.Serve(cfg))
```

`httpserve.Serve` builds `[]dopdb.DatasourceConfig` from `cfg.Kvrocks`, calls `ConnectDatasources`, then `SetDatasources`. Requests select a source with `?ds=<name>`, default `default`. Collections are exposed/authorized via their own `.HttpOn(...)`; an optional `httpserve.WithPermissions(perms)` still works as a runtime override.

### Connection pool

`ConnectDatasources` raises an unset or small pool to a floor of 64 connections. The client library defaults to 10 per CPU, which starves on a small container: the scoped write and the in-document increment each hold a connection across a `WATCH` round trip, so a handful of concurrent writers can exhaust the pool and surface as *"connection pool timeout"* rather than as honest contention. An explicit `?pool_size=` in the URL always wins.

## Wiring (TS)

```ts
import { serveFromConfig } from "@kequnyang/dopdb/server";
import { schema } from "./schema";  // collections declare .httpOn(...) themselves
await serveFromConfig("config.toml", { schema });
// serveFromConfig loads every [[kvrocks]] as a data source
```

## Relevant env vars

- `DOPTIME_JWT_SECRET` (example name): HS256 secret or RS256 PEM public key.
- `DOPTIME_KVROCKS_URI` (example name): the default data source's connection URL.
- `DOPTIME_KVROCKS_PASSWORD` (example name): the default data source's auth token.
- `DOPDB_TEST_KVROCKS_URI`: **test-only**; when set, integration tests run. Any Redis-protocol server works — dopdb uses only the common command set.
- `DOPDB_TS_NODE`: optional path to a Node ≥ 20.6 binary for the conformance test's TS subprocess (see `RUNBOOK`).
