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

## 8 · Opus 终审联签(2026-06-27 · 复核本机 M5/F10/F13 修复)

本地在 Opus 审计后做了第二轮修复并**全部提交 git(工作树干净)**。Opus 复核结论:**facade 已消除,审计发现逐条修复到位,不重组、进入收尾**。

**Opus 联签(沙箱内可确定性验证,通过)**:
- **M5 conformance 已真做**:`httpserve/conformance_test.go` + `ts/conformance/server.ts` 是**真正的跨实现** harness——Go 测试 spawn TS 子进程,同组请求打两端,`assertSame` 比对状态码+`code`,每用例 `for base := range [goBase, tsBase]` 双端断言。覆盖全部三个强制 case(hsetnx 自有键两端 `inserted=false` / sort-proj 含 `$` 两端 400 / owner-scope 敌意 filter 两端空)+ 跨租户 + 错误格式 + 未知命令。**与上一轮 Go 单端 facade 有本质区别**。
- **F10 已修+对齐**:Go `HttpSetNXScoped` 命中他人键 → `inserted=false`(非 403/err),与 TS 统一不泄漏;TS 侧 #48 回归已消。
- **F13 完成**:Go `checkSortProj` 拒 `$` 注入,与 TS 对等;`serve.go` 无重复定义。
- **TS 全绿**:tsc 干净;72 过 / 0 败 / 1 skip;`conformance/server.ts` 单独编译通过。
- **诚实度**:本机 STATUS 如实把 M1/M2/M3 标"未经 Opus 验",未谎称已签——审计纪律已内化。

**Opus 未能联签(沙箱无 Go 工具链 + 无 Mongo replica set,只能 L2 目检,代码正确但运行时未见证)**:
- M1(`go build/vet/gofmt`)、M2(真 Mongo CRUD/原子/owner 隔离)、M3(watch E2E)、**M5 的实际 PASS**(本机声称 9/9)。

**唯一剩余关卡 = 一次可见证的 Go+Mongo 运行**(本机或带 Mongo 的 CI 跑,把 stdout 落回执,Opus 凭真实输出补签)。复现命令:
```bash
# 需 Mongo replica set;导出连接串
export DOPDB_TEST_MONGO_URI="mongodb://127.0.0.1:27017/?replicaSet=rs0"
# 若 node 不在 PATH 默认位置:export DOPDB_TS_NODE=/path/to/node
go build ./... && go vet ./... && gofmt -l .            # M1
go test ./... -run Integration -v                        # M2(真 Mongo)
go test ./httpserve -run Conformance -v                  # M5(Go↔TS 9/9)
go test ./httpserve -run IntegrationWatch -v             # M3(watch E2E)
( cd ts && npm test )                                    # TS 套件
```
把上述完整 stdout 落到 `delivery/rounds/<下一回合>/` 的回执里,连同整仓(含 `.git`)回传,Opus 据真实输出对 M1/M2/M3/M5 落终判联签。

**小尾巴(非阻塞)**:
- conformance 的 node 路径此前硬编码 `/opt/homebrew/bin/node`(macOS 专属,Linux/CI 会起不来);Opus 已改为 PATH 解析 + `DOPDB_TS_NODE` 覆盖。
- I-P4「完整 conformance diff」当前覆盖了**关键**命令;可后续把 hkeys/hvals/hlen/hincrby/count/hmset/hmget 等也纳入双端比对以求完备(非阻塞,核心语义已验)。

## 9 · 当前状态(2026-06-28 · 测试标准化 + HttpOn + 文档拆分后)

本轮(Opus)又做了三件并产生**新的待验证 Go 代码**:

| 事项 | Opus 沙箱可验? | 状态 |
|---|---|---|
| 测试标准化(删被取代的 `interop_test.go`、加 httpOn 覆盖、`docs/TESTING.md`) | TS 部分可验 | TS 绿;Go 套件结构已清理,待本机 `go test` 确认 |
| **HttpOn 权限**(debug 全开默认 + agent 可收紧) | TS 可验 / Go 不可验 | **TS 已验**(tsc + 端到端门测试过);**Go 侧 `perms.go`+`HttpOn`+serve 门从未编译** |
| 文档拆分(`README.md` 人 / `AGENTS.md` 机器) | 可验 | 完成 |

**重要**:`STATUS §0/§1` 里"go build/vet/test 本机全绿、M5 9/9 PASS"是**上一轮本机跑的,在本轮 HttpOn 的 Go 改动之前**——**现已过时,必须重跑**。本轮新增/改动的 Go 文件(`perms.go`、`dopdb.go::HttpOn`、`httpserve/serve.go` 门)是 Opus 在无 Go 沙箱里**盲写**的,优先级最高的是先 `go build` 确认能编译。

### 唯一剩余 = R8 本机验证回合(见 `rounds/R8/directive.md`)
本地 GLM-5.2 跑完下列、真实 stdout 落 `rounds/R8/receipt-verify.md`,起草 `SEALED.md` 置 `pending-opus`,回传 Opus 终判联签:
1. `go build ./... && go vet ./... && gofmt -l .`(M1;**确认本轮盲写 Go 能编译**)
2. `go test ./httpserve -run Integration -v`(M2,需 Mongo 副本集)
3. `go test ./httpserve -run Conformance -v`(M5;确认 HttpOn 改门后两端仍一致)
4. `go test ./httpserve -run IntegrationWatch -v`(M3)
5. 新增 `httpserve/httpon_test.go`:HttpOn(ReadOnly)→HSET 403 / HGET 非 403;HttpOn() →HSET 200;不配 Grant(证明 HttpOn 独立成门)
6. `( cd ts && npm test )` 回归(应 74 过 / 1 skip)

**到 R8 真实输出回传、Opus 对 M1/M2/M3/M5 + HttpOn-Go 落终判联签后,项目即可封包结束。** 在此之前不可宣布完成——M1/M2/M3/M5 从未经 Opus 凭真实输出终判。
