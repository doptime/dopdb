包: P-R6-m3-ts · 上游: 无 · 回合: R6
分级: 🟢 可客观自证 — 硬判据: TS watch E2E 测试全过(insert/update 事件收到/Last-Event-ID 续传/scoped delete 不投递); 回归基线不坏
全景: 与 P-R6-m3-go 并排; TS 侧 watch E2E
任务一句话: 建 TS watch E2E 测试, 验证 client.watch + Last-Event-ID 续传
回执写到: delivery/rounds/R6/receipt-P-R6-m3-ts.md

## 1 背景 · 现在是什么情况
- MongoDB 副本集已启动 `mongodb://localhost:27017=PRIMARY`
- TS `ts/src/server.ts` 已有 watch SSE 实现
- TS `ts/src/client.ts` 已有 client.watch 实现 (EventSource 或 fetch SSE)
- `ts/test/watch-reconnect.test.ts` 已有 watch 重连测试 (但用的是 mock)
- 无真实 MongoDB watch E2E 测试

## 2 意图 · 为什么做、什么算好
完成: 新增 watch E2E 测试, 用真实 MongoDB 副本集验证 insert/update 事件推送、断线续传、scoped delete 不投递。红线: RL1-RL8 全部适用。 修改令: 无(只加新测试)。

## 3 任务 · 具体做什么
**单元 1**: `ts/test/watch-e2e.test.ts` 建真实 watch E2E:
- 用 `serve()` 起 TS 服务端 (mongo: {uri: "mongodb://localhost:27017", db: "dopdb_watch_test"})
- 用 `clientDb()` 建客户端
- 开 watch → insert 文档 → 验证收到事件 → update 文档 → 验证收到事件
- 关键断言: 事件 type 为 "insert"/"update", doc 字段正确

**单元 2 (断线续传)**:
- 开 watch → 收 1 个事件 → 停 watch → 再写文档 → 重开 watch (Last-Event-ID) → 验证收到断线后事件
- 关键断言: 续传后收到新事件, 不漏

**单元 3 (scoped delete 不投递)**:
- 建 scoped collection → 开 scoped watch → 插入 → 收到 insert → 删除 → 验证 emit 没被调用
- 关键断言: delete 事件不投递 (I-WA2)

铁顺序: 先落产物 → 自检全过 → 记进度

## 4 验收 · 怎么算完成
- [ ] `ts/test/watch-e2e.test.ts` 存在且非空
- [ ] `cd ts && npx tsx --test test/watch-e2e.test.ts` 通过
- [ ] `cd ts && npx tsc --noEmit` 干净
- [ ] 进度账落 `delivery/rounds/R6/progress.md`

## 5 边界 · 不要做什么
- 只读区: `ts/src/` 全部源文件不改
- 可写区: `ts/test/watch-e2e.test.ts`
- 明确不做: 不改服务端逻辑; 不碰 Go

## 6 预算与换法
每单元最多 3 次。整包超 60 分钟 → 截断。

## 7 收尾
按协议 §3 写回执; 「异常发现」必写清单: ① SSE 连接建立失败; ② Last-Event-ID 续传不工作; ③ 事件收不到
