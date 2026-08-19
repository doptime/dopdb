# package-06 · 双端一致性：响应形状、事件语义、错误码

> 严重度 🟠。对应审计 S2-8、S2-29、S2-30、S2-45，外加**两条审计没发现的**。

## 为什么这些能存活

上一轮四个修复只落 Go 端；conformance 只有 16 个用例，且 TS 侧用
`permit: () => true` 绕过权限门。**S2-8 和 S2-9 恰好都落在没覆盖的命令上——不是巧合。**
本包修行为，package-09 修"为什么没人发现"。

## 问题与修复

### 6.1 HGETALL 响应形状分裂（S2-8）

Go 返回 `[]V`（数组），TS 返回 `Record<id, Doc>`。而**客户端类型和文档都承诺 Record**
（`ts/src/client.ts`、`AGENTS.md`：`db.notes.hgetall() // Record<id, Note>`）。

按文档写 `all[id]` 的客户端连 TS 正常，切 Go（文档明确支持混用）**全部 undefined**。

**修复**：定死契约为 **Record**（与客户端类型和文档一致）。Go 的 `HttpGetAll` 按 ids
组装 map；`HVALS` 拆成独立的 `HttpVals` 返回数组（HVALS 的语义本来就是数组）。

### 6.2 事件分类不实（S2-29）

- TS `putScoped` 恒发 `"replace"` —— **事务内已经读过 prev，信息在手却不用**
- TS `putMany` 对全新 id 也发 `"replace"`

owner-scoped 集合首次创建文档时，订阅方收到 replace；依赖 insert 做新建通知的前端
永远收不到。双引擎混布时同一次写产生不同事件——直接违背核心等价契约。

**修复**：`putScoped` 按事务内 prev 是否存在如实上报；`putMany` 先批量 `hmget`
判存在再分类。

### 6.3 del 超报删除事件（S2-30）

TS 仍是批量 `hdel(...ids)` + `if (n > 0) for (const id of ids) publish("delete", ...)`
—— 批次里删掉一个就为**所有** id 发事件，包括从未存在的。Go 已改为逐 id pipeline。

**修复**：TS 照抄 Go 的逐 id pipeline —— 同样一次往返，但每条回复告诉我们**那个 id**
是否存在，事件因此精确。

### 6.4 watch 事件字段双端不一致（**审计未发现**）

Go 发 `{type, id, doc}`，TS 客户端类型声明的是 `key`。TS 客户端连 Go 服务端
`ev.key` 恒为 `undefined`。

**修复**：两端都发 `key`（canonical）与 `id`（兼容别名）。客户端类型把 `id` 标为
可选别名。**这条是在补 watch conformance 用例时当场抓到的。**

### 6.5 未知集合响应分裂（**审计未发现**）

Go 先查权限 → 403 `forbidden`；TS 先查注册 → 404 `not_found`。

**修复**：统一为**先查权限**。这不只是为了一致——对一个从未被授权的调用者回答
"没有这个集合"，等于把接口变成集合名探测器。403 既是一致的答案，也是不泄漏的答案。

### 6.6 413 错误码分裂（S2-45）

TS 两处 413 发 `"too_large"`，而 `errors.ts` 的 `PayloadTooLargeError` 是
`"payload_too_large"`；客户端映射表没有 413 条目，一律落 generic。

**修复**：统一 `payload_too_large`；客户端补 405/413 映射，新增
`MethodNotAllowedError`。

## 判断为"不改"的一条

**TS 端非 Hash 集合执行 H* 命令**（S2-9 的 TS 侧）。

Go 里 `NewList`/`NewSet`/`NewZSet`/`NewString` 是**不同的 Go 类型**，dispatcher 知道
命令不适用。TS 里所有集合都是 `collection()`，键布局由命令决定，**没有可校验的类型**：
`hgetall` 读的是空的哈希 key，与列表自己的 key 互不相干，返回 `{}`，不碰列表数据。

这是**结构性 API 差异，不是漂移**。给 TS 加集合类型是 API 新增，不是修 bug，仓促做
会破坏现有 TS 用户。

处理方式：**修 Go 的 panic（无歧义的 bug），把差异写进 conformance 测试和
`docs/04-typescript.md`，而不是假装一致。** 测试分别断言两端各自的契约，所以它不会
再悄悄漂移。

## 涉及文件

| 文件 | 类型 |
|---|---|
| `http_accessor.go` | 修改（HttpGetAll → map，新增 HttpVals） |
| `httpserve/serve.go` | 修改（HGETALL/HVALS 分派，watch 事件带 key） |
| `ts/src/kvrocks.ts` | 修改（事件分类、逐 id del） |
| `ts/src/server.ts` | 修改（权限门顺序、watch 事件字段） |
| `ts/src/client.ts` | 修改（WatchEvent.id 别名） |
| `ts/src/errors.ts` | 修改（405/413 映射、MethodNotAllowedError） |
| `httpserve/conformance_test.go` | 修改（新增 5 组差分用例） |
| `docs/04-typescript.md` | 修改（记录结构性差异与 watch 实况） |

## 验证

```bash
go test -count=1 -run 'Conformance' -v ./httpserve/
```

关键用例：
- `TestConformanceHashReadShapes` —— hgetall 是 map、hvals 是数组，双端
- `TestConformanceWatchEventShape` —— 双端 `ev.key` 都等于写入的 key
- `TestConformanceErrorStatusParity` —— 未知集合 / 缺 f= / 保留名 / 坏令牌
- `TestConformanceHashCommandOnNonHashCollection` —— 分别断言两端契约

## 验收标准

1. `hgetall` 双端都是 `{id: doc}`；`hvals` 双端都是数组。
2. owner-scoped 集合首次写入，事件是 `insert` 而非 `replace`，双端一致。
3. `hdel f=a&f=missing&f=b` 只广播实际删掉的 id，双端一致。
4. watch 事件的 `key` 字段双端都等于文档 key。
5. 未知集合双端都是 403 `forbidden`。
6. 413 的 code 双端都是 `payload_too_large`，客户端能还原成
   `PayloadTooLargeError`。
7. 非 Hash 集合的 H* 命令：Go 404、TS 空结果，**且有测试钉住**。

## 风险与影响

- **破坏性变更**：Go 的 `hgetall` 响应从数组变成对象。任何按数组消费 Go 端 hgetall
  的客户端需要改。这是向文档和 TS 客户端类型对齐，方向是对的，但必须写进 CHANGELOG。
