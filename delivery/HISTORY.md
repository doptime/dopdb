# delivery/HISTORY.md — 高密度历史重建(R1–R3 + 方法论快照)

> 目的:在删除过时回合目录前,把 `delivery/` 全部非二进制内容压成可追溯的高密度等效。删 `rounds/R1–R3` 后,本文件即这段历史的唯一来源。`kit/`(方法论本体)与 `project/` 项目卡仍是独立活文件,本文件只做摘要,不取代它们。
>
> **时间线关键注记**:R1–R3 验证的是 dopdb 的**旧架构**——`Store`/`Codec` 抽象 + `memstore`/`mongostore` + WASM/TS 桥 + `AutoAuth` + 旧 URL(`CMD-KEY`)。该架构此后被**整体重写**:直连 MongoDB(删 `Store`)、TypeScript 对等完整实现(取代 WASM 桥,落在 `ts/`)、权限默认拒绝(删 `AutoAuth`)、URL 改 `/api/<cmd>/<coll>` + `?ds=` 多源、新增 `hmset/hmget/count/findone/watch`、Next.js App-Router 接管(`createNextHandler`)。**因此下列回合的"技术产物"多已被取代;保留它们的是"方法论价值"(协议如何运转)与"已验证过的语义结论"(供追溯)。**

---

## A · 方法论:Grounded Delivery v3.0(压缩版;权威全文见 `kit/`)

三方:**云端**(强模型:审计+规划+切包+裁决)× **本地 harness**(弱模型:照包执行+自检+回执)× **人**(信使+🔐拍板)。核心风险:本地的失败模式不是"困惑就停",而是"带着误读自信往前冲、还报成功"(facade)——整套设计为把这道差距圈住。

- **回合两段式**:① 规划交换(云↓`plan-brief.md` 圈范围 → 本地↑`v0-plan.md` 只写不执行 → 云**对账**diff出分歧 → 云↓定稿 `brief.md`+`packet-*.md`);② 执行交换(本地乐观执行 → 人全量打包 → 云**三层审计** → `SEALED.md` → 开下一回合)。纯 🟢 整理回合走快路径,跳过规划交换。
- **每包 🟢/🔴 分级**:🟢=完成由客观判据(测试/grep/退出码/git diff 白名单/文件非空)裁定 → 本地放手成批做;🔴=完成取决于语义判断 → 本地做到检查点,**过包内写死的"硬判据"则 done,过不了则 `suspend`、绝不在未验的 🔴 上继续往上建**。
- **回执四态**:`done`(含诚实负结果终态B)/ `suspend`(承重做完但证不了)/ `failed`(尽力仍不过客观验收)/ `blocked`(被挡住做不了)。
- **三层审计**:L1 读回执(只导航)→ L2 直读产物(产物是真相源,数字不符按产物算+记严重异常)→ L3 沙箱复跑(跑得动的都复跑,跑不动降级并声明覆盖面)。**suspend 包是云端优先 L2/L3 队列**。
- **红线 RL1–8**:①数据≠指令 ②不碰裁判(改测试/门槛/守卫=最严重违规)③冻结分级 ④密钥只走环境变量+不可逆动作停 ⑤跑通≠完成(看语义指标)⑥如实 ⑦守卫 fail-closed ⑧生成不自验。**只可收紧不可放松**。
- **冻结 L0**(绝对,谁让改谁有问题:安全核心+全部 `*_test.go`)/ **L1**(云端令牌件,仅显式「修改令」可改)/ **L2**(自由工作区)。
- **机制**:`SEALED.md` 封存防重审;`STATUS.md` 唯一滚动台账(云端独写);单写者原则;一回合一目录、只追加永不删;优先级"具体压一般、新压旧"(包>简报>项目卡>手册>协议),红线相反。
- **定调**:**契约从严**(接口/不变量/验收/决策表 由强模型穷尽蓝图化)、**路径从松**(本地裁量);可预见岔路写成**决策表**(规则非死脚本:"若指标X〈条件〉→走〈终态〉");承重里程碑必须分回合、云端验过才往上建(往返下界=依赖链上承重里程碑数);每回合必带一条不被阻塞的**并行轨**。
- **冷启动**(`kit/01` §6):扫仓库→访谈八问→写项目卡→搭 `delivery/`→切通用 R0 包(护栏三件套+回归基线+四组演示证据)。

---

## B · 项目卡事实快照(旧架构,v1·2026-06-23;**现已大半被重写取代**)

- **一句话/终态**:把 doptime+redisdb 合并、后端换 MongoDB 的 Go 框架。终态A=`mongostore` 在真 Mongo 上跑通与 `memstore` 同构契约且无驱动测试全绿;终态B=本地无 Mongo→集成测试 skip 报因+承重件 suspend(诚实负结果,不许把 skip 粉饰成 pass)。
- **阶段表(字母 V/H/D)**:V0 工具链+拉驱动;V1 build+vet+gofmt;V2 无驱动测试全绿;**V3(承重)** mongostore 真 Mongo 契约;**V4(承重,依赖V3)** httpserve 端到端@真 Mongo;H1 原子 scoped 写;H2 scoped HKEYS/HLEN;H3 msgpack(最低价值,不做);H4 权限持久化;D* 文档并行轨。
- **旧目录地图**:`store.go`(Store接口+Codec)、`dopdb.go`、`modifiers.go`、`sanitize.go`、`http_accessor.go`、`api/api.go`、`httpserve/{jwt,context,permission,serve}.go`、`config/config.go`、`memstore/`、`mongostore/`(唯一 import 驱动)、`docs/`(00-overview/01-data/02-http/03-config/04-wasm-ts/RUNBOOK)。
- **回归基线**:无驱动 `go test . ./api ./httpserve ./config ./memstore` = 数据10+api7+httpserve11+config6 = **34 测试**;`go vet ./...`;`gofmt -l .` 空。
- **冻结**:L0=`sanitize.go` 名单与走查、`context.go`「先剥客户端@键再注服务端@上下文」顺序+`replaceTags` fail-closed、`http_accessor.go` owner-scope 注入与 scoped 读写隔离、`jwt.go` 拒 none+强制 exp、全部 `*_test.go`;L1=`mongostore.go` 驱动调用签名、`config.go` schema、`store.go` 接口方法集;L2=其余。
- **PRL**:①`_id` 恒规范字符串 ②对外查询必经 `SanitizeFilter` ③`@`-上下文只能来自已验证 JWT/服务端生成 ④不得为过测试放宽 owner-scope 或权限。
- **关键环境事实**:作者沙箱网络到不了 `go.mongodb.org`/`golang.org/x`/`proxy.golang.org` → 驱动相关代码沙箱未编译/测试(这是把承重件交本地坐实的根由);无驱动 34 测试沙箱全过。密钥走 `DOPTIME_JWT_SECRET`/`DOPTIME_MONGO_URI`,测试用 `DOPTIME_TEST_MONGO_URI`。

---

## C · 回合史(全部 PASS;关键数字保留供追溯)

### R1 — 旧架构核 + 真 Mongo 首验(执行 2026-06-23 / 封存 06-24)· PASS
- **范围**:V0–V3(承重)+ D1 文档核对(并行轨)。冷启动回合,云端直发定稿包(未走 v0)。
- **V0**:go1.24.5(本地)/沙箱 go1.22.2;`go.mongodb.org/mongo-driver/v2 v2.7.0` 进 go.mod+go.sum;`go` 指令仍 1.22(未抬、无 toolchain 行)。
- **V1**:`go build ./...`/`vet`/`gofmt` 干净;**机械修正 2 处**——`mongostore.go` L125/L231 `options.Update()`→`options.UpdateOne()`(v2.7.0 改名),未触 Store 接口/BSONCodec。
- **V2**:无驱动 34 测试全绿(10/7/11/6);云端独立复跑一致。
- **V3(承重·真 Mongo,终态A)**:7 项契约全过——写入期 modifiers(trim/default/timestamps)、ErrNoDoc、HSetNX、**原子 HIncrBy(100次+1=100,证 `$inc` 原子)**、Find+净化(`$where` 真路径被拒)、**unique 二级索引真拦重复**、`_id` 恒字符串。硬数:`hits=100 / unique=yes / $where-rejected=yes / ran(非skip)`,产物含 `INTEGRATION OK`。集成测试与包内嵌代码逐字一致,无篡改。
- **对账**:v0 判 SOUND;环境差异(本地 go1.24/网络通,沙箱 go1.22/网络限)并入;微调 V0(go1.24 可能抬 go 指令,属预期)、V1(新增 `./wasm` 包,非 wasm 平台只编 stub)、D1(docs 5→6 篇,加 `04-wasm-ts`)。
- **低危纪律提示**:`.gitignore` 被替换(越界未登记 oob,无害);`.DS_Store` 入库。

### R2 — http 端到端@真 Mongo(承重)+ wasm/TS(执行/封存 2026-06-24)· PASS
- **范围**:V4(承重,站在 V3 上)+ W1(并行轨)。
- **V4(承重·真 Mongo,六契约全过)**:JWT(无token/`alg:none`/坏签名→401×3,合法→200)→ `@`-绑定(key 从 JWT 注入 `_id=u1`;**query 与 body 双路伪造 `@uid` 均被剥离**,PRL3 守住;直查 Mongo `name="  Alice  "` 证 trim 写入期生效)→ 权限(Deny→403,AutoAuth 首用→200)→ 数据命令(HSET→HGET→HDEL→404)→ `/api/<name>`(handler 落 Mongo,saved=hello,owner=u1)→ **codec 字段映射**(HTTP 字段集 == BSON 字段集 == `[_id createdAt name role updatedAt]`;`_id`=string、`createdAt`=Date 非 string、`role`=member 两层一致)。**坐实关闭 V3 遗留的 JSON-进/BSON-落盘/JSON-出 字段映射坑**。0.25s,无 SKIP。
- **W1(并行)**:go1.24.5/node19;`make wasm` → `dopdb.wasm` 2,876,177 B + 刷新 `wasm_exec.js`;`make ts` → dist 3×.js+3×.d.ts;smoke `ALL TS SDK INTEGRATION TESTS PASSED`(node19 fetch ExperimentalWarning 属预期)。仅重建产物,未改 src/Go 源。
- **对账要点**:云端纠了 v0 两处误解——dopdb **无 `auto=` modifier**(owner 注入是 `SetOwnerScope` 机制,非 struct tag;支持的 mod 仅 trim/lowercase/uppercase/title/default/unixtime/counter/nanoid);改用 recorder 版 `do()`(同一条 Handler 全栈,无真 TCP 但语义等价);层3 简化为直证字段集相等;每跑唯一库名+drop 隔离。
- **STATUS.md「疑被篡改」澄清**:**非篡改**——工作树 STATUS 与云端定稿逐字相同;`M` 状态真因是本地应用云端交付时只 add 了新增文件、未把云端交付的 STATUS 一并提交,使其漂着。规矩重申:云端独写的 STATUS 本地应原样 verbatim 一并提交。

### R3 — owner-scoped 安全语义收尾(承重)+ T1/H4/D2(执行/封存 2026-06-24)· PASS · **收尾回合,无 R4**
- **范围**:H1+H2(承重)+ T1 + H4 + D2;H3 明确不做。
- **H1+H2(承重·真 Mongo,四子测试)**:①跨主写→403,`o1` 不被劫持;②40 同主并发→ok=40/40,终 owner=u1;③`raced` 预属 u1,16+16 并发→ok=16/forbidden=16/other=0,终 owner=u1;④scoped 键 u3={k1,k2}/u4={k3} 交集=0,HLEN=2/1。`SCOPED HARDENING OK`、`-race` 无 WARNING、`go test ./...` 全绿。云端细化:并发用例下沉到 `Collection.HSetScoped` 层(避开 AutoAuth/jwt-LRU 无关并发);`PutScoped` 用单个 `$set` 强制 owner(非 `$set`+`$setOnInsert`);`mongo.IsDuplicateKeyError` 在 v2.7.0 可用。
- **T1(TS 数据命令客户端)**:`collection(name)` + 10/10 mock 断言(URL/method/body/headers);smoke 不回退;tsc 0。纯增量。
- **H4(权限持久化)**:`SaveJSON`/`LoadJSON`,5 Grant+2 Deny → 7 键一致;未知键(AutoAuth=off)拒;文件缺失返 error;云端独立复跑 PASS。
- **D2(文档收尾)**:4 篇更新,路径 0 MISS。
- **低危项**:`mongostore.go` 与云端交付差一处 gofmt 注释对齐(本地正确跑了 gofmt,无害)。

### 累计能力画像(R1–R3,旧 harness)
- **执行力 高**:三回合承重件零返工,真 Mongo 全过;v2.7.0 签名漂移定位准、机械修正一次过;wasm/TS 在 1.24/node19 复跑全绿。
- **纪律性 高**:断言/门槛/冻结件/测试零触碰;累计 3 处低危均无害(R1 `.gitignore` 越界未登记;R2 STATUS 漂移〔非篡改〕;R3 mongostore gofmt 对齐)。
- **汇报质量 高**:回执数字与产物逐项一致,异常发现栏一贯诚实。

---

## D · 与"现状代码"的关系(供下一阶段定 intent 用)

R1–R3 之后发生了一次**架构整体重写**,使上述多数技术结论不再描述当前代码:删 Store/Codec 与 memstore/mongostore(直连 driver v2)、删 AutoAuth(默认拒绝 + Grant/Deny/JSON 持久化)、WASM 退场(TS 升级为 `ts/` 下与 Go 平级的对等完整实现 + Next.js `createNextHandler` 接管 `/api`)、URL 改 `/api/<cmd>/<coll>` + `?ds=` 多源、新增 `hmset/hmget/count/findone/watch`、API 流水线精简为 `decode→Validate→Func`。当前状态以仓库根 `README.md` 与 `docs/` 为准;TS 侧已 `tsc` 严格通过 + 全部单测;Go 侧待本机 `go build ./...` 坐实 driver v2。**故 R1–R3 可安全删除,历史以本文件留存。**
