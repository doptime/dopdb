# v0 计划 · dopdb · R2(2026-06-24)

> 本地理解与拟建方案。只写不执行。重点写清 V4 如何客观自证、codec 字段映射如何验。

## 环境快照

| 项 | 实测值 |
|---|---|
| Go | 1.24.5 darwin/arm64 |
| MongoDB | Docker `localhost:27017`, 无认证, R1 V3 已跑通 |
| Node | v19.0.0, npm 8.19.2 |
| DOPTIME_TEST_MONGO_URI | 可复用 `mongodb://localhost:27017` |

## 包分解

---

### P-V4-http-mongo-integration 🔴 承重

**目标**: 真实 MongoDB 后端上, httpserve 全栈端到端——JWT 验证 → @-绑定 → 权限 → 数据命令 + API 流水线 → 落 Mongo 往返——全部跑通。

**拟建**: 新建 `httpserve/http_integration_test.go`(预授权文件名), 用 `httptest.NewServer` + 真实 `mongostore` 后端(非 memstore), 覆盖 6 项契约。

测试结构体沿用现有 serve_test.go 的 `Profile` / `Order`, 另加一个含 bson+json 双标签 + 时间戳 + @uid 字段的 `CodecTestProfile` 专验 codec 映射:

```go
type CodecTestProfile struct {
    UID       string    `bson:"_id" json:"_id"`
    Name      string    `bson:"name" json:"name"`
    Role      string    `bson:"role" json:"role" mod:"default=member"`
    CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
    UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
    Owner     string    `bson:"owner" json:"owner" mod:"auto=@uid"`
}
```

**自证**(硬判据): 每条子测试用独立 `t.Run`, 全部用 `httptest.NewServer` 真实 HTTP, 后端换为 mongostore(BSONCodec)。

`go test -run TestHTTPMongo -v ./httpserve` 退出 0, 输出无 `SKIP`。

逐子测试硬判据:

#### 子测试 1 · JWT 验证

| 场景 | 预期 HTTP 状态码 |
|---|---|
| 无 Authorization header | 401 |
| `alg: none` 伪造 token | 401 |
| 签名错误 token | 401 |
| 合法 HS256 token(带 uid claim) | 200 |

断言: `rr.Code == http.StatusXXX`

#### 子测试 2 · @-绑定与防伪

- 用合法 uid=u1 的 token, `?f=@uid` 做 HSET → HGET
- **断言 1**: 取回响应 JSON 中 `_id == "u1"` (JWT claim 注入)
- **断言 2**: 客户端在 query 里传 `?f=@uid&@uid=u2` → 取回 `_id == "u1"` (伪造被剥离, 不是 u2)
- **断言 3**: 直查 Mongo(绕过 HTTP): `mongosh db.<collection>.findOne({_id:"u1"})` → `_id == "u1"`, `name == "  Alice  "`(trim 在写入期生效)
- 用 mongosh 验证, 输出存入回执关键数字

#### 子测试 3 · 权限白名单

- `AutoAuth=true` 首次调 HGET-Profile → **断言**: status 200(首用授予)
- `perms.Deny("HGETALL", "Profile")` → 调 HGETALL-Profile → **断言**: status 403

#### 子测试 4 · 数据命令 @ 真 Mongo

- HSET Profile name=Alice → HGET → **断言**: JSON 响应 `name == "Alice"`, `_id == "u1"`
- HGETALL → **断言**: 数组长度 == 集合中已有文档数
- HDEL → HGET → **断言**: 响应为空或 404

关键数字: 每条 HTTP 调用的状态码、响应 body 中的字段值。

#### 子测试 5 · /api/<name> @ 真 Mongo

- 注册一个 Go API endpoint, handler 内对一个集合做 HSet → HGet
- HTTP POST /api/<name> → **断言**: 响应含 handler 预期输出
- 直查 Mongo 验证 handler 内写的文档已落盘

#### 子测试 6 · codec 字段映射 (V3 遗留, 最核心风险点)

**风险**: HTTP 入口用 JSON 解码(json tag), mongostore 用 BSON 落盘(bson tag)。若 struct 的 `json` 与 `bson` 标签不一致, 会出现:
- 写入: HTTP 解码时按 `json` tag 设字段 → 编码给 Store 时用 `defaultCodec.Marshal`(BSONCodec, 按 `bson` tag)
- 取回: Store 返回 BSON 字节 → HTTP 用 JSON 编码响应(按 `json` tag)
- 若 json tag ≠ bson tag, 落盘字段名与 HTTP 响应字段名会串

**验法**(三层对照):

**层 1 — HTTP JSON 进 → HTTP JSON 出:**
- 发 HSET body `{"name":"Alice","role":""}`(role 走 default=member)
- 收 HGET 响应 JSON
- **断言**: 响应字段名集合 == `{"_id", "name", "role", "createdAt", "updatedAt", "owner"}` (6 个)
- **断言**: `_id == "u1"`, `name == "Alice"`, `role == "member"`, `createdAt` 非空字符串, `updatedAt` 非空字符串, `owner == "u1"`

**层 2 — 直查 Mongo 原始 BSON:**
- 用 `mongosh` 或 `mongo.Collection.FindOne` 直查该文档
- **断言**: BSON 字段名集合 == `{_id, name, role, createdAt, updatedAt, owner}` (bson tag 值)
- **断言**: `_id` 的 BSON 类型 == String(非 ObjectId/Number)
- **断言**: `createdAt` 的 BSON 类型 == Date(非 String)
- **断言**: `role` == "member"(default modifier 在写入期生效, 通过 BSON 落盘)

**层 3 — 字段名 1:1 对齐:**
- **断言**: JSON 响应字段名集合 == BSON 文档字段名集合 (一一对应)
- **断言**: 若 json tag 与 bson tag 故意不一致(如加一个 `json:"jname" bson:"bname"` 的字段) → 写入后 HTTP 响应中该字段名为 `jname`, 但 Mongo 中为 `bname` → 这**是预期行为**(json tag 控制 HTTP 响应, bson tag 控制 Mongo 落盘), 但需记录此差异, 交云端裁是否属于 facade 风险

**层 4 — @uid 在 API 流水线中的 codec 路径:**
- 定义 API 输入结构体含 `json:"@uid"` 字段:
  ```go
  type SelfQuery struct {
      UID  string `json:"@uid"`
      Name string `json:"name"`
  }
  ```
- 经 HTTP POST /api/selfquery → handler 输入 `.UID` == JWT claim "u1"(@ 字段从 JWT 注入)
- 客户端 body 里伪造 `{"@uid":"u2"}` → 被 buildParams 剥离 → handler `.UID` 仍 == "u1"
- **断言**: handler 内部拿到的 UID 值

**关键数字**:
- HTTP 响应 JSON 字段名列表 (6 个)
- Mongo BSON 文档字段名列表 (6 个)
- `_id` 在两层中的值(均 == "u1")
- `createdAt`/`updatedAt` 是否非空(两层)
- `role` 是否为 "member"(两层)

---

### P-W1-wasm-ts 🟢 并行轨

**目标**: 在 go 1.24.5 + node v19 上复跑 wasm 构建 + TS 编译 + smoke test, 证 wasm 桥 + TS 客户端工作。

**拟建**:
1. `make wasm` — 重建 `dopdb.wasm` + 同步 `wasm_exec.js`(用 Go 1.24 的运行时)
2. `make ts` — `npm install` + `tsc`
3. `node clients/ts/smoke-test.mjs` — SDK 端到端

**自证**:
- `make wasm` 退出 0, `clients/ts/wasm/dopdb.wasm` 存在且非空, `wasm_exec.js` 与 Go 1.24 一致
- `make ts` 退出 0, `clients/ts/dist/` 有 .js/.d.ts 产物
- smoke-test 退出 0, stdout 含 `ALL TS SDK INTEGRATION TESTS PASSED`

**前置检查**: Go 1.24.5 ✅, node v19 ✅, npm 8.19 ✅, 网络可用(R1 已证)

---

## 执行顺序与并行

```
承重主线: V4  (站在 R1 封存的 V3 上)
并行轨:   W1  (不依赖 Mongo, 独立)
```

- V4 与 W1 互不依赖, 可并跑
- V4 卡在 codec/@-注入需云端裁 → 转 W1 封掉
- W1 卡住(无 node / 编译问题) → 不阻塞 V4

## 已知风险

1. **codec 字段映射不一致**: json tag 与 bson tag 在现有结构体中已对齐(如 `bson:"_id" json:"_id"`), 但从未在真 Mongo 上端到端验过。层 3 的对齐断言专门抓这个 facade。
2. **@ 字段经 API 流水线的路径**: `api.CallByMap` 的 `decodeInput` 用 JSON 编解码, `buildParams` 注入的 `@uid` 进 param map → 再被 JSON 解码为输入结构体。该路径从未在真 Mongo 上验。
3. **Go 1.24 wasm_exec.js 可能变化**: 1.24 的 `wasm_exec.js` 若与 1.22 有差异, 可能影响 wasm 桥。smoke-test 若过则说明兼容。
4. **权限内存态**: `Permissions` 是内存 map, `httptest.NewServer` 的每次测试用同一个 `*Handler` 实例 → 首用授予的权限在测试间共享, 需每个子测试独立 new Handler。
5. **时间戳比较**: `createdAt`/`updatedAt` 在 HTTP 响应为 RFC3339 字符串, 在 Mongo BSON 为 Date 类型。两层均验"非空/非零", 不比较具体值。
