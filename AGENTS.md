# dopdb: Master Developer Context (Agent Cookbook)

Terse, full-coverage usage reference. For an AI coding agent or an experienced developer to follow. Philosophy first; per-topic depth in `docs/`.

**Core Philosophy**
1. **One schema, two equivalent engines**: the same schema drives both Go and TypeScript as **complete equivalent implementations** (same URL wire protocol, same command vocabulary, same `@`-binding / isolation / permission model). Mix freely (Go server + TS client, or vice versa).
2. **The frontend talks to data, no API layer**: the frontend writes no fetch code — it calls "database methods" (`db.coll.hGet(...)`), and the framework does auth / isolation / routing. Reach for functional APIs only for complex logic.
3. **String keys only**: keys are always strings. Integer keys are forbidden (JS large-integer precision loss). Convert all IDs to strings.
4. **Direct to Mongo**: the root package uses `go.mongodb.org/mongo-driver/v2` directly — no Store abstraction. `$inc`, change streams, unique indexes, `2dsphere` all available.
5. **Closed command vocabulary**: only the commands listed in §3 exist; anything else is a 400.
6. **`@`-binding**: the server injects `@`-prefixed context (user, request info, target metadata); any `@`-key sent by the client is stripped (anti-forgery).

---

## 1. Infrastructure & Config

**DB**: MongoDB (watch needs a replica set). **Config**: `config.toml` (local) or `CONFIG_URL` (prod). **Multiple data sources**: define several `[[mongo]]`; pick per-request with `?ds=<name>`, default `default`. The data source is **not** in the path.

```toml
[[mongo]]
  Name = "default"
  URI  = "mongodb://127.0.0.1:27017/?replicaSet=rs0"
  DB   = "app"
[http]
  Port        = 8080
  JWTSecret   = "..."          # HS256 secret; RS256 uses a PEM/SPKI public key
  CORSOrigins = ["https://app.example.com"]
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
| stream | `watch` (change stream → SSE) |

`hset` on an existing key overwrites; `hsetnx` on an existing key (regardless of owner) returns `{inserted:false}` — never 403, never leaks ownership.

### String (`{_id, v, expireAt?}`)
`strget` `strset` `strsetall` `strgetall` `strdel`. `strset` accepts an optional expiration (TTL, see §7). `strgetall` takes a glob `?match=`.

### List (`{_id, items[]}`)
`lpush` `rpush` `lpop` `rpop` `lrange` `llen` `lindex` `lset` `lrem` `ltrim` `linsertbefore` `linsertafter`. Pops are atomic (`findOneAndUpdate` + `$pop`). Blocking ops (`blpop`/`brpop`/`brpoplpush`) are **not** implemented.

### Set (`{_id, members[]}`)
`sadd` `srem` `smembers` `sismember` `scard`.

### ZSet (`{_id, members:[{m,score}]}`)
`zadd` `zrem` `zscore` `zcard` `zcount` `zincrby` `zrange` `zrevrange` `zrangebyscore` `zrevrangebyscore` `zrank` `zrevrank` `zpopmin` `zpopmax` `zremrangebyrank` `zremrangebyscore`. Range/score params via query: `?start=&stop=`, `?min=&max=`, `?count=`, `?withscores=1`.

> Every command above is covered by the Go↔TS conformance harness (both engines, same Mongo, per-command diff). Naming avoids collisions: String uses the `STR*` prefix so it never clashes with Set's `S*`.

---

## 4. Backend (Go)

**Lang**: Go 1.24+ **Package**: `github.com/doptime/dopdb` (+ `api/` `httpserve/` `config/`).

### 4.1 Hash collection (the primary, fullest-surface type)

```go
import "github.com/doptime/dopdb"

type Note struct {
    ID    string `json:"id"   bson:"_id"`            // _id is the key (string)
    Owner string `json:"owner" bson:"owner"`          // owner field (for owner-scope)
    Text  string `json:"text"  bson:"text" validate:"required"`
}

// Factory: New[K, V](opts...). K = key type (string), V = value type (pointer or value).
// json tags == bson tags (the HTTP body is JSON-round-tripped into V).
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

Each is a first-class Go type backed by its own Mongo collection, registered + authorized via `HttpOn`, and reached over the **wire commands** in §3:

```go
cfg  := dopdb.NewString[string](dopdb.WithCollection("cfg")).HttpOn()   // STR* commands
queue := dopdb.NewList[string, *Job](dopdb.WithCollection("queue")).HttpOn() // L*/R* commands
tags := dopdb.NewSet[string](dopdb.WithCollection("tags")).HttpOn()      // S* commands
board := dopdb.NewZSet[string](dopdb.WithCollection("board")).HttpOn()    // Z* commands
```

- Doc shapes: String `{_id,v,expireAt?}`, List `{_id,items[]}`, Set `{_id,members[]}`, ZSet `{_id,members:[{m,score}]}`.
- **Owner-scope** applies identically: the owner lives at the document top level and the gate ANDs `{_id, owner}` (see §4.5).
- These types currently expose the **HTTP command layer** (handlers `HttpStrGet`, `HttpSAdd`, `HttpLPush`, `HttpZAdd`, …) and are reached via the §3 wire commands; the Hash family additionally has native non-HTTP Go methods.
- **TTL**: `NewString(...).EnsureTTL(ctx, ds)` builds the TTL index; `strset` with an expiration sets `expireAt` (§7).

### 4.4 `@`-binding

Server-injected, client `@`-keys stripped:
- **Identity** (JWT): `@uid`, `@email`, `@role`, …
- **Request info**: `@remoteAddr`, `@host`, `@method`, `@path`, `@rawQuery`.
- **Target metadata**: `@key` (collection key), `@field` (field).
`?f=@uid` = "my own row". Go structs receive the corresponding bson field (injected per owner-scope / binding).

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

**Package**: `dopdb`. Browser uses `dopdb/client`, Node server uses `dopdb/server`. The TS engine is an equivalent re-implementation: the **server** handles the full command vocabulary of §3 (conformance-verified against Go); the **typed client** today exposes the Hash family (`db.coll.hGet/hSet/hGetAll/hDel/...`). For String/List/Set/ZSet, drive the §3 wire commands (typed client wrappers for them are a follow-up).

### 5.1 Define the schema (shared by both engines)

```ts
import { collection, f, HGet, HGetAll, HSet, HDel, ReadOnly, All } from "dopdb";

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
`.httpOn(...)`: same meaning as Go. The Perm constants (`HGet`…`Watch`, `HScan`/`HScanNoValues`/`HRandField`, `ReadOnly`/`Writes`/`All`/`HashAll`, plus the String/List/Set/ZSet command bits) are exported from `dopdb` as **BigInt** (the bitmask exceeds 32 bits across all types); bit values match Go.

### 5.2 Browser client (no fetch code)

```ts
import { clientDb } from "dopdb/client";
import { schema } from "./schema";

const db = clientDb(schema, {
  baseUrl: "https://api.example.com",
  token: async () => await getJWT(),   // static string or async function
});

await db.notes.hSet("@uuid", { text: "buy milk" }); // create, server generates id
const mine = await db.notes.hGetAll();               // Map<id, Note>, only mine
await db.notes.hSet(id, { text: "edit" });           // update
await db.notes.hDel(id);                             // delete
```

| Op | Method | Key strategy |
|---|---|---|
| List | `hGetAll()` | all of my hash (owner-scope filtered) |
| Create | `hSet("@uuid", v)` | `"@uuid"` triggers server-side id generation |
| Update | `hSet(id, v)` | existing id |
| Delete | `hDel(id)` | — |

### 5.3 Node server (the equivalent of Go)

```ts
import { serve } from "dopdb/server";
import { schema } from "./schema";

const srv = await serve({
  schema,
  mongo: { uri: process.env.MONGO_URI!, db: "app" },
  jwtSecret: process.env.JWT_SECRET!,
  // No permit/permissions: each collection's .httpOn() bitmask authorizes (same as Go).
  port: 8080,
});
```

`serverDb(schema, db)` gives typed server-side collections in Node; `defineApi(fn)` defines a functional API.

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

- **watch** = Mongo change stream → SSE (`text/event-stream`). **Needs a replica set.** Under owner-scope it filters by `{owner: me}`; reconnect resumes via resume token; under owner-scope a delete event isn't delivered (no fullDocument). The client subscribes via `GET /api/watch/<coll>`.
- **TTL**: String collections support expiration. A doc carries `expireAt: Date` and the collection has a TTL index `{expireAt:1}, expireAfterSeconds:0`; an expiration `> 0` sets `expireAt = now + d`. Background expiry is MongoDB's job (~60s granularity).

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

See `docs/TESTING.md`. Go unit tests live beside the code (`*_test.go`); tests needing a real Mongo are gated by `DOPDB_TEST_MONGO_URI` (skipped if unset); cross-implementation consistency is guarded by `httpserve/conformance_test.go` (it starts a TS subprocess, drives both engines, and diffs every command — including all String/List/Set/ZSet commands). **Never substitute a single-engine test for a consistency claim.**

---

## 10. Meta-Instructions for AI Code Generation

Follow strictly:

1. **Keys are always strings**; `"@uuid"` triggers server-side id generation; `?f=@uid` means "my row".
2. **Backend (Hash)**: `dopdb.New[string, *T](dopdb.WithCollection("name"))`; **expose + authorize with `.HttpOn(...)`** (debug `.HttpOn()` first, then tighten) — do not write `RegisterHttp` + per-command `Grant`. Struct tags `json` (== `bson`) + `validate`. Multi-tenant: `dopdb.SetOwnerScope(coll, ownerField, claim)`.
3. **Backend (String/List/Set/ZSet)**: `dopdb.NewString[K](...)` / `NewList[K,E](...)` / `NewSet[K](...)` / `NewZSet[K](...)`, then `.HttpOn(...)`; reach them via the §3 wire commands; owner-scope and TTL apply as in §4.3/§4.5/§7.
4. **Frontend**: `collection(shape).named().ownerScope().httpOn(...)`; `clientDb(schema, {baseUrl, token})`; call `db.coll.hSet/hGetAll/hDel` directly — **no fetch, no API layer**.
5. **Permissions**: data commands are 403 by default; a collection must `.httpOn(...)` to be reachable; `.httpOn()` with no args = all on (debug only — always tell the user to tighten it).
6. **`@`-keys**: never have the client send `@uid`/`@owner` etc. — the framework strips and injects them; bind ownership with `.bind("@uid")` (TS) or an owner-scope declaration.
7. **Commands**: use only the §3 vocabulary; reads GET, writes POST, watch SSE. Blocking list ops are not available.
8. **Consistency**: any change to two-engine behavior must be verified with `conformance_test.go` (drive both engines, diff empty); never substitute a single-engine test.
9. **Imports**: TS permission constants come from `dopdb` (`HGet`/`ReadOnly`/`All`/… as BigInt); browser `dopdb/client`, Node `dopdb/server`.
