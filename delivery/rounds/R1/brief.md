# 回合简报 brief · dopdb · R1(2026-06-26)

## 0 一句话 + 本回合承重里程碑

**M2 真 Mongo 集成(承重)**: 在真实 MongoDB 上验 Go+TS 全链路 CRUD/原子/净化/unique/@@-绑定/owner-scope 隔离/敌意 filter 空/ownerField 未绑定启动报错。M0(工作流重组)与 M1(Go build 坐实)已在上一轮完成,本回合不再触碰。

## 1 范围与不在范围

- **在范围**: Go 集成测试(已跑,6/6); TS 集成测试(server.test.ts 注入 Mongo); F1 敌意 owner filter 回归; F2 启动拒绝回归; F10 hsetnx 评估; 刷新 STATUS.md 封存 M0/M1。
- **不在范围**: M3(watch,需副本集), M4(Next.js app), M5(conformance), Go s/p 解析(F13,进 M2 但不阻塞)。

## 2 事实快照(本机实测)

| 探针 | 结果 |
|---|---|
| `DOPDB_TEST_MONGO_URI="mongodb://localhost:27017" go test -run Integration ./...` | 6/6 PASS, 1.6s |
| `go build ./...` | 干净 |
| `go vet ./...` | 干净 |
| `gofmt -l .` | 空 |
| `cd ts && npx tsx --test test/config.test.ts test/schema.test.ts ... test/client.test.ts` | 9/9 PASS |
| `cd ts && npx -p typescript tsc --noEmit` | 干净 |
| `npx tsx --test test/hardening.test.ts` | 1/1 PASS (F2) |

## 3 上回裁决

M0/M1 无正式回合目录,由 Opus 在 STATUS.md §4 直接记录。本次一并封存。

## 4 已知约束

- L0: `sanitize.go`, `httpserve/context.go` @-顺序, `http_accessor.go` mergeScope, `jwt.go` 拒 none, 全部 `*_test.go`
- PRL1-6 全部适用
- MongoDB 普通实例(非副本集)→ watch 需跳

## 5 可预见岔路

- 集成测试某断言不过: 不改测试(RL2), 记 failed + suspend
- TS server.test.ts 跑不到 Mongo: 排查连接串

## 6 并行轨候选

- 🟢 文档同步核对(0 MISS)
- 🟢 I-Q9 迁移边界声明补 README

## 7 复跑声明

Go 集成已复跑, 6/6 通过。TS 单测已复跑, 9/9 通过。

## 8 给 Qwen 的话

本回合纯承重验证,无代码改动。跑完集成测试后写回执。
