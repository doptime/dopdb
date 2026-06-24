# 执行简报 · dopdb · R3(2026-06-24)· 段②执行交换

> 规划已定稿(reconcile.md)。本回合为**最后收尾回合**,4 包一次执行。承重轨 H1H2 的框架实现已在本交付内(6 个文件),套用即可。

## 包清单
- **P-H1H2-scoped-hardening**(🔴 承重):套用 6 个框架文件 + 逐字新建 `httpserve/scoped_integration_test.go` + 真 Mongo 跑。
- **P-T1-ts-data-client**(🟢):按规格实现 `collection(name)` + mock 测试。
- **P-H4-perm-persist**(🟡):按规格实现 `SaveJSON/LoadJSON` + 单测。
- **P-D2-docs-runbook**(🟢):文档收尾 + 路径核对。

## 本交付内的框架文件(P-H1H2 套用,勿手改)
`store.go`、`memstore/memstore.go`、`mongostore/mongostore.go`、`dopdb.go`、`http_accessor.go`、`httpserve/serve.go` —— 覆盖仓库根即生效。**不含 `go.mod/go.sum`**(驱动你已装,云端不动)。

## 执行顺序
H1H2(承重,需 Mongo)与 T1/H4/D2(均不需 Mongo)并行。建议先跑 H1H2 的 `go build ./...` 确认框架编译过,再铺开三条并行轨。

## 红线(全适用)
- **RL2**:不改测试/门槛/语义凑过;旧测试挂 = 停 + suspend。
- **RL5/RL6**:exit-0 ≠ done;flaky 不算 pass;诚实回执。
- **PRL1–4**:scoped 读写永不跨主泄漏(H1H2 尤其)。
- **L0 冻结**:sanitize/@-注入/JWT-no-none/全部旧测试不许动。
- 任何越出各包「可写区」的改动 → 记 `delivery/rounds/R3/oob.md`。
- **STATUS.md 云端独写**:套用本交付时把它原样一并提交,勿手改、勿漂着。

## 终态
- 全包 done → 回传回执,云端三层审计 → 封 R3 → 项目收尾(无 R4)。
- H1H2 无 Mongo → 该包终态 B suspend(不阻塞 T1/H4/D2);其余包照常 done。

## 回执
每包一份 `receipt-P-*.md`(done|suspend|failed|blocked + 关键数字 + 异常发现);进度汇总 `progress.md`。
