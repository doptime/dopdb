# redisdb-compatible method surface: gap analysis + Mongo mapping

Goal: make dopdb's DB API cover as much of `github.com/doptime/doptime/db` (redisdb) as possible. Each Redis **key type** (String/Hash/List/Set/ZSet) maps to a dopdb **collection type** (its own method set + document shape). The **only** thing not done is blocking semantics (`BLPop`/`BRPop`/`BRPopLPush` — MongoDB has no native blocking; the subscription need is covered by `watch`/change streams).

## 0 · Status

Before M6, dopdb = redisdb's **Hash family** (minus 3 methods) + Mongo-native `find/count/watch`. M6 adds the missing Hash methods and the four missing key types. Legend: ✅ present · ➕ added · ⛔ not done (blocking).

## 1 · Hash (existing type; 3 methods added)
| redisdb | status | Mongo mapping |
|---|---|---|
| HGet/HSet/HSetNX/HDel/HExists/HGetAll/HKeys/HVals/HLen/HIncrBy/HIncrByFloat/HMSet/HMGet | ✅ | collection = hash, doc = field |
| **HRandField(count)** | ➕ | `aggregate([{$sample:{size:count}}])` → `_id` |
| **HScan(cursor,match,count)** | ➕ | glob→regex, `find({_id:/re/}).sort(_id).skip(cursor).limit(count)`; nextCursor = cursor+len (full) else 0 |
| **HScanNoValues** | ➕ | same, projecting only `_id` |

## 2 · String (`StringCollection[K]`; doc `{_id, v, expireAt?}`)
| redisdb | Mongo |
|---|---|
| Get(field) | `findOne({_id}) → v` |
| Set(key,value,expiration) | `updateOne({_id}, {$set:{v}, expireAt?}, upsert)` |
| SetAll(map) / GetAll(match) | bulk upsert / `find({_id:/re/})` |
| Del(key) | `deleteOne` |

Commands use the `STR*` prefix (to avoid clashing with Set's `S*`): STRGET/STRSET/STRSETALL/STRGETALL/STRDEL. String semantics are close to the existing Hash; the implementation reuses Hash backend primitives where possible.

## 3 · List (`ListCollection[K,E]`; doc `{_id, items:[E]}`)
| redisdb | Mongo |
|---|---|
| LPush(..e)/RPush(..e)/RPushX | `$push{items:{$each,$position:0}}` / `$push{$each}` / X: filter `{_id}`, no upsert |
| LPop/RPop | `findOneAndUpdate(..., {$pop:{items:-1\|1}})`, returns the first/last element of the old doc |
| LRange/LLen/LIndex | `$slice` aggregation / `$size` / `$arrayElemAt` |
| LSet(i,e)/LRem(count,e)/LTrim(s,t) | `$set{"items.i"}` (i<0 resolved first) / `$pull` or read-modify-write / `$set{items:{$slice}}` |
| LInsertBefore/After(pivot,e) | read items → locate → `$set` the whole array |
| ⛔ BLPop/BRPop/BRPopLPush | not done |

## 4 · Set (`SetCollection[K]`; doc `{_id, members:[M]}`)
| redisdb | Mongo |
|---|---|
| SAdd(m)/SRem(m) | `$addToSet` upsert / `$pull` |
| SMembers/SIsMember(m)/SCard | project members / `findOne({_id, members:m})` / `$size` |

## 5 · ZSet (`ZSetCollection[K]`; doc `{_id, members:[{m,score}]}`)
| redisdb | Mongo |
|---|---|
| ZAdd(m,score)/ZRem(..m) | update score (arrayFilters) or `$push` if absent / `$pull{members:{m:{$in}}}` |
| ZScore/ZCard/ZCount(min,max) | aggregate score / `$size` / aggregate count |
| ZIncrBy(inc,m) | `$inc{"members.$[e].score"}` arrayFilters |
| ZRange/ZRevRange(s,t)[WithScores] | read-modify-write with derived order (score asc, member asc) + slice |
| ZRangeByScore/ZRevRangeByScore | filter score ∈ [min,max] + order |
| ZRank/ZRevRank(m) | locate index in the ordered members |
| ZPopMin/ZPopMax(count) | take extremes + `$pull` |
| ZRemRangeByRank/ByScore | locate + `$pull` |

The implementation keeps a derived order (score asc, member asc) in the document rather than relying on aggregation per call, so Go and TS agree byte-for-byte.

## 6 · Architecture (folding multiple types in)

- The existing `Collection[K,V]` = Hash; unchanged.
- New peer types: `StringCollection[K]` / `ListCollection[K,E]` / `SetCollection[K]` / `ZSetCollection[K]`, each with a constructor (`NewString`/`NewList`/`NewSet`/`NewZSet`) + its method set + an `HttpAccessor` extension (the corresponding `Http*` handlers).
- One Mongo collection per type. owner-scope: the owner lives at the document top level `{_id, owner, ...}` and the gate ANDs `{_id, owner}` — same model as Hash, reused, not reinvented.

## 7 · TTL (the expiration parameter)

Methods taking `expiration`: the doc carries `expireAt: Date` and the collection has a TTL index `{expireAt:1}, expireAfterSeconds:0`. `expiration > 0` sets `expireAt = now + d`; `0` leaves it unset. `EnsureTTL` builds the index. Background expiry is MongoDB's job (~60s granularity).

## 8 · Wire protocol + permissions

- New commands join `httpserve.dataCommands` + each type's dispatch; naming avoids collisions (String `STR*`). URL unchanged `/api/<cmd>/<coll>?ds=`, key `?f=`, range/score via query (`?start=&stop=&min=&max=&count=&withscores=1`) or body; reads GET, writes POST.
- `perms.go`'s `Perm` (a `uint64` bitmask) has one bit per new command; group `ReadOnly` (STRGET/LRANGE/LLEN/LINDEX/SMEMBERS/SISMEMBER/SCARD/ZSCORE/ZRANGE.../ZCARD/ZCOUNT/ZRANK + HSCAN/HRANDFIELD), `Writes` (the rest), `All` covering both. The TS side mirrors these as BigInt. `HttpOn` applies to the new commands.

## 9 · Consistency (conformance)

Every new command has a two-engine case in `httpserve/conformance_test.go` (Go server vs TS subprocess, real Mongo), covering normal + edge (empty key, out-of-range index, missing member, cross-tenant owner-scope). The two engines must agree status+code+body per command; a single-engine test does not count as consistency evidence.

## 10 · M6 outcome

Implemented and conformance-verified (15 conformance functions, ZSet covering 16/16 commands): the 3 Hash methods (HScan/HScanNoValues/HRandField) and the four new key types (String/List/Set/ZSet), Go + TS, two-engine consistent on real Mongo. `HttpOn` gates the new commands; owner-scope and TTL apply. Blocking ops are intentionally not implemented. Optional follow-ups: typed TS client wrappers for the new types, and an independent TTL-expiry behavior test.
