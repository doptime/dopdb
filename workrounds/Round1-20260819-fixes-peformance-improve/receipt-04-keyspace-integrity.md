# Receipt · package-04-keyspace-integrity

- **状态**: PASS
- **执行时间**: 2026-08-19T12:08+08:00
- **执行环境**: Go 1.24.5 darwin/arm64 / Node v20.20.2 / 服务器 redis 8.10.1 (port 6666)
- **应用的文件**: `string.go`、`list.go`、`set.go`、`zset.go`、`ts/src/server.ts`、`ts/src/kvrocks.ts` —— 已确认存在且覆盖。

## 验证记录

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| `go test -count=1 -run 'ReservedKeyNames' -v ./httpserve/` | PASS | `--- PASS: TestIntegrationReservedKeyNamesAreRefused (0.00s)` `ok github.com/doptime/dopdb/httpserve 0.266s` | PASS |
| `go test -count=1 -run 'Conformance' -v ./httpserve/`（含 reserved key 差分） | 全部 PASS | 21/21 `--- PASS: TestConformance*`，含 `TestConformanceErrorStatusParity` | PASS |
| `cd ts && node --import tsx --test test/server.test.ts` | PASS | `# tests 30  # pass 30  # fail 0` | PASS |

## 验收标准逐条核对

1. 条目命令对 `__owner`/`__events`/`__uniq:` 一律 400 —— 达成。
2. 攻击后集合正常 scoped 读写仍可用 —— 达成（用例内断言）。
3. 被拒的写不留 owner 声明 —— 达成。
4. 空键名被拒 —— 达成。
5. 双端 400/`validation` —— 达成（conformance 差分）。

## 偏差

真机 KVRocks 补跑（SET-on-hash 静默转换）未执行——本机只有 redis 8.10.1。开题报告第七节已把真机 KVRocks 验证列为本轮做不到的事，与之一致。

## 备注

无。
