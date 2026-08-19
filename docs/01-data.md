# 01 · Data layer (Collection · methods · modifiers · indexes · queries · data sources)

The data layer is the trusted face: server code reads/writes directly, with no owner-scope and no JWT. In Go it is the generic `Collection[K, V]` (Hash) plus the String/List/Set/ZSet collection types; in TS it is the typed `db` returned by `serverDb(schema, backend)`.

## Direct to KVRocks (no Store abstraction)

dopdb **has no `Store`/`Codec` abstraction**, nor `memstore`/`mongostore`. The root package speaks the Redis protocol directly: the concrete `kvBackend{rdb, ns}` in `kvrocks.go` holds the key layout, the CRUD commands, the `WATCH`/`MULTI` compare-and-set writes, the scan primitives and the change channel.

### The key layout

```
<ns>:<coll>                  HASH    a Hash collection: field = document id, value = the CBOR document
<ns>:<coll>:<key>            native STRING / LIST / SET / ZSET for the four other collection types
<ns>:<coll>:__owner          HASH    key -> owner, row isolation for the non-Hash types
<ns>:<coll>:__uniq:<field>   HASH    unique-index claims
<ns>:<coll>:__events         channel for watch
```

This is the redisdb layout dopdb originally came from, which is why the Hash family maps onto `HGET`/`HSET`/`HDEL`/`HEXISTS`/`HLEN`/`HKEYS`/`HVALS`/`HSCAN`/`HRANDFIELD` one-for-one, and why the List/Set/ZSet families are the *real* Redis commands rather than array surgery on a document.

### CBOR is the value format

KVRocks stores opaque bytes, so dopdb owns the value format and uses **CBOR** in canonical (deterministic) mode. Determinism is load-bearing: Set members are deduplicated by their encoded bytes, and unique-index claims are keyed by an encoded value.

Field names come from the `json:"..."` struct tags — cbor falls back to the json tag — so the stored field names and the HTTP JSON field names are identical. This is why the old `bson:"..."` tags are simply **gone** rather than renamed.

## Defining a collection (Hash)

```go
type User struct {
    Name  string    `json:"name"`
    Email string    `json:"email" index:"unique"`
    Age   int       `json:"age"   index:"1"`
    Loc   []float64 `json:"loc"   index:"2dsphere"`
}
users := dopdb.New[string, *User](dopdb.WithCollection("users"))
```

`New[K,V]` options: `WithCollection(name)` (= `WithKey`), `WithDB(name)` (binds a data source, for native methods; on the HTTP side `?ds=` decides). `New` only records index specs and does not open a connection.

### What an index declaration means now

KVRocks is a key/value store: no secondary indexes, no query planner. So:

| Tag | Effect |
|---|---|
| `index:"unique"` | **enforced**, by dopdb itself |
| `index:"1"` / `"-1"` | recorded, **inert** — `Find` scans and sorts in-process |
| `index:"text"` / `"2dsphere"` | recorded, **inert** |
| per-document TTL | **not expressible** — a Hash collection is one Redis key |

Unique enforcement uses a side hash `<ns>:<coll>:__uniq:<field>` mapping an encoded value to the document id that holds it. Every write claims its values before the document lands and releases the ones it no longer holds afterwards; a violation returns `ErrDuplicate` (HTTP **409**).

Two limitations, stated rather than hidden:

- The claim and the document write are separate commands, so a crash between them can leave a stale claim. It is self-healing: the next write of the same document id re-claims it, and reads are unaffected. Within a process, a write that does not land — a refused scoped write, an insert that finds the id taken, exhausted contention, a server error — gives its claim back explicitly, so it cannot reserve a value for a document that does not have it.
- A missing/nil value is **not** claimed (sparse behaviour), so several documents may omit a unique field. A non-sparse Mongo unique index would have rejected the second null.

## Native methods (Hash, signatures)

```
HSet(k, v) / Save(v)                 upsert (Save reads the key from v's primary-key field)
HSetNX(k, v) (bool, err)             write only if absent (HSETNX)
HSetScoped(k, v, ownerField, val)    compare-and-set on the owner (row-level isolation)
HGet(k) (V, err)                     get one (missing → ErrNoDoc)
HMGet(...k) ([]V, err)               batch get (aligned; missing = zero value)
HMSet(map[K]V)                       batch write
HGetAll() (map[K]V, err)             all
HDel(...k) / Del(k)                  delete
HExists(k) (bool, err)
HKeys() ([]K, err) / HVals() ([]V, err) / HLen() (int64, err)
HIncrBy(k, fieldPath, int64)         atomic increment of a field INSIDE the document
HIncrByFloat(k, fieldPath, float64)
HRandField(count) ([]K, err)         native HRANDFIELD
HScan(cursor, match, count) ([]K, []V, next, err)   native HSCAN (server-side glob)
HScanNoValues(cursor, match, count) ([]K, next, err)
Find(filter M, opt FindOpt) ([]V, err)
FindOne(filter M) (V, err)
```

The TS engine mirrors the names (`hget/hset/.../find/findone/watch/hscan/hrandfield` + `get/set/save` aliases on the server).

### Reserved entry key names

For the String/List/Set/ZSet families, user entries and dopdb's own bookkeeping
share one keyspace. An entry named `__owner` would resolve to the same Redis key
as the collection's isolation index, and on KVRocks a `SET` over an existing hash
key **converts the type** instead of raising `WRONGTYPE` — one such write would
destroy the index and break every later scoped write in that collection. So these
names are refused with `ErrReservedKey` (HTTP **400**), on read and write alike:

- an entry key ending in `__owner` or `__events`
- an entry key containing `__uniq:`
- an empty entry key

Hash collections are unaffected: their document ids are hash *fields*, not keys.

### About `HIncrBy`

Redis `HINCRBYFLOAT` increments a hash *field*, but a field here holds a whole CBOR document. So the increment is a `WATCH`-guarded read-modify-write: still atomic, just not one command. An integral result is stored as an integer, exactly as Mongo's `$inc` kept `int + int` an `int` — writing `25.0` where the struct field is an `int` would make the document undecodable on the next read.

A field that already holds a string, bool, array or object is **refused**
(`ErrFieldType`, HTTP **409**) rather than replaced by a number — Mongo's `$inc`
refused it too, and silently overwriting is data loss dressed up as success. An
absent field, or one explicitly `null`, starts from zero, which destroys nothing.

`WATCH` granularity is the **key**, and a Hash collection is one key, so a concurrent write to *any* document in the collection aborts the transaction. That is paid in retries (64, with jittered backoff), never in correctness: an aborted transaction does not write.

**Scoped increments carry the ownership test inside the transaction.** Under an
owner scope a missing document is `ErrForbidden`, never an upsert: creating a row
with no owner field would make it invisible to every scoped read and delete
afterwards. `HIncrByScoped` / the `hincrby` HTTP command handle this; the
unscoped `HIncrBy` still upserts, as before.

## The Redis-compatible data structures (String / List / Set / ZSet)

Four more key types, each now backed by the **native Redis type**:

```go
cfg   := dopdb.NewString[string](dopdb.WithCollection("cfg")).HttpOn()       // STRING per key, native TTL
queue := dopdb.NewList[string, *Job](dopdb.WithCollection("queue")).HttpOn() // LIST per key
tags  := dopdb.NewSet[string](dopdb.WithCollection("tags")).HttpOn()         // SET per key
board := dopdb.NewZSet[string](dopdb.WithCollection("board")).HttpOn()       // ZSET per key
```

- These types expose the **HTTP command layer** (`HttpStrGet`, `HttpSAdd`, `HttpLPush`, `HttpZAdd`, …); the Hash family additionally has native non-HTTP Go methods.
- Element/member values are stored CBOR-encoded, so any JSON value round-trips. ZSet members are plain strings (Redis requires it), which the accessor signatures already assumed.
- **Owner-scope** applies identically, but a native list has no document in which to store an owner, so isolation for these four is enforced against the collection's `__owner` index: first writer claims the key; a foreign key reads as absent and writes as `403`.
  - A claim does **not** outlive its data. Redis drops a list/set/zset key the
    moment its last element goes, so every emptying path (`LPOP`/`RPOP`/`LREM`/
    `LTRIM`/`SREM`/`ZREM`/`ZPOP*`/`ZREMRANGEBY*`) releases the claim, and
    `claimOwner` additionally reclaims a claim whose key no longer exists — which
    also covers a String key expiring on its TTL and a crash between claim and
    write. Without that, the first user to touch a key name would own it forever
    and everyone else would get a permanent 403 for a key that does not exist.
- **TTL** (String): `?expiration=` is now the server's own `EXPIRE`, not a swept index. `EnsureTTL(...)` is kept for source compatibility and does nothing.
- Every op is atomic because it is one Redis command — including the ZSet ops, which on Mongo were read-modify-write and could lose a concurrent `ZINCRBY`.
- **`LPUSH` with several items** inserts them at the head *in the order given* (`[a,b]` onto `[c]` yields `[a,b,c]`), which is what the Mongo build did. Raw Redis `LPUSH` would yield `[b,a,c]`; dopdb reverses before pushing so existing callers and the TS engine keep agreeing.

Blocking list ops (`BLPop`/`BRPop`/`BRPopLPush`) remain unimplemented. See `REDISDB-COMPAT`.

## Write modifiers

`modifiers.go` processes a value before writing: filling timestamps (`createdAt`/`updatedAt`), populating server-side fields per `@`-binding. The trusted path trusts the incoming value by default; the HTTP path first strips client `@`-keys, then fills them. Unchanged by the storage swap — it runs on WRITE, on every path.

## Queries: the part that changed most

`Find`/`FindOne`/`Count` still take the same filter dialect (`dopdb.M`), and `FindOpt` still carries `SortKeys`, `Sort`, `Limit`, `Skip`, `Projection`. **The dialect did not move; the evaluation did.**

KVRocks cannot query by content, so `query.go` (and its twin `ts/src/query.ts`) walks the collection hash with `HSCAN`, decodes each document, evaluates the filter in-process, then sorts, skips, limits and projects.

### The cost model, measured

Numbers from `make bench` on a 20,000-document collection (`bench_test.go`):

| query | time | allocated |
|---|---|---|
| `count` with no filter | **0.013 ms** | 205 B |
| `find` paging, no filter | 20 ms | 3.1 MB |
| `find` with an equality filter | 55 ms | 13 MB |
| `find` with `$regex` | 63 ms | 13 MB |

Three things that are worth knowing about those numbers:

- **`count` with no filter is a single `HLEN`.** It does not scan. Any other
  count does.
- **A `$regex` filter is no longer several times the cost of an equality one.**
  The pattern is compiled once per query, not once per document (it used to be
  the latter: 121 ms and 57 MB for the same query).
- **`LIMIT` bounds memory, not work.** The scan still reads every document —
  KVRocks has no index to consult — but only the rows that can still make the
  answer are retained. A sorted `limit 10` over 20,000 matches holds ten rows,
  not twenty thousand (`mem_test.go` asserts it: ~0 KiB vs 1.87 MiB).

What that costs you, plainly:

- Cost is **O(collection)**, not O(result). Filter on a small collection, or keep the working set addressable by key.
- There is **no index-backed ordering**; sort runs on the matched slice.
- Type ordering across mixed types is dopdb's own (`null < number < string < bool < date < array < object`), not BSON's — deterministic and documented rather than undefined.

Supported operators (the allowlist in `sanitize.go`, implemented exactly by the engine): `$eq $ne $gt $gte $lt $lte $in $nin $and $or $nor $not $exists $type $all $elemMatch $size $regex $options $mod`. `$type` takes JSON-flavoured names (`"string"`, `"number"`, `"bool"`, `"array"`, `"object"`, `"null"`, `"date"`); Mongo's numeric BSON type codes have no meaning here.

The same query is also reachable as SQL — `users.Query("SELECT * FROM users WHERE age >= 18 ORDER BY name LIMIT 20")` — which parses to this exact shape and runs on this exact evaluator. It is read-only and Hash-only; see `05-sql`.

Sanitization still runs on every external filter. Its job has shifted: the server-side injection class is gone with the server-side query, but an unbounded filter is still a denial-of-service surface, and the allowlist is what keeps the accepted dialect identical to the one the TypeScript engine implements. An operator outside it is **rejected**, and — belt and braces — an operator the evaluator does not implement matches *nothing* rather than everything, so a filter can never widen a result set.

## Multiple data sources

`kvrocks.go` keeps a `Datasources` registry (name → backend, default name `default`):

```go
ds := dopdb.NewDatasources()
ds.Add("default", client, "appdb")        // client + key namespace
ds.Add("analytics", client, "analytics")
dopdb.SetDatasources(ds)
// or connect once from config:
ds, _ := dopdb.ConnectDatasources(ctx, []dopdb.DatasourceConfig{
    {Name: "default", URI: "redis://localhost:6666", Namespace: "appdb"},
})
```

Native methods use the collection's bound source (`WithDB`, default `default`); HTTP requests select with `?ds=<name>` (default `default`) — **the request parameter wins on the HTTP side**.
