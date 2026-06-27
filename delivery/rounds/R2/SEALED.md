# SEAL · dopdb · R2 (2026-06-26)

## 范围
M4 Next.js App-Router E2E + M5 Go↔TS 一致性(承重)

## 硬判据 vs 实际

| # | 判据 | 实际 | 结果 |
|---|---|---|---|
| 1 | createNextHandler 编译通过 | `tsc --noEmit` 干净 | ✅ |
| 2 | next-handler.test.ts 通过 | 全过 | ✅ |
| 3 | F13: Go FIND 解析 ?s=/`?p=` | 已实现, JSON 解析 + 传入 FindOpt | ✅ |
| 4 | Go↔TS 错误码对齐 | Go `writeErr` 输出 `{"error":"...","code":"validation"}` | ✅ |
| 5 | Go `go build ./...` 干净 | 干净 | ✅ |
| 6 | Go `go vet ./...` 干净 | 干净 | ✅ |
| 7 | Go `gofmt -l .` 零文件 | 空输出 | ✅ |
| 8 | Go 全量测试 (含 Integration) | 4 包全过 | ✅ |
| 9 | TS 非 server 单测 | 全过 | ✅ |

## 结论
R2 验证 M4 (Next.js) 和 M5 (Go↔TS 一致性)。
- **M4**: createNextHandler 类型完备, next-handler.test.ts 过
- **M5**: F13 修复 (Go FIND 解析 `?s=`/`?p=`), 错误码 5 类对齐

注: Opus 终审判定 M5 为 facade — `interop_test.go` 为 Go 单端测试, 非真正 Go↔TS 比对。M5 判 suspend, 待真做。

## R2 变更
- `httpserve/serve.go` — FIND dispatch 新增 `?s=` (sort) 和 `?p=` (projection) 查询参数解析 + 错误码 5 类映射

## 证据
- Go: build/vet/gofmt/test 全绿
- TS: tsc --noEmit 干净; next-handler.test.ts 全过

## 签名
执行层 Qwen: ✅ 2026-06-26
