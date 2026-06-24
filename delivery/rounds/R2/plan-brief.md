# 规划简报 · dopdb · R2(2026-06-24)

> 规划交换的下行件:只圈本回合范围,逼出你的 v0 理解,**不含定稿包**。读完请写 `delivery/rounds/R2/v0-plan.md`(只写不执行),云端对账后再下定稿 `brief.md` + `packet-*.md`。
>
> (本回合**不是**冷启动,按规矩走两段:先 v0 对账,后执行。别直接执行。)

## 0 本回合承重里程碑(一句话 + 为什么是它)

**V4:在真实 MongoDB 后端上,跑通 `httpserve` 的端到端全栈——JWT 验证 → `@`-绑定注入 → 权限白名单 → 数据命令 + `/api/<name>` → 落 Mongo 往返。** 为什么是它:R1 的 V3 只坐实了**数据层**(`mongostore` 契约);它上面那层安全与路由栈(JWT/`@`-注入/权限/命令派发/api 流水线)在**真 Mongo + BSON 往返**上**从未端到端跑过**。尤其 V3 明确**留了一个坑**:HTTP 入口走 **JSON 解码**、`mongostore` 走 **BSON 落盘**,`bson:"_id"` 与 `json:"_id"`、以及 `json:"@uid"` 这类字段在「JSON 进 → BSON 存 → 取回」全链路上是否字段对齐、`@uid` 是否正确从 JWT 注入并往返——这正是 V4 要抓的 facade。

## 1 范围与不在范围

**在范围**:
- **V4 🔴 承重**:新建一个 httpserve 端到端集成测试(`httptest.Server` + 真实 `mongostore` 后端),覆盖契约:
  1. **JWT**:无/坏 token 被拒;合法 token(`SignHS256`,带 `@uid` 等 claim)放行。
  2. **`@`-绑定**:key/field 里的 `@uid` 由**已验证 JWT claim** 注入;**客户端自带的 `@uid` 被先剥离**(防伪);落盘 `_id` == claim 值。
  3. **权限**:`command::collection` 白名单按 `auto_auth` 语义工作(dev 首用授予 / 未授拒为 403)。
  4. **数据命令 @ 真 Mongo**:`HSET`/`HGET`/`HGETALL`/`HDEL` 经 HTTP 往返,落 Mongo、取回一致。
  5. **`/api/<name>` @ 真 Mongo**:一个 Go 注册的 API 端点经 HTTP 跑通 api 流水线(可在 handler 内读写一个集合,证 api 层 + 数据层贯通)。
  6. **codec 字段映射**(V3 遗留):`bson` 与 `json` 同名标签 + `@uid` 字段在 JSON-进/BSON-存/取-回全链路字段对齐(`_id`、时间戳不丢不串)。
- **W1 🟢 并行轨**:在本地 **go1.24.5** 上复跑上轮加的 wasm/TS:`make wasm`(用 1.24 重建 `dopdb.wasm` 并同步刷新 `wasm_exec.js`)→ `make ts`(`npm install` + `tsc`)→ `node clients/ts/smoke-test.mjs`(SDK 端到端)。证 wasm 桥 + TS 客户端在 1.24 上工作,且 `wasm_exec.js` 与 `dopdb.wasm` 同版本匹配。

**不在范围**:H1–H4 硬化项(原子 scoped-write、scoped HKEYS/HLEN、msgpack-at-rest、权限持久化)——留后续回合;TS **数据命令**客户端移植(hashKey 等)——独立任务,不在 W1;任何对 L0 安全核心 / 测试 / 门槛的改动。

## 2 已知约束(L0/PRL、上游、🔐)

- **L0 冻结**(项目卡):sanitize 名单、`@`-注入顺序、owner-scope 隔离、JWT 拒 none、**全部现有测试**——谁也不许改。
- **PRL1–PRL4** 适用:`_id` 恒字符串、Find 必经净化、`@` 只来自 JWT、不放宽隔离/权限。**V4 尤其盯 PRL3**(`@uid` 只能来自 JWT,客户端伪造必须被剥离)。
- **上游**:R1 已封存——`mongostore` 数据层在真 Mongo 验过(V3 终态 A),驱动 `mongo-driver/v2 v2.7.0` 已进 go.mod。V4 直接站在其上。
- **🔐**:无放宽类拍板。Mongo URI 本地已具备(V3 用本地 Docker `mongodb://localhost:27017` 跑通),V4 复用 `DOPTIME_TEST_MONGO_URI`。

## 3 可预见的岔路(在 v0 里想好怎么分支,云端定稿收成决策表)

1. **没有测试 Mongo / 连不上**:V4 集成测试 `t.Skip` 报因 → 终态 B、suspend,**不阻塞 W1**。(本地有 Docker,预期不触发。)
2. **codec 字段映射对不上**(取回的 `_id`/`@uid`/时间戳字段名或值不符):这正是 V4 要抓的——**不改测试**,记 failed + 关键数字(哪个字段、期望 vs 实际)+ 异常发现,suspend。**可能根因在 store.go 键编解码或 mongostore BSON 落盘**,交云端裁,**别擅自改 L0**。
3. **`@uid` 注入/防伪失效**(客户端伪造的 `@uid` 落了盘,或 JWT 的没注入):**严重**(PRL3)——failed + 异常发现,suspend。
4. **权限未按预期拦截**(未授命令返回非 403 / 首用授予语义不符):记关键数字 + 异常,按决策表判 failed/suspend。
5. **W1:本地无 node/npm 或 npm 装不上**:W1 该单元 blocked/suspend(终态 B),**不阻塞 V4**。
6. **W1:1.24 重建后 smoke-test 失败**(wasm_exec.js↔wasm 版本不匹配,或 1.24 行为差异):记失败现象 + go 版本,suspend 交云端(可能要云端按 1.24 调 wasm 桥)。

## 4 并行轨候选(主线卡住时顶上)

**W1**(wasm/TS,本身就是并行轨,不依赖 Mongo)。若 V4 卡在 codec/`@`-注入需云端裁,转去把 W1 做完封掉。W1 与 V4 互不依赖,可并跑。

(如本回合 docs 有随动,附一条极薄 D2 文档核对;无则免。)

## 5 给本地的话

请写 `delivery/rounds/R2/v0-plan.md`——逐拟议包列 `目标 / 拟建什么 / 打算怎么自证`,**只写不执行**。两条信号最高:

- **V4 怎么客观自证**:你打算用什么判据证明「真 Mongo 上 HTTP 全栈正确」?写实——理想是:每步 HTTP 状态码、取回的 `_id` 值 == JWT claim、客户端伪造 `@uid` 被剥离(yes/no)、未授命令得 403(yes/no)、`/api/<name>` 返回正确(yes/no)、Mongo 里实际落了什么字段。想得到客观断言就写断言;只想到「看着对」就如实写——那正提示这条该由云端给你写死硬判据 + 内嵌测试代码(像 V3 那样)。
- **codec 字段映射**:V3 把它留给了 V4。你打算怎么验「JSON 进 / BSON 存 / 取回」字段不丢不串?这条最容易出 facade,想清楚验法。

W1 相对直接(跑三条命令、贴退出码 + `ALL TS SDK INTEGRATION TESTS PASSED`),v0 里简述即可,重点写清「本地 go 版本」「node/npm 是否就绪」两个前置。
