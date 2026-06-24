# 回执 · P-D2-docs-runbook(R3 · 🟢 并行 · 独立)

状态:**done**

## 改了什么 docs

| 文件 | 改动 |
|---|---|
| `docs/01-data.md` | 方法表加 `HSetScoped`(原子 scoped upsert) + `HKeysScoped/HLenScoped`(不泄漏 scoped 键/计数) |
| `docs/02-http.md` | scoped 写语义:check-then-act → 原子 `PutScoped`(dup-key→403);HKEYS/HLEN 由 403 改为 scoped 版本;加权限持久化(§`SaveJSON/LoadJSON`);删旧 caveat |
| `docs/04-wasm-ts.md` | 新增 §3d 数据命令客户端:`collection(name)` 方法表 + 示例 |
| `docs/RUNBOOK.md` | §未竟:H1/H2/H4 标 ✓ 完成(H3 标 ✗ 已砍) |

## 路径核对

- `PutScoped`/`HSetScoped`/`HttpKeysScoped`/`HttpLenScoped`/`SaveJSON`/`LoadJSON`/`collection` 均在仓库对应文件存在
- MISS 数: **0**

## gofmt/vet

- `gofmt -l .` 空输出
- `go vet ./...` 无错误

## git diff 仅 docs/+ delivery

D2 本轨仅改 `docs/**`。`delivery/rounds/R3/` 为回执文件,属正常产出。

## 异常发现

无。
