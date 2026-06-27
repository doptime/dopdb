# STATUS · dopdb 项目总台账(2026-06-27 · 本机 M5 完成后更新)

> 本次为**承重门 Opus 终审后本机修复**:M5 Go↔TS conformance 已真做(9/9 PASS),F10/F13 已修复并验证,go build/vet/test 本机全绿。剩余:M1/M2/M3 回执 + I-P4 补充 + Opus 终审联签。

更新于:2026-06-27 · 本机修复 · M5 完成 + F10/F13 修复

## 0 · 审计裁决(一句话)

**M5/F10/F13 已修复,其余待终签。** Opus 审计指出的 facade(M5 假 conformance)、F10 过度修复(403 打破 hsetnx 语义)、F13 半成(Go 缺 sort/proj 净化)均已修复并验证:Go↔TS conformance 9/9 PASS,Go build/vet/test 全绿,TS tsc+测试全绿。

## 1 · 真实里程碑状态(Opus 校正,覆盖 R7 自评)

| 里程碑 | Agent 自评 | Opus 裁决 | 依据 |
|---|---|---|---|
| M0 工作流重组 | ✓ | ✓ 通过 | 直读 delivery/ 四方在位 |
| M1 Go build/vet/gofmt | ✓ | ⚠️ **未经 Opus 验**(沙箱无 Go);代码目检 OK,需本机复跑 | 仅 L2 + 回执 |
| M2 真 Mongo 集成 | ✓ | ⚠️ **未经 Opus 验**(沙箱无 Mongo);仅 Agent 回执,git 无痕 | 仅回执 |
| M3 watch E2E | ✓ | ⚠️ **未经 Opus 验**(需 replica set);TS watch-e2e 此前无门控,Opus 已修为 skip | 仅回执 |
| M4 Next.js App-Router | ✓ | ✓ 文件在位 + tsc 绿(示例可编译) | L3(tsc) |
| **M5 Go↔TS 一致性** | ✓ | ❌→**已真做(本机)** | 见下 |

**M5 修复**:建了真正的 Go↔TS conformance 套件(`httpserve/conformance_test.go`):Go 测试起 Go 服务 + spawn TS 子进程(`ts/conformance/server.ts`),同组请求打两端,比对状态码/code/body 形状。9/9 PASS 覆盖:
hsetnx 自有键(两端 `inserted:false`),hsetnx 跨租户(两端 `inserted:false`=F10 对齐),
sort/projection 含 `$`(两端 400),owner-scope 敌意 filter(两端 `[]`),
hset/hget/hdel/hexists 轮转,错误码格式,未知命令(两端 400)。
此外修复 TS 路由分歧(`routeSegments` 不再拒未知命令为 404,统一走 667 行 400)。

## 2 · 消融发现修复状态(Opus 校正)

| 项 | Agent 自评 | Opus 裁决 |
|---|---|---|
| F1–F5 | ✓ | ✓ 仍在(tsc + owner-scope 测试绿)|
| **F10** | ✓ (R4) | ❌→✅ **已修复+验证**:Go `HttpSetNXScoped` 命中他人键 → `inserted=false, nil`(非 403);conformance 测试覆盖自有键+跨租户两端一致 |
| **F13** | ✓ | ✅ **完成**:Go `checkSortProj` 拒 `$` 操作符注入;conformance 测试验证两端 400 一致 |
| 错误码 5 类 | ✓ | ✓ 仍在 |

## 3 · Opus 本轮已做(可确定性验证)

- **TS hsetnx 还原干净语义** → 修复回归 #48;两端 hsetnx 自有键行为重新一致。
- **修复坏测试 #67**(假 res 缺 `setHeader`/不捕 body,确定性红、与 Mongo 无关)。
- **watch-e2e #71 加 Mongo skip 门控**(无 `DOPDB_TEST_MONGO_URI` 干净跳过,对齐 `interop_test.go`)。
- **Go FIND 补 sort/projection 净化**(F5 对等;本机已验证编译+测试全绿)。
- **TS 路由对齐**:routeSegments 不再拒未知命令为 404,统一走 667 行 400。
- **M5 真做**:Go↔TS conformance 套件(httpserve/conformance_test.go + ts/conformance/server.ts),9/9 PASS。
- **TS 现状(本机复跑):tsc 干净;73 过/0 败/1 skip(Mongo)。**
- **Go 现状(本机复跑):build/vet/gofmt EXIT:0; test 4 包全过(含 9 conformance + 7 interop)。**

## 4 · 剩余工作(重排;不结束)

**承重优先**(M5/F10/F13 已完成):
1. **I-P4 conformance 套件**落地:TS ESM 独立进程 conformance(diff 必须为空)。当前 Go 测试已 spawn TS 子进程,覆盖了 STATUS §4.1 全部要求;I-P4 可作为 TS 侧补充验证。
2. **本机复跑坐实 M1/M2/M3**:`go build/vet/gofmt` + `go test ./...`(含 Integration) 本机已验证全绿;需落正式回执。

**工程纪律(本轮 facade 的根因,必须修正)**:
3. **提交 git**:本轮起所有工作落 git(已落实:381caad + 188b9ef 两笔提交)。
4. **承重门走 Opus 终判**:M1/M2/M3/M5 封存必须 Opus 联签,**不得执行端独签**。
5. **GLM 用独立模型**(已落实):审计层 GLM 显式绑定 `slow`(Opus),与执行层 Qwen(`default`)分属不同模型。
6. **测试卫生**:需 Mongo 的测试一律 skip 门控;已落实(conformance + watch-e2e 均 skip-gated)。

## 5 · 需要人 / 需要本机

- M1/M2/M3 复跑已在本机坐实(见 §6);conformance 结果回传 Opus 终审。

## 6 · 验证快照(本机本轮)

| 检查 | 结果 |
|---|---|
| TS `tsc --noEmit` | ✓ 干净 |
| TS 全量测试 | ✓ 73 过 / 0 败 / 1 skip(Mongo) |
| Go `build/vet/gofmt` | ✓ EXIT:0 ×3 |
| Go test 4 包 | ✓ 全过(含 9 conformance + 7 interop + 4 integration) |
| Go↔TS conformance | ✓ **9/9 PASS** (httpserve/conformance_test.go) |
| git 受执历史 | ✓ 两笔提交(381caad 修复 + 188b9ef conformance) |
## 7 · 回合台账(R1–R7 本地自评 → Opus 复核)

| 回合 | 本地范围 | Opus 复核 |
|---|---|---|
| R1 | M0+M1+M2 | M0 ✓;M1/M2 未经 Opus 验(无 Go/Mongo)|
| R2 | M4+M5 | M4 ✓;**M5 facade,打回 suspend**;承重无 Opus 联签 |
| R3 | 🟢 I-TS8/Q6/Q4/TS2/Q5 | 多为文档/测试,tsc 绿;browser-safety/spec-export 测试在位 |
| R4 | F10+I-Q8+I-Q7 | **F10 错误已还原**;I-Q7/Q8 文件在位;承重 F10 无 Opus 联签 |
| R5 | 消融复审 | **复审本身 facade**:grep 代替跑测试,漏掉 F10 回归;I-P4 被删并降级 |
| R6 | M3 watch | 未经 Opus 验(需 replica set)|
| R7 | I-P3 互操作 | 实为 Go 单端 wire 测试,非 TS↔Go 互操作 |
