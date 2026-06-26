# STATUS · dopdb 项目总台账(运营层 GLM 每回合刷新)

> 四方想知道「项目到哪了」只读这一个文件;深究证据再按指针进对应回合目录。本文件由**运营层 GLM 独写**(承重回合的 `SEALED.md` 终判由意图层 Opus 联签),执行层 Qwen 与人只读。

更新于:2026-06-26 · 重组节点(非常规回合)· 由 Opus/意图层落定,GLM 接管后续刷新

## 0 · 本次重组发生了什么(读这一段就懂当前态)

1. **架构已重写**:dopdb 现为 **Go 与 TS 两套对等全实现**(同一 wire 协议)、直连 Mongo(无 Store 抽象)、@-绑定、`/api/<cmd>/<coll>?ds=` 多源、默认拒权限、18 命令。旧的 Store/memstore/mongostore/wasm/AutoAuth 架构**作废**。
2. **旧回合 R1–R3 已归档删除**:其方法论与历史(裁决/关键数字/能力画像)高密度留存于 `delivery/HISTORY.md`;R1–R3 验的是**旧架构**,结论不再适用新代码。`rounds/` 现为空(仅 `.gitkeep`)。
3. **工作流升级到 v3.1 四方**:云端一分为二——**意图层 Opus**(人工上传/契约/承重终判)+ **运营层 GLM**(本地自动/审计封包对账出包);执行层 Qwen + 人。回合走**显式 6 步**(见 `README.md` / `kit/00-protocol.md` §1.1)。`kit/` 三件、`ROLES.md`、`README.md` 均已改写到位。
4. **项目卡刷新到 v2.1**:含**全量意图清单 60 条**(I-T/P/D/W/S/WA/TS/C/Q)+ 里程碑链 + 决策表 + PRL1–6 + §13 消融发现 + §13.1 修复后复审。
5. **本次做了一轮代码消融 + 直接修复**(详见 §代码修复状态)。

## 1 · 里程碑链(本项目用 M 编号;阶段可跨回合)

```
M0 工作流重组        ▣ 进行中(delivery 改四方 + 代码消融修复;本节点)
M1 Go build 坐实     ·  承重 · 待本机 go build/vet/gofmt(沙箱无 Go 工具链)
M2 真 Mongo 集成     ·  承重 · 依赖 M1;DOPDB_TEST_MONGO_URI 下 go+ts 集成全过
M3 watch E2E         ·  依赖 M2(change stream→SSE 往返)
M4 Next.js App-Router·  依赖 M2(createNextHandler 端到端)
M5 Go↔TS 一致性      ·  承重 · 依赖 M2(conformance:同请求两端语义/状态码/code/方法一致)
并行 🟢 轨           ·  文档/示例/清理 随主线跑

▣=进行中  ✓=封存验过  ·=未排
```
**承重门(=人介入点)= M1、M2、M5**:每个需 Opus 终审过才允许往上建。M1/M2 还需人提供本机 Go 工具链与真 Mongo。

## 2 · 阶段表

| 里程碑 | 状态 | 证据/判据指针 |
|---|---|---|
| M0 工作流重组 + 代码消融修复 | ▣ 进行中 | 本文件 §代码修复状态;`delivery/*` 已改;项目卡 v2.1 §13/§13.1 |
| M1 Go build 坐实(承重) | · 待排 | 项目卡 §验收命令(make build/vet/fmt-check);**沙箱无 Go,必本机** |
| M2 真 Mongo 集成(承重) | · 待排 | 项目卡 M2 硬判据(CRUD/原子/净化/unique + HTTP@@-绑定+owner 隔离 + 敌意 owner filter 空 + ownerField 未绑定启动报错 + TS 同测) |
| M3 watch E2E | · 待排 | change stream→SSE 往返 + Last-Event-ID 续传 |
| M4 Next.js App-Router E2E | · 待排 | createNextHandler 催化的最小示例 app(I-Q7 待建) |
| M5 Go↔TS 一致性(承重) | · 待排 | conformance:scope-merge(AND)/错误 5 类/GET-POST/含 sort-projection 两端 diff 为空(含 F13) |

## 3 · 回合台账

| 回合 | 段 | 范围 | 状态 |
|---|---|---|---|
| —  | — | 尚无活动回合;下一回合由 GLM 在 M1 起开(第 2 步开回合) | 待开 |

(R1–R3 已归档至 `HISTORY.md`,不再列此。)

## 4 · 代码修复状态(2026-06-26 消融 → 直接修复)

本轮对意图/代码做消融,发现 1 个真实安全缺陷(F1,TS)+ 多处加固/契约缺口,已直接修复:

| 项 | 修复 | 验证 |
|---|---|---|
| F1 owner-scope 被用户 filter 覆盖(TS) | `find/count/findone` 改 `mergeScope`(`$and`,scope 终胜),对齐 Go | **TS tsc + 64 测试绿**;端到端 M2 坐实 |
| F2 owner 未绑定静默失效(TS) | `buildRuntime` 连库前 fail-closed 校验 | **新增 `ts/test/hardening.test.ts`,64/64**;Go 不适用 |
| F3 find limit 无上界 | TS 默认 100/上限 1000;Go `FIND` 同值 clamp | TS 绿;**Go 未编译验证(M1)** |
| F4 body 无上限 | TS readBody+content-length→413;Go `MaxBytesReader`+parse 413 | TS 绿;**Go 未编译验证(M1)** |
| F5 sort/projection 不净化(TS) | TS `checkSortProj` 拒 `$`/非法路径/非标量 | TS tsc 绿;行为 M2 验 |
| F13 sort/projection 线协议偏差 | TS 暴露 `s=`/`p=`;**Go FIND 未解析** → Go 欠实现 I-W1 | **M2 给 Go 补 s/p 解析+校验;M5 验一致** |
| F10 hsetnx 跨租户存在性泄漏 | 维持现状 | M2 评估(非阻塞) |

**TS 侧**:`ts/src/server.ts`(+`errors` 导入 `ValidationError`)、新增 `ts/test/hardening.test.ts`。**已验证**(tsc 严格 + 64/64)。
**Go 侧**:`httpserve/serve.go`(常量 + ServeHTTP `MaxBytesReader` + FIND clamp)、`httpserve/context.go`(body 读错→413)。**沙箱无 Go 工具链,未编译验证**——列 M1 硬判据(`go build`/`vet`/`gofmt`)。

## 5 · 需要人拍板 / 需要人提供的(🔐 与承重门)

- **M1(承重门)**:沙箱**无 Go 工具链**,无法编译/vet/测 Go;需人在本机跑 `make build/vet/fmt-check`(或等价),把结果回带,Opus 终审。这是下一个人介入点。
- **M2(承重门)**:需人提供真 Mongo(`DOPDB_TEST_MONGO_URI`),跑 Go+TS 集成。
- 目前无"放宽 L0/方向性变更"类 🔐 待决。

## 6 · 能力画像(执行层 Qwen)

- 尚无新架构下的回合数据;沿用迁移前印象仅作参考(详见 `HISTORY.md`:旧 harness 执行力/纪律/汇报均高)。新回合从 M1 起按实际回执重建画像。
