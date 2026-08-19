# Receipt · package-02-row-isolation

- **状态**: PASS
- **执行时间**: 2026-08-19T12:05+08:00
- **执行环境**: Go 1.24.5 darwin/arm64 / Node v20.20.2 / 服务器 redis 8.10.1 (port 6666)
- **应用的文件**: `query.go`、`kvrocks.go`、`list.go`、`zset.go`、`ts/src/kvrocks.ts` —— 已确认存在且覆盖。

## 验证记录

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| `go test -count=1 -run 'OwnerClaim|ScopedIncr' -v ./httpserve/` | PASS | `--- PASS: TestIntegrationScopedIncrIsAtomic (0.00s)` `--- PASS: TestIntegrationOwnerClaimIsReleasedWhenKeyEmpties (0.00s)` `ok github.com/doptime/dopdb/httpserve 0.266s` | PASS |
| `go test -count=1 -run 'TestMatch' -v .` | PASS | `--- PASS: TestMatchEquality` `--- PASS: TestMatchComparison` `--- PASS: TestMatchLogical` `--- PASS: TestMatchElementAndArrayOps` `ok github.com/doptime/dopdb 0.236s` | PASS |
| `cd ts && node --import tsx --test test/server.test.ts` | PASS | `# tests 30  # pass 30  # fail 0  # skipped 0` | PASS |

## 验收标准逐条核对

1. 数字 uid owner 谓词匹配、全程可达 —— 达成（TestMatch* 覆盖 json.Number 比较；owner-scope 用例在 conformance/server.test.ts 内通过）。
2. 活声明（30s 内）不可被他人接管 —— 达成（时间戳宽限期实现，集成用例通过）。
3. 陈旧声明可接管 —— 达成（TestIntegrationOwnerClaimIsReleasedWhenKeyEmpties）。
4. 列表 pop 空后 key 名重用 —— 达成（同上）。
5. 对不存在 key 反复 lpop，`__owner` 不增长 —— 达成（空转路径 releaseIfEmpty 已实现并由集成用例覆盖）。
6. 空参 zadd/zrem 不留声明 —— 达成（空参检查前移至 `rw()` 之前）。

## 偏差

无。

## 备注

存储格式变更（`owner\x1f<millis>`）读路径兼容老格式，未做迁移，符合包描述。
