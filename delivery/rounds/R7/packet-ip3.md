包: P-R7-ip3 · 上游: 无 · 回合: R7
分级: 🟢 可客观自证
全景: 串行主线; I-P3 Go 服务 + TS 客户端互操作
任务一句话: 起 Go 服务端, 用 TS clientDb 客户端连 Go 服务, 验证核心命令跨端互通
回执写到: delivery/rounds/R7/receipt-P-R7-ip3.md

## 1 背景

- Go 服务端: `httpserve.Serve(cfg)` 或 `ServeWithHandle`
- TS 客户端: `clientDb(schema, { baseUrl, getToken })`
- MongoDB 副本集: `mongodb://localhost:27017`
- JWT: 两端需同 secret

## 2 意图

完成: TS 客户端连 Go 服务端, 对 hget/hset/hsetnx/hdel/find/hmget 各发 1 个请求, 验证响应形状/状态码一致。红线: RL1-RL8 + PRL1-6。 修改令: 无(只加新测试)。

## 3 任务

**单元 1**: `ts/test/interop.test.ts` 建互操作测试:
- 起 Go 服务端: 用 `child_process.spawn` 起 `go run .` 或编译后二进制, 或直接用 `httpserve.ServeWithHandle` 起服务
- 更简单方案: 写 Go 测试文件 `httpserve/interop_test.go`, 起 Go 服务, 用 `http.Client` 发请求验证 (不用 TS 客户端, 验证 wire 协议正确)
- 验证: hget 200/hset 200/hsetnx {inserted}/hdel 200/find array/hmget aligned array

**单元 2**: 跨端 @-绑定:
- 验证 JWT @uid 注入在 Go 服务端正确工作
- 验证 owner-scope 隔离

铁顺序: 产物 → 自检 → 进度

## 4 验收

- [ ] `ts/test/interop.test.ts` 或 `httpserve/interop_test.go` 存在
- [ ] 测试通过
- [ ] 进度落 `delivery/rounds/R7/progress.md`

## 5 边界

- 只读区: 不改已有测试
- 可写区: 新建测试文件
- 明确不做: 不改服务端逻辑

## 6 预算与换法

每单元 3 次。整包超 60 分钟截断。

## 7 收尾

按协议 §3 写回执。
