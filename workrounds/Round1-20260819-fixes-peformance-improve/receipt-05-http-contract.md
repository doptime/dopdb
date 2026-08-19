# Receipt · package-05-http-contract

- **状态**: PASS
- **执行时间**: 2026-08-19T12:09+08:00
- **执行环境**: Go 1.24.5 darwin/arm64 / Node v20.20.2 / 服务器 redis 8.10.1 (port 6666)
- **应用的文件**: `httpserve/serve.go`、`httpserve/context.go`、`kvrocks.go`、`ts/src/server.ts` —— 已确认存在且覆盖。

## 验证记录

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| `go test -count=1 -run 'Conformance' -v ./httpserve/`（含 MethodEnforcement / ErrorStatusParity） | 全部 PASS | `--- PASS: TestConformanceMethodEnforcement (0.31s)` `--- PASS: TestConformanceErrorStatusParity (0.31s)` 等 21/21 | PASS |
| `go test -count=1 ./httpserve/` | ok | `ok github.com/doptime/dopdb/httpserve 7.905s` | PASS |
| `cd ts && node --import tsx --test test/server.test.ts` | PASS | `# tests 30  # pass 30  # fail 0` | PASS |

## 验收标准逐条核对

1. 写命令 GET → 405 双端一致，POST 正常 —— 达成（TestConformanceMethodEnforcement）。
2. 非 Hash 集合 Hash 命令 Go 404、无 panic —— 达成（TestConformanceHashCommandOnNonHashCollection）。
3. 未知 `?ds=` → 400 —— 达成（参数校验，ErrorStatusParity 覆盖）。
4. 文档不含 Authorization/Cookie 头字段 —— 达成（Params 不再含请求头）。
5. 缺 `?f=` 双端 400 —— 达成（ErrorStatusParity）。
6. `?index=abc` / `?count=abc` → 400 —— 达成。
7. `?count` 超大钳制到 1000 —— 达成。

## 偏差

无。

## 备注

写命令仅 POST、GET 405 是本轮刻意契约（见 round AGENTS.md §六），破坏性变更应记入 CHANGELOG（发版不在本轮）。
