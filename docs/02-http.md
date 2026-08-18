# 02 · HTTP layer & security model (URL · `@`-binding · command vocabulary · permissions · row-level isolation · watch)

The HTTP layer exposes the schema as a **closed** set of data commands plus a functional API, enforcing JWT `@`-binding, row-level isolation, and permissions at the boundary. Go lives in `httpserve/`, TS in `ts/src/server.ts`; behavior matches.

## URL scheme

- **Data commands**: `/api/<cmd>/<coll>` — two segments after `/api/`: command, then collection.
- **Functional API**: `/api/<name>` — one segment after `/api/`.
- **Data source**: query parameter `?ds=<name>`, default `default`. **The source is not in the path.**
- **Keys**: query parameter `?f=` (repeatable, for hmget/hdel/etc.); `?f=@uid` triggers `@`-resolution.
- **Queries**: the filter for `find/findone/count` goes in the **POST body** (JSON); plus `?limit= &skip= &s=<sort JSON> &p=<projection JSON>`. Range/score params for List/ZSet via query (`?start=&stop=`, `?min=&max=`, `?count=`, `?withscores=1`).

Disambiguation: `/api/` followed by ≥2 segments whose first is in the vocabulary → data command; otherwise it is treated as `/api/<name>` functional API.

## Command vocabulary (closed)

**Hash**: `hget hset hsetnx hdel del hexists hgetall hkeys hvals hlen hincrby hincrbyfloat hmset hmget count find findone watch hscan hscannovalues hrandfield`

**String**: `strget strset strsetall strgetall strdel` (STR* prefix avoids clashing with Set's S*)

**List**: `lpush rpush lpop rpop lrange llen lindex lset lrem ltrim linsertbefore linsertafter` (blocking ops not implemented)

**Set**: `sadd srem smembers sismember scard`

**ZSet**: `zadd zrem zscore zcard zcount zincrby zrange zrevrange zrangebyscore zrevrangebyscore zrank zrevrank zpopmin zpopmax zremrangebyrank zremrangebyscore`

Selected Hash semantics:

| Command | Method | Semantics |
|---|---|---|
| `hget` | GET | get one (scoped: only mine, else 404) |
| `hset` | POST | upsert one (body is the value; scoped write to another's id → 403) |
| `hsetnx` | POST | write only if absent (existing → `{inserted:false}`, never 403) |
| `hdel`/`del` | POST/GET | delete one/many (`?f=` repeatable) |
| `hgetall`/`hkeys`/`hvals`/`hlen` | GET | all values / keys / count (scoped: only mine) |
| `hincrby`/`hincrbyfloat` | GET | atomic `$inc` (int / float) |
| `hmset`/`hmget` | POST/GET | batch write (`{id:{...}}`) / batch get (aligned to input, missing = null) |
| `count`/`find`/`findone` | POST | count / array / first (filter in body; scoped AND-ed) |
| `hscan`/`hscannovalues`/`hrandfield` | GET | glob scan (paginated) / random keys |
| `watch` | GET | change stream → SSE |

Reads are GET, writes are POST, `watch` is GET + SSE. Every command is guarded by the Go↔TS conformance harness.

## `@`-binding (anti-forgery identity injection)

For each request the server builds a context: verified JWT claims + server-injected `@key` (collection), `@field` (default = record key), `@remoteAddr`, `@host`, `@method`, `@path`, `@rawQuery`.

- On write, fields tagged with `@uid` etc. are filled from the context, and **any `@`-key sent by the client is stripped** — identity cannot be forged.
- `@`-markers in keys: `@uuid`/`@nanoid` are generated on the fly; `@<claim>` comes from the JWT. So `?f=@uid` means "my own record"; a missing claim **fails closed** (rejected).

## SQL

`POST /api/sql/<coll>` (body = the statement, or `{"sql":"..."}`) and `GET /api/sql/<coll>?q=<statement>` run a read-only `SELECT` against a **Hash** collection. It is a front end over the same query engine `FIND` uses — same cost, same operator set, same owner scope — with its own `Perm` bit so a collection exposes it explicitly. Rejected statements are `400 validation`. Full grammar and the security checks: `05-sql`.

## Row-level isolation (owner scope)

redisdb got tenant isolation for free from key naming (e.g. `userInfo<uid>`) — at the cost of every caller remembering to name keys that way. dopdb keeps isolation explicit instead, as a predicate the framework enforces.

- Declare: `dopdb.SetOwnerScope("orders", "owner", "uid")` (document `owner` field == JWT claim `uid`). TS: collection `.ownerScope("owner")`.
- Whole-collection reads (hgetall/hkeys/hlen/find/...) are forced to AND `{owner: me}`; the client cannot widen it.
- Per-key ops (scoped hget/hset/hdel) intersect `{_id, owner}` and write through a `WATCH`/`MULTI` compare-and-set, eliminating a "read-then-write" race; writing another's id → 403, reading another's id → 404.
- If a collection declares a scope but the request lacks the claim → rejected.
- The same model applies to String/List/Set/ZSet — but a native Redis list has no document in which to store an owner, so for those four the predicate is enforced against the collection's owner index (`<ns>:<coll>:__owner`, key → owner): the first writer claims the key, and afterwards a foreign key reads as absent and writes as 403.

## Permissions (expose + authorize via HttpOn)

A collection is **off by default** on the HTTP side: until it calls `HttpOn(...)`, its data commands return 403. `HttpOn(...)` both registers the collection and declares which commands the client may call, as a bitmask:

```go
notes.HttpOn()                                 // no args = debug: ALL commands on
notes.HttpOn(dopdb.ReadOnly)                    // reads only
notes.HttpOn(dopdb.HGet | dopdb.HSet | dopdb.HDel) // exact set
```

`Perm` is a `uint64` bitmask (one bit per command across all types); groups `ReadOnly` / `Writes` / `All` / `HashAll` (= All). Tighten at runtime with `dopdb.SetHttpPerm(coll, ...)`; introspect with `dopdb.HttpPermNames(p)`. The gate is `dopdb.HttpAllowed(cmd, coll)`. A legacy `httpserve.Permissions` map (`Grant`/`Deny`, JSON-persistable) still works as a runtime override and is OR-ed with the bitmask. The functional API is gated under `API::<name>`.

## watch (change channel → SSE)

`GET /api/watch/<coll>` returns `text/event-stream`, one line per change `data: {"type","id","doc"}`.

KVRocks has no change stream, so dopdb publishes its own: every mutating Hash operation `PUBLISH`es to `<ns>:<coll>:__events`, and a watcher subscribes to that channel. Two things this buys, and two it costs.

Buys:

- **No replica set**, and no `notify-keyspace-events` configuration — it works on a stock KVRocks.
- The event carries the decoded document, not a cursor token.

Costs, stated plainly:

- **No replay.** Redis pub/sub is fire-and-forget. There is no resume token, the server sends **no SSE `id:` line**, `Last-Event-ID` is deliberately ignored rather than pretending to resume, and a reconnect starts fresh — changes during the gap are missed.
- **Only writes made through dopdb are visible.** A process writing the same keys with `redis-cli` is invisible to watchers.

Unchanged: under owner-scope, an event is delivered only when its document satisfies the predicate — and **a delete carries no document, so it is not delivered under scope**.

## JWT

`Authorization: Bearer <token>`. Supports **HS256** (HMAC secret) and **RS256** (PEM/SPKI public-key verification); rejects `none` and unknown algorithms; checks `exp`.

## Error wire protocol

Every non-2xx response uses a uniform shape: `{ "error": "...", "code": "..." }`. Status ↔ `code` is fixed at 5 classes:

| Status | code | Meaning |
|---|---|---|
| 400 | `validation` | invalid params/input; may also carry `fields` per-field detail |
| 401 | `unauthorized` | not authenticated / invalid JWT |
| 403 | `forbidden` | not authorized, or row-level isolation denial (write to another → 403) |
| 404 | `not_found` | document missing or collection not registered |
| 409 | `conflict` | a `index:"unique"` violation (`ErrDuplicate`) — enforced by dopdb itself, since KVRocks has no unique index |
| 500 | `error` | internal server error fallback |

Both engines match; the client reconstructs a typed error from `code` (preferred) → `status` for `instanceof` branching.

## One-line serve (Go)

```go
cfg, _ := config.Load("config.toml")            // read all [[kvrocks]]
log.Fatal(httpserve.Serve(cfg))
// connect all sources → SetDatasources → NewHandler → CORS → ListenAndServe
```
