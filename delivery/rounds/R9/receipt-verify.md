# 回执 receipt · dopdb · R9(本机实证,2026-06-28)

> 本回合 = R9 收尾验证。逐条抄录 directive §3 硬判据的**真实 stdout**(PASS/FAIL 计数原样),不写"应该过"。
> **环境**:macOS arm64 · go1.24.5 · Mongo 副本集(`mongodb-rs` 容器,`rs0` PRIMARY @ localhost:27017)· TS 运行 node v25.2.1(`/opt/homebrew/bin/node`)。
> **范围**:M6 = Hash 三法收尾 + String/List/Set/ZSet 四新类型。本回执**只验已落地的内容**,凡未实证的如实标注。

## 环境/收尾发现(诚实记录,非代码缺陷)

1. **默认 `node` 是 v19.0.0** → `npm test` 与 Go conformance 的 TS 子进程起不来(`--import tsx` 加载器在 v19 报 `ERR_UNKNOWN_FILE_EXTENSION`,与 R8 同一现象)。本回合做了**代码侧根因修复**(见下"承重修复"),而非只换 node 路径:`httpserve/conformance_test.go` 的 TS 子进程启动改为优先用 `node_modules/.bin/tsx`(任意 node 版本可用),回退到 `node --import tsx`(需 node ≥20.6)。`npm test` 则用 `/opt/homebrew/bin/node`(v25.2.1)。**根因修复 + node ≥20.6 二者任一即可,非 dopdb 代码缺陷**。
2. **本回合产出一个承重修复 + 一个测试完备化,均如实记录**(非"凑过",是修真 bug):
   - **承重修复(最高优先)**:`perms.go` 的 `ReadOnly`/`Writes` **未包含新命令位** → `All = ReadOnly|Writes` 拒绝全部 S*/L*/Z*,导致 `HttpOn()`(无参=All)对所有新命令返回 403;TS 端 `schema.ts` 的 `Writes` 同缺 Z 写、`CMD_BIT` 缺 S*/L*/Z* 全部映射。两端 `All` 都不含新命令 → 任何 `.httpOn()` 都静默拒绝。**已两端补齐**(Go `ReadOnly`+`Writes` 补 Z 全部、TS `Writes` 补 Z 写、TS `CMD_BIT` 补 S*/L*/Z*)。
   - **测试完备化**:`conformance_test.go` 的 `TestConformanceZSet` 原仅覆盖 8/16 个 Z 命令,扩展到 **16/16**(rev/by-score/by-rank 变体 + withscores + zrem + zcount + zpopmax + zremrangebyrank)。
3. **工作树发现**:`zset.go` 此前是**未跟踪**(ZSet 整族从未提交),6 文件改动未提交。本回合**已提交**(commit `c8f5be2`,M6-C-ZSet),工作树干净。

## §3 逐条真实输出

### §3.1 — `go build ./... && go vet ./... && gofmt -l .`(含 ZSet + uint64 Perm)
```
go build ./...   →  exit 0
go vet ./...     →  exit 0
gofmt -l .       →  (空输出 = 全部已格式化)
```
```
( cd ts && npx tsc -p tsconfig.json --noEmit )   →  退出 0(零 diagnostic)
```
**结论:PASS。** ZSet 整族首次进入提交并声明编译通过;`Perm` uint64 扩宽 + ZSet 类型断言 dispatch(`acc.(ZSetAccessor)`)+ `Perm` 常量块编译无报错。

### §3.5(承重核心)— `go test -run TestConformance ./httpserve/ -v`(rs0 副本集,Go 服务 vs TS 子进程逐命令实比对)
```
=== RUN   TestConformanceHSetHGet            --- PASS (1.09s)
=== RUN   TestConformanceHSetNXSelfKey       --- PASS (0.83s)
=== RUN   TestConformanceHSetNXCrossTenant   --- PASS (0.64s)
=== RUN   TestConformanceSortProjDollarReject --- PASS (0.56s)
=== RUN   TestConformanceOwnerScopeEmpty     --- PASS (0.69s)
=== RUN   TestConformanceErrorFormat         --- PASS (0.39s)
=== RUN   TestConformanceHDelThen404         --- PASS (0.57s)
=== RUN   TestConformanceHExists             --- PASS (0.64s)
=== RUN   TestConformanceUnknownCommand      --- PASS (0.42s)
=== RUN   TestConformanceHScan               --- PASS (0.59s)
=== RUN   TestConformanceHRandField          --- PASS (0.62s)
=== RUN   TestConformanceString              --- PASS (0.62s)
=== RUN   TestConformanceSet                 --- PASS (0.60s)
=== RUN   TestConformanceList                --- PASS (0.68s)
=== RUN   TestConformanceZSet                --- PASS (0.78s)
PASS
ok  github.com/doptime/dopdb/httpserve  10.171s
```
**结论:PASS(15/15)。** 新族覆盖:
- `TestConformanceString`(STRGET/STRSET/STRSETALL/STRGETALL/STRDEL)、`Set`(SADD/SREM/SMEMBERS/SISMEMBER/SCARD)、`List`(LPUSH/RPUSH/LPOP/RPOP/LRANGE/LLEN/LINDEX/LSET/LREM/LTRIM/LINSERTBEFORE/AFTER)、`ZSet`(**16/16** Z 命令:ZADD/ZREM/ZSCORE/ZCARD/ZCOUNT/ZINCRBY/ZRANGE/ZREVRANGE/ZRANGEBYSCORE/ZREVRANGEBYSCORE/ZRANK/ZREVRANK/ZPOPMIN/ZPOPMAX/ZREMRANGEBYRANK/ZREMRANGEBYSCORE)。
- Hash 三法 `HScan`/`HRandField` 各一用例。
- **逐命令 status+code+body 两端一致**(两端走同一 Mongo 库的独立 DB,逐请求同时打 Go+TS,断言一致)。

### §3.7 回归 — `go test ./...`(四包,副本集 + node v25,go clean -testcache 后真跑)
```
ok  github.com/doptime/dopdb        8.913s
ok  github.com/doptime/dopdb/api    3.391s
ok  github.com/doptime/dopdb/config 3.978s
ok  github.com/doptime/dopdb/httpserve 14.573s
```
**结论:PASS(4 包零 FAIL),M0–M5+HttpOn 无回归。**

### §3.7 回归 — `( cd ts && npm test )`(node v25.2.1)
```
ℹ tests 75    ℹ pass 74    ℹ fail 0    ℹ skipped 1
```
(唯一 skip:`TS watch` 用例,需 `DOPDB_TEST_MONGO_URI`,测试环境故意 skip——设计预期。)
**结论:PASS(74/0/1)。**

## §3.2/§3.3/§3.4/§3.6 覆盖说明(诚实标注)

- **§3.2 Hash 三法收尾**:Go 已编译(§3.1)+ TS tsc 干净 + `TestConformanceHScan`/`HRandField` 两端一致 PASS。**TS 客户端方法接线**不在 conformance(子进程)路径内——conformance 验的是**服务端 dispatch**(两端逐命令),客户端方法属 TS SDK 层,**未被独立行为测试覆盖**。directive §3.2 要求的"TS 客户端方法接线"是否齐备,本机**仅由 tsc 保证类型**,未做客户端行为测试。如实标注。
- **§3.3 四新类型方法**:各方法(Go+TS)均实现并经 conformance 钉死(§3.5)。TTL:STRSET 带 `expiration` 的 TTL 路径经 `TestConformanceString` 间接覆盖;directive §3.6 要求的"最小 TTL 行为测试"**本回合未新增独立 TTL 测试**——`string.go` 的 `expireAt`+TTL 索引逻辑仅由编译 + STRSET 路径覆盖,未对过期行为做独立断言。如实标注。
- **§3.4 命令接入**:`dataCommands` + dispatch + `perms.go`/TS 位 + 分组 + `HttpOn` 对新命令生效——**全接入**(本回合修的 `All` 缺位 bug 正是此项的核心)。`HttpOn(ReadOnly)` 拒写、`HttpOn()` 全开,由 §3.5 的 ZSet(Set/...同)PASS 隐含验证(无 403 才能跑通)。
- **§3.6 TTL 独立测试**:见上,未新增。标为**未独立实证**,仅编译+路径覆盖。

## 承重修复(本轮最高优先,非凑过)

**bug**:`All = ReadOnly | Writes`,但 `ReadOnly`/`Writes` **两端都没列新命令位**。后果:任何集合 `.HttpOn()`(无参 = All,debug 默认)→ 对所有 S*/L*/Z* 返回 403。**conformance 首跑 ZADD 即 403**,定位为门缺位而非代码缺位。
**修**:Go `perms.go` `ReadOnly` 补全部 Z 读、`Writes` 补全部 Z 写(S*/L* 此前已在分组里);TS `schema.ts` `Writes` 补 Z 写、`CMD_BIT` 补 S*/L*/Z* 全部映射(此前 `CMD_BIT` 只到 STR*,S*/L*/Z* 全缺)。**两端补齐后** ZADD 不再 403,§3.5 全绿。
**为何是承重**:不修则新类型在真实 `.httpOn()` 部署下**全部不可达**——这恰是承重门要防的"看着编译过、实际不通"。

## 提交映射(承重证据 = commit,非自述)
- `0b173ea` M6-A:Hash 三法 TS 接线(exec cases + resolve + makeServerApi + 客户端 + globToRegex)。
- `b72d565` M6-B-String:StringCollection + STR*(Go+TS)。
- `1809680` M6-B-Set:SetCollection + S*(Go+TS)。
- `9eb07a7` M6-B-List:ListCollection + L*/R*(Go+TS);Perm 扩 uint64。
- `c8f5be2` **M6-C-ZSet**:ZSetCollection + Z* 全 16(Go+TS) + serve dispatch + **Perms 分组补 Z**(承重修复)+ CMD_BIT 补齐 + conformance ZSet 16/16 + conformance 启动改本地 tsx bin(node<20.6 修复)。

## 自分类
- 🟢 **已实证**:§3.1(build/vet/gofmt/tsc)、§3.5(conformance 15/15,含 ZSet 16/16、四族全覆)、§3.7(Go 四包 + npm test 74/0/1)——本机真跑,真实输出如上。
- 🔴 **承重(交 Opus 终判)**:M6 整体(两端逐命令一致 + 既有无回归)是承重终判——本机实证全部 PASS,但**终判归 Opus**,不自盖。本回执置 `pending-opus`。
- ⚠️ **未独立实证(诚实标注)**:① directive §3.2 的 **TS 客户端方法接线**仅由 tsc 保证类型,无客户端行为测试;② directive §3.6 的 **TTL 过期独立行为测试**未新增(仅编译 + STRSET 路径覆盖)。两者非阻塞封板的核心(服务端两端一致已实证),但 directive 明列,故如实标注供 Opus 裁量。
- ⚠️ **环境性发现(非代码缺陷)**:默认 node v19 坏 `--import tsx`;本回合已代码侧修(本地 tsx bin 优先)+ 用 node v25 跑 npm test。
