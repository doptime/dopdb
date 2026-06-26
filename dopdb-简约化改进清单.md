# dopdb 简约化改进清单(架构主线 · 决策用)

> **状态(本轮已落地)**:本清单评估的简约化方向已实施,并完成 4 项课程修正——(1) 删除 Store/Codec 抽象、直连 MongoDB;(2) 多数据源 + `?ds=` 参数(缺省 `default`);(3) 权限默认拒绝(去 AutoAuth);(4) TypeScript 为 Go 的对等完整实现(非客户端)。此外:URL 改 `/api/<cmd>/<coll>`、新增 `hmset/hmget/count/findone/watch`、`watch` 走 change stream→SSE、API 流水线精简为 `decode→Validate→Func`、WASM 退场。详见 `README` 与 `docs/`。本文件以下内容为当初的评估记录,保留以备追溯。

> 目的:把「让 DB+API 用起来的代码再变少」这件事,所有可能的改进 + 每一处开发者体验改进,全部列出,按**用户体验改进程度**排序,供你判断要不要实施。本文件只评估、不编码。
>
> 这里的「用户」= 用 dopdb 的开发者,所以「用户体验(UX)」≈ 开发者体验(DX)。

## 0 判据与参照系

dopdb 的全部意义:**用最少的代码,把数据库和 API 都用起来。** 衡量一条改进好不好,就看它把"从想法到能跑的类型安全调用"这条路缩短了多少。

参照这个世界做得最好的几家:

- **tRPC** — 服务端定义过程,客户端**自动推断类型**(零 codegen),`trpc.user.get.query()`。端到端类型安全是它的招牌。
- **Convex** — TS 函数**就是**后端;查询默认**响应式**;端到端类型;`useQuery(api.messages.list)`。
- **Prisma / Drizzle** — schema → 生成类型化客户端,`prisma.user.findMany({where})`。
- **Firestore** — 客户端 SDK,`db.collection('users').doc(id).onSnapshot()`;声明式安全规则;实时。
- **Supabase / PostgREST** — 从 Postgres schema 自动出 REST;行级安全(RLS)在库里;realtime。
- **Hono + Eden / Elysia** — 类型化路由,客户端从**服务端类型**推断(`treaty`)。
- **Zod** — 一处声明 → 运行时校验 + 静态类型,二合一。

**dopdb 当前最大的缺口**:它是 **Go 后端 + TS 前端**,而上面几家要么是纯 TS(类型靠推断免费拿)、要么从 schema/DB 生成客户端。dopdb 现在**类型写两遍**(Go struct 一遍、TS interface 一遍),还用字符串拼集合名调用。补上这道"Go↔TS 类型桥"是单点回报最高的事。

> 排序按 **UX 改进程度**(不是工作量)。每条另标**成本**与**依赖**,方便你做性价比权衡。文中 before→after 为**示意**(非最终代码)。

---

## Tier 1 — 变革级(直接兑现"最简单的代码")

### T1.1 单一类型源:从 Go 定义自动生成 TS 客户端与类型

- **是什么**:加一个 codegen 步骤(如 `dopdb gen ts`),读取已注册的 Go 集合 + API(反射或 AST),产出 `dopdb.gen.ts`——含类型化的集合访问器和 API 调用器。前端直接 `import`,**零手写类型**。
- **参照**:tRPC / Convex 的端到端类型;Prisma 的"schema→客户端";Go 生态的 `tygo`、`openapi-typescript`。
- **现在 → 之后**:
  - 现在:Go 写 `type User struct{...}`,TS 再写一遍 `interface User{...}`,还要 `collection("User")` 字符串拼。三处、易漂移。
  - 之后:只在 Go 写 `User`;`dopdb gen ts` 自动产出 TS 类型 + 客户端;前端 `import { db } from "./dopdb.gen"`。
- **UX 收益**:🟢🟢🟢🟢🟢 **最大**。消灭类型重复、漂移风险、stringly 调用;前端拿到自动补全 + 编译期安全。这就是"最简单代码"的核心。
- **成本**:中高(要写 Go→TS 类型发射器 + 一个 CLI)。但你已有 BSON/JSON tag + Collection 里的反射,可作起点;先支持 string/number/bool/struct/数组/可选,逐步加。
- **依赖**:无(地基);T1.2 建在它之上。

### T1.2 类型化 `db` 客户端对象,取代字符串式 `collection("User").hget`

- **是什么**:前端用一个生成出来的 `db` 对象,`db.User.get(id)` 直接返回 `User`,而不是 `collection("User").hget(id)` 返回 `unknown` 再强转。
- **参照**:Prisma `prisma.user.findMany`;Convex `api.messages.list`;Firestore `db.collection(...).doc(...)`。
- **现在 → 之后**:
  - 现在:`const u = await collection("User").hget("u1");  // u: unknown,要 cast`
  - 之后:`const u = await db.User.get("u1");  // u: User,全程类型`;`await db.Order.find({ owner: uid });  // Order[]`
- **UX 收益**:🟢🟢🟢🟢🟢(与 T1.1 合体)。把"用起数据"压到一行且全类型——dopdb 立意的终点。
- **成本**:低(骑在 T1.1 的 codegen 上,主要是模板)。
- **依赖**:T1.1。

> T1.1 + T1.2 是一套:类型桥 + 类型化 db。做完这两条,dopdb 在"最少代码用 DB"上就追平甚至超过纯 TS 方案(因为后端还是高性能 Go)。

---

## Tier 2 — 高影响(大体感提升,范围更小)

### T2.1 `createApi` 返回可调用句柄,收掉 `callApi` / `removeApi`

- **是什么**:`defineApi(handler)` 返回一个**带类型的可调用对象**——直接调它就是发起调用(in-process 走 wasm、远程走 client.ts),`.remove()` 注销。`callApi`/`removeApi` 降级为动态/解耦派发的逃生口。
- **参照**:tRPC 过程;React Query 返回的函数句柄。
- **现在 → 之后**:
  - 现在:`createApi("greet", h); await callApi("greet", input); removeApi("greet");`(三个以名字为中心的函数)
  - 之后:`const greet = defineApi(h); await greet(input); greet.remove();`
- **UX 收益**:🟢🟢🟢🟢 高(心智模型更干净、调用类型安全、概念更少)。这是你上轮第 3 个直觉,成立。
- **成本**:低-中(wasm 桥与 client 返回句柄)。
- **依赖**:无(与 T2.2 同改一处最省)。

### T2.2 API 名从 handler 推导,name 变可选

- **是什么**:`defineApi(greet)` → 名字取 `greet.name`;`defineApi(greet, "greetV2")` 显式覆盖。
- **参照**:Convex(文件/函数名即端点);tRPC(键即过程名)。**你系统里已经一半这么做了**:TS `defineApi(fn)` 和 Go `api.Api` 类型名推导;只有 wasm 桥那个必填 name 不一致。
- **现在 → 之后**:`createApi("greet", h)` → `defineApi(greet)`(名字自动)。
- **UX 收益**:🟢🟢🟢🟢 高(少一处要手写并保持同步的东西)。你上轮第 2 个直觉,成立。
- **成本**:低。**唯一要写进文档的坑**:压缩器会把 `fn.name` 改成 `t`,生产 bundle 建议显式传 name(同 React 的 `displayName` 老问题)。
- **依赖**:无(与 T2.1 合并一次性做)。

### T2.3 响应式查询(Mongo change streams → 实时数据)

- **是什么**:`db.Order.watch({ owner: uid }, cb)` —— 服务端经 **Mongo change streams** + owner-scope 过滤,把变更推给客户端(WebSocket/SSE),UI 自动更新。
- **参照**:Firestore `onSnapshot`;Convex(默认响应式);Supabase realtime。
- **现在 → 之后**:
  - 现在:前端轮询 `db.Order.find(...)` 拿更新。
  - 之后:`const stop = db.Order.watch({owner: uid}, orders => setState(orders));`
- **UX 收益**:🟢🟢🟢🟢 高(对做应用 UI 是质变:无轮询、实时)。但这是**新能力**,不是对现有代码的简化,范围更大。
- **成本**:高(WebSocket/SSE 传输 + change-stream 管线 + 客户端订阅 API + 流上的鉴权)。
- **依赖**:鉴权/owner-scope 已有(可复用);最好在 T1.2 之后(类型化订阅)。

### T2.4 一行起服务

- **是什么**:`dopdb.Serve(dopdb.Config{Mongo: uri, Secret: ..., Dev: true})` 自动注册所有声明的集合/API 并启动,替代手工拼 `Server`+`Permissions`+`Handler`+mux。
- **参照**:Hono/Elysia(`new Hono()`);Convex(零服务端拼装)。
- **现在 → 之后**:
  - 现在:建 Server、建 Permissions、建 Handler、逐个 RegisterHttp、挂 mux、ListenAndServe。
  - 之后:`dopdb.Serve(cfg)` 一行。
- **UX 收益**:🟢🟢🟢 中-高(显著减少起步样板)。
- **成本**:低-中(一个 Config + 自动注册的便利层,底层不变)。
- **依赖**:无。

---

## Tier 3 — 中影响(清理为主,多为内部面)

### T3.1 更干净的 URL 方案(单 POST `/api/db`,或 `/api/<coll>/<cmd>`)

- **是什么**:把 `CMD-Coll?f=` 的连字符拼接换掉。两条路:① 两段路径 `/api/<coll>/<cmd>`(Next.js `app/api/[coll]/[cmd]/route.ts`,去脆性、保 GET 可缓存);② 单 POST `/api/db`,`{cmd, collection, key, filter, body}` 全进 JSON——一个路由文件、零路径解析、中间件统一(代价:丢读请求 URL 级缓存)。
- **参照**:JSON-RPC;GraphQL(单端点);PostgREST(`/table`);tRPC(批量 POST)。
- **现在 → 之后**:`GET /HGET-User?f=@uid` → `GET /api/User/hget?f=@uid`(方案①)或 `POST /api/db {cmd:"get",collection:"User",key:"@uid"}`(方案②)。
- **UX 收益**:🟢🟢🟢 中——去掉连字符解析脆性(集合名能含 `-`)、Next.js 集成更顺、中间件统一。但**类型化客户端会把 URL 藏起来**,所以对终端 DX 的直接收益有限,除非你常手写请求/调试。属内部健壮性 + 集成收益。你上轮第 1 个问题的答案落在这里。
- **成本**:中(服务端派发 + 客户端 + 中间件;留个向后兼容垫片)。
- **依赖**:无;与 T1.1 配合时,生成的客户端自然用新方案。

### T3.2 文档原生命令词表(get/set/find 替 HGET/HSET)

- **是什么**:把 Redis 味的 `HGET/HSET/HKEYS` 换成文档存储更自然的 `get/set/update/delete/query`(保留旧名做别名 + 弃用期)。
- **参照**:Firestore / Mongo 动词(get/set/update/delete/query)。
- **UX 收益**:🟢🟢 中-低(减少"Redis 既视感",更好读)。若类型化客户端已把命令藏起,基本是表层。
- **成本**:低-中(加别名、弃用旧名;或仅在客户端层改名)。
- **依赖**:无。

### T3.3 共享校验 / 模式(Zod 式,一处声明 → 两端类型+校验)

- **是什么**:字段规则声明一次(Go tag 或一个 schema),生成 **Go 校验 + TS(Zod)校验 + 类型**。现在只有写时修饰器(trim/default/时间戳)+ 一个 Go 侧 validator hook,TS 端无运行时校验。
- **参照**:Zod、Pydantic、Prisma schema、Convex validators。
- **UX 收益**:🟢🟢🟢 中-高(一份 schema 出类型+两端校验)。但与 T1.1 高度重叠(同一条类型桥的延伸)。
- **成本**:中-高。
- **依赖**:T1.1(同一套生成管线)。

### T3.4 OpenAPI / API 浏览器生成

- **是什么**:从注册的集合/API 自动出 OpenAPI + 一个可点的浏览器/playground。
- **参照**:FastAPI 的 `/docs`;Swagger。
- **UX 收益**:🟢🟢🟢 中(可发现性、联调、上手)。
- **成本**:中。
- **依赖**:与 T3.1 的统一端点配合更顺。

---

## Tier 4 — 打磨级(好用,但用户可见度低)

### T4.1 客户端类型化错误

- 把 `dopdb: 403 ...` 字符串错误换成判别式错误类型(`ForbiddenError`/`NotFoundError`/...),前端可 `instanceof` 分支。UX 🟢🟢;成本低。

### T4.2 `@`-绑定移入结构化 body

- 把 query string 里的 `@uid` 等身份绑定挪进 JSON body(配合 T3.1 方案②),更清晰、更易审计。UX 🟢(主要是清晰度/安全审计);成本中(随 T3.1 一起)。

### T4.3 批处理 / 事务 `db.batch([...])`

- 一次发多条命令 / 跨文档事务(Mongo 支持多文档事务)。UX 🟢🟢(进阶场景显著);成本中。

### T4.4 Store 接口瘦身 / options 模式(内部)

- `Store` 在变大(这轮 +`PutScoped`)。把"原子原语"收进子接口或 options,防接口膨胀。**纯内部维护性**,用户不可见。UX —;成本中。

### T4.5 开发面板

- 浏览集合、看权限、重放请求的本地仪表盘(配合 AutoAuth)。UX 🟢🟢(开发期);成本中-高;非核心。

---

## 推荐与实施排序建议

按"性价比"(单位工作量的 UX 提升)+ "体感天花板"给你三条线:

1. **先做、最划算(小改动大体感)**:**T2.1 + T2.2**(`defineApi` 返回句柄 + 名字自动)。同改一处、风险低,直接兑现你上轮两个直觉,API 路径立刻像 tRPC。顺手再做 **T2.4**(一行起服务)、**T3.2**(命令改名)、**T4.1**(类型化错误)这些低风险项。

2. **体感天花板(dopdb 立意的核心,值得投入)**:**T1.1 + T1.2**(Go→TS 类型桥 + 类型化 `db`)。中高成本,但这是"用最简单代码用起 DB"的终局——做完前端零手写类型、全程自动补全。强烈建议作为这条主线的主目标。**T3.3**(共享校验)可作为 T1.1 的自然延伸一起规划。

3. **看产品方向再决定**:
   - 要做实时应用 → **T2.3**(响应式查询),质变但范围大。
   - 在意 Next.js 集成 / 常手写请求调试 → **T3.1**(URL 方案)。
   - 强调可发现性 / 对外开放 → **T3.4**(OpenAPI/playground)。
   - 纯内部健康度 → **T4.4**(接口瘦身),随手做。

**一句话**:想用最小代价提体感,先做 Tier 2 的 T2.1/T2.2;想真正兑现 dopdb 的立意,投 Tier 1 的类型桥(T1.1/T1.2)。其余按你的产品方向挑。要不要、按什么顺序实施,你定;定了我再开正式编码。
