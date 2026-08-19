# package-04 · 键空间完整性：保留名拒绝

> 严重度 🔴。对应审计 S2-5。

## 问题

String/List/Set/ZSet 的用户条目与 dopdb 自己的簿记共享同一个键空间：

```
<ns>:<coll>:<key>            用户条目
<ns>:<coll>:__owner          owner 隔离索引
<ns>:<coll>:__events         变更频道
<ns>:<coll>:__uniq:<field>   unique 占位
```

一个名叫 `__owner` 的条目，解析出来**就是**该集合的隔离索引本身。

上一轮加了 `entryKey()` 守卫，但只覆盖了**单键**路径。TS 的两条多键路径漏了：

- `strsetall`：循环内 `pipe.set(b.memberKey(coll, k), ...)`
- `strdel`：`b.redis.del(...owned.map((k) => b.memberKey(coll, k)))`

（原因很具体：上一轮的批量替换只处理了 `(coll, key)`，没处理 `(coll, k)`。）

### KVRocks 把后果放大了一个量级

标准 Redis 上，对已存在的 hash key 执行 `SET` 会报 **WRONGTYPE**，写入无效果。
**KVRocks 2.16 不报错，静默把 key 转成 string**。于是：

- `strsetall {"__owner": "x"}` → owner 索引哈希被字符串覆盖 → **索引整体消失**
- 该集合后续所有 scoped 写的 `claimOwner`（HSETNX）打在字符串上 → WRONGTYPE → 500
- **整个集合的隔离被砖化**

`strdel f=__owner` 更直接：一条 DEL 删掉索引。

在 Redis 上这是"写保留名无效果"，在 KVRocks 上是"**一次写入砖化整个集合**"。

## 修复

`entryKey()` 覆盖**所有** String/List/Set/ZSet 路径，读写皆然：

```ts
// strsetall：先把整批 key 全部校验，再动手写任何一条
for (const [k] of entries) b.entryKey(coll, k);
// strdel：删除任何东西之前先校验
b.entryKey(coll, k);
```

拒绝的名字：以 `:__owner` / `:__events` 结尾、含 `:__uniq:`、空键名。
返回 **400 `validation`**（`ErrReservedKey`）。

### 顺序也是修复的一部分

守卫必须在 `claimOwner` **之前**。上一轮 TS 的 `guardWrite()` 先声明后校验，
于是一次被拒的写仍然在 owner 索引里留下声明——把这个名字锁给一个从没写入过的用户。
这条是在写复现脚本时当场抓到的，报告里没有。

Hash 集合不受影响：它的文档 id 是哈希的 **field**，不是 key，不存在碰撞。

## 涉及文件

| 文件 | 类型 |
|---|---|
| `ts/src/server.ts` | 修改（strsetall / strdel 补 entryKey） |
| `ts/src/kvrocks.ts` | 修改（entryKey 实现与注释） |
| `string.go` `list.go` `set.go` `zset.go` | 修改（Go 侧全路径覆盖，含读路径） |

## 验证

```bash
go test -count=1 -run 'ReservedKeyNames' -v ./httpserve/
go test -count=1 -run 'Conformance' -v ./httpserve/   # 含 reserved key name 差分
cd ts && node --import tsx --test test/server.test.ts
```

**建议在真机 KVRocks 上补跑**（本轮在 Redis 7.0 上验证，SET-on-hash 静默转换是
KVRocks 特有行为）：

```
POST /api/strsetall/<coll>  body {"__owner": "evil"}   → 期望 400
TYPE <ns>:<coll>:__owner                                → 期望 hash（不是 string）
POST /api/strset/<coll>?f=normal                        → 期望 200（集合仍可用）
```

## 验收标准

1. `strset` / `strsetall` / `strdel` / `sadd` / `lpush` 等所有条目命令，
   对 `__owner`、`__events`、含 `__uniq:` 的键名一律 400。
2. 攻击尝试之后，该集合的正常 scoped 读写**仍然可用**（索引未被破坏）。
3. 被拒的写**不留下** owner 声明。
4. 空键名被拒。
5. 双端同样返回 400/`validation`（conformance 覆盖）。

## 风险与影响

- 若已有部署真的使用了 `__owner` 这类名字作为业务键（极不可能），升级后这些键将
  无法访问。这是有意的：它们本来就在破坏索引。
