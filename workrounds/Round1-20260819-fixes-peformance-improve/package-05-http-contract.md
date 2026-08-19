# package-05 · HTTP 契约：方法强制、参数校验、类型守卫

> 严重度 🟠。对应审计 S2-7、S2-9、S2-11、S2-13、S2-20、S2-32、S2-33。

## 问题与修复

### 5.1 GET 可以执行任何写命令（S2-13，双端）

`AGENTS.md §2` 一直写着"reads = GET, writes = POST"，但两端都没有强制
（Go 全文 `r.Method` 零匹配；TS 三个方法指向同一个 handler）。

这不只是整洁问题。**一个能被 GET 触发的写，就是一个能被链接触发的写**：邮件扫描器、
浏览器预取、CDN 预载、共享代理都会跟随链接，并带上客户端附加的 Authorization 头；
同时这个 URL 会落进缓存和访问日志，把令牌泄露面一起扩大。

`https://app/api/del/notes?f=n1` 出现在任何日志里，就等于数据被删。

**修复**：双端按读写位掩码强制。写命令仅 POST，否则 **405 `method_not_allowed`**；
非 GET/POST 一律 405。

> 加上强制后 conformance 和集成测试立刻变红——因为**测试自己在用 GET 发写命令**。
> 两端报的都是同样的 405，说明分叉是关的；红的是测试。已一并修正。

### 5.2 非 Hash 集合执行 H* 命令 → Go nil panic（S2-9）

```go
ha, _ := acc.(dopdb.HttpAccessor)  // 非 Hash 集合为 nil
```

只有 SQL 分支有 nil 守卫，HSET/HGETALL/FIND/HRANDFIELD/WATCH 直接解引用。而
`NewList(...).HttpOn()`（文档示例原样）的无参 `HttpOn()` = `All`，**包含全部 H* 位**
→ 任何人 `GET /api/hgetall/<listColl>` 都能触发 nil 解引用 → 每请求一次 panic，
可无限重复，是个 DoS 放大面。

**修复**：dispatch 顶部统一守卫，非 Hash 集合的 Hash 命令 → **404**（沿用 SQL 分支
既有的状态码，避免无谓的 wire 变更）。

TS 侧的对应行为是结构性差异，见 package-06。

### 5.3 未知 `?ds=` 静默回落（S2-7）

`Datasources.get` 对未知名字静默回落到 default。前端写 `?ds=analytics` 而运维忘了
配 → 所有请求静默写进 default 命名空间，**数据混库、零报错**。

**修复**：新增 `HasDatasource`，未注册 → **400 `validation`**。

### 5.4 请求头被写进文档（S2-11）

`buildParams` 把 `r.Header` **逐键**并入 `c.Params`，而 HSET 直接以 Params 为值源。
在值类型是 map 的集合上：`Authorization: Bearer <JWT 原文>`、全部 Cookie **明文入库**，
随后被任何 `hgetall`/`find` 读出 → 会话劫持。

**修复**：请求头**不再**并入 Params。框架没有任何地方把请求头当数据读——@-绑定从
已验证的 claims 取身份，少数需要的头是显式读的。

### 5.5 缺 `?f=` 静默写空键（S2-20，TS）

TS 无 f 时 `key = undefined`，ioredis 序列化为 `""` → `HSET hash "" doc`。记录落在
field `""` 下，`hgetall` 会返回这条幽灵记录，而按 key 读写永远摸不到。Go 同请求 400。

**修复**：TS 补齐单键（`KEYED_COMMANDS`）与多键（`MULTIKEY_COMMANDS`）校验 → 400。

### 5.6 `Atoi` 静默回退 0（S2-33，Go）

```go
idx, _ := strconv.Atoi(c.Queries.Get("index"))   // 失败即 0
```

- `?index=abc` → LSET **静默覆盖队头元素**（错元素数据损坏）
- `?count=abc` → LREM count=0 = **删除全部匹配**而不是一条

**修复**：新增 `intParam`，解析失败 → 400，与 `?f=` 缺失同风格。

### 5.7 HSCAN count 无上限（S2-32）

FIND 的 limit 有 1000 钳制，HSCAN 的 count 漏了 → `?count=999999999` 是单页全量扫描
+ 无界响应。**修复**：与 FIND 同样钳制。

## 涉及文件

| 文件 | 类型 |
|---|---|
| `httpserve/serve.go` | 修改（方法强制、nil 守卫、ds 校验、intParam、count 钳制） |
| `httpserve/context.go` | 修改（不再并入请求头） |
| `kvrocks.go` | 修改（`Datasources.Has` / `HasDatasource`） |
| `ts/src/server.ts` | 修改（方法强制、缺 f= 校验） |
| `httpserve/conformance_test.go` | 修改（写命令改 POST；新增方法强制差分用例） |

## 验证

```bash
go test -count=1 -run 'Conformance' -v ./httpserve/    # 含 MethodEnforcement / ErrorStatusParity
go test -count=1 ./httpserve/
cd ts && node --import tsx --test test/server.test.ts
```

手工：

```
GET  /api/hdel/<coll>?f=k       → 405，Allow: POST
POST /api/hdel/<coll>?f=k       → 200
GET  /api/hgetall/<listColl>    → 404（且服务端无 panic）
GET  /api/hget/<coll>?ds=nope&f=k → 400
POST /api/hset/<coll>           （无 f=）→ 400，双端一致
GET  /api/lset/<coll>?f=k&index=abc → 400（不是静默改第 0 个）
```

## 验收标准

1. 全部写命令 GET → 405，双端一致；POST 正常。
2. 非 Hash 集合的 Hash 命令 → Go 404，**服务端无 panic**（日志无 stack）。
3. 未知 `?ds=` → 400。
4. HSET 一个 map 值类型的集合后，文档里**不含** `Authorization`/`Cookie` 等头字段。
5. 缺 `?f=` 双端都是 400。
6. `?index=abc` / `?count=abc` → 400。
7. `?count` 超大值被钳制到 1000。

## 风险与影响

- **破坏性变更**：任何依赖 GET 发写命令的客户端会得到 405。这是协议本来就规定的，
  但如果有客户端在违规使用，升级前需要改。这一条应写进 CHANGELOG。
- Params 不再含请求头：如果有 API endpoint 依赖从 Params 读某个头，需要显式读取。
