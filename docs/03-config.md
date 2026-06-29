# 03 · Configuration (TOML · env-var secrets · wiring)

`config/` (Go) and `ts/src/config.ts` (TS) read the same TOML schema. Both pull in **no third-party dependency** (a tiny parser covering exactly this schema), and resolve secrets and connection strings from **environment variables** — never plaintext from a file.

## Schema

```toml
[http]
addr           = ":8080"
jwt_secret_env = "DOPTIME_JWT_SECRET"    # name of the env var holding the HS256 secret / RS256 PEM public key
cors_origins   = ["https://app.example.com"]

# multiple [[mongo]] allowed; exactly one must be name = "default"
[[mongo]]
name    = "default"
uri_env = "DOPTIME_MONGO_URI"            # name of the env var holding the connection string (may contain credentials)
uri     = "mongodb://localhost:27017"    # literal fallback (dev only); env wins
db      = "appdb"

[[mongo]]
name = "analytics"
uri  = "mongodb://localhost:27017"
db   = "analytics"
```

> `auto_auth` has been **removed**. A collection is reachable over HTTP only after it calls `.HttpOn(...)` (default off); see `02-http`.

## Parsing and validation

- `jwt_secret`: resolved from the env var named by `jwt_secret_env`; empty → error.
- Each `[[mongo]]`'s `uri`: the env var named by `uri_env` wins, else the literal `uri`.
- Validation: at least one `[[mongo]]`; a `name="default"` must exist; names unique; each source needs a `uri` and a `db`.
- `Warnings()`: lists non-fatal risks (e.g. "literal uri contains credentials — use `uri_env`") to print at startup.

## Wiring (Go)

```go
cfg, err := config.Load("config.toml")
if err != nil { log.Fatal(err) }
for _, w := range cfg.Warnings() { log.Println("warn:", w) }

// one-line serve: connect all sources → SetDatasources → Handler → CORS → listen
log.Fatal(httpserve.Serve(cfg))
```

`httpserve.Serve` builds `[]dopdb.DatasourceConfig` from `cfg.Mongo`, calls `ConnectDatasources`, then `SetDatasources`. Requests select a source with `?ds=<name>`, default `default`. Collections are exposed/authorized via their own `.HttpOn(...)`; an optional `httpserve.WithPermissions(perms)` still works as a runtime override.

## Wiring (TS)

```ts
import { serveFromConfig } from "@kequnyang/dopdb/server";
import { schema } from "./schema";  // collections declare .httpOn(...) themselves
await serveFromConfig("config.toml", { schema });
// serveFromConfig loads every [[mongo]] as a data source
```

## Relevant env vars

- `DOPTIME_JWT_SECRET` (example name): HS256 secret or RS256 PEM public key.
- `DOPTIME_MONGO_URI` (example name): the default data source's connection string.
- `DOPDB_TEST_MONGO_URI`: **test-only**; when set, integration tests run (watch needs a replica set).
- `DOPDB_TS_NODE`: optional path to a Node ≥ 20.6 binary for the conformance test's TS subprocess (see `RUNBOOK`).
