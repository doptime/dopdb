# 回合简报 · dopdb · R1(2026-06-23 · 真实 MongoDB 首次验证)

> 执行交换定稿件。任务全景权威版在 `../../STATUS.md`,本简报只放切片与裁决叙事。

## 0 一句话

把只在内存 store 验过的 dopdb,在真实 MongoDB 上坐实——构建 `mongostore`、跑通契约,同时维持无驱动那批测试全绿。

## 1 事实快照(云端实测,带探针)

- 模块 `github.com/doptime/dopdb`,go 1.22(探针:`head -3 go.mod`)。
- 无驱动那批测试在云端沙箱全过(探针:`go test . ./api ./httpserve ./config ./memstore` → `ok` × 4,memstore 无测试文件):
  - 数据核心 10、api 7、httpserve 11、config 6 = **34 测试**。
- `gofmt -l .` 空输出;`go vet` 在无驱动包上干净。
- **`mongostore` 未编译**:沙箱 egress 到不了 `go.mongodb.org`/`golang.org/x`/`proxy.golang.org`(探针:`go build ./mongostore` → 拉包失败)。这是本回合头号坐实项。

## 2 上回裁决

冷启动,无上回。

## 2.5 v0 对账纪要

本项目冷启动,云端直发定稿包(未走 v0)。**后续回合一律两段式**:先 `plan-brief` → 你写 `v0-plan` → 云端对账 → 定稿。本轮若你在执行前发现与简报有分歧,先回传 v0。

## 3 复跑声明(云端沙箱复跑了什么、没覆盖什么)

- **已复跑**:无驱动 34 测试、`go vet`(无驱动包)、`gofmt -l`。
- **未覆盖(本回合靠你)**:`mongostore` 的编译与运行、任何对真实 MongoDB 的读写、BSON codec 下的字段名行为(memstore 用 JSON codec,无法建模 bson tag ≠ json tag 的分歧——V4 会专门验,本轮 V3 先把适配器契约坐实)。

## 4 本回合目标与终态(A / B,两者皆 done)

- **A**:V0–V2 全绿;V3 集成测试在真 Mongo 上断言全过。
- **B(诚实负结果)**:本地无测试 Mongo → V3 集成测试 skip 并报因,记 suspend;V0–V2 + D1 全绿。B 走完即 done,不许把 skip 粉饰成 pass。

## 5 本回合包清单(每包标 🟢/🔴;全景看 ../../STATUS.md)

| 包 | 分级 | 上游 | 一句话 |
|---|---|---|---|
| P-V0-toolchain | 🟢 | 无 | Go 体检 + 拉 mongo 驱动 + 锁 go.sum + delivery 落库 |
| P-V1-build | 🟢 | V0 | `go build ./...`(含 mongostore)+ vet + gofmt |
| P-V2-unit | 🟢 | 无(与 V0/V1 并行,不依赖驱动) | 无驱动 34 测试 + config.Load 冒烟 |
| **P-V3-mongostore-contract** | 🔴 承重 | V1(驱动编译过) | 新建集成测试,对真实 Mongo 跑通契约 |
| P-D1-docs-check | 🟢 并行轨 | 无 | 核对 docs 命令/路径与仓库一致,只修文档 |

依赖序:V0→V1→V3 是承重主线;V2、D1 是不被阻塞的并行轨(没 Mongo / driver 卡住时转 V2、D1,不空转)。

## 6 需要人拍板(🔐)

- 无放宽类拍板。
- 请人提供 `DOPTIME_TEST_MONGO_URI`(隔离/一次性库);没有则 V3 走终态 B。

## 7 预算与停机现况

每包小预算见各包 §6。整轮:🟢 包放手成批做;V3 过不了硬判据就 suspend,**不在未验的 V3 上接着建 V4**。停机三件套:待办空 / 预算尽 / 完成数连两轮不动。

## 8 harness 能力画像(三行)

本台 harness 首次上岗,无历史。回执回来后据「执行力 / 纪律性 / 汇报质量」建画像。在此之前,V3 的硬判据写得尽量死,单元切得细。
