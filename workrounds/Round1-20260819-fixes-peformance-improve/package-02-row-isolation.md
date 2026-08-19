# package-02 · 行级隔离：数字 claim、声明时效、空转泄漏

> 严重度 🔴。对应审计 S2-2、S2-3、S2-28。

## 问题

### 2.1 数字 uid 让隔离静默失效（S2-2）

JWT 解码用 `dec.UseNumber()`，所以数字 claim 是 `json.Number`。它被原样放进 owner
谓词 `M{field: v}`，再交给比较器——而 `json.Number` 是**命名字串类型**：

- `asFloat` 的 switch 覆盖 float/int/uint 全系，**没有** `case json.Number`
- 类型 switch 的 `case string` 也不匹配它

所以它与任何值都不等。实测确认：

```
BUG CONFIRMED: asFloat(json.Number) fails (got 0 ok=false)
BUG CONFIRMED: numeric-claim owner predicate never matches -> row unreachable
```

场景：签发方把 uid 铸成数字（JWT 里极常见）。首次 `hset` 成功（文档不存在时
`putScoped` 不做 owner 检查），此后**本人**的 `hset/hdel/hincrby` 一律 403，
`hgetall/find/watch` 永远看不到这一行——**数据永久不可达，等效丢失**。

TS 侧用 `String(cur) !== String(ownerVal)`，不受影响 → **双端从此分叉**。

### 2.2 接管在途声明（S2-3，上一轮引入的回归）

上一轮为了解决"声明长于数据"（Redis 删空列表后 owner 声明还在，别人永久 403），
加了 `takeOverStaleClaim`：holder 不同 **且** 数据 key 不存在 → 接管。

问题：**合法首写者恰好就处在这个状态**。`claimOwner` 成功后，写命令还在路上，约一个
RTT 的窗口里数据 key 确实不存在。攻击者对已知 key 名高频写即可狙击：

- B claim 成功、写未发出 → A 接管为 owner=A → A 写入成功
- B 那条已过检查的写落在 A 名下 → key 里是 B 的数据、owner 是 A
- A 可以读改 B 的数据；B 对自己刚写的数据读空/403

### 2.3 空转与失败路径留下声明（S2-28）

`rw()` 先 claim 再执行命令。若命令是 no-op 或失败：

- `LPOP`/`RPOP` 返回 `redis.Nil` → 直接 return，**跳过** releaseIfEmpty
- `LSET` 越界、`LINSERT` pivot 不存在 → 同样
- `ZAdd`/`ZRem` 的空参数判断在 `rw()` **之后** → 空请求也留 claim

对随机不存在的 key 名反复 lpop → `__owner` 每个名字留一条永久声明，无上限增长。
叠加 2.2 的宽限期后，这条从"慢性泄漏"升级为"直接锁死"——本轮修复时就是这么被
集成测试抓出来的。

## 修复

**2.1** `asFloat` 加 `case json.Number`（走 `Float64()`）；`typeRank` 把它归入数字类，
使排序与比较都正确。一行级修复，影响面精确。

**2.2** 声明带时间戳：`owner\x1f<unixMillis>`，**30 秒宽限期**内不可接管。

- 老格式（无分隔符）解析为时间戳 0 → 视为足够老，可接管。它按定义不可能在途，
  向后兼容且安全。
- `checkOwner` / `ownedKeys` / 接管事务全部改为**解析后比较 owner**，不再比较原始值。

**2.3** 三处补齐：
- `LPop`/`RPop` 的 `redis.Nil` 分支、`LSet` 失败分支、`LInsert` 之后 → `releaseIfEmpty`
- `HttpZAdd`/`HttpZRem` 的空参数检查**移到 `rw()` 之前**——不写任何数据的请求不该占名字

## 涉及文件

| 文件 | 类型 |
|---|---|
| `query.go` | 修改（json.Number） |
| `kvrocks.go` | 修改（声明时间戳、宽限期、接管事务） |
| `list.go` `zset.go` | 修改（空转/失败路径释放） |
| `ts/src/kvrocks.ts` | 修改（同构：时间戳、宽限期、解析比较） |

## 验证

```bash
go test -count=1 -run 'OwnerClaim|ScopedIncr' -v ./httpserve/
go test -count=1 -run 'TestMatch' -v .          # json.Number 比较
cd ts && node --import tsx --test test/server.test.ts
```

手工验证 2.1（无服务器）：

```go
var n json.Number = "123"
f, ok := asFloat(n)                     // 必须 ok == true, f == 123
matchFilter(map[string]any{"owner": int64(123)}, M{"owner": n})  // 必须 true
```

## 验收标准

1. 数字 uid 的 owner 谓词能匹配存储的数字 owner 字段；建-读-写-删全程可达。
2. 活的声明（30 秒内）**不可**被他人接管；他人得到 403。
3. 数据已不存在的陈旧声明**可以**被接管——包括 TTL 到期、崩溃遗留两种来源。
4. 列表被 pop 空后，key 名对其他用户重新可用。
5. 对不存在的 key 反复 `lpop`，`__owner` 不增长。
6. 空参数的 `zadd`/`zrem` 不留下声明。

## 风险与影响

- **存储格式变更**：`__owner` 的值从 `<owner>` 变为 `<owner>\x1f<millis>`。
  读路径兼容老格式，**无需迁移**。若有外部工具直接读这个哈希，需要同步改。
- 宽限期 30 秒是个判断：远大于任何在途写的 RTT，远小于人会注意到的时间。
  crash 遗留的声明最多 30 秒后可回收。
