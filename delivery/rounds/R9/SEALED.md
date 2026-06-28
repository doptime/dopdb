# SEAL · dopdb · R9 / M6 (2026-06-28) · ✅ Opus 终判联签 · M6 封板

> **状态:SEALED(Opus 已终判联签)** —— 本机实证全 PASS,Opus 独立复核佐证通过,M6 redisdb-compat(Hash 三法 + String/List/Set/ZSet)成立。**M6 封板。**

## 范围
R9 = M6 redisdb-compat 收尾验证回合:把 Hash 三法 + String/List/Set/ZSet 四新类型从"工作树/未验证"转为"真实输出可终判"。Opus 此前已交接 Hash 三法 Go 实现盲写 + TS Perm 同步;String/Set/List 已分三次提交落地;**ZSet 整族此前未提交**——本回合完成 ZSet(Go+TS)+ 修一个承重门 bug + 补 conformance 完备性,全部提交并真跑。

## 硬判据 vs 实际(directive §3)

| # | 判据 | 实际(真实 stdout 见 `receipt-verify.md`) | 结果 |
|---|---|---|---|
| 1 | build/vet/gofmt/tsc 零错 | Go 三项 exit 0;gofmt 空;tsc 零 diagnostic | ✅ |
| 2 | Hash 三法 Go+TS 齐 + conformance 双端 | Go 编译过;TS tsc 过;`HScan`/`HRandField` conformance PASS | ✅(服务端;**TS 客户端方法仅 tsc**) |
| 3 | 四新类型各方法 Go+TS | String/Set/List/ZSet 全实现,conformance 钉死 | ✅ |
| 4 | 命令接入 dataCommands+dispatch+perms+分组+HttpOn | 全接入;**本轮修了 `All` 缺新位 bug**(承重修复) | ✅ |
| 5 | conformance 每新命令双端一致(承重核心) | **15/15 PASS**,ZSet **16/16** 覆盖 | ✅ |
| 6 | TTL 带 expiration 最小行为测试 | STRSET 路径间接覆盖;**独立 TTL 过期测试未新增** | ⚠️ 部分 |
| 7 | 回归 go test 四包 + npm test | Go 4 包零 FAIL;npm test 74/0/1 | ✅ |

## 全量 Go 套件
`go test ./...`(go clean -testcache 后真跑)→ 4 包(dopdb / api / config / httpserve)**零 FAIL**。

## 本回合变更(本地,commit `c8f5be2`)
- **新增 `zset.go`** —— ZSetCollection[K] + Z* 全 16 命令(doc `{_id,members:[{m,score}]}`,读改写 + 派生序 score-asc/m-asc,无聚合分歧)。
- **`httpserve/serve.go`** —— Z 命令 dispatch(`acc.(ZSetAccessor)`)+ `parseMinMax` 辅助。
- **承重修复 `perms.go` + `ts/src/schema.ts`** —— `ReadOnly`/`Writes`/`CMD_BIT` 补齐 S*/L*/Z* 全部新位(修前 `All` 缺位致 `.httpOn()` 对新命令全 403)。
- **`ts/src/server.ts`** —— exec z* 全 16 case + zrem body 解析 + zLoad/zSave key 类型放宽。
- **`httpserve/conformance_test.go`** —— `TestConformanceZSet` 从 8/16 扩到 16/16;**TS 子进程启动改用本地 `node_modules/.bin/tsx`**(修 node<20.6 的 `--import tsx` 失败,与 R8 同一环境问题,本轮做代码侧根因修复)。
- **`ts/conformance/server.ts`** —— 加 Zsetvals 集合。

## 承重修复(本轮最高优先,诚实记)
**bug**:`All = ReadOnly|Writes`,但两端 `ReadOnly`/`Writes` 都没列新命令位 → `.httpOn()`(无参=All,debug 默认)对所有 S*/L*/Z* 返回 403。**conformance 首跑 ZADD 即 403**,定位为门缺位。两端补齐后 §3.5 全绿。**不修则新类型在真实部署下全部不可达**——正是承重门要防的"看着编译过、实际不通"。

## 两个诚实标注(非阻塞核心,供 Opus 裁量)
1. **directive §3.2 的 TS 客户端方法接线**:本回合**仅由 tsc 保证类型**,未做客户端行为测试(conformance 验的是服务端 dispatch,两端逐命令一致)。
2. **directive §3.6 的 TTL 过期独立行为测试**:**未新增**;`string.go` 的 `expireAt`+TTL 索引仅由编译 + STRSET 路径覆盖。

## 环境性发现(非代码缺陷)
默认 node v19 坏 `--import tsx`(报 `ERR_UNKNOWN_FILE_EXTENSION`)。本轮**代码侧修**(conformance 子进程优先用本地 tsx bin)+ npm test 用 `/opt/homebrew/bin/node`(v25.2.1)。建议 CI/运行说明注明 node ≥ 20.6 或用 bun。

## 结论(本地起草,待 Opus 终判)
R9 的实证目标是"M6 两端逐命令一致 + 既有无回归":**conformance 15/15 PASS(ZSet 16/16 全覆),Go 四包零 FAIL,npm test 74/0/1**。承重核心(真 Mongo 上四族逐命令两端一致)以真实输出通过。承重门 bug(`All` 缺位)已定位并两端修复,不留静默不通。

**未达 100%**:directive §3.2 的 TS 客户端方法接线仅 tsc、§3.6 的 TTL 独立测试未新增——均非"两端逐命令一致"的核心,但 directive 明列,如实标注供 Opus 裁量是否要求补齐。

**承重终判归 Opus**:请据 `receipt-verify.md` 真实输出对 M6 落终判联签。

## Opus 终判(2026-06-28 · 全新上下文独立复核)

承重门终审。**裁决:M6 成立,封板。**

**Opus 沙箱内亲验**:① TS 亲跑——tsc 干净,npm test 74/0/1(与回执一致);② 交叉核对——15 个 conformance 测试函数(String/Set/List/ZSet/HScan/HRandField + 原 Hash)在 conformance_test.go 全部实存,与回执 15/15 吻合;③ 核实承重修复——perms.go ReadOnly/Writes 现含全部 S*/L*/Z* 命令位,TS CMD_BIT 补 35 个新命令,All 缺位 bug 确已两端修复;④ 直读四个新类型 Go 源真实完整,zset.go 已提交(c8f5be2),工作树干净。

**据本地真实输出 + git 联签**(沙箱无 Go/Mongo):§3.1 build/vet/gofmt/tsc 零错;§3.5 conformance 15/15(ZSet 16/16)真 Mongo 两端逐命令一致;§3.7 go test 四包零 FAIL（testcache 清后真跑)+ npm test 74/0/1。

**佐证强度最高**:本地跑真 conformance 时 ZADD 真的返回 403 → 定位 All 分组漏新位 → 两端修复(我已核实代码确含全部新位)。"跑了、真失败、根因、修复"无法伪造,我能验的全部对得上,无 facade 迹象。

**两个标注缺口的裁量(非阻塞)**:① TS 客户端方法仅 tsc 类型保证——承重核心是服务端两端一致(已实证),客户端方法是类型安全的请求构造器,目标协议已被 conformance 钉死,接受,记可选跟进;② TTL 过期无独立测试——写路径(expireAt+TTL 索引)已编译+STRSET 路径覆盖,Mongo 后台过期为 Mongo 职责,接受,记可选跟进。均不影响 M6 承重核心。

**结论**:dopdb 的 DB API 已补齐到 redisdb 兼容——Hash 全族 + String/List/Set/ZSet,真 Mongo 上 Go↔TS 逐命令一致(conformance 15/15),HttpOn 对新命令生效,owner-scope 沿用,TTL 写路径就位。⛔ 阻塞 op 按设计不做。**M6 封板。**

## 签名
- 本地 GLM-5.2(实证 + 承重修复 + 起草): ✅ 2026-06-28
- Opus(承重终判联签): ✅ **2026-06-28 · SEALED**
