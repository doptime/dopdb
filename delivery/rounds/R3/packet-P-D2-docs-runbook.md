# 包 · P-D2-docs-runbook(R3 · 🟢 并行 · 独立)

> 段:执行交换。文档收尾,反映 R3 成果。本地实现 + 路径核对自证(同 R1 的 D1 套路)。

## 1 目标
docs/ 与 RUNBOOK 反映:H1(原子 scoped 写)、H2(scoped HKEYS/HLEN 可用)、H4(权限持久化)、T1(TS 数据命令客户端);并标注 H3(msgpack-at-rest)**已砍**。

## 2 规格
1. 在相应 docs(如 `docs/02-*` 数据命令篇 / `docs/03-*` 权限与 scoped 篇,按现有编号就近)补:
   - **scoped 写语义**:scoped 集合的 per-key 写是**原子**的;跨主 id 返回 403(`ErrForbidden`),不存在 check-then-act 窗口。
   - **scoped HKEYS/HLEN**:scoped 集合现返回**调用者本人**的键/计数(此前为 403)。
   - **权限持久化**:`Permissions.SaveJSON/LoadJSON` 文件式落盘/恢复用法 + AutoAuth 注意。
2. `docs/04-wasm-ts.md` 补 **T1 数据命令客户端**:`collection(name)` 的方法表 + 一段示例(`const orders = collection("Order"); await orders.hset("o1", {...}); await orders.hkeys();`)。
3. `docs/RUNBOOK.md` 的「§未竟 / 硬化」:H1/H2/H4 标 ✓ 完成(指向 R3),H3 标「已砍——价值最低,JSON+BSON 两 codec 已足」。
4. 核对所有命令/路径/类型名与仓库现状一致(尤其新方法名 `PutScoped`/`HSetScoped`/`HttpKeysScoped`/`collection`)。

## 3 自证(🟢)
- 路径/命令核对脚本(同 D1):列出 docs 中出现的文件路径与符号,核对仓库存在,**无 MISS**。
- `gofmt -l .` 与 `go vet ./...`(非 Mongo 包)干净(文档轨不应碰代码,若动了任何 .go → 记 oob)。
- `git diff --name-only` 仅 `docs/`(+ `delivery/rounds/R3/`)。
留痕 `delivery/rounds/R3/d2_check.txt`。

## 4 红线 / 岔路
- **纯文档轨**:不许改任何 .go / .ts(那是 H1/H2/T1/H4 的事)。若发现文档与代码不符,以**代码为准**改文档,并在回执「异常发现」记下不符点。
- 可写区:`docs/**`、`delivery/rounds/R3/`。

## 5 回执
`receipt-P-D2-docs-runbook.md`:状态、改了哪些 docs、路径核对 MISS 数(应 0)、git diff 是否仅 docs、异常发现。
