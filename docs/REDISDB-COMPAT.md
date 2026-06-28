# redisdb 兼容方法面:gap 分析 + Mongo 映射设计

目标:dopdb 的 DB API 尽可能覆盖 `github.com/doptime/doptime/db`(redisdb)的全部操作。Redis 每个 **key 类型**(String/Hash/List/Set/ZSet)映射成一个 dopdb **集合类型**(各自方法集 + doc 形态)。**唯一不做**:阻塞语义(`BLPop/BRPop/BRPopLPush`——Mongo 无原生阻塞;订阅需求已由 `watch`/change stream 覆盖)。

## 0 · 现状 vs 目标
dopdb 现有 = redisdb 的 **Hash 一族**(缺 3 法)+ Mongo 原生 `find/count/watch`。**完全缺** String / List / Set / ZSet 四类型。

图例:✅ 已有 · ➕ 本回合 Opus 补 · 🔜 交本地 R9 实现 · ⛔ 不做

## 1 · Hash(现有类型,补 3 法)
| redisdb | 状态 | Mongo 映射 |
|---|---|---|
| HGet/HSet/HSetNX/HDel/HExists/HGetAll/HKeys/HVals/HLen/HIncrBy/HIncrByFloat/HMSet/HMGet | ✅ | 集合=hash,doc=field |
| **HRandField(count)** | ➕ | `aggregate([{$sample:{size:count}}])` → `_id` |
| **HScan(cursor,match,count)** | ➕ | glob→regex,`find({_id:/re/}).sort(_id).skip(cursor).limit(count)`;nextCursor=cursor+len(满)否则 0 |
| **HScanNoValues** | ➕ | 同上仅投影 `_id` |

## 2 · String(新 `StringCollection[K]`;doc=`{_id,v,expireAt?}`)
| redisdb | Mongo |
|---|---|
| Get(field) | `findOne({_id})→v` |
| Set(key,value,expiration) | `updateOne({_id},{$set:{v},expireAt?},upsert)` |
| SetAll(map) / GetAll(match) | bulk upsert / `find({_id:/re/})` |
| Del(key) | `deleteOne` |
> 命令前缀 `STR*`(避免与 Set 的 `S*` 冲突):STRGET/STRSET/STRSETALL/STRGETALL/STRDEL。注:String 多数语义与现 Hash 接近,实现时可复用 Hash 后端原语。

## 3 · List(新 `ListCollection[K,E]`;doc=`{_id,items:[E]}`)
| redisdb | Mongo |
|---|---|
| LPush(..e)/RPush(..e)/RPushX | `$push{items:{$each,$position:0}}` / `$push{$each}` / X:filter `{_id}` 不 upsert |
| LPop/RPop | `findOneAndUpdate(...,{$pop:{items:-1\|1}})` 取旧文档首/尾元素返回 |
| LRange/LLen/LIndex | `$slice` 聚合 / `$size` / `$arrayElemAt` |
| LSet(i,e)/LRem(count,e)/LTrim(s,t) | `$set{"items.i"}`(i<0 先算长)/ `$pull` 或读改写 / `$set{items:{$slice}}` |
| LInsertBefore/After(pivot,e) | 读 items→定位→`$set` 整数组 |
| ⛔ BLPop/BRPop/BRPopLPush | 不做 |

## 4 · Set(新 `SetCollection[K,M]`;doc=`{_id,members:[M]}`)
| redisdb | Mongo |
|---|---|
| SAdd(m)/SRem(m) | `$addToSet` upsert / `$pull` |
| SMembers/SIsMember(m)/SCard | 投影 members / `findOne({_id,members:m})` / `$size` |
| SScan(cursor,match,count) | 应用层对 members 过滤分页 |

## 5 · ZSet(新 `ZSetCollection[K,M]`;doc=`{_id,members:[{m,score}]}`)
| redisdb | Mongo |
|---|---|
| ZAdd(m,score)/ZRem(..m) | 有则改 score(arrayFilters)无则 `$push` / `$pull{members:{m:{$in}}}` |
| ZScore/ZCard/ZCount(min,max) | 聚合取 score / `$size` / 聚合计数 |
| ZIncrBy(inc,m) | `$inc{"members.$[e].score"}` arrayFilters |
| ZRange/ZRevRange(s,t)[WithScores] | 聚合 `$sort members.score` + `$slice` |
| ZRangeByScore/ZRevRangeByScore[WithScores] | 聚合 filter score∈[min,max] + sort |
| ZRank/ZRevRank(m) | 聚合排序后定位 index |
| ZPopMin/ZPopMax(count) | 聚合取极值 + `$pull` |
| ZRemRangeByRank/ByScore | 聚合定位 + `$pull` |
| ZScan/ZLexCount | 应用层(可选) |

## 6 · 架构(多类型并入)
- 现 `Collection[K,V]` = Hash,不变。
- 新增并列:`StringCollection[K]`/`ListCollection[K,E]`/`SetCollection[K,M]`/`ZSetCollection[K,M]`,各构造器(`NewString`/`NewList`/`NewSet`/`NewZSet`)+ 方法集 + 各实现 `HttpAccessor` 扩展(对应 Http* 方法)。
- 每类型一个 Mongo 集合。owner-scope:owner 存 doc 顶层 `{_id,owner,...}`,门按 `{_id,owner}` 过滤(与 Hash 一致,**沿用**不另造)。

## 7 · TTL(expiration 参数)
带 `expiration` 的方法:doc 加 `expireAt:Date` + 集合建 TTL 索引 `{expireAt:1},expireAfterSeconds:0`。`expiration>0`→`expireAt=now+exp`,=0 不设。`WithTTL()` 选项触发建索引。

## 8 · 线协议 + 权限
- 新命令并入 `httpserve.dataCommands` + 各类型 dispatch;命名避冲突(String `STR*`)。URL 不变 `/api/<cmd>/<coll>?ds=`,键 `?f=`,range/score 走 query(`?start=&stop=&min=&max=&count=`)或 body;读 GET 写 POST。
- `perms.go` 的 `Perm` 每个新命令一位;`ReadOnly`(STRGET/LRANGE/LLEN/LINDEX/SMEMBERS/SISMEMBER/SCARD/ZSCORE/ZRANGE.../ZCARD/ZCOUNT/ZRANK + HSCAN/HRANDFIELD)、`Writes`(其余),`All` 自动覆盖。`HttpOn` 对新命令同样生效。

## 9 · 一致性(conformance)
每个新命令进 `httpserve/conformance_test.go` 双端比对(Go 服务 vs TS 子进程,真 Mongo),覆盖正常 + 边界(空 key/越界 index/不存在 member/owner-scope 跨租户)。**两端逐命令 status+code+body 一致**;单端测试不算一致性证据。

## 10 · 本回合 Opus 已做 vs 交本地
- **Opus 已补**(TS 验编译,Go 待本机 build):Hash 三法 `HScan`/`HScanNoValues`/`HRandField`(扩展现有类型,后端加 `sample`/`scan` 两原语)。
- **交本地 R9 实现+实测+提交**:String/List/Set/ZSet 四类型(按 §2–§9),Go+TS,真 Mongo conformance。理由:四个数据结构的 Mongo 语义(原子弹出、ZSet 排名聚合、arrayFilters、TTL 索引)需真环境验证,从干净 spec 实现快于调试盲写代码;本地有 Go+Mongo,正是实证方。
