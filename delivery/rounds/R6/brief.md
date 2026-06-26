# 回合简报 brief · dopdb · R6(2026-06-26)

## 0 一句话 + 本回合承重里程碑

**R6: M3 watch E2E 真验证 (🔴)**——MongoDB 副本集已启动 (`localhost:27017=PRIMARY`)，可跑 change stream → SSE 往返集成测试。这是项目唯一未完成的承重里程碑。

## 1 范围与不在范围

### 在范围
- **Go watch 集成测试 (🔴)**: 建 `dopdb_test.go` 或 `mongo_test.go` 中新增 watch 集成测试，验证：
  - insert → 推送 change event
  - update → 推送 change event
  - resume token 续传（停流后重连不漏）
  - scoped delete 不投递（已知限制 I-WA2）
- **TS watch 集成测试 (🟢)**: 在 `ts/test/watch-e2e.test.ts` 中验证：
  - client.watch 收到 insert/update 事件
  - 断线续传（Last-Event-ID）
  - scoped delete 不投递
- **M3 封存**: 若测试全过 → M3 从 suspend 改通过

### 不在范围
- I-P3 互操作（Go 服务 + TS 客户端）——进 R7
- 任何生产级 watch 优化（backpressure、批量推送等）

## 2 事实快照(本机实测)

| 探针 | 结果 |
|---|---|
| `go build ./...` | OK |
| `go vet ./...` | OK |
| `gofmt -l .` | 空 |
| `go test ./...` | 4 包 OK |
| `cd ts && npx tsc --noEmit` | 干净 |
| `cd ts && npx tsx --test test/*.test.ts` | 10/10 全过 |
| MongoDB 副本集 | `localhost:27017=PRIMARY` ✅ |
| `DOPDB_TEST_MONGO_URI` | 未设置（需设） |

## 3 上回裁决

| 回合 | 裁决 | 指针 |
|---|---|---|
| R1 | M0/M1/M2 通过 | `delivery/rounds/R1/SEAL.md` |
| R2 | M4/M5 通过 | `delivery/rounds/R2/SEAL.md` |
| R3 | 全 🟢 通过 | `delivery/rounds/R3/SEALED.md` |
| R4 | F10 修复+I-Q8+I-Q7 通过 | `delivery/rounds/R4/SEALED.md` |
| R5 | 消融复审通过; M3 suspend | `delivery/rounds/R5/SEALED.md` |

**异常发现回应**: 无。

## 4 已知约束

- L0: 全部 `*_test.go` 与 `ts/test/*`(RL2 不碰已有测试)
- PRL1–6 全部适用
- **新增测试预授权**: 项目卡 §10 允许新建测试文件
- MongoDB 副本集 `mongodb://localhost:27017` 已可用
- Node 19 不支持 `--import tsx`，用 `npx tsx --test`

## 5 可预见岔路

- **Go watch 测试**: change stream 是异步的，需等待事件到达；超时处理需小心
- **TS watch 测试**: SSE 流式读取在 Node 19 可用，但 `fetch` 的 SSE 支持有限；可能需要用 `node:http` 直接连接
- **resume token 续传**: Go 侧 `watch()` 函数已有自动恢复逻辑（`mongo.go:387-426`）；TS 侧客户端用 `Last-Event-ID` 续传

## 6 并行轨候选

Go watch 集成测试和 TS watch 集成测试互不写同一文件，可并行。

## 7 复跑声明

L3 复跑全量回归基线（Go build/vet/gofmt/test + TS tsc + TS 单测）全部通过。

## 8 给 Qwen 的话

本回合有 **1 个 🔴**（Go watch 集成测试）。走 plan.md 规划步：请写 `delivery/rounds/R6/plan.md`——逐拟议包列 目标/拟建什么/打算怎么自证；只写不执行。

M3 是项目最后一项承重里程碑，务必严谨。

## 9 Qwen 能力画像（三行）

- **执行力 高**: R1–R5 连续 5 回合零返工。
- **纪律性 高**: 冻结件/裁判零触碰。
- **汇报质量 高**: 回执数字与产物一致。
