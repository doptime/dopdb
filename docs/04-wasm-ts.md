# 04 · WASM 与 TypeScript/JavaScript(`/api/<name>` · 用 TS 函数创建 API)

> 这是"最终用 TS 重写以消除前后端分离"的第一步:把 dopdb 的 api 核心编译成 **WebAssembly**,在任意 JS 运行时(浏览器 / Node / Deno / Bun / 边缘)里加载;你**传入一个带接口类型的 TS/JS 函数**就能创建 API,路由默认 `/api/<名称>`。Go 服务端的路由也同步改成 `/api/<名称>`,两侧互通。

## 1. API 路由:`/api/<name>`

旧版用"路径段不含 `-` 即为 API"的启发式,脆弱(API 名一旦含 `-` 就会被当成数据命令)。现在显式按前缀判定:

- 路径里出现 `/api/` → 其后的整段就是 API 名(可含 `-`,会做 URL 反转义)。
- 其余路径 → 必须是数据命令 `CMD-KEY`(必须含 `-`);不含 `-` 且非 `/api/` 的路径返回 400。

名称规则(类型名 **或** 指定名,指定名非必须):

- Go 端:`api.Api(func(in *DreamAnalyzerInput)(Out,error))` 自动取类型名,去掉一个首/尾词缀(`In/Out/Req/Arg/Input/Param/...`)并小写 → `dreamanalyzer`;或 `api.WithName("dream")` 显式指定(显式名只小写、不去缀)。
- 查表大小写不敏感:`/api/DreamAnalyzer` 与 `/api/dreamanalyzer` 命中同一端点。
- 标量输入类型且未显式命名 → 直接 panic(这是编程错误)。

```
POST /api/dreamanalyzer        body: {"text":"..."}        # API 调用
GET  /HGET-User?f=@uid                                      # 数据命令(不变)
```

## 2. WASM 桥:Go 核心 → `dopdb.wasm`

`wasm/main.go`(`//go:build js && wasm`)用 `syscall/js` 把 api 核心暴露成一个全局对象 `dopdb`:

| 方法 | 说明 |
|---|---|
| `dopdb.createApi(name, handler)` | 把一个 JS 函数注册成 `/api/<name>` 端点;返回注册名;重复名先 `removeApi` 再注册(支持热替换) |
| `dopdb.callApi(name, input)` | 跑端点流水线,返回 **Promise**(handler 可异步) |
| `dopdb.removeApi(name)` | 注销 |
| `dopdb.apiNames()` | 已注册端点名数组 |
| `dopdb.sanitizeFilter(filter)` | 用算子白名单校验查询;通过则返回过滤器,违规则返回一个 `Error` 对象 |
| `dopdb.version` | 版本串 |

桥接要点(都在 `wasm/main.go`):

- handler 既可同步返回对象,也可返回 Promise;桥会检测 thenable 并 `await`(goroutine 阻塞在 channel 上,JS 事件循环照常推进)。
- `callApi` 返回 JS `Promise`:executor 里开一条 goroutine 跑 `api.CallByName`,完成后 `resolve/reject`。
- **不要用 panic 抛错**:在 `syscall/js` 回调里 panic 会**崩掉整个 wasm 实例**。所有错误都"返回一个 JS `Error` 对象",由 TS 包装层决定是否 throw。
- 值转换 `goToJS`/`jsToGo` 递归处理 `map[string]any`/`[]any`/数值/字符串/布尔/null。

底层只 import `dopdb` + `dopdb/api`(**不**碰 `mongostore`),所以编译 wasm 不需要 Mongo 驱动。

构建:

```bash
make wasm     # = GOOS=js GOARCH=wasm go build -o clients/ts/wasm/dopdb.wasm ./wasm  (+拷 wasm_exec.js)
```

`wasm/stub.go`(`//go:build !(js && wasm)`)是一个空 `main`,只为让 `go build ./...` 在非 wasm 平台不报错。

## 3. TS/JS 客户端 `dopdb-client`(`clients/ts/`)

两个互不依赖的部分:**WASM 运行时**(在 JS 里定义并伺服 API)和**远程调用器**(前端调远端 dopdb 服务)。同时发布 `.ts` 源码 + `tsc` 产物 `.js` + `.d.ts`,故 TS、JS 都能用。无运行时依赖(用原生 `fetch` + `WebAssembly` + 内置 wasm)。

### 3a. 传入接口类型的函数创建 API(WASM 运行时)

```ts
import { loadDopdb } from "dopdb-client";
const db = await loadDopdb();           // 加载一次 dopdb.wasm(单例)

interface GreetIn  { name: string }
interface GreetOut { msg: string }

// 路由名 = 函数名(这里的"类型名")→ /api/greet
const greet = db.api((input: GreetIn): GreetOut => ({ msg: "hi " + input.name }));
await greet({ name: "Ada" });           // { msg: "hi Ada" }(进程内,走 wasm 流水线)
greet.apiName;                          // "greet"

// 或异步 handler;函数名 DreamAnalyzer → /api/dreamanalyzer
const dream = db.api(async function DreamAnalyzer(i: { text: string }) {
  return { text: i.text, mood: "calm" };
});

// 或显式指定名(指定名非必须)
const add = db.api("add", (i: { a: number; b: number }) => ({ sum: i.a + i.b }));
```

名称来源:显式字符串,否则 `fn.name`(箭头函数赋给 `const Greet = ...` 时 JS 也会推出名字)。匿名函数无名 → 抛错,提示用 `db.api('name', fn)`。也有便捷的 `defineApi(fn)` / `defineApi(name, fn)`(内部 `await loadDopdb()`)。

### 3b. 伺服 `/api/<name>`(用 wasm 引擎当服务端)

```ts
// Node
import { createServer } from "node:http";
createServer(db.nodeListener).listen(8080);

// Deno / Bun / Cloudflare Workers
Deno.serve(db.fetchHandler);
```

适配器把 query string 与 JSON body 合并成 input,调 `db.call(name, input)`,回 JSON;非 `/api/` 路径回 404。

### 3c. 远程调用器(前端调远端 Go 服务)

```ts
import { configure, createApi } from "dopdb-client";
configure({ baseUrl: "https://api.example.com", getToken: () => localStorage.token });

const greet = createApi<GreetIn, GreetOut>("greet");
await greet({ name: "Ada" });   // POST {baseUrl}/api/greet,JSON body,Bearer 鉴权
```

构建:

```bash
make ts       # 隐含 make wasm,然后 cd clients/ts && npm install && npm run build
```

## 4. 边界:wasm/TS 运行时只是 **API 层**

WASM 桥跑的是 api 端点流水线(你的 handler 即 Func)。它**不**做:JWT 验证、`@`-绑定注入、`command::collection` 权限白名单、owner 行级隔离、以及 `HGET/HSET/...` 数据命令——这些仍由 Go 的 `httpserve` 承担(见 `02-http.md`)。

因此典型部署是两种之一:

1. **Go 服务端为主**:`httpserve` 提供完整安全栈 + 数据命令 + Go 写的 API;前端用 `createApi` 远程调 `/api/<name>`。
2. **JS 运行时为主(边缘/同构)**:`db.nodeListener`/`db.fetchHandler` 用 wasm 引擎伺服纯 API 端点(轻量、无 Mongo);需要鉴权/数据命令时仍回落到 Go 服务端。

`sanitizeFilter` 两侧共用同一份 Go 实现(编进 wasm),保证"算子白名单"这条安全底线在 Go 与 JS 里**字节级一致**。

## 5. 验证(本仓已跑通)

- `GOOS=js GOARCH=wasm go build ./wasm` 通过;`dopdb.wasm` 在 Node 里加载,createApi/callApi 同步与异步 handler、大小写不敏感查表、apiNames、sanitizeFilter(通过 + 拒 `$where`)全部通过。
- TS 包 `tsc` 严格模式零报错;端到端集成测试:`loadDopdb` → `db.api(fn)` → `db.nodeListener` 伺服 → 远程 `createApi` 与原生 `fetch` 命中 `/api/<name>` → 非 API 路径 404,全部通过。
