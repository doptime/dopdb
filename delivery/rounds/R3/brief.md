# 回合简报 brief · dopdb · R3(2026-06-26)

## 0 一句话 + 本回合承重里程碑

**并行 🟢 轨道:框架收尾加固** —— M3(watch E2E)被副本集阻塞,主线暂停;本回合走全 🟢 收尾包:I-TS8(浏览器打包安全守卫) + I-Q6(schema-as-data 导出验证) + I-Q5(打包核对) + I-TS2(Pages Router 验证) + I-Q4(文档同步)。

本回合**无承重里程碑**——纯整理+可验证加固,快路径出包。

## 1 范围与不在范围

### 在范围
- **I-TS8**: 为 `dopdb` 根入口与 `dopdb/client` 入口写一条**可验证守卫**(import 图检查),命中 `mongodb`/`node:*` 即失败;进 `ts/test/`。
- **I-Q6**: 跑 `dopdb-spec` CLI(`ts/bin/spec.ts`) 对真实 schema 导出 `dopdb.schema.json`,核对结构。
- **I-Q5**: 核对 `package.json` `files` 字段排除了 `node_modules`/`dist`;打包单层。
- **I-TS2**: 验证 `serve()` 返回的 `DopdbServer.listener` 可用作 Pages Router 入口(`export default (req, res) => srv.listener(req, res)`)。
- **I-Q4**: `README.md` + `docs/` 全部 6 篇与当前代码同步核对(命令词表、URL 方案、API 签名),记录 MISS。
- **F10 评估**: 审视 `server.ts` hsetnx 跨租户存在性泄漏,写结论(修 or 接受)。

### 不在范围
- M3(watch E2E)——需副本集,进下一轮。
- I-Q7(最小 Next.js 示例 app)——需额外依赖,留待 R4。
- I-Q8(优雅关闭 close())——非阻塞,留待 R4。
- 任何 Go 代码改动(本包纯 🟢 整理;Go 回归基线已坐实)。

## 2 事实快照(本回合本机实测)

| 探针 | 结果 |
|---|---|
| `go build ./...` | 干净, EXIT:0 |
| `go vet ./...` | 干净, EXIT:0 |
| `gofmt -l .` | 空输出 |
| `go test ./...` | 4 包 OK |
| `cd ts && npx tsc --noEmit` | 干净, EXIT:0 |
| `cd ts && npx tsx --test test/*.test.ts` | 11/11 全过 |
| `node -v` | v19.0.0 (tsx 可跑) |
| Go version | go1.24.5 darwin/arm64 |

## 3 上回裁决

| 回合 | 裁决 | 指针 |
|---|---|---|
| R1 | M0/M1/M2 通过;Go 6/6 集成 + TS 9/9 单测 | `delivery/rounds/R1/SEAL.md` |
| R2 | M4/M5 通过;F13 修复+错误码对齐 | `delivery/rounds/R2/SEAL.md` |

**异常发现逐条回应**: R1 提 `server.test.ts` `base` undefined 是 TS 测试框架自身问题(非代码 bug),不在 M2 判据范围。已归档。

## 4 已知约束

- L0: 全部 `*_test.go` 与 `ts/test/*`(RL2 不碰裁判); `sanitize.go`, `httpserve/context.go` @-顺序, `http_accessor.go` mergeScope, `jwt.go` 拒 none。
- PRL1–6 全部适用。
- 本回合**不修改 Go 源码**——纯 TS 侧加固与文档。
- Node 19 不支持 `--import tsx` ESM loader;测试用 `npx tsx --test` 代替。

## 5 可预见岔路

- **I-TS8 守卫设计**: 可选 ① `import` 图静态扫描(test 里 `require` 根入口,检查导出链不引 mongodb) ② 打包工具分析。① 更简单、不增依赖。
- **F10 hsetnx 评估**: 结论可能是"接受"(hsetnx 语义本就是"不存在才写",泄漏的是 key 存在性而非数据内容)或"前置 scoped exists 检查"。以项目卡决策表为准。
- **I-Q4 文档 MISS**: 若发现与代码不同步,直接改 docs(非 L0);若发现需改代码,记 suspend 进 R4。

## 6 并行轨候选

本回合即并行轨,无额外轨道。

## 7 复跑声明

GLM 已本机复跑全量回归基线(Go build/vet/gofmt/test + TS tsc + TS 11/11 单测),全部通过。

## 8 给 Qwen 的话

本回合纯 🟢,无承重里程碑。**不走 plan.md 规划步**——GLM 直接出定稿包(`packet-*.md`)。

请等 GLM 下发 `packet-*.md` 后,按包执行。每包带 🟢 标,放手成批做;过自检即 done。

## 9 Qwen 能力画像(三行)

- **执行力 高**: R1/R2 承重件一次性通过;F13 修复准确,Go build/vet/test 全绿;无 facade,无缩范围。
- **纪律性 高**: 断言/门槛/冻结件/测试零触碰;累计 3 处低危均无害。
- **汇报质量 高**: 回执数字与产物逐项一致;异常发现一贯诚实。
