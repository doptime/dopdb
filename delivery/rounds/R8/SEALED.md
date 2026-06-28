# SEAL(草稿 · pending-opus)· dopdb · R8 (2026-06-28)

> **状态:`pending-opus`** —— 本机实证全部 PASS,但承重终判归 Opus。本文件为本地起草,等操作者上传 → Opus 据真实输出落终判联签后方生效。

## 范围
R8 收尾验证回合:把 M1/M2/M3/M5 + HttpOn-Go 从"Opus 盲写/未亲验"转为"真实输出可终判"。无新功能,仅真跑 + 留痕 + 补一个 Go 门测试(`httpserve/httpon_test.go`)。

## 硬判据 vs 实际(directive §3)

| # | 判据 | 实际(真实 stdout 见 `receipt-verify.md`) | 结果 |
|---|---|---|---|
| M1 | `go build/vet/gofmt` 零错 | build 0 / vet 0 / gofmt 空(opus 盲写 Go 编译通过) | ✅ |
| M2 | `httpserve -run Integration` | 3/3 PASS(HTTPRoundTrip / OwnerScope / MultiDatasource) | ✅ |
| M3 | Go watch @ 副本集 | 根包 `TestIntegrationWatchInsertUpdate` + `TestIntegrationWatchScopedDelete` 2/2 PASS | ✅ |
| M5 | `httpserve -run Conformance` | 9/9 PASS(两端实比对,HttpOn 改门后仍逐命令一致) | ✅ |
| §3.5 | HttpOn-Go 门行为(新测) | `TestHttpOnGate` PASS:ReadOnly→HSET 403/HGET 非403;HttpOn()→HSET 200;零 Grant | ✅ |
| §3.6 | TS 回归 `npm test` | 74 过 / 0 败 / 1 skip(watch 需 Mongo,设计 skip) | ✅ |

## 全量 Go 套件
`go test ./...` → 4 包(dopdb / api / config / httpserve)**零 FAIL**(根包含 2 watch @ 真副本集)。

## R8 变更(本地)
- 新增 `httpserve/httpon_test.go` —— HttpOn 位掩码独立成门的 Go 端到端测试(镜像 TS,零 Grant 证明独立)。
- 新增 `delivery/rounds/R8/receipt-verify.md` —— §3 逐条真实 stdout。
- **无代码改动**(dopdb.go 仅从误删中恢复,非修改;HttpOn/_perms/serve.go 全部原样,编译即过)。

## 两个环境性发现(非代码缺陷,提请 Opus 知悉)
1. **`dopdb.go` 曾被本地误删**(工作树删除,HEAD 完好)→ 已 `git checkout HEAD -- dopdb.go` 恢复。恢复是还原受跟踪内容,非代码变更。
2. **默认 `node` v19.0.0 坏 `--import tsx` 加载器** → `npm test` 与 Go conformance 的 TS 子进程起不来。改用 `/opt/homebrew/bin/node`(v25.2.1,经 `DOPDB_TS_NODE` 注入)后全绿。**这是本机 node 版本问题,非 dopdb 缺陷**;建议 CI/运行说明里注明 node ≥ 20.6 或用 bun。

## 结论(本地起草,待 Opus 终判)
R8 的实证目标是"系统符合预期":**承重里程碑 M1/M2/M3/M5 + HttpOn-Go 全部以真实输出通过**,两端一致性经 conformance 9/9 实证(HttpOn 改门无回归),watch 经根包 2 测试 @ 真副本集实证,HttpOn 位掩码门行为经独立测试实证。本地判 R8 实证完成,系统行为符合预期。

**承重终判归 Opus**:请据 `receipt-verify.md` 真实输出对 M1/M2/M3/M5 + HttpOn-Go 落终判联签。联签后项目可封包结束。

## 签名
- 本地 GLM-5.2(实证 + 起草): ✅ 2026-06-28
- Opus(承重终判联签): ⏳ **pending-opus**
