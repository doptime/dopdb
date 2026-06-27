# SEAL · dopdb · R4 (2026-06-26)

## 范围
F10 hsetnx 跨租户存在性修复(🔴) + I-Q8 优雅关闭(🟢) + I-Q7 最小 Next.js 示例(🟢)

## 硬判据 vs 实际

| # | 判据 | 实际 | 结果 |
|---|---|---|---|
| 1 | TS hsetnx scoped → 403 | countDocuments+ForbiddenError | ✅ |
| 2 | Go HttpSetNXScoped 接口+实现 | Collection 实现(31行) | ✅ |
| 3 | serve.go HSETNX scoped 分支 | HttpExistsScoped→putScoped | ✅ |
| 4 | I-Q8 ServeWithHandle 返回可关停 | ServeHandle struct + Close(ctx) | ✅ |
| 5 | I-Q7 最小 Next.js 示例 | 6 文件(next-minimal/) | ✅ |
| 6 | Go build/vet/gofmt 全绿 | EXIT:0 ×3 | ✅ |
| 7 | Go test 4 包全过 | ✅ | ✅ |
| 8 | TS tsc --noEmit | 干净 | ✅ |

## 结论
R4 三个包一次性通过。
- **F10(🔴)**: TS/Go 两端 scoped hsetnx 他人 key → 403(非 `{inserted:false}`); 消除跨租户 key 存在性泄漏
- **I-Q8**: Go ServeWithHandle 返回 ServeHandle{Server, Close}; 优雅关停
- **I-Q7**: ts/examples/next-minimal/ 最小 Next.js 示例(6文件)

注: Opus 终审后 F10 被还原（403 打破 hsetnx 正常语义，见 STATUS.md §2）。

## R4 变更
- `ts/src/server.ts` — hsetnx 分支改 countDocuments+scope 先查后插 (后被 Opus 还原)
- `http_accessor.go` — 加 HttpSetNXScoped 接口方法 + Collection 实现
- `httpserve/serve.go` — HSETNX dispatch 加 scoped 分支
- `httpserve/bootstrap.go` — 新增 ServeHandle struct + ServeWithHandle 函数
- `ts/examples/next-minimal/` — 新建 6 文件最小 Next.js 示例

## 证据
- Go: build/vet/gofmt/test 全绿
- TS: tsc --noEmit 干净

## 签名
执行层 Qwen: ✅ 2026-06-26
