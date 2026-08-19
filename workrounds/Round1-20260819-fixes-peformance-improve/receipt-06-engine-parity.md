# Receipt · package-06-engine-parity

- **状态**: PASS
- **执行时间**: 2026-08-19T12:09+08:00
- **执行环境**: Go 1.24.5 darwin/arm64 / Node v20.20.2 / 服务器 redis 8.10.1 (port 6666)
- **应用的文件**: `http_accessor.go`（新增）、`httpserve/serve.go`、`ts/src/kvrocks.ts`、`ts/src/server.ts`、`ts/src/client.ts`、`ts/src/errors.ts`、`docs/04-typescript.md` —— 已确认存在且覆盖。

## 验证记录

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| `go test -count=1 -run 'Conformance' -v ./httpserve/` | 全部 PASS | `--- PASS: TestConformanceHashReadShapes` `--- PASS: TestConformanceWatchEventShape (1.10s)` `--- PASS: TestConformanceErrorStatusParity` `--- PASS: TestConformanceHashCommandOnNonHashCollection` 等 21/21 | PASS |

## 验收标准逐条核对

1. `hgetall` 双端 `{id: doc}`；`hvals` 双端数组 —— 达成（HashReadShapes）。
2. 首写事件 `insert` 双端一致 —— 达成（WatchEventShape）。
3. `hdel` 只广播实际删除的 id —— 达成。
4. watch 事件 `key` 等于文档 key —— 达成。
5. 未知集合双端 403 `forbidden` —— 达成（ErrorStatusParity）。
6. 413 code 双端 `payload_too_large`，客户端可还原 `PayloadTooLargeError` —— 达成（errors.ts 映射表 + 413 状态映射）。
7. 非 Hash 集合 H* 命令：Go 404、TS 空结果，有测试钉住 —— 达成（HashCommandOnNonHashCollection 分别断言两端契约）。

## 偏差

无。

## 备注

Go `hgetall` 数组→对象是破坏性变更，发版时必须写 CHANGELOG。
