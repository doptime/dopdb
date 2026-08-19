# package-09 · 可验证性：CI、覆盖如实、权限门、发版

> 严重度 🟠。对应审计 S2-23、S2-24、S2-25、S2-26、S2-40、S2-41、S2-42。
>
> **本包不修任何业务 bug，它修的是"为什么这些 bug 没人发现"。**

## 9.1 仓库零 CI（S2-24）

`.github/workflows/` 只有 `publish-npm.yml`，而它的 `npm test` 在**没有服务器**的环境
里运行 —— 所有依赖服务器的测试全部 skip。conformance 同样 skip。

结论：**任何破坏双端一致性的提交在发布流程中零拦截；线上 npm 包由"从未跑过一条服务器
测试"的流水线发布。**

**修复**：新增 `.github/workflows/ci.yml`：

- 起 `apache/kvrocks` 服务容器（带 healthcheck）
- Go job：`gofmt` 门禁 + `go vet` + `go test ./...`（含 conformance）
  - conformance job 需要 node + TS 依赖，因为它会 spawn TS 子进程
- TS job：`typecheck` + `test` + `build`
- **conformance 被 skip 就让构建失败** —— 绿灯跑在空气上正是本包要防的事：

```yaml
if grep -q -- "--- SKIP" /tmp/conf.log; then
  echo "conformance skipped — the server was not reachable"; exit 1
fi
```

publish 工作流也补上同样的服务容器，让发版前的测试是真的。

## 9.2 覆盖被系统性夸大（S2-25）

实测 conformance 只有 **16 个** `TestConformance*`，而四处文档写着
"Every command is covered"（`AGENTS.md`、`docs/REDISDB-COMPAT.md`、`docs/02-http.md`、
`docs/TESTING.md`）。

没覆盖的包括：watch、hgetall/hkeys/hvals/hlen/hmget/hmset、count、findone、del、
hincrby 族、TTL、`?ds=`、401/403/409/413 差分。

**S2-8（hgetall 形状分裂）和 S2-9（非 Hash 集合 panic）恰好都落在没覆盖的命令上。
这不是巧合。**

**修复**：
- 新增 5 组差分用例：`HashReadShapes`、`HashCommandOnNonHashCollection`、
  `MethodEnforcement`、`ErrorStatusParity`、`WatchEventShape`
- 补齐 conformance 的授权清单（原先漏了 HGETALL/HVALS/HMGET/COUNT/FINDONE/WATCH…）
- **文档改成如实列出覆盖与未覆盖**：

> 未覆盖：`watch` 事件流的完整语义、TTL 过期、`?ds=` 选择、409/413 差分。
> 这些被点名而不是被暗示已覆盖——"every command is covered" 出现在四份文档里而
> 套件只有十六个用例，**过度声称的覆盖比诚实的缺口更危险，因为它让人停止查看**。

- `Makefile` 的 `test-kvrocks` 从 `-run Integration` 改为 `-run 'Integration|Conformance'`
  —— 之前它**永远不含** conformance，而人们拿它的绿灯当一致性已验证。

## 9.3 conformance 绕过权限门（S2-23）

`ts/conformance/server.ts` 用 `permit: () => true`。于是 TS 引擎允许一切，
**Go 引擎的每一个 403 都变成不可比的差异** —— 权限门是这套"一致性"套件唯一永远比不到
的东西。

**修复**：删掉全开 permit，每个集合声明自己的 `httpOn(All)` bitmask，与 Go 侧的
grant 对应。

**改完当场抓到一条**：未知集合 Go 403 / TS 404（见 package-06）。这条审计报告没有
——它只可能被"真的比较权限门"的测试发现。

## 9.4 npm dist-tag 恒为 alpha（S2-26）

```yaml
TAG="${{ inputs.npm_tag || 'alpha' }}"
```

`inputs.*` 只在 `workflow_dispatch` 有值，`release:` 事件为空 → **一律 alpha**
（与紧邻的注释自相矛盾）。走 GitHub Release 发布，新包只挂 alpha，
`npm i @kequnyang/dopdb` 继续装旧版。

**修复**：`inputs.npm_tag` 为空时**按版本号推导** —— prerelease 走对应标签，
其余走 `latest`，并在日志中回显推导结果。

## 9.5 权限位未导出（S2-40）

`ts/src/index.ts` 只导出 HGet…HRandField 与 ReadOnly/Writes/All/CMD_BIT。
String/List/Set/ZSet 的命令位在 `schema.ts` 里定义了但**没从入口导出**，而
`AGENTS.md §5.1` 明示它们可从包根导入。

用户按文档写 `.httpOn(StrSet | StrGet)` → 编译成 `undefined | undefined` → 只能退回
`All`（过宽，**正好与最小权限相反**）。

**修复**：补齐全部 Str*/L*/S*/Z*/SQL 位。新增 `ts/test/exports.test.ts`，
**把导出表钉死在命令表上**：

- `CMD_BIT` 里的每个命令都必须能从包根导出
- 所有位互不相同且非零
- `ReadOnly` **不含任何写命令位**

## 9.6 文档漂移（S2-41、S2-42）

- `docs/04-typescript.md` 残留 Mongo 时代说法：watch "needs a replica set"、
  "resumes via `Last-Event-ID`"、示例用 `ev.key` 而 Go 实际发 `id`
  （最后一条在 package-06 里被修成了真 bug）
- `AGENTS.md` 写 Go 1.24+，`go.mod` 是 1.22，RUNBOOK 是 ≥1.22 —— 三处口径不一

**修复**：watch 一节按实况重写（无副本集、无回放、不发 `id:`、忽略
`Last-Event-ID`、只见经 dopdb 的写入）；Go 版本统一到 `go.mod` 的 1.22。

## 涉及文件

| 文件 | 类型 |
|---|---|
| `.github/workflows/ci.yml` | **新增** |
| `.github/workflows/publish-npm.yml` | 修改（服务容器、dist-tag 推导） |
| `Makefile` | 修改（test-kvrocks 含 conformance、新增 bench） |
| `httpserve/conformance_test.go` | 修改（5 组新用例 + 授权清单） |
| `ts/conformance/server.ts` | 修改（真实权限门） |
| `ts/src/index.ts` | 修改（补齐命令位导出） |
| `ts/test/exports.test.ts` | **新增** |
| `AGENTS.md` `docs/TESTING.md` `docs/REDISDB-COMPAT.md` `docs/02-http.md` `docs/04-typescript.md` | 修改（覆盖如实、版本对齐、watch 实况） |

## 验证

```bash
make test-kvrocks                    # 现在包含 conformance
go test -count=1 -run Conformance -v ./httpserve/
cd ts && node --import tsx --test test/exports.test.ts
```

CI 需要在真实 GitHub Actions 上验证一次（本地无法验证 services 容器）。

## 验收标准

1. `ci.yml` 在 PR 上运行，Go 与 TS 两个 job 都绿。
2. **人为让服务器不可达时，CI 因 conformance skip 而失败**（不是绿灯通过）。
3. `make test-kvrocks` 的输出里能看到 `TestConformance*`。
4. 文档中不再出现 "every command is covered" 一类无条件断言；未覆盖清单存在且准确。
5. `ts/test/exports.test.ts` 通过 —— 每个 `CMD_BIT` 命令都能从包根导出。
6. `.httpOn(StrSet | StrGet)` 在 TS 中类型正确且求值非零。
7. 版本口径三处一致。

## 风险与影响

- CI 会显著拉长 PR 反馈时间（需拉容器镜像）。这是必要成本。
- `apache/kvrocks:latest` 可能随上游变化。若出现不稳定，可钉到具体 tag，或换用
  `redis:7`（dopdb 只用公共命令集）。
- **发版仍需人工**：dist-tag 逻辑已修，但线上 `latest` 仍指向 Mongo 版，
  值格式（BSON vs CBOR）与 Perm 位图（新增 SQL 位）都不兼容。
  **老客户端连新服务端会坏，发版必须标 breaking change。**
