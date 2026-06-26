# 04 · TypeScript 等效实现(Next.js 接管 · 浏览器客户端 · 独立 Node · 函数式 API)

`ts/` 是 Go 的**对等完整实现**(与 Go 平级,不是客户端、不是 WASM 桥)。同一线协议、同一命令词表、同一 `@`-绑定 / 行级隔离 / 权限模型。**用 TS 时与 Go 完全无关**:不向任何 Go 后端转发,Go 在 Mongo 上处理的一切由 TS 自己处理。

入口(`package.json` exports):
- `dopdb` —— 共享 schema(`collection`、`f`、`Infer` 等),不引入 node/mongodb,浏览器安全。
- `dopdb/client` —— 浏览器 `fetch` 客户端(`clientDb`、`apiClient`)。
- `dopdb/server` —— Node + MongoDB 服务端(`createNextHandler`、`serve`、`serveFromConfig`、`serverDb`、`defineApi`、`Permissions`)。

## 一份 schema,贯穿三端

```ts
// dopdb-schema.ts(client / server / Next.js 都 import 它)
import { collection, f } from "dopdb";
export const schema = {
  users: collection({
    name:  f.string(),
    email: f.string().unique(),
    age:   f.int(),
  }).named("users").ownerScope("owner"),          // 行级隔离(可选)
  // orders: collection({...}).named("orders").inDb("analytics"), // 绑定非默认数据源
};
```

`.named()` 同时改公开名与存储名;`.inDb("analytics")` 绑定数据源(客户端据此带 `?ds=analytics`);`.ownerScope("owner")` 声明行级隔离。

## 在 Next.js 中接管 API 路由(主部署路径,零配置)

App Router:放一个 catch-all 路由文件,一行接管该路径下的 `/api/*`,无需额外配置:

```ts
// app/api/[...slug]/route.ts
import { createNextHandler, Permissions } from "dopdb/server";
import { schema } from "@/dopdb-schema";

const perms = new Permissions()
  .grant("HGET", "users").grant("HSET", "users").grant("FIND", "users");

export const { GET, POST, OPTIONS } = createNextHandler({
  schema,
  mongo: { uri: process.env.MONGO_URI!, db: "appdb" },   // 或 datasources: [...]
  jwtSecret: process.env.JWT_SECRET!,                     // HS256 密钥 或 RS256 PEM 公钥
  permissions: perms,
});

export const runtime = "nodejs"; // MongoDB 驱动不兼容 Edge
```

要点:
- **接管即生效**:`GET/POST/OPTIONS` 直接由 dopdb 处理 `/api/hget/users`、`/api/find/orders`、`/api/<函数名>` 等。
- **前缀名可配**:路由处理器读 Next.js 的 catch-all 段(挂载点之后的部分),所以前缀就是你放的文件夹——把 `app/api/[...slug]` 改成 `app/db/[...slug]` 即用 `/db/*`,**无需改代码**;客户端再设 `apiBase: "/db"` 对齐。
- **连接惰性**:Mongo 在首个请求时连接并复用;CORS 预检(OPTIONS)不连库。
- **watch(SSE)**:`GET /api/watch/<coll>` 返回 `text/event-stream`,内部用 `ReadableStream` 推送(需副本集)。
- 多数据源:把 `mongo` 换成 `datasources: [{ name, mongo }, ...]`,请求用 `?ds=` 选。
- 从配置文件起:用 `nextHandlerFromConfig("config.toml", { schema, permissions })`,数据源/密钥/CORS 全来自配置。

Pages Router:用独立 Node 的 `listener`(下一节)即可:`export default (req, res) => srv.listener(req, res)`。

## 独立 Node 服务端

不在 Next.js 里时,直接起一个 http server:

```ts
import { serve, serverDb, Permissions } from "dopdb/server";
const perms = new Permissions().grant("HGET", "users").grant("HSET", "users");

const srv = await serve({ schema, mongo: { uri, db: "appdb" }, jwtSecret, permissions: perms, port: 8080 });
// 多数据源:
//   await serve({ schema, jwtSecret, permissions: perms, port: 8080,
//     datasources: [{ name:"default", mongo:{uri,db:"appdb"} }, { name:"analytics", mongo:{uri,db:"analytics"} }] });
```

服务端内部要直接读写(可信、无 scope/JWT):`const db = serverDb(schema, mongoDb); await db.users.hget("u1")`。`srv.listener` 也可直接用于 Pages Router 的 API 路由。

### 权限(默认拒绝,等价 Go)

```ts
const perms = new Permissions();          // 空 = 全拒
perms.grant("HGET", "users").deny("DEL", "users");
await perms.saveJSON("perm.json");
const q = await Permissions.loadJSON("perm.json");
```

门控顺序:显式 `permit` 回调 > `permissions` 映射 > **默认拒绝**。函数式 API 用 `API::<name>` 同样受门控。

### JWT

HS256(HMAC 密钥)与 RS256(PEM/SPKI 公钥,`createVerify("RSA-SHA256")` 验签),拒绝 `none`;校验 `exp`。

## 浏览器客户端

```ts
import { clientDb, apiClient } from "dopdb/client";
const db = clientDb(schema, {
  baseUrl: "https://api.example.com",   // 指向你的 Next.js / Node dopdb 服务
  getToken: () => localStorage.token,
  // apiBase: "/db",                    // 若服务端挂在非 /api 前缀
});

await db.users.hset("u1", { name: "Ada", email: "ada@x.io", age: 30 }); // 类型受 schema 约束
const u   = await db.users.hget("u1");      // User | null
const all = await db.users.find({ age: { $gte: 18 } }, { limit: 20 });
const unsub = await db.users.watch((ev) => console.log(ev.type, ev.key, ev.doc));
```

客户端按命令词表拼 `/<apiBase>/<cmd>/<coll>`;非默认数据源加 `?ds=`;键用 `?f=`。`watch` 用 `fetch` 流式读取 SSE(能带 Bearer token,`EventSource` 不能),断线按 `Last-Event-ID` 续传。

## 函数式 API(`/api/<name>`)

```ts
import { defineApi } from "dopdb/server";
const greet = defineApi(function greet(input: { name: string }, ctx) {
  return { msg: `hi ${input.name}`, caller: ctx.claims["uid"] ?? null };
});
await greet({ name: "Ada" });   // 进程内调用,带类型
```

流水线极简:`decode → validate(可选) → handler`(无 ParamEnhancer/ResultSaver/ResponseModifier)。客户端用 `apiClient<typeof api>({ baseUrl })` 类型安全调用;handler 代码不会进浏览器(只跨端共享输入/输出**类型**)。

## 构建 / 测试

```
make ts            # cd ts && npm install && npm run build
make ts-test       # node --import tsx --test test/*.test.ts
make ts-typecheck  # tsc -p tsconfig.json --noEmit(strict)
```

> TS 侧已通过严格 `tsc --noEmit` 与全部单测(含 Permissions、`?ds=` URL、watch 重连、`createNextHandler` 形态与 CORS 预检、`apiBase` 前缀)。
