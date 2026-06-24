# 规划简报 · dopdb · R3(2026-06-24)· 收尾硬化(单回合吃掉全部余量)

> 规划交换下行件:只圈范围、逼出你的 v0 理解,**不含定稿包**。读完写 `delivery/rounds/R3/v0-plan.md`(只写不执行),云端对账后下定稿 `brief.md` + `packet-*.md`。
>
> **本回合按人指示设计为「最后一个收尾回合」**:一回合内打包全部剩余可做项,以减少 rounds 总数。故 v0 请**一次覆盖全部 5 条轨**。框架级承重改动(H1/H2)是安全语义改动,务必经此规划交换对齐设计再动手——这正是规划交换存在的意义。

## 0 本回合两类工作

- **承重主线(🔴):H1 + H2 = 收尾 owner-scoped 集合的安全语义**(在真 Mongo 上)。这是唯一承重轨,两件事同源(都改 Store 接口 + http_accessor + 两个 store + serve 派发)。
- **并行轨(独立,主线卡住时顶上)**:T1(TS 数据命令客户端,🟢)、H4(权限持久化,🟡)、D2(docs/RUNBOOK 收尾,🟢)。
- **明确不做**:H3 msgpack-at-rest——价值最低(已有 JSON@memstore + BSON@mongostore 两种 codec),除非人明示要,否则砍掉,别花力气。

## 1 各轨范围与云端设计意图

### 轨 A · H1 原子 scoped 写(🔴 承重)

**问题**:`http_accessor.go` 的 `HttpSetScoped` 是 **check-then-act**(先 `Find` 查归属、再 `HttpSet` 写)。两步之间归属可能被并发改动——存在 lost-update / 劫持窗口。文件里那条 `NOTE: ... a planned Store extension` 就是指这个。

**云端意图**:给 `Store` 接口加一个**原子过滤式 upsert** 原语,设计倾向:
- 签名 `PutScoped(ctx, coll, id string, doc []byte, ownerField, ownerVal string) error`。
- 语义 = 原子 `updateOne({_id:id, ownerField:ownerVal}, {$set: <doc 字段>, $setOnInsert:{_id:id, ownerField:ownerVal}}, upsert:true)`。
- 关键技巧:若 `_id=id` 的文档**已存在但 owner≠ownerVal** → 过滤器匹配空 → upsert 试图**插入** `_id=id` 的新文档 → `_id` 唯一 → **E11000 dup-key** → 映射为 `ErrForbidden`(原子地挡掉跨主劫持,无需 check-then-act)。
- `mongostore`:真 `UpdateOne` + dup-key 判定(`mongo.IsDuplicateKeyError` 或等价)→ `dopdb.ErrForbidden`。
- `memstore`:在其 mutex 下模拟同语义(查现存 `_id` 的 owner;存在且 owner≠caller → `ErrForbidden`;否则强制 owner 后写)。
- `HttpSetScoped` 改为调用它(经 `Collection.HSetScoped`,codec marshal 后转 `store.PutScoped`),**删掉 check-then-act**。
- **承重测试(真 Mongo)**:① 跨主劫持:u2 覆盖 u1 的 key → `ErrForbidden`(403);② 原子性:N 个并发**同主**写同 key → 无错、终值一致、无中间态丢失;③ 隔离仍守(u1 看不见/盖不掉 u2)。

**你在 v0 想清楚**:这个原子原语的判据怎么客观验?并发用例你打算怎么构造(goroutine 数、断言什么)?dup-key→ErrForbidden 在 v2.7.0 用哪个 API 判定?(云端会把测试 + 实现都写死给你逐字落地,像 V3/V4;但你的 v0 信号决定云端写得多细。)

### 轨 B · H2 scoped HKEYS/HLEN(🔴 承重·与 A 同轨)

**问题**:`serve.go` 现在对 row-scoped 集合的 `HKEYS`/`HLEN` 直接 **403**(安全默认:拒)。该补上「只返回**调用者本人**的键/计数」的 scoped 版本。

**云端意图**:`Collection` 已有 `Find(scope)`。故 `HttpKeysScoped(scope)` = `Find(scope, 只取 _id)` → 返回这些 `_id`;`HttpLenScoped(scope)` = 其计数。`serve.go` 把那两处 403 改为:scoped 集合走 scoped 变体、非 scoped 走原 HKEYS/HLEN。
- **不可泄漏**:u1 的 HKEYS 只能含 owner==u1 的 `_id`。
- **承重测试(真 Mongo)**:u1 建 o1/o2、u2 建 o3 → u1 HKEYS-Order = {o1,o2}(无 o3)、u2 = {o3};HLEN 对应。

**你在 v0 想清楚**:scoped HKEYS 的「不泄漏」怎么客观断言?(逐用户键集相等)。

> A+B 合成一个承重测试文件(真 Mongo),与 R2 的 `http_integration_test.go` 并存或并入,云端定稿时给逐字代码。

### 轨 T1 · TS 数据命令客户端(🟢 并行·独立)

**现状**:TS SDK(`clients/ts/src/client.ts`)只有 `createApi(name)` 打 `/api/<name>`。**数据命令**(`/HSET-<coll>?f=...`、`/HGET-...`、`/FIND-...` 等)还没有 TS 客户端。

**云端意图**:加一个数据命令客户端,如 `collection(name)` 返回 `{ hget(field), hset(field,value), hdel(field), find(filter), ... }`,按 `client.ts` 既有 fetch 模式(baseUrl + Bearer + JSON)拼 `/CMD-<coll>` 请求。**自证**(不依赖活后端):`tsc` 干净 + 一个 **mock-fetch 单测**断言请求成形正确(URL/method/body/headers),且现有 `smoke-test.mjs` 仍过。

**你在 v0 想清楚**:用 mock fetch 验请求成形,还是起一个临时 Go httpserve 做端到端?(云端倾向 mock fetch,自包含、🟢 可批量自证。)

### 轨 H4 · 权限持久化(🟡 并行)

**现状**:`permission.go` 的 `Permissions` 是内存 map,重启即丢。

**云端意图**:加 load/save——把授权集序列化(JSON),启动时载入。**载体待定**:文件 vs Store 集合——你在 v0 提倾向,云端定。**自证**:单测——授若干权 → save → 新 `Permissions` load → 断言授权恢复(文件式无需 Mongo)。

### 轨 D2 · docs/RUNBOOK 收尾(🟢 并行)

反映 H1(原子 scoped 写)、H2(scoped 键/计数now可用)、T1(TS 数据客户端)、H4(权限持久化);RUNBOOK §未竟 标 H1/H2/H4 完成、H3 砍掉。**自证**:路径/计数核对(同 D1 套路)+ gofmt/vet 干净。

## 2 已知约束(L0/PRL、上游、🔐)

- **L0 冻结**:sanitize 名单、`@`-注入顺序、JWT 拒 none、**全部现有测试**——不许改。**注意**:H1/H2 会**新增** Store 接口方法 + 改 `http_accessor`/`serve.go` 的 scoped 分支 + 两个 store——这是**有意的框架演进**,但 owner-scope 的**隔离保证只能加强、不能放宽**(PRL:scoped 读写永不跨主泄漏)。改完**所有旧测试必须仍全绿**(含 R1/R2 的隔离用例)。
- **PRL1–PRL4** 全适用,**H1/H2 尤其盯隔离**:任何 scoped 操作只能见/动 owner==caller 的行。
- **上游**:R1+R2 已封存,框架核心在真 Mongo 上全绿。R3 是在此之上的硬化,不是补救。
- **🔐**:无放宽类拍板。承重件复用本地 Docker Mongo。**唯一待人确认**:H3 砍掉是否 OK(默认砍)。

## 3 可预见的岔路(v0 里想好分支)

1. **H1 dup-key 判定在 v2.7.0 API 不同**:云端定稿会给 v2.7.0 的判定写法;若仍漂移,**仅**机械适配那一处判定调用(参照 mongostore 既有用法),不动语义。
2. **H1/H2 改完某旧测试挂了**:说明动到了语义——**停**,记 failed + 哪个测试 + 现象,suspend 交云端。绝不改旧测试迁就。
3. **scoped HKEYS 泄漏了别人的键**:严重(PRL)——failed + 异常发现,suspend。
4. **并发用例不稳定/偶发**:记现象 + 复现率,别把 flaky 当 pass(RL5/RL6)。
5. **无 Mongo**:H1/H2 承重测试 skip → 终态 B、suspend;**不阻塞** T1/H4/D2(都不需 Mongo)。
6. **T1/H4 tsc/单测报错**:记原文,suspend。

## 4 并行轨调度

T1、H4、D2 三条都**不依赖 Mongo**,与承重轨可并跑。H1/H2 若卡在设计需云端裁,转去把这三条封掉。这样即便承重轨要二轮对账,并行轨也能在 R3 内落地。

## 5 给本地的话

写 `delivery/rounds/R3/v0-plan.md`,**一次覆盖 5 条轨**,逐轨列 `目标 / 拟建什么 / 打算怎么自证`。信号最高的两条:

- **H1 原子性怎么客观验**:并发用例的构造 + 断言(终值一致、无错、跨主 403)。想得到客观判据就写;只想到「看着对」就如实写——那提示云端该给你写死。
- **H2 不泄漏怎么客观验**:逐用户键集相等的断言。

H1/H2 的**实现 + 测试**云端都会写死逐字给你落地(框架级安全改动不该由本地即兴实现);你的 v0 主要是对齐设计意图、暴露你对隔离语义的理解、并确认本地环境(Mongo/node 就绪)。T1/H4/D2 相对独立,v0 简述自证法即可。

> 提醒:STATUS.md 是云端独写件。应用云端交付时把它**原样一并提交**,别手改、别让它以未提交状态漂着(见 R2 SEALED 末)。
