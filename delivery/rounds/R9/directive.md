# 回合指令 directive · dopdb · R9(2026-06-28,Opus↓ · 新里程碑 M6)

## 0 一句话 + 本回合承重里程碑
**把 dopdb 的 DB API 补齐到尽可能全的 redisdb 兼容。** 承重里程碑 **M6:redisdb-compat key 类型**——按 `docs/REDISDB-COMPAT.md` 实现并实测。
> 项目此前 R8 已封 M0–M5+HttpOn(仍有效,不回退)。M6 是**新增**范围。

## 1 Opus 本回合已做(交接给你的起点)
- **Go Hash 三法已实现**(`HRandField`/`HScan`/`HScanNoValues`):`mongo.go`(`sample`/`scan`/`globToRegex` 三原语)、`dopdb.go`(Collection 方法 + `keysFromIDs`)、`http_accessor.go`(`HttpRandField`/`HttpScan`/`HttpScanNoValues` + 接口)、`httpserve/serve.go`(词表 + dispatch,读 `?count=&cursor=&match=`)、`perms.go`(3 位 + ReadOnly + cmdPerm + HttpPermNames)。
- **TS 已同步 Perm 常量**(`HScan`/`HScanNoValues`/`HRandField` 位值与 Go 一致 + CMD_BIT + ReadOnly + index 导出)。
- **未验证**:Go 全未编译(沙箱无 Go);TS 仅验 tsc + 既有测试(74/0/1),Hash 三法的 TS *客户端/服务端方法接线*尚未写。

## 2 范围
- **A. 收尾 Hash 三法**:① `go build/vet` 通过我写的 Go(预期风险:`sample` 的 `mongo.Pipeline`/`bson.D`、`scan` 的 regex+skip+limit、owner-scope 的 `scope` 合并);② 补 **TS 侧**这三法的客户端方法(`hScan`/`hScanNoValues`/`hRandField`)+ 服务端 dispatch,使两端等价;③ 加 conformance 用例。
- **B. 四个新 key 类型**(按 `docs/REDISDB-COMPAT.md` §2–§9 全实现):`StringCollection[K]`、`ListCollection[K,E]`、`SetCollection[K,M]`、`ZSetCollection[K,M]`,Go+TS,含命令词表 + dispatch + `perms.go`/TS 位 + HttpOn + owner-scope + TTL。
- **不在范围**:⛔ `BLPop/BRPop/BRPopLPush`(阻塞,不做);不动 M0–M5+HttpOn 既有语义。有歧义先升级问(闸 a)。

## 3 硬判据(逐条客观,真实 stdout 落回执)
1. `go build ./... && go vet ./... && gofmt -l .` 零错;`( cd ts && npx tsc --noEmit )` 零错。
2. Hash 三法:Go + TS 实现齐 + 单测过 + conformance 双端一致。
3. 四新类型:各方法按设计实现,Go+TS。
4. 命令并入 `dataCommands` + dispatch + `perms.go`/TS 位 + 分组;`HttpOn` 对新命令生效。
5. **conformance(承重核心)**:`httpserve/conformance_test.go` 为**每个新命令**加双端比对(Go 服务 vs TS 子进程,真 Mongo),覆盖正常 + 边界(空 key/越界 index/不存在 member/owner-scope 跨租户),**逐命令 status+code+body 一致**,全绿。
6. TTL:带 expiration 的方法按 §7(`expireAt`+TTL 索引),加最小行为测试。
7. 回归:`go test ./...` 四包 + `npm test` 全绿,M0–M5+HttpOn 无回归。

## 4 已知约束
- RL2/RL5/RL6:不改判据凑过;退出码 0 ≠ 完成,语义(PASS 计数、两端 diff 空)为准;诚实失败优于假装。
- L0:线协议既有命令、`@`-绑定、owner-scope、错误 5 类——新类型**沿用**(owner 存 doc 顶层,门按 `{_id,owner}`),不另造语义。
- 命名避冲突:String 用 `STR*` 前缀(STRGET/STRSET/...),Set 用 `S*`(SADD/...),不撞。
- node ≥ 20.6(`--import tsx`;conformance 起 TS 子进程),需要时 `DOPDB_TS_NODE`。

## 5 实证要求
每条 §3 的真实 stdout 抄进 `rounds/R9/receipt-verify.md`(PASS 计数、conformance 用例数原样)。任一 FAIL 如实记 + 报错原文,不往上盖。**Opus 写的 Go 若编译报错,做最小修复并在回执记录改了什么、为什么**(这是盲写的预期风险点,不算你的失误)。全绿后产 `rounds/R9/SEALED.md` 草稿置 `pending-opus`,回传 Opus 终判联签。

## 6 自分类提醒
- 🟢 已实证:本机真跑过、真实输出在回执(尤其每个新命令 conformance 两端一致)。
- 🔴 承重:M6 整体(两端逐命令一致 + 既有无回归)是承重终判 → 做完+实证+起草 SEALED+pending-opus,终判归 Opus。
- 规模提示:可分多次提交(先收尾 Hash 三法 → String → Set → List → ZSet,由简到繁);Mongo 重点风险区:原子弹出 `findOneAndUpdate`+`$pop`、ZSet 排名聚合、`arrayFilters`、TTL 索引——逐条用 conformance 钉死。
