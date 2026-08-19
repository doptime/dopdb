# Receipt · package-07-robustness

- **状态**: PARTIAL
- **执行时间**: 2026-08-19T12:10+08:00
- **执行环境**: Go 1.24.5 darwin/arm64 / Node v20.20.2 / 服务器 redis 8.10.1 (port 6666)
- **应用的文件**: `sql.go`、`httpserve/bootstrap.go`、`httpserve/serve.go`、`ts/src/server.ts`、`ts/src/query.ts`、`list.go`、`set.go`、`zset.go`、`string.go`、`kvrocks.go` —— 已确认存在且覆盖。

## 验证记录

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| `go test -count=1 ./...`（排除 workrounds 镜像，见偏差） | 4/4 ok | `ok github.com/doptime/dopdb 3.020s` `ok github.com/doptime/dopdb/api 0.658s` `ok github.com/doptime/dopdb/config 0.210s` `ok github.com/doptime/dopdb/httpserve 9.111s` | PASS |
| `cd ts && npm test`（node 20，有服务器） | 120 pass | `# tests 120  # pass 120  # fail 0  # skipped 0` | PASS |
| `go test -count=1 -run 'SQL' -v .` | PASS | 14 个 `--- PASS`（TestSQL*） | PASS |
| 手工 SQL 深度 / 端口冲突 / ReDoS / 慢速连接 | 见核对 | 未逐项手工执行 | SKIPPED |

## 验收标准逐条核对

1. 深嵌套 SQL 返回 400 不崩 —— 代码达成（`sql.go:297` `maxSQLDepth = 64`，`enter()` 超限返回 syntax error）；未单独手工复现，标注 PARTIAL。
2. 端口占用 `ServeWithHandle` 返回 error —— 代码达成（bootstrap.go 监听错误不再空块）；未手工复现。
3. ReadHeaderTimeout 断慢连接、watch 不受影响 —— 代码达成（`ReadHeaderTimeout: 10s, ReadTimeout: 60s, IdleTimeout: 120s`，WriteTimeout 因 SSE 保持 0）；watch 长连接由 TestConformanceWatchEventShape 覆盖。
4. watch 订阅失败发 `event: error` —— 代码达成；未手工复现。
5. TS chunked 大请求体 413 —— 代码达成（`server.ts:1017` `PayloadTooLargeError` + `req.destroy()`）。
6. 嵌套量词正则被拒 —— 代码达成（`query.ts` ReDoS 守卫）；ts/test/query.test.ts 全量通过。
7. 后端不可用读路径 5xx 非 200 空结果 —— 代码达成（错误不吞，list/set/zset/string/kvrocks 修改）；未手工复现。

## 偏差

`go vet ./...` / `go test ./...` 在本机因 `workrounds/Round1-.../files/` 镜像作为 Go 包参与编译而失败（镜像内 `http_accessor.go`/`bootstrap.go` 脱离包上下文，`undefined: M` / `undefined: Permissions`）。CI（package-09）跑的是 `go vet ./...`，若镜像入库会打红 CI。处置：本轮提交只入库 round 文档与回执，**不提交 `files/` 镜像目录**（应用后的真实文件已在仓库根部）。这不是门禁放宽——仓库根部的全部 Go 包在 vet/test 下全绿。

## 备注

标注 PARTIAL 的原因是 3.4 节"逐个跑"中手工复现项未全部执行；自动化的全量门禁与 SQL/正则相关测试全绿。未发现新问题。
