# Receipt · package-01-auth-jwt

- **状态**: PASS
- **执行时间**: 2026-08-19T12:05+08:00
- **执行环境**: Go 1.24.5 darwin/arm64 / Node v20.20.2 / 服务器 redis 8.10.1 (port 6666)
- **应用的文件**: `httpserve/jwt.go`、`httpserve/jwt_algconfusion_test.go`（新增）、`ts/test/jwt.test.ts`（新增）、`httpserve/serve.go` —— 已随 `files/` 整体覆盖确认存在。

## 验证记录

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| `go test -count=1 -run 'TestJWT' -v ./httpserve/` | 3 用例 PASS | `--- PASS: TestJWTRejectsAlgConfusion (0.11s)` `--- PASS: TestJWTHS256DeploymentRejectsRS256Header (0.10s)` `--- PASS: TestJWTExpFailsClosed (0.00s)` `ok github.com/doptime/dopdb/httpserve 0.482s` | PASS |
| `cd ts && node --import tsx --test test/jwt.test.ts` | 4 用例 PASS | `# tests 4  # pass 4  # fail 0` | PASS |

## 验收标准逐条核对

1. RS256 部署下公钥 PEM 签的 HS256 令牌被拒 —— 达成（TestJWTRejectsAlgConfusion）。
2. RS256 正常令牌通过、claims 完整 —— 达成（同测试反向断言）。
3. HS256 部署拒 RS256 header；正常 HS256 通过 —— 达成（TestJWTHS256DeploymentRejectsRS256Header）。
4. exp 七形态两端一致、只有"未来"通过 —— 达成（TestJWTExpFailsClosed + jwt.test.ts）。
5. 畸形令牌 401 而非 500 —— 达成（TS 测试覆盖解析路径）。
6. 401 `code: "unauthorized"`，不泄漏内部原因 —— 达成（serve.go:100 统一 `invalid token`，conformance ErrorStatusParity 覆盖）。

## 偏差

无。

## 备注

测试需 Node 20（本机默认 Node 19 下 `node --import tsx --test` 报 `ERR_UNKNOWN_FILE_EXTENSION`，换 `node@20` 后通过；CI 已钉 node 20）。
