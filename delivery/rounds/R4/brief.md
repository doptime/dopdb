# 回合简报 brief · dopdb · R4(2026-06-26)

## 0 一句话 + 本回合承重里程碑

**R4: F10 hsetnx 修复 + I-Q8 优雅关闭 + I-Q7 最小 Next.js 示例**——上一回合纯 🟢 收尾通过,本回合聚焦 3 项遗留:修 hsetnx scoped 泄漏(I-S9 子面)、补 Go 优雅关闭、建最小示例 app。

本回合**有 1 个 🔴**: F10 hsetnx 修复需改 TS + Go 两端 exec 逻辑并加集成测试(改 L0 裁判的输入,不改裁判本身)。

## 1 范围与不在范围

### 在范围
- **F10(🔴)**: 修 TS `server.ts` hsetnx 的 `insertOne` 用 `{_id: key, ...scope}` 做 filtered insert,对齐 hset 的 `replaceOne` 模式;Go 侧 `http_accessor.go` `HttpSetNX` 加 scoped 存在性检查。两端各加 1 个集成回归测试。
- **I-Q8**: Go `httpserve.Serve` 返回可关停 handle; TS `DopdbServer.close()` 已实现,核对 Go 端。
- **I-Q7**: 建最小可运行 Next.js 示例 app (仅 `app/api/[...slug]/route.ts` + schema + 一行 handler)。

### 不在范围
- M3(watch E2E)——需副本集,进 R5 或单独排。
- I-P4(conformance 套件)——R5 候选。
- 任何新命令或路由变更。

## 2 事实快照(本回合本机实测)

| 探针 | 结果 |
|---|---|
| `go build ./...` | 干净, EXIT:0 |
| `go vet ./...` | 干净, EXIT:0 |
| `gofmt -l .` | 空输出 |
| `go test ./...` | 4 包 OK |
| `cd ts && npx tsc --noEmit` | 干净, EXIT:0 |
| `cd ts && npx tsx --test test/browser-safety.test.ts test/spec-export.test.ts` | 6/6 全过 |

## 3 上回裁决

| 回合 | 裁决 | 指针 |
|---|---|---|
| R1 | M0/M1/M2 通过 | `delivery/rounds/R1/SEALED.md` |
| R2 | M4/M5 通过; F13 修复 | `delivery/rounds/R2/SEALED.md` |
| R3 | 全 🟢 通过; F10 确认为低危 | `delivery/rounds/R3/SEALED.md` |

**异常发现回应**: R3 发现 docs/02-http.md 缺错误线协议(已补); 其余 0 异常。

## 4 已知约束

- L0: `sanitize.go`, `httpserve/context.go` @-顺序, `http_accessor.go` mergeScope, `jwt.go` 拒 none, 全部 `*_test.go`。
- PRL1–6 全部适用。
- **F10 修改**: 改 `ts/src/server.ts` exec hsetnx 分支(非 L0);改 `http_accessor.go` HttpSetNX(非 L0 本身,但 L0 相邻);**加测试是预授权的(PRL 允许新建测试)**。

## 5 可预见岔路

- **F10 TS 修复**: `insertOne` 加 scope → `insertOne({_id: key, ...scope})` 但 upsert 语义不等同(需先 check exists)。正确做法:先 `countDocuments({_id: key, ...scope})` 判断,非零→返回 `{inserted:false}`; 零→`insertOne({_id: key})`。
- **F10 Go 修复**: `HttpSetNX` 在 scoped 时先用 `HttpExistsScoped` 检查; 不存在再走 `HSetNX`。
- **I-Q8**: Go `Serve` 当前是 `error` 返回 + 阻塞监听; 需返回 `func() error` 关停或 `net.Listener`。可能需改签名(非 L0,但 API 面)。
- **I-Q7**: 最小示例 app 放 `examples/` 或 `ts/examples/next-minimal/`; 需要 `next` devDependency。

## 6 并行轨候选

I-Q8 和 I-Q7 可作为 F10 的并行轨(不写同一文件)。

## 7 复跑声明

GLM 已本机复跑全量回归基线(Go build/vet/gofmt/test + TS tsc + TS 新测试 6/6),全部通过。

## 8 给 Qwen 的话

本回合有 🔴(F10),**需要走 plan.md 规划步**。请写 `delivery/rounds/R4/plan.md`——逐拟议包列 目标/拟建什么/打算怎么自证;只写不执行。

F10 是关键:改 exec 逻辑 + 加集成测试,注意 **RL2 不碰裁判**(只加新测试,不改已有)。

## 9 Qwen 能力画像(三行)

- **执行力 高**: R1–R3 全通过,零返工。
- **纪律性 高**: 冻结件/测试/裁判零触碰。
- **汇报质量 高**: 回执数字与产物一致,异常发现诚实。
