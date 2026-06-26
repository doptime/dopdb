# SEAL · dopdb · R2 (2026-06-26)

## 范围
M4 (Next.js App-Router) + M5 (Go↔TS 一致性)

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
| 9 | TS 非 server 单测 | 9/9 通过 | ✅ |

## M4/M5 结论
**M4 通过**: tsc strict 干净, createNextHandler 类型完备, next-handler.test.ts 过。
**M5 通过**: F13 修复 (Go FIND 解析 `?s=`/`?p=`), 错误码格式对齐 (新增 `code` 字段, 5 类映射: validation/forbidden/not_found/unauthorized/error), 默认未知错误从 400→500。

## R2 变更

### Go 侧 (`httpserve/serve.go`)
- **F13**: FIND dispatch 新增 `?s=` (sort) 和 `?p=` (projection) 查询参数解析, JSON 反序列化入 `FindOpt.Sort` / `FindOpt.Projection`
- **错误码**: `writeErr` 签名由 `(w, status, err)` → `(w, status, code, err)`, 输出 `{"error":msg,"code":"..."}`
- **状态映射**: 未知错误从 `400` → `500` (对齐 TS); 新增 401 `unauthorized` case
- **import**: 新增 `fmt` 包

### 无 TS 侧变更 (M4/M5)
F13 是 Go 追 TS, 非 TS 改 Go。

## 证据
- `go build/vet/gofmt` 全绿
- `go test ./...` 4 包 OK
- `tsc --noEmit` 干净
- TS 单测 9/9

## 签名
执行层 Qwen: ✅ 2026-06-26
