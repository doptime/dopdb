# dopdb-client

WASM-backed API runtime **and** remote caller for the [dopdb](../../README.md) framework. TypeScript and JavaScript, no runtime dependencies (native `fetch` + `WebAssembly` + the bundled `dopdb.wasm`).

两个互不依赖的部分:

- **WASM 运行时** — 把带接口类型的 TS/JS 函数注册成 API,在 JS 里直接跑或伺服 `/api/<name>`。
- **远程调用器** — 前端调远端 dopdb 服务的 `/api/<name>` 路由。

## 安装 / 构建

```bash
# 从仓库根构建 wasm 产物 + 编译 TS:
make ts
# 或在本目录:
npm install && npm run build   # 产物在 dist/;wasm 在 wasm/
```

## 用接口类型的函数创建 API

```ts
import { loadDopdb } from "dopdb-client";
const db = await loadDopdb();

interface GreetIn  { name: string }
interface GreetOut { msg: string }

// 路由名取自函数名 → /api/greet(也可 db.api("greet", fn) 显式指定)
const greet = db.api((input: GreetIn): GreetOut => ({ msg: "hi " + input.name }));
await greet({ name: "Ada" });        // { msg: "hi Ada" }(进程内)

// 异步 handler 也支持
const dream = db.api(async function DreamAnalyzer(i: { text: string }) {
  return { text: i.text, mood: "calm" };
});
```

## 伺服 `/api/<name>`(wasm 引擎当服务端)

```ts
import { createServer } from "node:http";
createServer(db.nodeListener).listen(8080);     // Node

Deno.serve(db.fetchHandler);                     // Deno / Bun / Workers
```

## 远程调用器(前端 → 远端 Go 服务)

```ts
import { configure, createApi } from "dopdb-client";
configure({ baseUrl: "https://api.example.com", getToken: () => localStorage.token });

const greet = createApi<GreetIn, GreetOut>("greet");
await greet({ name: "Ada" });        // POST {baseUrl}/api/greet
```

## 浏览器

Node 下 `loadDopdb()` 自动定位内置的 `wasm/dopdb.wasm` 与 `wasm/wasm_exec.js`。浏览器需给出 URL:

```ts
const db = await loadDopdb({
  wasmUrl: "/static/dopdb.wasm",
  wasmExecUrl: "/static/wasm_exec.js",
});
```

## 边界

WASM 运行时只跑 **API 层**(你的 handler 即端点逻辑)。JWT、`@`-绑定、权限白名单、owner 行级隔离、`HGET/HSET/...` 数据命令仍由 Go 的 `httpserve` 承担。详见仓库 `docs/04-wasm-ts.md`。

> `wasm/wasm_exec.js` 来自 Go 发行版(BSD 许可),需与编译 `dopdb.wasm` 的 Go 版本匹配。`make wasm` 会一并刷新它。
