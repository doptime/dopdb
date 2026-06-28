# 01 · Data layer (Collection · methods · modifiers · indexes · queries · data sources)

The data layer is the trusted face: server code reads/writes directly, with no owner-scope and no JWT. In Go it is the generic `Collection[K, V]` (Hash) plus the String/List/Set/ZSet collection types; in TS it is the typed `db` returned by `serverDb(schema, db)`.

## Direct to MongoDB (no Store abstraction)

dopdb **no longer has a `Store`/`Codec` abstraction**, nor `memstore`/`mongostore`. The root package uses `go.mongodb.org/mongo-driver/v2` directly: the concrete `mongoBackend{db *mongo.Database}` in `mongo.go` inlines all of the former mongostore driver-v2 logic (ReplaceOne/UpdateOne upsert, FindOne.Raw, Find cursor, CountDocuments, BulkWrite, unique indexes, `IsDuplicateKeyError`, plus `sample`/`scan` for HRANDFIELD/HSCAN).

Encoding goes straight through `bson.Marshal`/`bson.Unmarshal`; the string key is the document `_id`.

## Defining a collection (Hash)

```go
type User struct {
    Name  string `json:"name"  bson:"name"`
    Email string `json:"email" bson:"email" index:"unique"`
    Age   int    `json:"age"   bson:"age"   index:"1"`
    Loc   []float64 `json:"loc" bson:"loc"  index:"2dsphere"`
}
users := dopdb.New[string, *User](dopdb.WithCollection("users"))
```

`New[K,V]` options: `WithCollection(name)` (= `WithKey`), `WithDB(name)` (binds a data source, for native methods; on the HTTP side `?ds=` decides). `New` only registers index specs and does not open a connection; indexes are built on demand the first time a data source is used (once per source).

Indexes come from the struct tag `index:"..."`: `"unique"`, `"1"`/`"-1"` (asc/desc), `"text"`, `"2dsphere"` (geo, via `IndexSpec.Geo`), TTL, etc.

## Native methods (Hash, signatures)

```
HSet(k, v) / Save(v)                 upsert (Save reads the key from v._id)
HSetNX(k, v) (bool, err)             write only if absent
HSetScoped(k, v, ownerField, val)    filtered upsert (the basis of row-level isolation)
HGet(k) (V, err)                     get one (missing → ErrNoDoc)
HMGet(...k) ([]V, err)               batch get (aligned; missing = zero value)
HMSet(map[K]V)                       batch write
HGetAll() (map[K]V, err)             all
HDel(...k) / Del(k)                  delete
HExists(k) (bool, err)
HKeys() ([]K, err) / HVals() ([]V, err) / HLen() (int64, err)
HIncrBy(k, fieldPath, int64)         atomic integer $inc
HIncrByFloat(k, fieldPath, float64)  atomic float $inc
HRandField(count) ([]K, err)         random field keys ($sample)
HScan(cursor, match, count) ([]K, []V, next, err)   glob-matched, offset-cursor pagination
HScanNoValues(cursor, match, count) ([]K, next, err)
Find(filter M, opt FindOpt) ([]V, err)
FindOne(filter M) (V, err)
```

The TS engine mirrors the names (`hget/hset/.../find/findone/watch/hscan/hrandfield` + `get/set/save` aliases on the server).

## The Redis-compatible data structures (String / List / Set / ZSet)

Beyond Hash, four more key types — each a first-class Go collection backed by its own Mongo collection, declared with its own constructor + `HttpOn`, reached over the wire commands in `02-http`:

```go
cfg   := dopdb.NewString[string](dopdb.WithCollection("cfg")).HttpOn()       // String: {_id,v,expireAt?}
queue := dopdb.NewList[string, *Job](dopdb.WithCollection("queue")).HttpOn() // List:   {_id,items[]}
tags  := dopdb.NewSet[string](dopdb.WithCollection("tags")).HttpOn()         // Set:    {_id,members[]}
board := dopdb.NewZSet[string](dopdb.WithCollection("board")).HttpOn()       // ZSet:   {_id,members:[{m,score}]}
```

- These types expose the **HTTP command layer** (handlers `HttpStrGet`, `HttpSAdd`, `HttpLPush`, `HttpZAdd`, …) and are reached via the wire commands; the Hash family additionally has native non-HTTP Go methods.
- **Owner-scope** applies identically (owner at the document top level; the gate ANDs `{_id, owner}`).
- **TTL** (String): `NewString(...).EnsureTTL(ctx, ds)` builds a TTL index; `strset` with an expiration sets `expireAt`.
- List pops are atomic (`findOneAndUpdate` + `$pop`). ZSet keeps a derived order (score asc, member asc). Blocking list ops are not implemented.

See `REDISDB-COMPAT` for the full per-method Mongo mapping.

## Write modifiers

`modifiers.go` processes a value before writing: filling timestamps (e.g. `createdAt`/`updatedAt`), populating server-side fields per `@`-binding (identity, etc.). The trusted path trusts the incoming value by default; the HTTP path first strips client `@`-keys, then fills them.

## Queries and sanitization

`Find`/`FindOne`/`Count` take a Mongo-style filter (`dopdb.M`). `FindOpt`: `SortKeys []SortKey{Field, Asc}` (ordered multi-key), `Sort` (single-key map), `Limit`, `Skip`, `Projection`.

Every external filter first passes `sanitize.go`: it rejects `$`-operators injected as field keys (injection defense); operators used as values (`$in`/`$and`, etc.) are allowed as needed.

## Multiple data sources

`mongo.go` keeps a `Datasources` registry (name → `*mongo.Database`, default name `default`):

```go
ds := dopdb.NewDatasources()
ds.Add("default", client.Database("appdb"))
ds.Add("analytics", client.Database("analytics"))
dopdb.SetDatasources(ds)
// or connect once from config:
ds, _ := dopdb.ConnectDatasources(ctx, []dopdb.DatasourceConfig{{Name:"default", URI:uri, DB:"appdb"}})
```

Native methods use the collection's bound source (`WithDB`, default `default`); HTTP requests select with `?ds=<name>` (default `default`) — **the request parameter wins on the HTTP side**.
