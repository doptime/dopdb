# 包: P-W1-wasm-ts · 回合 R2(2026-06-24 · wasm/TS 本地复跑)

分级: 🟢 客观自证(批量)。三条命令各看退出码 + 产物存在 + smoke-test 的成功串;无判断空间。
上游: 无(独立于 Mongo)。前置:本地 go1.24.5 + node + npm + 网络(R1 V0 已证联网)。
回执写到: `delivery/rounds/R2/receipt-P-W1-wasm-ts.md`

全景: STATUS.md 甘特 R2 的 W1,本回合**并行轨**。与 V4 互不依赖,可并跑;V4 卡住时顶上。

任务一句话: 在本地 go1.24.5 上 `make wasm`(重建 dopdb.wasm + 刷 wasm_exec.js)→ `make ts`(npm install + tsc)→ `node clients/ts/smoke-test.mjs`(SDK 端到端),证 wasm 桥 + TS 客户端在 1.24/node19 上工作、wasm_exec.js 与 dopdb.wasm 同版本。

## 1 背景 · 现在是什么情况

R1 给框架加了 WASM + TS 支持:Go api 核心编进 `clients/ts/wasm/dopdb.wasm`,TS SDK(`clients/ts/src/*.ts`)用 `wasm_exec.js` 加载它,暴露 `loadDopdb()` / `db.api(fn)` / `nodeListener` 等。作者沙箱在 **go1.22 + node22** 上自测过(tsc 干净、smoke-test 端到端过)。本包在**本地 go1.24.5 + node v19** 复跑,坐实:① 1.24 重建的 wasm 能被 1.24 的 wasm_exec.js 加载;② tsc 在本地干净;③ SDK 端到端过。

`make wasm` 会用本地 1.24 工具链重建 `dopdb.wasm` 并**同步刷新** `wasm_exec.js`(二者同源 1.24,故必然匹配)——这正面消解 v0 风险 3。

## 2 意图 · 为什么做、什么算好

把「wasm/TS 在 1.22/node22 之外也工作」从假设变证据。好的定义:三条命令退出 0,产物齐,smoke-test 打印成功串。

红线: RL1–RL8 适用。**RL5**:exit 0 还要看 smoke-test 那行成功串真在;**RL6**:1.24 上若有差异,如实记,别掩。
修改令: 本包是**构建+跑**,不是改源。允许产物被重建/覆盖(见 §5),**不改** `clients/ts/src/*.ts` 与 Go wasm 源(`wasm/main.go`/`stub.go`)。

## 3 任务 · 具体做什么

### 单元 1 · 重建 wasm
```bash
make wasm 2>&1 | tee delivery/rounds/R2/w1_wasm.txt
echo "EXIT: $?"
ls -l clients/ts/wasm/dopdb.wasm clients/ts/wasm/wasm_exec.js
```
看:退出 0;`dopdb.wasm` 存在且非空;`wasm_exec.js` 已刷新(来自本地 1.24 GOROOT)。

### 单元 2 · 编译 TS
```bash
make ts 2>&1 | tee delivery/rounds/R2/w1_ts.txt
echo "EXIT: $?"
ls clients/ts/dist/*.js clients/ts/dist/*.d.ts
```
看:退出 0(npm install + tsc 均成功);`dist/` 有 `.js` 与 `.d.ts`。

### 单元 3 · SDK 端到端冒烟
```bash
node clients/ts/smoke-test.mjs 2>&1 | tee delivery/rounds/R2/w1_smoke.txt
echo "EXIT: $?"
```
看:退出 0;stdout 含 `ALL TS SDK INTEGRATION TESTS PASSED`。
**注**:node v19 的原生 `fetch` 仍是实验特性,stderr 可能打印 `ExperimentalWarning: The Fetch API is an experimental feature`——**这是预期,不算失败**;只看退出码 + 成功串。

## 4 验收 · 怎么算完成
- [ ] `make wasm` 退出 0;`clients/ts/wasm/dopdb.wasm` 非空;`wasm_exec.js` 已刷新
- [ ] `make ts` 退出 0;`clients/ts/dist/` 有 `.js` + `.d.ts`
- [ ] `node clients/ts/smoke-test.mjs` 退出 0 且含 `ALL TS SDK INTEGRATION TESTS PASSED`
- [ ] 三个 `w1_*.txt` 留痕;关键数字抄:本地 go 版本、node 版本、各步退出码、wasm 字节数
- [ ] 进度账落 `delivery/rounds/R2/progress.md`

## 5 边界 · 不要做什么
可写(均为构建产物 / 依赖,允许重建覆盖):`clients/ts/wasm/dopdb.wasm`、`clients/ts/wasm/wasm_exec.js`、`clients/ts/dist/*`、`clients/ts/node_modules/`(npm install)、`delivery/rounds/R2/`。
禁改:`clients/ts/src/*.ts`、`wasm/main.go`、`wasm/stub.go`、任何 Go 框架/测试/L0。越界登记 `delivery/rounds/R2/oob.md`。

## 6 预算与换法 · 决策表
| 情况 | 动作 |
|---|---|
| 三步全 0、产物齐、含成功串 | done;抄退出码 + 版本 + wasm 字节数 |
| 本地无 node / npm | 终态 B;回执 suspend,写「无 node/npm,wasm/TS 复跑不了」;**不阻塞 V4** |
| `npm install` 联网失败 | 重试 1 次;仍失败 → blocked,记 registry 错误一行,suspend |
| `make wasm` 失败(找不到 wasm_exec.js / 编译错) | 记错误;若是 1.24 GOROOT 路径变化致 Makefile 找不到 wasm_exec.js → 记一行(云端会调 Makefile 定位逻辑),suspend |
| `make ts`/tsc 报错 | 记 tsc 错误原文(可能 1.24/node19 类型环境差异);suspend |
| smoke-test 退出非 0(wasm_exec↔wasm 版本不匹配,或 1.24 wasm 行为差异) | failed,贴 `w1_smoke.txt` 末尾报错,异常发现写「1.24 重建后 SDK 端到端不过」,suspend 交云端(可能要云端按 1.24 调 wasm 桥) |
| stderr 仅有 fetch ExperimentalWarning、退出 0、含成功串 | **算 done**(警告非失败) |

整包 ~3–5 分钟(含 npm install)。本包不改任何源码;唯一"改动"是构建产物被重建,属预期。

## 7 收尾
按协议 §3 写回执;关键数字抄:本地 `go version`、`node --version`、三步退出码、`dopdb.wasm` 字节数、smoke-test 成功串是否出现。「异常发现」必写:① 1.24/node19 与 1.22/node22 的任何行为差异;② wasm_exec↔wasm 不匹配迹象;③ 任何「exit 0 但成功串没出现」。
