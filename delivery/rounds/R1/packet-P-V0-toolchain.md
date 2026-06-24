# 包: P-V0-toolchain · 回合 R1(2026-06-23 · 真实 MongoDB 首次验证)

分级: 🟢 可客观自证(go version 退出 0 + go.sum 含 mongo 驱动 + delivery 已提交)
上游: 无
回执写到: `delivery/rounds/R1/receipt-P-V0-toolchain.md`(模板见 delivery/kit/00-protocol.md §3)

全景: 你在 STATUS.md 甘特的 V0;承重主线的起点;并行轨是 V2、D1。

任务一句话: 体检 Go 工具链,拉取 MongoDB 驱动锁进 go.sum,把 delivery/ 落库提交。

## 1 背景 · 现在是什么情况

仓库是 dopdb 框架(`go.mod` module `github.com/doptime/dopdb`,go 1.22)。`mongostore` 包 import `go.mongodb.org/mongo-driver/v2`,但 go.mod **尚未声明该依赖**——作者沙箱网络到不了驱动域名,没拉过。本包负责把驱动拉进来。

探针(本次上传实测):`grep mongo go.mod` 预期**无输出**(还没加依赖)。

## 2 意图 · 为什么做、什么算好

后续 V1 构建 mongostore 需要驱动在 go.mod/go.sum 里。完成 = Go 工具链可用 + 驱动依赖锁定 + delivery 提交进 git(让云端能审痕迹)。

红线: RL1–RL8 全部适用(见 delivery/kit/00-protocol.md §4)。本包追加: 无。
修改令: 允许修改 `go.mod`/`go.sum` 添加 mongo 驱动(项目卡预授权)。

## 3 任务 · 具体做什么

### 单元 1 · 工具链体检

```bash
go version                 # 期望 go1.22+ ;留痕
go env GOPROXY GOFLAGS     # 记录代理设置
```

### 单元 2 · 拉 mongo 驱动

```bash
go get go.mongodb.org/mongo-driver/v2@latest
go mod tidy
grep mongo go.mod          # 期望出现 go.mongodb.org/mongo-driver/v2
```

驱动 v2 需要的传递依赖(golang.org/x/* 等)由 `go mod tidy` 一并解决。网络不通见决策表。

### 单元 3 · delivery 落库

```bash
git add delivery/ go.mod go.sum
git commit -m "R1: fetch mongo-driver/v2, commit delivery (P-V0)"
```

铁顺序:先确认驱动进了 go.sum(产物非空)→ 再提交 → 最后记一行进度。

## 4 验收 · 怎么算完成(harness 复跑,云端三层审计再复核)

- [ ] `go version` 退出 0,版本 ≥ go1.22
- [ ] `grep "go.mongodb.org/mongo-driver/v2" go.sum` 有命中
- [ ] `git log --oneline -1` 显示本包提交
- [ ] 进度账落 `delivery/rounds/R1/progress.md`

## 5 边界 · 不要做什么

可写:`go.mod`、`go.sum`、`delivery/rounds/R1/`。
禁改:任何 `*.go`(本包不碰代码)、L0 冻结件。越界发现登记 `delivery/rounds/R1/oob.md`,不深做。

## 6 预算与换法

单元 2 最多 3 次:第 1 次 `go get @latest`;第 2 次指定一个明确的 v2 版本(如 `@v2.0.0`)再 tidy;第 3 次仍网络失败 → 该单元 blocked,记清错误一行,转 V2/D1。整包 ~5 分钟。

## 7 收尾

按协议 §3 写回执;「异常发现」必写清单:① `go version` < 1.22;② 驱动拉取报代理/校验错误;③ `go mod tidy` 改动了预期外的依赖;④ go.sum 出现疑似被篡改的哈希。

## 8 调和补注(R1 reconcile · 2026-06-23)

本地实测 Go **1.24.5**(≥1.22,满足)。在 1.24 上 `go mod tidy` 可能**上抬 `go` 指令(如 `go 1.22`→匹配驱动最低版本)或追加 `toolchain go1.24.5` 行**。这**属预期**:本包本就改 `go.mod`/`go.sum`(引入驱动),`go` 指令/`toolchain` 的随动**不算违反冻结、不必记成异常**(上面收尾清单第 ③ 项指的是"依赖项被意外改动",不含这类版本指令随动)。其余不变。
