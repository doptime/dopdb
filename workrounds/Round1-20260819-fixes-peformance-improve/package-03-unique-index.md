# package-03 · unique 索引：原子占位与失败回滚

> 严重度 🔴。对应审计 S2-4、S2-6。

## 背景

KVRocks 没有二级索引，所以 `index:"unique"` 是 dopdb **自己**实现的：侧哈希
`<ns>:<coll>:__uniq:<field>` 把"编码后的值"映射到"持有它的文档 id"。这是唯一有可
观测语义的索引类型，也是唯一由框架承担正确性责任的。

## 问题

### 3.1 占位是 check-then-act（S2-4，双端）

```go
holder, err := b.rdb.HGet(ctx, idxKey, slot).Result()   // 先读
if err == nil && holder != id { return ErrDuplicate }
b.rdb.HSet(ctx, idxKey, slot, id)                       // 后写
```

两个并发写者（不同 id、**相同** unique 值）都读到 `redis.Nil`，都判定"没人占"，
都写入（后写覆盖）。互相看不见，**双双 commit**。

结果：集合里出现两份相同 unique 值的文档。此后任一条被删除都可能把 slot 释放给
另一条，状态进一步错乱。

上一轮的 commit/rollback 只解决了**泄漏**，没有解决**并发双占**——这是两个不同的
问题，报告分得很准。

### 3.2 TS 侧失败路径不释放（S2-6，确定性触发）

上一轮的 commit/rollback **只落了 Go 端**。TS 的 `enforceUnique` 仍是单个 `release`：

- `put`：`hset` 抛错时 release 丢失（无 try/finally）
- `putScoped`：owner 检查失败返回 false、竞争 64 次耗尽 throw —— 都不 release
- `putIfAbsent`：`inserted === false` 不 release，且 `releaseUnique` 对
  `oldDoc === null` 直接返回，本次新 claim 的 slot 根本不在释放集合里

**无需竞态即可触发**：用户对已存在的 id 做 `hsetnx`，带一个全新的 email。
`enforceUnique` 已把 `__uniq:email:new@x → b` 写进去，随后 `inserted=false` →
slot 永久占用 → **此后任何人用该 email 写入都 409**，直到 b 被重写换值或进程重启。

## 修复

### 原子占位

`HGET`→`HSET` 换成 **`HSETNX`**：占位与判空成为同一个操作。

```go
fresh, err := b.rdb.HSetNX(ctx, idxKey, slot, id).Result()
if !fresh {
    holder, herr := b.rdb.HGet(ctx, idxKey, slot).Result()
    if herr == nil && holder != id { return ErrDuplicate }   // 真冲突
    if isRedisNil(herr) { /* 两次调用之间消失了：补写并标记为本次创建 */ }
}
```

`fresh` 同时是回滚所需的信息：**只有本次创建的槽位才能回滚**——发现时就已指向同一
id 的槽位属于已存文档，动它是错的。

### TS 补齐 commit/rollback

`enforceUnique` 返回 `{ commit, rollback }` 一对，与 Go 同构，**恰好执行一个**：

| 结局 | 动作 |
|---|---|
| 文档写入成功 | `commit()` —— 释放不再持有的旧值 |
| hset/hsetnx 抛错 | `rollback()` |
| owner 检查拒绝（403） | `rollback()` |
| WATCH 竞争耗尽 | `rollback()` |
| `inserted === false` | `rollback()` |
| 占位中途被拒 | 内部 `unclaim` —— 同一文档已占的字段不能留下 |

## 涉及文件

| 文件 | 类型 |
|---|---|
| `index.go` | 修改（HSETNX 占位、takenSlot.fresh） |
| `ts/src/kvrocks.ts` | 修改（HSETNX + commit/rollback + unclaim） |

## 验证

```bash
go test -count=1 -run 'UniqueClaimRollback|UniqueIndex' -v .
go test -count=1 -run 'UniqueConflict' -v ./httpserve/
cd ts && node --import tsx --test test/server.test.ts   # 409 冲突用例
```

并发双占的验证（建议在真机上手工跑一次）：并发发起两个 `hset`，不同 id、相同
unique 值，断言**恰好一个** 200、一个 409。

## 验收标准

1. 并发写入相同 unique 值：一个成功，一个 409。不允许双双成功。
2. 对已存在 id 的 `hsetnx`（带全新 unique 值）失败后，该值**仍然可被他人使用**。
3. owner 检查拒绝的写、竞争耗尽的写，都不留下槽位占用。
4. 同文档重写相同 unique 值不冲突。
5. 删除文档释放其槽位。
6. 双端行为一致：TS 的冲突是 409/`conflict`，与 Go 的 `ErrDuplicate` 对齐。

## 风险与影响

- 稀疏语义不变：缺失/nil 值不占槽位，多个文档可以都不带这个字段。
- 仍非完全事务：占位与文档写是两条命令，进程崩在中间可能留下陈旧占位。它自愈
  （同 id 下次重写会重新占位），且不影响读。这条限制在 `docs/01-data.md` 有记录。
