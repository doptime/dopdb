# dopdb

> `doptime` + `redisdb` + `doptime-client` 合并重写,数据后端换成 **MongoDB**。
> 一份 schema 同时产出**类型、校验、带类型的客户端、服务端**——不生成代码、不前后端各写一遍。

dopdb 有 **两套对等的完整实现**,共享同一套线协议与全部特性:

| 实现 | 位置 | 用途 |
|---|---|---|
| **Go** | 根包 `dopdb` + `api/` + `httpserve/` + `config/` | 服务端(直连 MongoDB 驱动) |
| **TypeScript** | `ts/` | 既能在 Node 跑同样的服务端,也能在浏览器做带类型的客户端 |

TypeScript 不是“客户端 SDK”,而是 Go 的等效重写:同样的 URL 方案、同样的命令词表、同样的 `@`-绑定/行级隔离/权限模型。两端可以混用(Go 服务 + TS 客户端,或反之)。

---

## 设计要点(本轮确定)

- **直连 Mongo,不要 Store 抽象**:删除了 `Store`/`Codec` 接口与 `memstore`/`mongostore`,根包直接用 `go.mongodb.org/mongo-driver/v2`。把 Mongo 当 Mongo 用(`$inc`、change stream、唯一索引、`2dsphere` 等)。
- **多数据源 + `?ds=` 参数**:配置可写多个 `[[mongo]]`;每个请求用查询参数 `?ds=<name>` 选择数据源,缺省为 `default`。数据源**不进路径**。
- **URL**:数据命令 `/api/<cmd>/<coll>`,函数式 API `/api/<name>`。键用 `?f=`(可多值)。
- **闭合命令词表**:`hget hset hsetnx hdel del hexists hgetall hkeys hvals hlen hincrby hincrbyfloat hmset hmget count find findone watch`。
- **`@`-绑定(很重要)**:服务端把 `@uid`/`@key`/`@field`/`@remoteAddr`/`@host`/`@method`/`@path`/`@rawQuery` 及 JWT claims 注入请求上下文;客户端传来的 `@`-键一律剥除(防伪造)。`?f=@uid` 即“我自己的那条记录”。
- **行级隔离(owner scope)**:声明某集合按 `owner` 字段隔离(对应某个 JWT claim),整集合读取会被强制加上 `{owner: 我}` 的过滤,客户端无法放宽。
- **权限:默认拒绝**:`Grant/Deny/Allowed` + JSON 持久化,键为 `COMMAND::collection`;**没有 AutoAuth**(不再首用即授权),授权一律显式。
- **JWT**:HS256 与 RS256(RS256 用 PEM/SPKI 公钥验签);拒绝 `none`。
- **watch**:Mongo change stream → SSE(`text/event-stream`),owner-scope 过滤、断线按 resume token 续传。**需要 MongoDB 以副本集运行**;owner-scope 下 delete 事件因无 fullDocument 不投递。
- **API 流水线极简**:`decode → Validate → Func`(去掉了 doptime 的 ParamEnhancer/ResultSaver/ResponseModifier 钩子链)。
- **WASM 已退场**:TS 是独立等效实现,不再通过 WASM 桥接 Go。

---

## 快速上手(Go)

```go
// 1) 定义集合(K=键类型,V=值类型)。schema 即真相,无代码生成。
type User struct {
    Name  string `json:"name"  bson:"name"`
    Email string `json:"email" bson:"email" index:"unique"`
    Age   int    `json:"age"   bson:"age"   index:"1"`
}
users := dopdb.New[string, *User](dopdb.WithCollection("users"))

// 2) 暴露给 HTTP 数据命令层 + 声明行级隔离(可选)。
dopdb.RegisterHttp(users)
// dopdb.SetOwnerScope("users", "owner", "uid") // 若 User 有 owner 字段

// 3) 一行起服务:连接配置里的所有数据源 → 建 Handler → CORS → 监听。
cfg, _ := config.Load("config.toml")
perms := httpserve.NewPermissions()
perms.Grant("HGET", "users"); perms.Grant("HSET", "users"); perms.Grant("FIND", "users")
log.Fatal(httpserve.Serve(cfg, httpserve.WithPermissions(perms)))
```

服务端代码内部要直接读写(可信、无 scope/JWT),直接用 `users.HGet("u1")`、`users.Find(dopdb.M{"age": 30}, dopdb.FindOpt{})` 等原生方法。

## 快速上手(TypeScript)

```ts
// 同一份 schema 两端共用。
import { collection, f } from "dopdb";
export const schema = {
  users: collection({ name: f.string(), email: f.string().unique(), age: f.int() })
    .named("users"),
};

// 服务端 —— 主路径:在 Next.js 中接管 /api(App Router,零配置)
// app/api/[...slug]/route.ts
import { createNextHandler, Permissions } from "dopdb/server";
const perms = new Permissions().grant("HGET", "users").grant("HSET", "users");
export const { GET, POST, OPTIONS } = createNextHandler({
  schema, mongo: { uri: process.env.MONGO_URI!, db: "appdb" },
  jwtSecret: process.env.JWT_SECRET!, permissions: perms,
});
export const runtime = "nodejs"; // Mongo 驱动不兼容 Edge

// 或:独立 Node 服务端
import { serveFromConfig } from "dopdb/server";
await serveFromConfig("config.toml", { schema, permissions: perms });

// 浏览器(fetch,带类型):
import { clientDb } from "dopdb/client";
const db = clientDb(schema, { baseUrl: "https://api.example.com", getToken: () => localStorage.token });
await db.users.hset("u1", { name: "Ada", email: "ada@x.io", age: 30 });
const u = await db.users.hget("u1"); // 类型为 User | null
```

用 TS 时与 Go 完全无关:TS 服务端自己处理 JWT/@-绑定/owner-scope/权限/watch,直连 Mongo,不向任何 Go 后端转发。前缀名可配:把路由文件夹从 `api` 改成别的(handler 读 catch-all 段),客户端设 `apiBase` 对齐。非默认数据源:集合上 `.inDb("analytics")` → 客户端自动带 `?ds=analytics`。

---

## 构建 / 测试

```
make test          # go test ./...(集成测试在未设 DOPDB_TEST_MONGO_URI 时自动跳过)
make test-mongo    # 跑集成测试(需 DOPDB_TEST_MONGO_URI;watch 需副本集)
make build         # go build ./...
make ts            # 构建 TypeScript 实现(ts)
make ts-test       # 跑 TS 测试
make ts-typecheck  # TS 严格类型检查
```

> Go 侧绑定 driver v2,需在本机 `go build ./...` 确认(本仓库的 Go 代码按 mongostore 的既有 driver-v2 写法实现)。TS 侧已通过严格 `tsc --noEmit` 与全部单测。

## 仓库结构

```
dopdb.go            根包:泛型 Collection[K,V](原生可信 API)
types.go            M / FindOpt / SortKey / IndexSpec / 错误
mongo.go            具体 mongoBackend(直连 driver v2)+ Datasources 注册表
http_accessor.go    类型擦除桥(HttpAccessor)+ owner-scope 策略
modifiers.go        写入修饰器(时间戳 / @-绑定字段)
sanitize.go         过滤器消毒(防注入)
api/                函数式 API 端点(decode → Validate → Func)
httpserve/          HTTP 运行时:路由 / JWT / 权限 / 派发 / watch(SSE)/ Serve
config/             TOML 配置(多 [[mongo]];密钥从环境变量解析)
ts/         TypeScript 等效实现(浏览器客户端 + Node 服务端)
docs/               设计文档
delivery/           交付物
```

详细文档见 `docs/`(总览、数据层、HTTP/安全、配置、TypeScript、RUNBOOK)。
