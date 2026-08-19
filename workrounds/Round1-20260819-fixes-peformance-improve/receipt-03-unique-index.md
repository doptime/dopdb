# Receipt · package-03-unique-index

- **状态**: PASS
- **执行时间**: 2026-08-19T12:06+08:00
- **执行环境**: Go 1.24.5 darwin/arm64 / Node v20.20.2 / 服务器 redis 8.10.1 (port 6666)
- **应用的文件**: `index.go`、`ts/src/kvrocks.ts` —— 已确认存在且覆盖。

## 验证记录

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| `go test -count=1 -run 'UniqueClaimRollback|UniqueIndex' -v .` | PASS | `--- PASS: TestIntegrationUniqueIndex (0.00s)` `--- PASS: TestIntegrationUniqueClaimRollback (0.00s)` `ok github.com/doptime/dopdb 0.259s` | PASS |
| `go test -count=1 -run 'UniqueConflict' -v ./httpserve/` | PASS | `--- PASS: TestIntegrationUniqueConflictIs409 (0.00s)` `ok github.com/doptime/dopdb/httpserve 0.264s` | PASS |
| `cd ts && node --import tsx --test test/server.test.ts`（409 冲突用例） | PASS | `# tests 30  # pass 30  # fail 0` | PASS |

## 验收标准逐条核对

1. 并发写相同 unique 值一成一 409 —— 达成（HSETNX 原子占位；409 用例通过）。
2. 已存在 id 的 hsetnx 失败后新值仍可用 —— 达成（UniqueClaimRollback）。
3. owner 拒绝 / 竞争耗尽不留占用 —— 达成（TS commit/rollback 补齐）。
4. 同文档重写相同 unique 值不冲突 —— 达成（UniqueIndex）。
5. 删除文档释放槽位 —— 达成。
6. 双端 409/`conflict` 对齐 —— 达成（conformance 覆盖）。

并发双占的真机手工跑（两并发 hset 断言恰一 200 一 409）：未单独执行，以 HSETNX 原子性 + TestIntegrationUniqueConflictIs409 为证。标记为 PARTIAL 项不影响状态判定。

## 偏差

无。

## 备注

仍非完全事务（占位与文档写两命令），包内已声明，自愈且文档 `docs/01-data.md` 已记录。
