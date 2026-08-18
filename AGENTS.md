# dopdb: Master Developer Context (Agent Cookbook)

Terse, full-coverage usage reference. For an AI coding agent or an experienced developer to follow. Philosophy first; per-topic depth in `docs/`.

**Core Philosophy**
1. **One schema, two equivalent engines**: the same schema drives both Go and TypeScript as **complete equivalent implementations** (same URL wire protocol, same command vocabulary, same `@`-binding / isolation / permission model). Mix freely (Go server + TS client, or vice versa).
2. **The frontend talks to data, no API layer**: the frontend writes no fetch code — it calls "database methods" (`db.coll.hget(...)`), and the framework does auth / isolation / routing. Reach for functional APIs only for complex logic.
3. **String keys only**: keys are always strings. Integer keys are forbidden (JS large-integer precision loss). Convert all IDs to strings.
4. **Direct to KVRocks**: the root package speaks the Redis protocol directly (`github.com/redis/go-redis/v9`) — no Store abstraction. A Hash collection is one Redis hash; String/List/Set/ZSet are the native types, so their commands are single atomic commands. Values are CBOR (`github.com/fxamacker/cbor/v2`), field names from the `json` tags.
7. **SQL is available, read-only and Hash-only**: `SELECT ... FROM <coll> WHERE ... ORDER BY ... LIMIT ...` over a Hash collection, as a front end to the same query engine (`docs/05-sql.md`). No JOIN, no GROUP BY, no writes. It costs what FIND costs.
8. **Know what the store cannot do**: `FIND`/`COUNT`/`FINDONE` scan the collection and filter in-process — O(collection), not O(result). The only enforceable index is `unique`. There is no per-document TTL. `watch` cannot replay. Design against these, do not discover them in production.
5. **Closed command vocabulary**: only the commands listed in §3 exist; anything else is a 400.
6. **`@`-binding**: the server injects `@`-prefixed context (user, request info, target metadata); any `@`-key sent by the client is stripped (anti-forgery).

---

## 1. Infrastructure & Config

**DB**: KVRocks (any Redis-protocol server works; no replica set, no `notify-keyspace-events`). **Config**: `config.toml` (local) or `CONFIG_URL` (prod). **Multiple data sources**: define several `[[kvrocks]]`; pick per-request with `?ds=<name>`, default `default`. The data source is **not** in the path.

`namespace` is the KVRocks stand-in for a database name: it is a **key prefix** applied to every key the datasource writes, because Redis has no databases-containing-collections.

```toml
[[kvrocks]]
  name         = "default"
  uri_env      = "DOPTIME_KVROCKS_URI"        # env wins over the literal below
  uri          = "redis://127.0.0.1:6666"
  password_env = "DOPTIME_KVROCKS_PASSWORD"   # KVRocks namespace token, if used
  namespace    = "app"
[http]
  addr           = ":8080"
  jwt_secret_env = "DOPTIME_JWT_SECRET"       # HS256 secret; RS256 uses a PEM/SPKI public key
  cors_origins   = ["https://app.example.com"]
```

**Key layout** (worth knowing when you inspect the store by hand):

```
<ns>:<coll>                  HASH   Hash collection: field = document id, value = CBOR document
<ns>:<coll>:<key>            native STRING / LIST / SET / ZSET
<ns>:<coll>:__owner          HASH   row isolation for the non-Hash types
<ns>:<coll>:__uniq:<field>   HASH   unique-index claims
<ns>:<coll>:__events         channel for watch
```

---

## 2. URL / Wire Protocol (authoritative reference)

- **Data commands**: `/<base>/<cmd>/<coll>?ds=<name>` (base defaults to `/api`). E.g. `/api/hget/notes?f=n1`.
- **Functional API**: `/api/<name>`.
- **Keys**: `?f=<id>` (repeatable: `?f=a&f=b`); `?f=@uid` = "my own row".
- **find options**: `?s=<json>` sort, `?p=<json>` projection (both reject `$`-operators → 400), `?limit=<n>` (default 100, max 1000).
- **Method**: reads = `GET`, writes = `POST`, `watch` = `GET` + SSE (`text/event-stream`).
- **Body**: a write command's value goes in the JSON body (max 1 MiB, over → 413).
- **Errors**: JSON `{ "error": "...", "code": "<class>" }`, see §8.

---

## 3. Command vocabulary (closed)

### Hash (the core type)
| Class | Commands |
|---|---|
| read | `hget` `hgetall` `hkeys` `hvals` `hlen` `hexists` `hmget` `count` `find` `findone` `hscan` `hscannovalues` `hrandfield` |
| write | `hset` `hsetnx` `hdel` `del` `hincrby` `hincrbyfloat` `hmset` |
| stream | `watch` (dopdb pub/sub channel → SSE; no replay, no resume) |

`hset` on an existing key overwrites; `hsetnx` on an existing key (regardless of owner) returns `{inserted:false}` — never 403, never leaks ownership.

### String (`{_id, v, expireAt?}`)
`strget` `strset` `strsetall` `strgetall` `strdel`. `strset` accepts an optional expiration (TTL, see §7). `strgetall` takes a glob `?match=`.

### List (`{_id, items[]}`)
`lpush` `rpush` `lpop` `rpop` `lrange` `llen` `lindex` `lset` `lrem` `ltrim` `linsertbefore` `linsertafter`. Pops are atomic (`findOneAndUpdate` + `$pop`). Blocking ops (`blpop`/`brpop`/`brpoplpush`) are **not** implemented.

### Set (`{_id, members[]}`)
`sadd` `srem` `smembers` `sismember` `scard`.

### ZSet (`{_id, members:[{m,score}]}`)
`zadd` `zrem` `zscore` `zcard` `zcount` `zincrby` `zrange` `zrevrange` `zrangebyscore` `zrevrangebyscore` `zrank` `zrevrank` `zpopmin` `zpopmax` `zremrangebyrank` `zremrangebyscore`. Range/score params via query: `?start=&stop=`, `?min=&max=`, `?count=`, `?withscores=1`.

> Every command above is covered by the Go↔TS conformance harness (both engines, one server, separate namespaces, per-command diff). Naming avoids collisions: String uses the `STR*` prefix so it never clashes with Set's `S*`.

---

## 4. Backend (Go)

**Lang**: Go 1.24+ **Package**: `github.com/doptime/dopdb` (+ `api/` `httpserve/` `config/`).

### 4.1 Hash collection (the primary, fullest-surface type)

```go
import "github.com/doptime/dopdb"

type Note struct {
    ID    string `json:"id"`                          // the key (string)
    Owner string `json:"owner"`                       // owner field (for owner-scope)
    Text  string `json:"text" validate:"required"`
}

// Factory: New[K, V](opts...). K = key type (string), V = value type (pointer or value).
// json tags name the stored CBOR fields too — one set of names end to end.
// (bson tags are gone: they meant nothing once the store stopped being Mongo.)
var Notes = dopdb.New[string, *Note](
    dopdb.WithCollection("notes"),   // collection name (else derived from V's type name)
    dopdb.WithDB("default"),          // specify only for a non-default data source
).HttpOn(dopdb.HGet | dopdb.HGetAll | dopdb.HSet | dopdb.HDel)
```

Hash exposes both **native Go methods** (`HGet`/`HSet`/`HSetNX`/`Save`/`HMGet`/`HMSet`/`HGetAll`/`HDel`/`Del`/`HExists`/`HKeys`/`HVals`/`HLen`/`HIncrBy`/`HIncrByFloat`/`HRandField`/`HScan`/`HScanNoValues`) **and** the HTTP command layer.

### 4.2 HttpOn: expose + authorize (one declaration)

`HttpOn(...)` registers a collection to the HTTP layer **and** declares which commands the client may call — doptime/redisdb style. It replaces a separate `RegisterHttp` + per-command `Grant`.

```go
Notes.HttpOn()                                   // no args = debug: ALL commands on
Notes.HttpOn(dopdb.ReadOnly)                      // reads only
Notes.HttpOn(dopdb.HGet | dopdb.HGetAll | dopdb.HSet | dopdb.HDel) // exact set
Notes.HttpOn(dopdb.HashAll)                        // = All, doptime-compatible alias
```

**Perm bits** (`dopdb.Perm`, a `uint64` bitmask — one bit per command across all types). **Groups**: `ReadOnly` (all reads), `Writes` (all writes), `All` (everything), `HashAll` (= All alias). Recommended flow: start with `.HttpOn()` (all on) to wire things up, then have an audit agent tighten it (edit the flags, or `dopdb.SetHttpPerm("notes", dopdb.HGet, dopdb.HSet)` at runtime; introspect with `dopdb.HttpPermNames(p)`). The gate is `dopdb.HttpAllowed(cmd, coll)`; the legacy `httpserve.Permissions` map still works as a runtime override (OR-ed).

### 4.3 The redisdb-compatible data structures (String / List / Set / ZSet)

Each is a first-class Go type backed by the **native Redis type**, registered + authorized via `HttpOn`, and reached over the **wire commands** in §3:

```go
cfg  := dopdb.NewString[string](dopdb.WithCollection("cfg")).HttpOn()   // STR* commands
queue := dopdb.NewList[string, *Job](dopdb.WithCollection("queue")).HttpOn() // L*/R* commands
tags := dopdb.NewSet[string](dopdb.WithCollection("tags")).HttpOn()      // S* commands
board := dopdb.NewZSet[string](dopdb.WithCollection("board")).HttpOn()    // Z* commands
```

- Storage: one native Redis key per entry at `<ns>:<coll>:<key>`. Values/elements/members are CBOR; ZSet members are plain strings.
- **Owner-scope** applies identically in effect, but a native list has no document to hold an owner field — so for these four it is enforced against the collection's `__owner` index: first writer claims the key, a foreign key then reads as absent and writes as 403 (see §4.5).
- These types expose the **HTTP command layer** (handlers `HttpStrGet`, `HttpSAdd`, `HttpLPush`, `HttpZAdd`, …) and are reached via the §3 wire commands; the Hash family additionally has native non-HTTP Go methods.
- **TTL**: `strset` with an expiration issues `SET ... EX` — a real per-key TTL. `EnsureTTL(...)` is kept for source compatibility and does nothing (§7).
- **`LPUSH` with several items** lands them head-first *in the order given* (`[a,b]` onto `[c]` → `[a,b,c]`), preserving the previous behaviour; raw Redis would give `[b,a,c]`.

### 4.4 `@`-binding

Server-injected, client `@`-keys stripped:
- **Identity** (JWT): `@uid`, `@email`, `@role`, …
- **Request info**: `@remoteAddr`, `@host`, `@method`, `@path`, `@rawQuery`.
- **Target metadata**: `@key` (collection key), `@field` (field).
`?f=@uid` = "my own row". Go structs receive the corresponding json-tagged field (injected per owner-scope / binding).

### 4.5 owner-scope (row-level isolation)

```go
// Declare: collection notes is isolated by field owner, bound to JWT claim "uid".
dopdb.SetOwnerScope("notes", "owner", "uid")
```
Whole-collection reads (`hgetall/find/count/hkeys/hlen`) are forced to AND `{owner: me}`; per-key ops verify ownership. The client cannot widen it. The same model applies to the String/List/Set/ZSet collections.

### 4.6 Functional API (when plain CRUD isn't enough)

```go
import "github.com/doptime/dopdb/api"

type SyncReq struct{ Email string `json:"email" validate:"required,email"` }
type SyncRes struct{ Status string `json:"status"` }

// Exposed at /api/sync (Req suffix dropped, lowercased). Pipeline: decode → Validate → Func.
var SyncApi = api.Api(func(req *SyncReq) (*SyncRes, error) {
    return &SyncRes{Status: "ok"}, nil
})
```

### 4.7 Boot

```go
import (
    "log"
    "github.com/doptime/dopdb/config"
    "github.com/doptime/dopdb/httpserve"
)
func main() {
    cfg, _ := config.Load("config.toml")
    // Collections registered+authorized via .HttpOn(); just serve.
    log.Fatal(httpserve.Serve(cfg))
}
```

---

## 5. Frontend / Server (TypeScript)

**Package**: `dopdb`. Browser uses `dopdb/client`, Node server uses `dopdb/server`. The TS engine is an equivalent re-implementation: the **server** handles the full command vocabulary of §3 (conformance-verified against Go); the **typed client** today exposes the Hash family (`db.coll.hget/hset/hgetall/hdel/...`). For String/List/Set/ZSet, drive the §3 wire commands (typed client wrappers for them are a follow-up).

### 5.1 Define the schema (shared by both engines)

```ts
import { collection, f, HGet, HGetAll, HSet, HDel, ReadOnly, All } from "@kequnyang/dopdb";

export const schema = {
  Notes: collection({
    _id: f.string(),
    owner: f.string().bind("@uid"),     // bind: owner comes from the JWT uid; client can't change it
    text: f.string().required(),
  })
    .named("notes")
    .ownerScope("owner")                  // row-level isolation
    .httpOn(HGet | HGetAll | HSet | HDel),// expose + authorize; no args = All (debug)
};
```

`f`: `f.string()` `f.number()` `f.boolean()` `f.object(...)` …; chain `.required()`, `.bind("@uid")`, `.default(x)`, etc.
`.httpOn(...)`: same meaning as Go. The Perm constants (`HGet`…`Watch`, `HScan`/`HScanNoValues`/`HRandField`, `ReadOnly`/`Writes`/`All`/`HashAll`, plus the String/List/Set/ZSet command bits) are exported from `@kequnyang/dopdb` as **BigInt** (the bitmask exceeds 32 bits across all types); bit values match Go.

### 5.2 Browser client (no fetch code)

```ts
import { clientDb } from "@kequnyang/dopdb/client";
import { schema } from "./schema";

const db = clientDb(schema, {
  baseUrl: "https://api.example.com",
  getToken: async () => await getJWT(),   // static string or async function
});

await db.notes.hset("@uuid", { text: "buy milk" }); // create, server generates id
const mine = await db.notes.hgetall();               // Record<id, Note>, only mine
await db.notes.hset(id, { text: "edit" });           // update
await db.notes.hdel(id);                             // delete
```

| Op | Method | Key strategy |
|---|---|---|
| List | `hgetall()` | all of my hash (owner-scope filtered) |
| Create | `hset("@uuid", v)` | `"@uuid"` triggers server-side id generation |
| Update | `hset(id, v)` | existing id |
| Delete | `hdel(id)` | — |

### 5.3 Node server (the equivalent of Go)

```ts
import { serve } from "@kequnyang/dopdb/server";
import { schema } from "./schema";

const srv = await serve({
  schema,
  kvrocks: { uri: process.env.KVROCKS_URI!, namespace: "app" },
  jwtSecret: process.env.JWT_SECRET!,
  // No permit/permissions: each collection's .httpOn() bitmask authorizes (same as Go).
  port: 8080,
});
```

`serverDb(schema, backend)` gives typed server-side collections in Node; `defineApi(fn)` defines a functional API.

---

## 6. Security & Architecture Constraints

1. **String-key rule**: large integers as keys are corrupted by JS. `hGet("123…")` is safe, `hGet(123…)` is dangerous. Always stringify keys both sides.
2. **`@`-anti-forgery**: the framework **removes** any `@`-prefixed key the client sends, then injects the system `@`-context. The client cannot forge identity / ownership.
3. **owner-scope**: once declared, whole-collection reads are forced to AND `{owner: me}`, no hand-written `WHERE`, no cross-tenant leak.
4. **filter/sort/projection sanitization**: `find` filter / `?s=` / `?p=` reject `$`-operators and illegal paths → 400 (both engines).
5. **JWT**: HS256 and RS256 (RS256 verifies with a PEM/SPKI public key); `alg:none` is rejected.
6. **Data-command default**: a collection that has not called `.httpOn()` → its data commands are 403 (must be explicitly exposed).
7. **Irreversible**: secrets only via config/env, never in code or logs.

---

## 7. watch + TTL

- **watch** = dopdb's own pub/sub channel → SSE (`text/event-stream`). No replica set and no server configuration needed. Under owner-scope it filters by `{owner: me}`, and a delete event isn't delivered under scope (no document to scope on). The client subscribes via `GET /api/watch/<coll>`.
  - **No replay.** Redis pub/sub is fire-and-forget: there is no resume token, the server sends no SSE `id:` line, `Last-Event-ID` is ignored, and a reconnect starts fresh. Only writes made **through dopdb** are visible — a process writing the same keys with `redis-cli` is invisible.
- **TTL**: per-**key** only, and native — `strset` with an expiration issues `SET ... EX` and KVRocks expires the key itself. A per-**document** TTL inside a Hash collection **cannot be expressed** (the collection is one Redis key); a `.ttl(...)` schema declaration is recorded and inert.
- **Indexes**: only `index:"unique"` is enforced (dopdb maintains a claim hash; a violation is **409**, `ErrDuplicate`). It is sparse: a missing value claims nothing. `1`/`-1`/`text`/`2dsphere` are accepted and inert.

---

## 8. Error taxonomy (5 classes + 500)

| HTTP | code | Meaning |
|---|---|---|
| 400 | `validation` | validation failed / unknown command / illegal sort/proj/filter |
| 401 | `unauthorized` | JWT missing/invalid |
| 403 | `forbidden` | command not authorized (HttpOn off) / accessing another user's data |
| 404 | `not_found` | key not found / collection not registered |
| 409 | `conflict` | unique-constraint conflict, etc. |
| 500 | (server) | internal error |

Both engines match field-for-field (a conformance test guards `status` + `code`).

---

## 9. Testing (standard suite)

See `docs/TESTING.md`. Go unit tests live beside the code (`*_test.go`) — `query_test.go` and `codec_test.go` pin the filter dialect and the storage format and need no server; tests needing a real KVRocks are gated by `DOPDB_TEST_KVROCKS_URI` (skipped if unset); cross-implementation consistency is guarded by `httpserve/conformance_test.go` (it starts a TS subprocess, drives both engines, and diffs every command — including all String/List/Set/ZSet commands). **Never substitute a single-engine test for a consistency claim.**

---

## 10. Meta-Instructions for AI Code Generation

Follow strictly:

1. **Keys are always strings**; `"@uuid"` triggers server-side id generation; `?f=@uid` means "my row".
2. **Backend (Hash)**: `dopdb.New[string, *T](dopdb.WithCollection("name"))`; **expose + authorize with `.HttpOn(...)`** (debug `.HttpOn()` first, then tighten) — do not write `RegisterHttp` + per-command `Grant`. Struct tags `json` + `validate` (no `bson`). Multi-tenant: `dopdb.SetOwnerScope(coll, ownerField, claim)`.
3. **Backend (String/List/Set/ZSet)**: `dopdb.NewString[K](...)` / `NewList[K,E](...)` / `NewSet[K](...)` / `NewZSet[K](...)`, then `.HttpOn(...)`; reach them via the §3 wire commands; owner-scope and TTL apply as in §4.3/§4.5/§7.
4. **Frontend**: `collection(shape).named().ownerScope().httpOn(...)`; `clientDb(schema, {baseUrl, getToken})`; call `db.coll.hset/hgetall/hdel` directly — **no fetch, no API layer**.
5. **Permissions**: data commands are 403 by default; a collection must `.httpOn(...)` to be reachable; `.httpOn()` with no args = all on (debug only — always tell the user to tighten it).
6. **`@`-keys**: never have the client send `@uid`/`@owner` etc. — the framework strips and injects them; bind ownership with `.bind("@uid")` (TS) or an owner-scope declaration.
7. **Commands**: use only the §3 vocabulary; reads GET, writes POST, watch SSE. Blocking list ops are not available.
8. **Consistency**: any change to two-engine behavior must be verified with `conformance_test.go` (drive both engines, diff empty); never substitute a single-engine test.
9. **Imports**: TS permission constants come from `@kequnyang/dopdb` (`HGet`/`ReadOnly`/`All`/… as BigInt); browser `@kequnyang/dopdb/client`, Node `@kequnyang/dopdb/server`.
