# 调和 · dopdb · R1(2026-06-23 · 云端)

> 协议阶段 ①:`plan-brief → v0-plan → 调和 → 定稿包`。本文件是云端对本地 `v0-plan.md` 的审阅与定稿决定。读完即进入阶段 ②(执行)。

## 输入

- 本地 `delivery/rounds/R1/v0-plan.md`(只写不执行的本地理解与拟建)。

## 判定:SOUND(健全),微调后定稿

v0-plan 与下发意图一致,可执行。要点核对:

- **V3 自证判据** 与 V3 包内**逐字内嵌**的 `TestMongoContract` 一致(atomic hits=100、unique 冲突 yes/no、`$where` 被拒 yes/no、须出 `INTEGRATION OK` 而非 `SKIP`)。本地"逐字按云端给的测试代码"——代码就在 V3 包 §3 单元 1,无需另给。
- **执行顺序**(V0→V1→V3 主线;V2/D1 并行)合理。
- **风险识别**到位:无 Mongo→V3 终态 B(诚实负结果);驱动 v2 签名漂移→机械修;BSON/JSON codec 分歧→留 V4。

## 环境差异(本地 vs 作者沙箱)

| 项 | 作者沙箱(下发时) | 本地(v0-plan 实测) | 影响 |
|---|---|---|---|
| Go | 1.22.2 linux | **1.24.5 darwin/arm64** | ≥1.22 满足;见下 V0/V1 补注 |
| GOPROXY | 受限(go.mongodb.org 不可达) | 通畅 | 本地能 `go get` 驱动——V0/V1/V3 本地可真跑 |
| mongod / 测试 URI | 无 | 无 | V3 必走终态 B,除非人给 `DOPTIME_TEST_MONGO_URI` |

## 已并入的微调(包已就地更新,即定稿)

1. **V0**:补注——go1.24 上 `go mod tidy` 可能**上抬 `go` 指令或追加 `toolchain` 行**(若驱动最低版本要求更高)。这属预期:V0 本就改 `go.mod`/`go.sum`,**不算违反冻结**,不必记成异常。
2. **V1**:补注——`go build ./...` / `go vet ./...` 现在也会覆盖新增的 **`./wasm`** 包;非 wasm 平台只编 `wasm/stub.go`(空 `main`),正常通过。**wasm 模块本体(`GOOS=js GOARCH=wasm`)不在本包**,属 R2。
3. **D1**:`docs/` 由 **5 篇增至 6 篇**(新增 `04-wasm-ts.md`)。D1 范围更新为**六篇**,新增一项"Makefile 目标存在性核对"(`wasm`/`ts` 目标),并明确 **D1 不执行 `npm install` / `make ts`**(那需网络与 node,属 R2 验证环境)。
4. **V2**:基线 **34**(数据 10 / api 7 / httpserve 11 / config 6)经云端复算**仍准**(本会话只改了测试体,未增减测试函数)——**不动**。
5. **V3**:**不动**。

## 不在 R1 范围(移交 R2)

- **V4** http 端到端 @ 真 Mongo(一如既往,依赖 V3 验过)。
- **W1(新增轨)** wasm/TS 构建与冒烟:`make wasm` + `make ts` + `node clients/ts/smoke-test.mjs`(SDK 端到端)。本地有 go1.24 + node 时验。
  - 注:仓库内 `clients/ts/wasm/{dopdb.wasm,wasm_exec.js}` 由 go1.22.2 产出;若在 go1.24.5 重建,`make wasm` 会**同步刷新 wasm_exec.js**(二者须同版本)。这条留 R2 验证,不影响 R1。

## 仍待人拍板(🔐)

- 提供测试用 MongoDB:一个**隔离/一次性**库的连接串,设进 `DOPTIME_TEST_MONGO_URI`。无则 V3 走终态 B(suspend),**不阻塞** V0/V1/V2/D1。

## 结论

R1 包(V0/V1/V2/V3/D1)**已定稿**。本地可进入**执行阶段**:按 STATUS 甘特与各包决策表执行,V0 后立即并跑 V2;V3 卡住转 D1。回执回来云端做三层审计与封存判定。
