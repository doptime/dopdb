# 回合规划 plan · dopdb · R6(2026-06-26)

## 包 A: P-R6-m3-go — Go watch 集成测试 (🔴 承重)

**目标**: 建 Go watch 集成测试，验证 change stream → SSE 往返。

**拟建什么**:
1. `dopdb/mongo_test.go` 或扩 `dopdb_test.go`: 新增 `TestIntegrationWatchInsertUpdate` — 写文档 → watch 收到 insert → 更新 → 收到 update
2. 新增 `TestIntegrationWatchResume` — 停流后重连，用 resume token 不漏事件
3. 新增 `TestIntegrationWatchScopedDelete` — scoped 下 delete 不投递（I-WA2）

**打算怎么自证**:
- `DOPDB_TEST_MONGO_URI="mongodb://localhost:27017" go test -run Integration ./...` 全过
- watch 测试收到预期事件数（insert=1, update=1）
- resume token 续传后收到断线后的事件
- scoped delete 不触发 emit 回调

## 包 B: P-R6-m3-ts — TS watch 集成测试 (🟢)

**目标**: 建 TS watch E2E 测试，验证 client.watch + Last-Event-ID 续传。

**拟建什么**:
1. `ts/test/watch-e2e.test.ts`: 用真实 MongoDB 副本集，serve + clientDb
2. 验证 insert/update 事件推送到 client
3. 断线后 Last-Event-ID 续传
4. scoped delete 不投递

**打算怎么自证**:
- `DOPDB_TEST_MONGO_URI="mongodb://localhost:27017" cd ts && npx tsx --test test/watch-e2e.test.ts` 全过
- 收到预期事件（type=insert, type=update）
- 断线续传后收到新事件

## 包 C: P-R6-m3-close — M3 封存 (🟢)

**目标**: 若 A+B 通过，更新 STATUS.md M3 状态，写 SEALED.md。

**拟建什么**: 更新 STATUS.md + 写 SEALED.md

**打算怎么自证**: 文件更新到位
