# Receipt · package-09-delivery-verifiability

- **状态**: PARTIAL
- **执行时间**: 2026-08-19T12:12+08:00
- **执行环境**: Go 1.24.5 darwin/arm64 / Node v20.20.2 / 服务器 redis 8.10.1 (port 6666)
- **应用的文件**: `.github/workflows/ci.yml`（新增）、`.github/workflows/publish-npm.yml`、`Makefile`、`httpserve/conformance_test.go`（新增）、`ts/conformance/server.ts`（新增）、`ts/src/index.ts`、`ts/test/exports.test.ts`（新增）、`AGENTS.md`、`docs/TESTING.md`、`docs/REDISDB-COMPAT.md`、`docs/02-http.md`、`docs/04-typescript.md` —— 已确认存在且覆盖。

## 验证记录

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| `make test-kvrocks` | 输出含 TestConformance* | `ok github.com/doptime/dopdb 2.566s` … 21 个 `--- PASS: TestConformance*`（HSetHGet、HSetNXSelfKey、…、WatchEventShape 等） | PASS |
| `go test -count=1 -run Conformance -v ./httpserve/` | 全部 PASS 且无 SKIP | 21/21 PASS，无 `--- SKIP` | PASS |
| `cd ts && node --import tsx --test test/exports.test.ts` | PASS | `# tests 3  # pass 3  # fail 0` | PASS |
| CI 真机（GitHub Actions services 容器） | 两 job 绿 | 本地无法验证 | SKIPPED |

## 验收标准逐条核对

1. ci.yml 在 PR 上两 job 绿 —— 未验证（需真实 GitHub Actions；推送后观察）。标 PARTIAL 的原因。
2. 服务器不可达时 CI 因 conformance skip 而失败 —— 逻辑达成（ci.yml "assert conformance actually ran" 步骤 grep SKIP 即 exit 1）；真机未验证。
3. `make test-kvrocks` 输出可见 `TestConformance*` —— 达成。
4. 文档不再有 "every command is covered" 无条件断言，未覆盖清单存在 —— 达成（docs/TESTING.md、REDISDB-COMPAT.md 已更新）。
5. exports.test.ts 通过，每个 CMD_BIT 命令可从包根导出 —— 达成。
6. `.httpOn(StrSet | StrGet)` 类型正确且求值非零 —— 达成（exports.test.ts 覆盖）。
7. 版本口径三处一致 —— 达成。

## 偏差

- 与 package-07 相同：`workrounds/.../files/` 镜像若入库会令 CI 的 `go vet ./...` 失败，本轮提交不含该镜像目录（应用后的文件在仓库根部，CI 编译对象是它们）。
- CI 用 `apache/kvrocks:latest`；本机以 redis 8.10.1 等价验证（dopdb 只用公共命令集）。

## 备注

发版（npm publish）按 round AGENTS.md §六不在本轮范围，须人工执行且标 breaking change。
