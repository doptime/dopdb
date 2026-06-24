# 回执 · P-T1-ts-data-client(R3 · 🟢 并行 · 独立)

状态:**done**

## 测试结果

- `npm run build`(tsc)退出 0,无类型错
- `data-client.test.mjs`:10 断言全部 PASS(hget URL, hget auth, hset URL, hset body, hset content-type, hdel URL, hkeys URL, hlen URL, find URL, find body)
- 输出 `ALL DATA-CLIENT TESTS PASSED`
- `smoke-test.mjs` 仍输出 `ALL TS SDK INTEGRATION TESTS PASSED`(不回退)

## 仅动允许文件

- `clients/ts/src/client.ts`:加 `DataClient` 类 + `collection()` 工厂
- `clients/ts/src/index.ts`:加 `DataClient` + `collection` 导出
- `clients/ts/data-client.test.mjs`:新建 mock-fetch 测试

未动 `createApi`/`wasm.ts`/任何 Go 文件。

## 异常发现

无。
