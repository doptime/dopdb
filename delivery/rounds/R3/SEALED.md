# SEALED · R3(2026-06-24 执行 / 审计封存)· 收尾回合 · 云端

> 协议 §2.1 封存章。本回合为**最后收尾回合**;此文件封存 R3 并为整个 dopdb 项目作结。封存目录内文件谁也不许改。

裁决:**R3 通过(PASS)。H1+H2 = done(承重·真 Mongo);T1 = done;H4 = done;D2 = done。** 项目可收尾,**无 R4**。

## 三层审计

- **L1 回执完备**:4 份 receipt + progress + 产物齐;无 `oob.md`。
- **L2 直读产物**:逐项核对 receipt = 产物。
- **L3 红线/范围**:逐字一致性、套用纯净性、冻结件、独立复跑。

## 裁决表

| 包 | 类 | 状态 | 关键数字(产物核对) | 范围/红线核对 |
|---|---|---|---|---|
| **P-H1H2** scoped 硬化 | 🔴 承重 | ✅ done | `scoped_mongo.txt`:`TestScopedHardening` 四子测试全 PASS、`SCOPED HARDENING OK`、0.10s、无 SKIP。① 跨主写→403,o1 不被劫持;② 40 同主并发→ok=40/40,终 owner=u1;③ raced 预属 u1,16+16 并发→ok=16/forbidden=16/other=0,终 owner=u1;④ scoped 键 u3={k1,k2}/u4={k3} 交集=0,HLEN 2/1。`-race` 无 WARNING;`go test ./...` 全绿 | `scoped_integration_test.go` 与包**逐字一致**;6 个框架文件套用——`mongostore.go` 仅 gofmt 注释对齐(无语义改动),其余 5 个逐字一致;✓ |
| **P-T1** TS 数据客户端 | 🟢 | ✅ done | `t1_test.txt`:`ALL DATA-CLIENT TESTS PASSED`(10/10 断言:URL/method/body/headers);`ALL TS SDK INTEGRATION TESTS PASSED`(smoke 不回退);tsc 退出 0 | `client.ts`/`index.ts` 加 `DataClient`+`collection`,**纯增量**;`createApi`/`wasm.ts`/Go 端零改;✓ |
| **P-H4** 权限持久化 | 🟡 | ✅ done | `h4_test.txt`:`TestSaveLoadJSON` PASS;5 Grant + 2 Deny → Save/Load 7 键一致;未知键(AutoAuth=off)拒;文件缺失返 error。**云端独立复跑该测试 PASS** | `permission.go` 仅加 `SaveJSON`/`LoadJSON`(+`encoding/json`/`os` import),`Allowed/Grant/Deny` 语义未动;✓ |
| **P-D2** 文档收尾 | 🟢 | ✅ done | 4 篇更新(`01-data`/`02-http`/`04-wasm-ts`/`RUNBOOK`);路径核对 **0 MISS**;`gofmt -l` 空、`go vet` 干净 | 仅改 `docs/**`;符号名与代码一致;✓ |

## 关键数字汇总

- H1H2:四子测试全 PASS,`-race` 干净,全量回归绿;原子 scoped 写在真 Mongo 上既挡跨主(403)又扛并发(40 同主全过 / 16+16 竞态 u2 全拒)。
- T1:10/10 请求成形断言过,smoke 不回退。
- H4:7 键持久化一致,云端独立复跑过。
- 云端独立复跑:无驱动 34 + httpserve 12(baseline 11 + `TestSaveLoadJSON`)全绿;冻结件无意外改动;STATUS.md **本轮已正确提交**(R2 的漂移已解决)。

## 指针

- 回执:`receipt-P-{H1H2,T1,H4,D2}.md` · 进度:`progress.md`
- 产物:`scoped_mongo.txt`、`t1_build.txt`/`t1_test.txt`、`h4_test.txt`
- 新代码:`httpserve/scoped_integration_test.go`(逐字按包)、`httpserve/permission_persist_test.go`、`clients/ts/data-client.test.mjs`
- 框架增量:`store.go`/`memstore`/`mongostore`/`dopdb.go`/`http_accessor.go`/`httpserve/serve.go`(H1/H2)、`httpserve/permission.go`(H4)、`clients/ts/src/{client,index}.ts`(T1)

## 微小项(无害,登记)

- `mongostore.go` 与云端交付差一处 **gofmt 注释对齐**(本地正确跑了 gofmt,把相邻两行注释对齐)——非语义改动,反而是规范化,正确。

---

## 项目收尾 · dopdb 全貌(R1→R3)

**dopdb** = 把 `doptime` + `redisdb` 合一、改建在 **MongoDB** 上的单一框架(`github.com/doptime/dopdb`),Go-first 再 TS,目标是用最少代码把数据库与 API 都用起来。三回合全部封存:

- **R1(PASS)**:工具链/构建/无驱动 34 测试/`mongostore` 真 Mongo 契约(承重)/文档。driver v2.7.0;`$where` 拒;唯一约束生效。
- **R2(PASS)**:http 端到端 @ 真 Mongo(承重,六契约:JWT/`@`-绑定防伪/权限/数据命令/api/codec 字段映射)+ wasm/TS 构建与 SDK 冒烟。坐实关闭 codec 字段映射坑。
- **R3(PASS)**:owner-scoped 集合安全语义收尾——H1 原子 scoped 写(关掉 TOCTOU 窗口,跨主 dup-key→403)、H2 scoped HKEYS/HLEN(只回本人、不泄漏);T1 TS 数据命令客户端;H4 权限文件持久化;D2 文档。**H3 msgpack-at-rest 明确不做**(价值最低)。

**已验证能力**:类型化集合(K/V 泛型)+ 写时修饰器(trim/default/时间戳/counter/nanoid)+ 单一 key codec;JSON@memstore / BSON@mongostore 双 codec 字段对齐;HTTP 命令派发 + JWT(拒 none)+ `@`-绑定(query/body 伪造均剥离)+ command::collection 权限(可持久化)+ owner-scope 行级隔离(原子、不泄漏);api 流水线(in-process via wasm + 远程 via TS client);TS SDK 双半(wasm 运行时 + 远程调用 + 数据命令客户端)。全部在真实 MongoDB 上端到端验过,且无驱动单测全绿。

**能力画像(累计 R1–R3)**:执行力 高(三回合承重件零返工,真 Mongo 全过);纪律性 高(累计 3 处低危均无害:R1 `.gitignore`、R2 STATUS 漂移〔非篡改〕、R3 mongostore gofmt 对齐;断言/门槛/冻结件/测试零触碰);汇报质量 高(回执数字与产物逐项一致,异常发现栏一贯诚实)。

— 封存于 2026-06-24,云端。dopdb 交付完结。后续「架构简约化」为另一条主线,见单独的改进清单。
