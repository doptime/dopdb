# 调和记录 · dopdb · R2(2026-06-24)· 云端

> 规划交换收口:云端审 v0-plan,记采纳/偏差/定稿。本地据 `brief.md` + 两个定稿 packet 执行 Phase ②。

## 裁决:v0-plan 合格(SOUND)

理解到位:准确抓住了 codec facade(JSON-进/BSON-落盘/JSON-出 字段映射)这个核心风险,识别了 `@uid` 经 api 流水线的路径、以及"每子测试需独立 new Handler"(风险 4)、时间戳"HTTP 字符串 vs BSON Date"(风险 5)。承重件自证设计扎实。据此定稿,云端**写死硬判据 + 内嵌测试代码**(像 V3),本地逐字建文件后跑。

## 偏差(已折进定稿包,附理由)

1. **codec 结构体去掉 `Owner` + `mod:"auto=@uid"`**。dopdb **没有 `auto=` 这个 modifier**——支持的只有 trim / lowercase / uppercase / title / default / unixtime / counter / nanoid(见 `modifiers.go`)。owner 注入是 `SetOwnerScope(coll, field, claim)` 机制(对 scoped 集合在写入期强制 owner=claim),**不是** struct mod 标签。为让 codec 测试聚焦"json↔bson 字段名/类型映射 + default + 时间戳",定稿 `CodecProfile` 砍到 5 字段、不带 owner。owner/`@uid` 注入另由子测试 2(query 伪造)+ 子测试 5/6L4(body 经 api 伪造)覆盖。

2. **复用 recorder 版 `do()` 而非 `httptest.NewServer`**。`do()`(httptest.NewRequest + `h.ServeHTTP` + recorder)走的是**同一条** Handler 全栈(路由 / JWT / `@`-绑定 / 权限 / 派发),与 NewServer 唯一差别是没有真 TCP socket——对被测的框架语义无影响。复用 `do()`/`tokenFor`/`decodeObj`/`Profile`/`Order` 与 `serve_test.go` 一致,免重复造辅助。

3. **层 3 简化**。v0 想"加一个 json/bson 标签故意不一致的字段"做对照——那只是信息性确认(json 控 HTTP 响应、bson 控 Mongo 落盘)。定稿直接证对齐:**HTTP 字段集 == BSON 字段集 == 预期 5 字段**(`[_id createdAt name role updatedAt]`),这才是承重断言,无需额外加错配字段。

4. **每次跑用唯一库名 + cleanup 时 drop**(`dopdb_http_it_<时间戳>`),而非固定集合名——彻底隔离多次运行,免残留文档导致计数脆。v0 风险 4(Handler 权限态共享)由"每子测试 `freshHandlerMongo()`"解决(定稿已做)。

5. **子测试 4 去掉 HGETALL 计数断言**——避免共享集合上的跨子测试计数脆;改为 HSET→HGET→HDEL→HGET(404),无歧义。

6. **承重件代码云端已写 + 内嵌**。框架子测试(api 写集合 + `@uid` 经流水线 + codec 字段映射 HTTP 层)已在沙箱 memstore 上编译+跑通;直读 Mongo 那段驱动调用照**已跑通的 `mongostore/mongostore.go`** 逐字镜像(沙箱无驱动不能编译这 4 类调用,与 V3 同级——本地真机编译+跑)。本地逐字建文件即可。

## W1

设计正确(三命令 + 退出码 + 成功串)。补一条:本地 node **v19** 原生 `fetch` 仍是实验特性,smoke-test 时 stderr 可能打 `ExperimentalWarning: The Fetch API is an experimental feature`——**预期,非失败**,只看退出码 + `ALL TS SDK INTEGRATION TESTS PASSED`。已写进 W1 决策表。`make wasm` 用本地 1.24 同源重建 wasm + wasm_exec.js,正面消解 v0 风险 3(版本必匹配)。

## 放行

两个定稿 packet:`packet-P-V4-http-mongo-integration.md`(🔴 承重)、`packet-P-W1-wasm-ts.md`(🟢 并行)。本地照 `brief.md` 执行 Phase ②:逐包做 → 写 receipt → 跑出 `*.txt` 留痕 → 回传,云端三层审计后封 R2。
