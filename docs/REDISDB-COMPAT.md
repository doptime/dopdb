# redisdb-compatible method surface: KVRocks mapping

Goal: make dopdb's DB API cover as much of `github.com/doptime/doptime/db` (redisdb) as possible. Each Redis **key type** (String/Hash/List/Set/ZSet) maps to a dopdb **collection type**.

On KVRocks this mapping stops being an emulation. redisdb's key types are Redis key types, and KVRocks speaks the Redis protocol — so with the storage swap most of this table collapses from *"how we fake it on Mongo"* into *"the command, sent"*. The **only** thing still not done is blocking semantics (`BLPop`/`BRPop`/`BRPopLPush`); the subscription need is covered by `watch`.

Legend: ✅ present · ⛔ not done (blocking).

## 1 · Hash

One Redis **hash per collection**: field = document id, value = the CBOR document. This is redisdb's own layout, so every Hash command is the real command again.

| redisdb | KVRocks |
|---|---|
| HGet/HSet/HSetNX/HDel/HExists/HGetAll/HKeys/HVals/HLen/HMSet/HMGet | ✅ `HGET`/`HSET`/`HSETNX`/`HDEL`/`HEXISTS`/`HGETALL`/`HKEYS`/`HVALS`/`HLEN`/`HSET`(multi)/`HMGET` |
| HRandField(count) | ✅ `HRANDFIELD` (unscoped); a filtered scan when owner-scoped, since the population must be restricted first |
| HScan(cursor,match,count) | ✅ `HSCAN` with server-side glob matching. The cursor is the server's own opaque cursor — as with real `HSCAN`, a page may be short or empty while the cursor is non-zero; **only cursor 0 means done** |
| HScanNoValues | ✅ same, values dropped |
| HIncrBy/HIncrByFloat | ✅ but **not** `HINCRBY`: a hash field holds a whole document here, so it is a `WATCH`/`MULTI` read-modify-write. Atomic; see `01-data` |

`Find`/`FindOne`/`Count` have no Redis counterpart at all — they scan and filter in-process (`query.go`). See `01-data` for the cost.

## 2 · String (`StringCollection[K]`)

A native Redis **string** at `<ns>:<coll>:<key>`, holding the CBOR-encoded value.

| redisdb | KVRocks |
|---|---|
| Get(field) | `GET` |
| Set(key,value,expiration) | `SET` (+ `EX` when an expiration is given — a **native TTL**, not a swept index) |
| SetAll(map) / GetAll(match) | pipelined `SET` / `SCAN` + `MGET` |
| Del(key) | `DEL` |

Commands use the `STR*` prefix (to avoid clashing with Set's `S*`): STRGET/STRSET/STRSETALL/STRGETALL/STRDEL.

## 3 · List (`ListCollection[K,E]`)

A native Redis **list** at `<ns>:<coll>:<key>`, elements CBOR-encoded.

| redisdb | KVRocks |
|---|---|
| LPush(..e)/RPush(..e) | `LPUSH` / `RPUSH`. `LPUSH` reverses the batch first so several items land head-first *in the order given* — preserving the Mongo build's behaviour |
| LPop/RPop | `LPOP` / `RPOP` — atomic, one command |
| LRange/LLen/LIndex | `LRANGE` / `LLEN` / `LINDEX` (negative indices native) |
| LSet(i,e)/LRem(count,e)/LTrim(s,t) | `LSET` / `LREM` / `LTRIM` |
| LInsertBefore/After(pivot,e) | `LINSERT BEFORE|AFTER`. A missing pivot answers −1, which stays a no-op rather than an error |
| ⛔ BLPop/BRPop/BRPopLPush | not done |

Every one of these was a read-modify-write on Mongo; `LREM` and `LINSERT` in particular could lose a concurrent update. They cannot now.

## 4 · Set (`SetCollection[K]`)

A native Redis **set** at `<ns>:<coll>:<key>`, members CBOR-encoded in canonical form — which is what makes deduplication correct: two equal values always produce identical bytes.

| redisdb | KVRocks |
|---|---|
| SAdd(m)/SRem(m) | `SADD` / `SREM` — dedup is the server's job |
| SMembers/SIsMember(m)/SCard | `SMEMBERS` / `SISMEMBER` / `SCARD` |

`SMEMBERS` order is unspecified in Redis, so dopdb sorts the encoded members before decoding: a set has no order to preserve, and a stable answer is worth more than the accident of the server's iteration.

## 5 · ZSet (`ZSetCollection[K]`)

A native Redis **sorted set** at `<ns>:<coll>:<key>`. Members are plain strings, which the accessor signatures already assumed.

| redisdb | KVRocks |
|---|---|
| ZAdd(m,score)/ZRem(..m) | `ZADD` / `ZREM` |
| ZScore/ZCard/ZCount(min,max) | `ZSCORE` / `ZCARD` / `ZCOUNT` (`-inf`/`+inf` for unbounded) |
| ZIncrBy(inc,m) | `ZINCRBY` — atomic, so two concurrent increments no longer lose one another |
| ZRange/ZRevRange(s,t)[WithScores] | `ZRANGE`/`ZREVRANGE ... WITHSCORES` |
| ZRangeByScore/ZRevRangeByScore | `ZRANGEBYSCORE`/`ZREVRANGEBYSCORE` |
| ZRank/ZRevRank(m) | `ZRANK`/`ZREVRANK` (absent member → −1, the previous contract) |
| ZPopMin/ZPopMax(count) | `ZPOPMIN`/`ZPOPMAX` |
| ZRemRangeByRank/ByScore | `ZREMRANGEBYRANK`/`ZREMRANGEBYSCORE` |

Ordering is now the server's index, not a derived order re-sorted on every read. Redis orders by score, then lexicographically by member — the same order the Mongo build computed, so the visible sequences are unchanged.

## 6 · Architecture

- `Collection[K,V]` = Hash; it keeps the generic machinery (write plan, value codec, index specs).
- The four peer types (`StringCollection` / `ListCollection` / `SetCollection` / `ZSetCollection`) no longer need any of that — there is no wrapper document to encode — so they share a small `keyspace` handle (name + datasource) and call Redis directly.
- **owner-scope**: Hash keeps the document-field predicate. The other four have no document to hold an owner, so isolation is enforced against the collection's `__owner` index (`<ns>:<coll>:__owner`, key → owner): first writer claims the key; a foreign key reads as absent and writes as `403`.

## 7 · TTL

Per-**key** TTL is native and better than before: `strset` with an expiration issues `SET ... EX`, and KVRocks expires the key itself.

Per-**document** TTL is **gone**. A Hash collection is one Redis key, so a single document inside it cannot carry its own expiry. `EnsureTTL(...)` is kept for source compatibility and does nothing; a `.ttl(...)` declaration in the TypeScript schema is recorded and inert. This is a real capability loss and is called out here rather than quietly dropped.

## 8 · Wire protocol + permissions

Unchanged by the storage swap. URL `/api/<cmd>/<coll>?ds=`, key `?f=`, range/score via query (`?start=&stop=&min=&max=&count=&withscores=1`) or body; reads GET, writes POST. `perms.go`'s `Perm` (a `uint64` bitmask) has one bit per command; groups `ReadOnly` / `Writes` / `All`. The TS side mirrors these as BigInt.

## 9 · Consistency (conformance)

Every command has a two-engine case in `httpserve/conformance_test.go` (Go server vs TS subprocess, both against a real KVRocks in separate namespaces), covering normal + edge cases (empty key, out-of-range index, missing member, cross-tenant owner-scope). The two engines must agree on status + code + body shape per command; a single-engine test does not count as consistency evidence.

## 10 · Net effect of the KVRocks migration

Gained: every List/Set/ZSet command is atomic and O(log n) instead of a read-modify-write over a whole array; String TTL is native; the Hash family is back on its original commands.

Lost: content queries have no server-side support (`FIND` scans), secondary indexes other than `unique` do not exist, per-document TTL is not expressible, and `watch` cannot replay. Each is documented where it bites — `01-data`, `02-http`, and the table above.
