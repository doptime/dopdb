# dopdb: Master Developer Context (Agent Cookbook)

精炼、全覆盖的用法参考。给 AI 编码代理或熟手照着写。哲学见首段,细节见 `docs/`。

**Core Philosophy**
1. **一份 schema,两端对等**:同一 schema 同时驱动 Go 与 TypeScript 两个**完整等效实现**(同 URL 线协议、同命令词表、同 `@`-绑定/隔离/权限)。可混用(Go 服务 + TS 客户端,或反之)。
2. **前端直连数据,无接口层**:前端不写 fetch,直接调"数据库方法"(`db.coll.hGet(...)`),框架做鉴权/隔离/路由。复杂逻辑才用函数式 API。
3. **String keys only**:键一律字符串。整数键禁止(JS 大整数精度丢失)。ID 一律转字符串。
4. **直连 Mongo**:根包直接用 `go.mongodb.org/mongo-driver/v2`,无 Store 抽象。`$inc`、change stream、唯一索引、`2dsphere` 都能用。
5. **闭合命令词表**:只有 18 个命令(见 §3),词表外一律 400。
6. **`@`-绑定**:服务端注入 `@`-前缀的上下文(用户、请求信息、目标元数据),客户端传来的 `@`-键一律剥除(防伪造)。

---

## 1. Infrastructure & Config

**DB**:MongoDB(watch 需副本集)。**配置**:`config.toml`(本地)或 `CONFIG_URL`(生产)。**多数据源**:可写多个 `[[mongo]]`;每请求用 `?ds=<name>` 选,缺省 `default`。数据源**不进路径**。

```toml
[[mongo]]
  Name = "default"
  URI  = "mongodb://127.0.0.1:27017/?replicaSet=rs0"
  DB   = "app"
[http]
  Port       = 8080
  JWTSecret  = "..."          # HS256 密钥;RS256 用 PEM/SPKI 公钥
  CORSOrigins = ["https://app.example.com"]
```

---

## 2. URL / 线协议(权威参考)

- **数据命令**:`/<base>/<cmd>/<coll>?ds=<name>`(base 默认 `/api`)。例:`/api/hget/notes?f=n1`。
- **函数式 API**:`/api/<name>`。
- **键**:`?f=<id>`(可多值:`?f=a&f=b`);`?f=@uid` = "我自己的那条"。
- **find 选项**:`?s=<json>` 排序、`?p=<json>` 投影(均拒 `$`-操作符 → 400)、`?limit=<n>`(缺省 100,上限 1000)。
- **方法**:读 = `GET`,写 = `POST`,`watch` = `GET` + SSE(`text/event-stream`)。
- **请求体**:写命令的值放 JSON body(上限 1 MiB,超 → 413)。
- **错误**:JSON `{ "error": "...", "code": "<class>" }`,见 §8。

---

## 3. 命令词表(闭合,18 个)

| 类 | 命令 |
|---|---|
| 读 | `hget` `hgetall` `hkeys` `hvals` `hlen` `hexists` `hmget` `count` `find` `findone` |
| 写 | `hset` `hsetnx` `hdel` `del` `hincrby` `hincrbyfloat` `hmset` |
| 流 | `watch`(change stream → SSE) |

`hset` 命中已存在键 = 覆盖;`hsetnx` 命中已存在键(无论归属) = `{inserted:false}`,绝不 403、不泄漏归属。

---

## 4. Backend (Go)

**Lang**:Go 1.24+ **Package**:`github.com/doptime/dopdb`(+ `api/` `httpserve/` `config/`)。

### 4.1 定义集合

```go
import "github.com/doptime/dopdb"

type Note struct {
    ID    string `json:"id"   bson:"_id"`            // _id 即键(字符串)
    Owner string `json:"owner" bson:"owner"`          // 归属字段(owner-scope 用)
    Text  string `json:"text"  bson:"text" validate:"required"`
}

// 工厂:New[K, V](opts...)。K=键类型(字符串),V=值类型(指针或值)。
// json 标签 == bson 标签(HTTP body 经 JSON round-trip 解进 V)。
var Notes = dopdb.New[string, *Note](
    dopdb.WithCollection("notes"),   // 集合名(否则由 V 类型名推导);WithKey 是别名
    dopdb.WithDB("default"),          // 非默认数据源时指定
).HttpOn(dopdb.HGet | dopdb.HGetAll | dopdb.HSet | dopdb.HDel)
```

### 4.2 HttpOn:暴露 + 授权(一处声明)

`HttpOn(...)` 把集合注册到 HTTP 层**并**声明客户端可调哪些命令——doptime/redisdb 风格。**它取代单独的 `RegisterHttp` + 逐命令 `Grant`**。

```go
Notes.HttpOn()                                   // 无参 = debug:全部命令开(先跑起来)
Notes.HttpOn(dopdb.ReadOnly)                      // 只读
Notes.HttpOn(dopdb.HGet | dopdb.HGetAll | dopdb.HSet | dopdb.HDel) // 精确集合
Notes.HttpOn(dopdb.HashAll)                        // = All,doptime 兼容别名
```

**权限位**(`dopdb.Perm`,逐命令一位):`HGet HSet HSetNX HDel Del HExists HGetAll HKeys HVals HLen HIncrBy HIncrByFloat HMSet HMGet Count Find FindOne Watch`。
**分组**:`ReadOnly`(全部读)、`Writes`(全部写)、`All`(全部)、`HashAll`(= All 别名)。

**推荐工作流**:先 `.HttpOn()` 全开联调 → 再用 agent 审查收紧。收紧两法:
- 改源码 flags(`.HttpOn(dopdb.HGet | dopdb.HSet)`);或
- 运行时覆盖:`dopdb.SetHttpPerm("notes", dopdb.HGet, dopdb.HGetAll)`(无参 = 全禁)。
- 审查内省:`dopdb.HttpPermNames(p)` → 该位掩码授予的命令名列表;`dopdb.HTTPPerm("notes")` → 当前位掩码。

> 门判定:`dopdb.HttpAllowed(cmd, coll)`(HttpOn 位掩码为准)。旧的 `httpserve.Permissions`(`Grant/Deny/SaveJSON`)仍可用作运行时覆盖/兼容,与 HttpOn 取或。

### 4.3 owner-scope(行级隔离)

```go
// 声明:集合 notes 按字段 owner 隔离,owner 绑定 JWT claim "uid"。
dopdb.SetOwnerScope("notes", "owner", "uid")
```
效果:整集合读取(`hgetall/find/count/hkeys/hlen`)被强制 AND 上 `{owner: 我}`;按键操作校验归属。客户端无法放宽。

### 4.4 `@`-绑定

服务端注入、客户端 `@`-键一律剥除:
- **身份**(JWT):`@uid`、`@email`、`@role` 等。
- **请求信息**:`@remoteAddr`、`@host`、`@method`、`@path`、`@rawQuery`。
- **目标元数据**:`@key`(集合键)、`@field`(字段)。
`?f=@uid` = "我自己的那条记录"。Go 结构体可用对应 bson 字段承接(框架按 owner-scope/绑定注入)。

### 4.5 函数式 API(纯 CRUD 不够时)

```go
import "github.com/doptime/dopdb/api"

type SyncReq struct{ Email string `json:"email" validate:"required,email"` }
type SyncRes struct{ Status string `json:"status"` }

// 暴露为 /api/sync(Req 后缀去除、小写)。流水线:decode → Validate → Func。
var SyncApi = api.Api(func(req *SyncReq) (*SyncRes, error) {
    return &SyncRes{Status: "ok"}, nil
})
```

### 4.6 启动

```go
import (
    "log"
    "github.com/doptime/dopdb/config"
    "github.com/doptime/dopdb/httpserve"
)
func main() {
    cfg, _ := config.Load("config.toml")
    // 集合用 .HttpOn() 已注册并授权;直接起服务。
    log.Fatal(httpserve.Serve(cfg))
}
```

### 4.7 Go 集合方法(无 `context.Context` 入参——服务端方法)

`HGet(k)` · `HSet(k,v)` · `HSetNX(k,v) (bool,error)` · `Save(v)`(从结构体取 PK) · `HMGet(...k)` · `HMSet(map[k]v)` · `HGetAll() map[k]v` · `HDel(...k)` · `Del(k)` · `HExists(k) bool` · `HKeys()` · `HVals()` · `HLen() int64` · `HIncrBy(k, fieldPath, deltaInt64)` · `HIncrByFloat(k, fieldPath, deltaFloat64)`。Scoped 变体(owner-scope 集合内部用):`HSetScoped/HGetScoped/...`。

---

## 5. Frontend / Server (TypeScript)

**包**:`dopdb`。浏览器用 `dopdb/client`,Node 服务端用 `dopdb/server`。

### 5.1 定义 schema(两端共用)

```ts
import { collection, f, HGet, HGetAll, HSet, HDel, ReadOnly, All } from "dopdb";

export const schema = {
  Notes: collection({
    _id: f.string(),
    owner: f.string().bind("@uid"),     // 绑定:owner 来自 JWT uid,客户端改不了
    text: f.string().required(),
  })
    .named("notes")
    .ownerScope("owner")                  // 行级隔离
    .httpOn(HGet | HGetAll | HSet | HDel),// 暴露 + 授权;无参 = All(debug)
};
```

`f`:`f.string()` `f.number()` `f.boolean()` `f.object(...)` …;链式 `.required()`、`.bind("@uid")`、`.default(x)` 等。
`.httpOn(...)`:与 Go 同义。常量 `HGet…Watch`、`ReadOnly`、`Writes`、`All`、`HashAll` 从 `dopdb` 导出(位值与 Go 一致)。

### 5.2 浏览器客户端(无 fetch 代码)

```ts
import { clientDb } from "dopdb/client";
import { schema } from "./schema";

const db = clientDb(schema, {
  baseUrl: "https://api.example.com",
  token: async () => await getJWT(),   // 静态串或异步函数
});

// CRUD —— 直接"调数据库":
await db.notes.hSet("@uuid", { text: "买牛奶" }); // 新建,后端生成 id
const mine = await db.notes.hGetAll();             // Map<id, Note>,只含我的
await db.notes.hSet(id, { text: "改一下" });        // 更新
await db.notes.hDel(id);                            // 删除
```

| 操作 | 方法 | 键策略 |
|---|---|---|
| List | `hGetAll()` | 取我 hash 全部(owner-scope 过滤) |
| Create | `hSet("@uuid", v)` | `"@uuid"` 触发后端生成 id |
| Update | `hSet(id, v)` | 用已有 id |
| Delete | `hDel(id)` | — |

### 5.3 Node 服务端(Go 的等效)

```ts
import { serve } from "dopdb/server";
import { schema } from "./schema";

const srv = await serve({
  schema,
  mongo: { uri: process.env.MONGO_URI!, db: "app" },
  jwtSecret: process.env.JWT_SECRET!,
  // 不传 permit/permissions:由各集合的 .httpOn() 位掩码授权(与 Go 一致)。
  port: 8080,
});
```

`serverDb(schema, db)` 在 Node 直接拿带类型的服务端集合;`defineApi(fn)` 定义函数式 API。

---

## 6. Security & Architecture Constraints

1. **String key 规则**:大整数当键会被 JS 破坏。`hGet("123…")` 安全,`hGet(123…)` 危险。两端一律转字符串。
2. **`@`-防篡改**:框架**移除**客户端传入的任何 `@`-前缀键,再注入系统 `@`-上下文。客户端无法伪造身份/归属。
3. **owner-scope**:声明后,整集合读取强制加 `{owner: 我}`,无需手写 `WHERE`,杜绝跨租户泄漏。
4. **过滤/排序/投影净化**:`find` 的 filter/`?s=`/`?p=` 拒绝 `$`-操作符与非法路径 → 400(两端一致)。
5. **JWT**:HS256 与 RS256(RS256 PEM/SPKI 公钥验签);拒绝 `alg:none`。
6. **数据命令默认**:集合未 `.httpOn()` → 该集合数据命令一律 403(显式暴露才可达)。
7. **不可逆**:密钥只走配置/环境,不进代码/日志。

---

## 7. watch(实时)

`watch` = Mongo change stream → SSE(`text/event-stream`)。**需 MongoDB 副本集**。owner-scope 下按 `{owner:我}` 过滤;断线按 resume token 续传;owner-scope 下 delete 事件因无 fullDocument 不投递。客户端订阅触发 `GET /api/watch/<coll>`。

---

## 8. 错误分类(5 类 + 500)

| HTTP | code | 含义 |
|---|---|---|
| 400 | `validation` | 校验失败 / 未知命令 / 非法 sort/proj/filter |
| 401 | `unauthorized` | JWT 缺失/无效 |
| 403 | `forbidden` | 命令未授权(HttpOn 未开)/ 越权访问他人数据 |
| 404 | `not_found` | 键不存在 / 集合未注册 |
| 409 | `conflict` | 唯一约束冲突等 |
| 500 | (server) | 内部错误 |

两端逐字段一致(有 conformance 测试守 `status` + `code`)。

---

## 9. 测试(标准套件)

见 `docs/TESTING.md`。要点:Go 单测就近放(`*_test.go`);需真 Mongo 的测试用 `DOPDB_TEST_MONGO_URI` 门控(缺则 skip);两端一致性由 `httpserve/conformance_test.go`(起 TS 子进程对打 Go,逐命令比对)守护——**不可用单端测试冒充一致性**。

---

## 10. Meta-Instructions for AI Code Generation

生成代码时严格遵守:

1. **键一律字符串**;`"@uuid"` 触发后端生成 id;`?f=@uid` 表示"我的记录"。
2. **后端**:`dopdb.New[string, *T](dopdb.WithCollection("name"))`;**用 `.HttpOn(...)` 暴露 + 授权**(联调先 `.HttpOn()` 全开,再收紧),不要再写 `RegisterHttp` + 逐命令 `Grant`。结构体标签 `json`(== `bson`)+ `validate`。多租户加 `dopdb.SetOwnerScope(coll, ownerField, claim)`。
3. **前端**:`collection(shape).named().ownerScope().httpOn(...)`;`clientDb(schema, {baseUrl, token})`;直接 `db.coll.hSet/hGetAll/hDel`,**不写 fetch、不写接口层**。
4. **权限**:数据命令默认 403,必须 `.httpOn(...)` 才可达;`.httpOn()` 无参 = 全开(仅 debug,务必提醒用户收紧)。
5. **`@`-键**:不要让客户端传 `@uid`/`@owner` 等——框架剥除并注入;归属用 `.bind("@uid")`(TS)或 owner-scope 声明。
6. **命令**:只用 §3 的 18 个;读 GET、写 POST、watch SSE。
7. **一致性**:任何两端行为改动,必须用 `conformance_test.go` 验(同请求打两端、diff 为空),不得用单端测试冒充。
8. **导入**:TS 权限常量从 `dopdb` 导入(`HGet`/`ReadOnly`/`All` 等);浏览器 `dopdb/client`,Node `dopdb/server`。
