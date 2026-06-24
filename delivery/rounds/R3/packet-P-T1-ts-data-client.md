# 包 · P-T1-ts-data-client(R3 · 🟢 并行 · 独立)

> 段:执行交换。给 TS SDK 补「数据命令」客户端(现仅有 `/api/<name>` 的 `createApi`)。本地按规格实现 + 自证;不依赖 Mongo。

## 1 目标
`clients/ts/src/client.ts` 加 `collection(name)`,返回数据命令客户端,按既有 fetch 模式(`Opt`/`buildUrl`/`authHeaders`/`parse`)打 `/CMD-<coll>` 路由,复用现有 bearer/baseUrl/params 机制。

## 2 规格:方法 → 请求(逐条对齐服务端既有线格式,R2 已验)
读用 GET、写用 POST;`f` 为 query 参数;body 为 JSON。

| 方法 | HTTP | 路径 | body |
|---|---|---|---|
| `hget(field)` | GET | `/HGET-<coll>?f=<field>` | — |
| `hset(field, value)` | POST | `/HSET-<coll>?f=<field>` | `JSON.stringify(value)` |
| `hsetnx(field, value)` | POST | `/HSETNX-<coll>?f=<field>` | `JSON.stringify(value)` |
| `hdel(field)` | POST | `/HDEL-<coll>?f=<field>` | — |
| `hexists(field)` | GET | `/HEXISTS-<coll>?f=<field>` | — |
| `hgetall()` | GET | `/HGETALL-<coll>` | — |
| `hkeys()` | GET | `/HKEYS-<coll>` | — |
| `hlen()` | GET | `/HLEN-<coll>` | — |
| `find(filter)` | POST | `/FIND-<coll>` | `JSON.stringify(filter)` |

- 所有请求带 `authHeaders`(`Authorization: Bearer <token>` 当有 token);写请求带 `Content-Type: application/json`。
- 用 `buildUrl(opt, path)` 拼 baseUrl + path;`?f=` 用 `encodeURIComponent(field)`;`<coll>` 同理。
- 响应走 `parse(res)`(非 2xx 抛 `dopdb: <status> ...`)。
- 签名建议:`export function collection(coll: string, options: RequestOptions = Opt)`;返回对象含上述方法。在 `clients/ts/src/index.ts` 导出 `collection`。

## 3 自证(🟢 客观)
1. `cd clients/ts && npm run build`(tsc)退出 0,无类型错。
2. 新建 `clients/ts/data-client.test.mjs`(纯 node,**不引 jest**,与现有 `smoke-test.mjs` 同风格):打桩 `globalThis.fetch` 捕获 `(url, init)`,对每个方法各一个断言:
   - URL 路径正确(含 `/HSET-Note?f=k1` 这种)、method 正确、写方法 body == 期望 JSON、`Authorization: Bearer t` 头在、写方法 `Content-Type: application/json` 在。
   - 至少覆盖:hget / hset / hdel / hkeys / hlen / find 六个。
   - 全部断言通过打印 `ALL DATA-CLIENT TESTS PASSED`。
3. `node clients/ts/smoke-test.mjs` 仍输出 `ALL TS SDK INTEGRATION TESTS PASSED`(不回退)。

留痕 `delivery/rounds/R3/t1_build.txt`(tsc)+ `t1_test.txt`(两个 node 测试输出)。

## 4 红线 / 岔路
- 不改 `createApi`/`wasm.ts`/Go 端任何东西——**仅新增** `collection` + 其导出 + 测试文件。
- 若发现某命令的服务端线格式与上表不符(对照 `httpserve/context.go` 的 buildParams/mergeBody):**停**,记 oob + suspend,不要自行猜测改服务端。
- 可写区:`clients/ts/src/client.ts`、`clients/ts/src/index.ts`、`clients/ts/data-client.test.mjs`、`delivery/rounds/R3/`。

## 5 回执
`receipt-P-T1-ts-data-client.md`:状态、tsc 结果、data-client 测试各断言 PASS 数、smoke 是否仍过、是否仅动允许文件、异常发现。
