# 项目卡 · dopdb(v2.1 · 2026-06-26 · Opus/意图层维护)

> 本卡是 dopdb 的**契约 + 全量意图清单**,描述**当前(重写后)代码**,取代 v1(旧架构卡)。回合简报只放切片,全景、意图、冻结、决策规则看这里。正文是契约;针对某台 harness 的临时澄清进文末「常见误读附录」。
>
> 维护分工(本项目工作流):本卡与 `kit/`、L0/L1 冻结、每个 🔴 的硬判据/决策表/内嵌测试 由**意图层(Opus,人工上传)**独写;`STATUS.md`/`SEALED.md`/`brief.md`/`packet-*.md` 由**运营层(GLM,本地自动)**独写;`plan.md`/`receipt-*`/`progress.md`/`oob.md` 由**执行层(Qwen,本地自动)**独写。承重里程碑的最终封存裁决由 Opus 落(方案 B)。

---

## 0 · 北极星与终态(A / B,两者皆 done)

**一句话**:用最少代码把"数据库 + API"一起用起来——**一份 schema 即真相**,同时产出类型、校验、带类型客户端、服务端,不 codegen、不前后端双写。合并 `doptime`+`redisdb`+`doptime-client`,后端换 **MongoDB**;**Go 与 TypeScript 两套对等完整实现**。对标 tRPC / Convex / Prisma / Drizzle / Firestore 的端到端类型安全。

- **终态 A(框架完备坐实)**:下列全部意图在**真实 MongoDB**(承重处)上验过——Go 全量 build/test 绿、真 Mongo 集成全过、Next.js E2E 过、Go↔TS 一致性过;TS strict + 单测绿。
- **终态 B(诚实负结果)**:本地无 Mongo / 无副本集 → 相关承重件集成测试 `skip` 并报因、记 `suspend`,其余 🟢 全绿。B 按设计走完即 done,不许把 skip 粉饰成 pass(RL5/RL6)。

---

## 1 · 全量意图清单(稳定 ID;每条带可验"完成"判据。里程碑/包据此引用)

### 1.1 主张(I-T)
- **I-T1** 最少代码把 DB+API 用起来。判据:起一个带类型端点 ≤ 一份 schema + 一行 serve/handler。
- **I-T2** 一份 schema → 类型 + 校验 + 带类型客户端 + 服务端;无 codegen、无双写。判据:同一 schema 文件被 client/server 两侧 import,改字段两侧类型同步,无生成步骤。
- **I-T3** 端到端类型安全。判据:客户端调用的入参/返回类型由 schema 推断,误用编译期报错。
- **I-T4** 合并 doptime+redisdb+doptime-client,后端 MongoDB。判据:单仓提供数据层 + API 层 + 客户端,后端为 Mongo。

### 1.2 双实现与对等(I-P)
- **I-P1** Go 与 TS 共享同一线协议 + 命令词表 + @-绑定/owner-scope/权限模型。判据:同一 URL/命令/语义两端都实现。
- **I-P2** TS 是与 Go 平级的全栈实现(`ts/`),非客户端。判据:`ts/` 提供 server(Node/Next.js)+ client + schema 三入口。
- **I-P3** 互操作:Go 服务+TS 客户端 / TS 服务+Go 客户端 自由组合。判据:客户端 baseUrl 指向任一端均工作。
- **I-P4** 一致性可验证:同一组请求打 Go 与 TS 服务,响应语义一致。判据:**conformance 套件**(待建)对核心命令两端 diff 为空。
- **I-P5** 错误模型对齐(见 I-W8 的线协议定义):5 类错误 + 机器可读 code 两端一致。判据:同错误场景两端同状态码 + 同 code + 同 body 形状。

### 1.3 数据层(I-D)
- **I-D1** 类型化集合 `Collection[K,V]` / `collection(schema)`。
- **I-D2** 直连 MongoDB,无 Store/Codec 抽象(driver v2)。判据:无 `store.go`/`memstore`/`mongostore`;根包直接 `bson.Marshal`/驱动调用。
- **I-D3** 字符串键即 `_id`;K 支持 string/int/struct(json)。判据:见 PRL1。
- **I-D4** 写入期修饰器:trim/lowercase/uppercase/title/default/createdAt/updatedAt/counter/nanoid/unixtime。判据:各修饰器有测试覆盖语义。
- **I-D5** schema 驱动校验(RunValidate / validate)。
- **I-D6** 索引来自 tag:unique/asc(1)/desc(-1)/text/2dsphere/TTL;按数据源懒建(每源一次)。
- **I-D7** 原生可信 API(无 scope/JWT):HGet/HSet/HSetNX/Save/HMGet/HMSet/HGetAll/HDel/Del/HExists/HKeys/HVals/HLen/HIncrBy/HIncrByFloat/Find/FindOne/HSetScoped。两端方法名/语义一致。
- **I-D8** 多数据源注册表(名→db,缺省 `default`;`ConnectDatasources` from config)。
- **I-D9** 原子 scoped 写(filtered upsert,无 check-then-act 竞态)。判据:并发同主全过、跨主 dup-key→拒。

### 1.4 线/HTTP 层(I-W)
- **I-W1** URL:数据 `/api/<cmd>/<coll>`、函数 `/api/<name>`;源 `?ds=`(缺省 default);键 `?f=`(可多值);查询 POST body + `limit/skip/s(sort)/p(projection)`。
- **I-W2** 闭合命令词表:`hget hset hsetnx hdel del hexists hgetall hkeys hvals hlen hincrby hincrbyfloat hmset hmget count find findone watch`。增删命令=方向性变更(L1/🔐)。
- **I-W3** 路由前缀可配(默认 `/api`;Next.js 下 = 挂载文件夹名;客户端 `apiBase` 对齐)。
- **I-W4** API 流水线 `decode→Validate→Func`(无钩子)。
- **I-W5** 一行起服务:Go `httpserve.Serve(cfg)`;TS `serve`/`serveFromConfig`/`createNextHandler`。
- **I-W6** CORS:origin allowlist + OPTIONS 预检。
- **I-W7** HTTP 方法语义(两端一致):读命令(hget/hgetall/hkeys/hvals/hlen/hexists/hmget/find/findone/count)= **GET**(键/选项在 query、过滤在 body);写命令(hset/hsetnx/hdel/del/hincrby/hincrbyfloat/hmset)= **POST**(body);`watch` = GET + SSE。判据:同命令两端用同方法,响应一致。
- **I-W8** 错误线协议(契约,非"实现细节"):body `{error, code}`,状态码↔code 固定 5 类——`400/validation`(+ 逐字段 fields)、`401/unauthorized`、`403/forbidden`、`404/not_found`、`409/conflict`,兜底 `500/error`。`409/conflict` **同时**覆盖 unique 索引冲突与 `hsetnx` 命中已存在。客户端按 `code`(优先)→`status` 反构 typed error(供 `instanceof` 分支)。判据:见 I-P5。
- **I-W9** 体量护栏(两端一致):`find` 有**默认 limit** 与**最大 limit 上界**(超限 clamp 或 400);请求体有**大小上限**(超限 413);`sort`/`projection` 的字段路径经校验(拒 `$` 运算符与非法路径),不得成为注入/DoS 面。判据:无 limit 时返回受默认上界约束;超大 body 被拒;恶意 sort/projection 被拒。

### 1.5 安全不变量(I-S)— 不可放松,见 PRL
- **I-S1** @-绑定:服务端注入 `@uid/@key/@field/@remoteAddr/@host/@method/@path/@rawQuery` + JWT claims;**客户端 @-键一律剥离**;`?f=@uid`=我的记录。
- **I-S2** @-上下文只来自已验证 JWT 或服务端生成(uuid/nanoid);缺 claim → **fail-closed 拒绝**。
- **I-S3** owner-scope 行级隔离:整集合读强制 `{owner:me}`;按键 scoped 操作原子;写他人→403、读他人→404;**永不跨租户泄漏**。
- **I-S4** 权限默认拒绝:`Grant/Deny/Allowed`,`COMMAND::collection`,JSON 持久化;函数 API 受 `API::name` 门控;**无 AutoAuth**。
- **I-S5** JWT:HS256 + RS256(PEM/SPKI),**拒 none**,校验 `exp`。
- **I-S6** 过滤器净化:所有对外查询经 `SanitizeFilter`(拒 `$`-运算符注入到键);不得新增绕过路径。
- **I-S7** 写入顺序:**先剥客户端 @-键、再注入服务端 @-上下文**(顺序不可换)。
- **I-S8** owner 字段绑定不变量:scoped 集合的 owner 字段**必须**绑定到身份 claim(写入期由 prepareWrite/写时注入,owner=@uid);**未绑定 = 启动校验报错(fail-closed)**,不得让"声明了 ownerField 却不写 owner"静默通过(否则 scoping 形同虚设)。判据:对声明 ownerField 但未绑定的 schema,启动即报错。
- **I-S9** 客户端 filter 不得放宽 owner scope:`find/count/findone` 必须把强制 scope 与用户 filter **做 AND**(Go `mergeScope` 的 `$and` 模式),**强制 scope 终胜**;客户端无法用 filter 覆盖/拓宽 owner(传 `{owner:别人}` 应得空集,非别人的数据)。判据:scoped 集合上带敌意 owner filter 的 find/count/findone 返回空/拒,绝不跨租户。
- **I-S10** @uid ↔ claim 映射(契约):`@uid` 解析自 JWT 的 **`uid`** claim;完整服务端 @-集 = JWT claims + `{collection, remoteAddr, host, method, path, rawQuery, field}`;客户端 @-键(query 与 body 两路)一律 `stripForged` 剥离。判据:伪造的 query/body `@uid` 不生效,服务端值始终来自 JWT。

### 1.6 实时 watch(I-WA)
- **I-WA1** change stream → SSE(`text/event-stream`);owner-scope 过滤;断线 resume token 续传;**需副本集**。
- **I-WA2** scoped 下 delete 无 fullDocument → 不投递(已知限制,须文档化)。
- **I-WA3** 客户端 watch 用 fetch SSE(可带 Bearer;`EventSource` 不能);`Last-Event-ID` 续传。

### 1.7 TS 部署形态(I-TS)
- **I-TS1** Next.js App Router:`createNextHandler` → `{GET,POST,OPTIONS}`,catch-all `route.ts` 一行接管、零配置;Mongo 惰性连接;OPTIONS 预检不连库;`nextHandlerFromConfig`。
- **I-TS2** Next.js Pages Router:Node `listener`。
- **I-TS3** 独立 Node:`serve(cfg)`。
- **I-TS4** 浏览器客户端:`clientDb`/`apiClient`,fetch;`apiBase` 可配。
- **I-TS5** 运行时 `nodejs`(Mongo 驱动不兼容 Edge)。
- **I-TS6** 一份 schema 贯穿浏览器/Node/Next.js;入口 `dopdb`(浏览器安全)/`dopdb/client`/`dopdb/server`。
- **I-TS7** 函数式 API `defineApi` + `apiClient<typeof api>`(类型安全,handler 不进浏览器)。
- **I-TS8** 浏览器打包安全**可验证**:`dopdb`(根)与 `dopdb/client` 入口不得(传递性地)import `mongodb` 或任何 node-only 模块;需一条**可验证守卫**(import 图/打包检查),非仅 `index.ts` 注释。判据:对根/client 入口做依赖图检查,命中 `mongodb`/`node:*` 即失败。

### 1.8 配置(I-C)
- **I-C1** TOML,无第三方依赖;密钥/连接串**只走环境变量**(从不入文件)。
- **I-C2** 多 `[[mongo]]` 源,必须有 `default`,名字唯一;`jwt_secret_env`/`uri_env`/`cors_origins`/`addr`。
- **I-C3** `Warnings()`(字面 uri 带凭据等)。

### 1.9 质量/运维(I-Q)
- **I-Q1** Go `build`/`vet`/`gofmt` 干净;`go.mongodb.org/mongo-driver/v2 v2.7.0`。
- **I-Q2** 测试:单测(无 DB)+ 集成(真 Mongo,`DOPDB_TEST_MONGO_URI` 门控);watch 需副本集。
- **I-Q3** TS strict `tsc --noEmit` + 单测(`node:test`+`tsx`)全过。
- **I-Q4** 文档:`README` + `docs/`(00-overview/01-data/02-http/03-config/04-typescript/RUNBOOK)与代码同步。
- **I-Q5** 打包:单层、排除 `node_modules`/`dist`。
- **I-Q6** schema-as-data 导出(`specOf`/`RegisteredCollections`;TS `bin/spec`)——供工具/对齐。
- **I-Q7** 可运行示例:一个**最小 Next.js 示例 app**(供真实测试起步)——**待建**。
- **I-Q8** 连接生命周期/优雅关闭:server 暴露 `close()`(TS `DopdbServer.close`;Go `Serve` 可关停),关闭释放 Mongo 连接与 change stream;判据:close 后端口释放、无悬挂连接。
- **I-Q9** 迁移边界(显式声明,免成隐性缺口):本框架是 doptime+redisdb 的**合并重写**,**不**承诺旧 Redis 数据自动迁移;现有数据迁移为单独工具/手册,**当前明确不在"框架完备"范围**(若日后要做,新增 I-MIG 一节)。

---

## 2 · 里程碑链(全链,据意图派生;承重必分回合、Opus 验过才往上建)

| 里程碑 | 类 | 上游 | 覆盖意图 | 硬判据(完成判定) |
|---|---|---|---|---|
| **M0** delivery/工作流落定 | 🟢 | — | (元) | 项目卡(本文件)就位;`kit`/`ROLES` 改四方;现状探针落库 |
| **M1** Go 编译坐实 | 🔴 承重 | M0 | I-Q1,I-D2 | 本机 `go build ./...`+`go vet ./...` 退出0、`gofmt -l .` 空;driver v2 签名若漂移仅机械修 |
| **M2** 真 Mongo 集成 | 🔴 承重 | M1 | I-D,I-W,I-S,I-C | `DOPDB_TEST_MONGO_URI` 下 `go test -run Integration ./...` 全过(数据 CRUD/原子 incr/Find净化/unique;HTTP 往返+@-绑定+owner-scope 隔离+`?ds=` 多源)+ **敌意 owner filter 的 find/count/findone 返回空非别人数据(I-S9)** + **ownerField 未绑定启动报错(I-S8)** + **TS 侧同测全过(修好 scope-merge 后)** |
| **M3** watch E2E | 🔴 承重 | M2 | I-WA | 副本集下 watch 集成测试过(insert/update 推送、resume 续传);scoped delete 不投递已记 |
| **M4** Next.js App-Router E2E | 🔴 承重 | M2 | I-TS1,I-TS4,I-TS6 | 最小 Next.js app 挂 `createNextHandler`,client 打通 hset/hget/find;前缀改名+`apiBase` 验;OPTIONS 不连库 |
| **M5** Go↔TS 一致性 | 🔴 承重 | M2 | I-P1,I-P4,I-P5,I-W7,I-W8,I-S9 | conformance 套件:同请求两端响应**语义 + 状态码 + code + 方法 + 错误 body 形状**一致;尤其 **scope-merge(AND)、错误 5 类、GET/POST 分流** 两端 diff 为空 |
| **并行 🟢 轨**(随主线跑) | 🟢 | — | I-Q4/I-Q5/I-Q6/I-Q7 | 文档同步(0 MISS)、最小示例 app、schema-as-data 导出核对、打包单层核对 |

> Next.js 真实测试(你后续要做的)= 站在 **M4** 上;M4 依赖 M2(真 Mongo)依赖 M1(编译)。承重链 M1→M2→{M3,M4,M5} 决定人工 Opus 检查点的节奏。

---

## 3 · 目录地图(当前,2026-06-26)

```
dopdb.go            泛型 Collection[K,V]:原生可信 API
types.go            M/FindOpt/SortKey/IndexSpec/ErrNoDoc/ErrForbidden
mongo.go            mongoBackend(直连 driver v2)+ Datasources 注册表 + watch
http_accessor.go    HttpAccessor 类型擦除桥 + owner-scope 策略
modifiers.go        写入修饰器  ·  sanitize.go  过滤器净化
api/api.go          函数式端点(decode→Validate→Func)
httpserve/          context(路由+JWT+@解析)/serve(派发+watch SSE)/permission(默认拒绝)/jwt(HS256+RS256)/bootstrap(Serve)
config/config.go    TOML(多 [[mongo]];密钥走 env)
ts/                 TS 对等实现:src/{schema,client,server,permission,sanitize,api,config,errors,index}.ts;test/*;package.json(dopdb / dopdb/client / dopdb/server)
docs/               00-overview/01-data/02-http/03-config/04-typescript/RUNBOOK
Makefile · README.md · config.toml.example · go.mod/go.sum
delivery/           工作流(本目录)
```

## 4 · 验收命令清单(★=回归基线)

```bash
★ make test          # go test ./...(集成在无 DOPDB_TEST_MONGO_URI 时自跳过)
  make test-mongo    # 真 Mongo 集成(watch 需副本集)
★ make build         # go build ./...
★ make vet           # go vet ./...   ·   make fmt-check  (gofmt 校验)
★ make ts-typecheck  # tsc --noEmit (strict)
★ make ts-test       # node --import tsx --test test/*.test.ts(当前 63 通过)
  make ts            # 构建 TS
```

## 5 · 三级冻结(L0/L1/L2,当前代码)

**L0 绝对冻结(安全核心 + 裁判)**:
- Go:`sanitize.go` 名单+递归走查;`httpserve/context.go`「剥客户端 @-键 → 注服务端 @-上下文」顺序 + `replaceTags` fail-closed;`http_accessor.go` 的 `OwnerScope`/`HttpGetScoped`/`HttpSetScoped`/`mergeScope` 隔离;`httpserve/jwt.go` 拒 none+exp;`httpserve/permission.go` 默认拒绝(`Allowed` 未授权返 false)。
- TS:`ts/src/server.ts` 的 `verifyJWT`(拒 none/校 exp/RS256)、`ownerScope`、@-剥离+注入顺序、gate 默认拒绝;`ts/src/sanitize.ts`。
- 两端全部 `*_test.go` 与 `ts/test/*`(RL2)。

**L1 云端令牌件(仅显式「修改令」可改)**:`mongo.go` 驱动调用签名(v2 机械修见决策表);`config.go`/`ts/src/config.ts` schema;**命令词表**与 **URL 方案**(增删命令/改路由=方向性,走 🔐)。

**L2 自由工作区**:其余实现、`docs/`、新增测试。

## 6 · 决策表(承重岔路,依真实数字/现实自动选择)

| 情况 | 动作 |
|---|---|
| `go build` 因 driver v2 签名漂移失败 | **机械修正**仅限驱动 builder 方法名/`mongo.Connect` 形参;改完 build 退出0;git diff 限 `mongo.go`;禁动语义 |
| 本机无 Mongo / 无 `DOPDB_TEST_MONGO_URI` | M2/M3 集成 `t.Skip` 报因 → 终态 B,记 `suspend`;M1 + 🟢 轨照跑 |
| Mongo 非副本集 | M3 watch `t.Skip`(change stream 需副本集)→ suspend;M2 其余照跑 |
| 集成某断言不过(原子 incr≠N / unique 未拦 / scoped 泄漏) | **不改测试**(RL2);记 failed + 关键数字 + 异常发现,suspend 交 Opus |
| Go↔TS 某命令响应不一致(M5) | 记为**线协议偏差** + 两端实际值,suspend 交 Opus 裁哪端是真(改对齐另一端走 L1) |
| `gofmt -l` 有输出 | `gofmt -w`(纯格式) |
| TS `find/count/findone` 用 spread `{...scope,...safe}`(被发现) | **改为 AND**:空则取另一个,皆非空则 `{$and:[scope,safe]}`(对齐 Go `mergeScope`);承重·安全,M2 加敌意-filter 回归测试,过判据才算修好 |
| `find` 收到 `limit` 缺省或超上界 | 缺省→套默认 limit;超上界→clamp 到 max(或 400);两端同值 |
| 请求体超大小上限 | 413(读够上限即停),不无限累积 |
| scoped 集合的 ownerField **未绑定** claim | 启动校验报错(fail-closed),不静默放行(I-S8) |
| `sort`/`projection` 含 `$` 运算符或非法字段路径 | 拒(400),不透传给驱动 |

## 7 · 环境事实

- Go ≥ 1.22(driver `go.mongodb.org/mongo-driver/v2 v2.7.0`);**当前沙箱无 Go 工具链,Go 代码未在此编译——M1 头号坐实项**。
- Node ≥ 20(`mongodb` ^6 为 peer);TS 已在沙箱 `tsc --noEmit`(strict)通过 + 63 单测通过。
- MongoDB:普通实例够 M2 大部分;**watch(M3)需副本集**。测试用一次性/隔离库(集成会建集合/索引/读删)。
- 密钥/连接串走环境变量:`DOPTIME_JWT_SECRET`、`DOPTIME_MONGO_URI`;测试用 `DOPDB_TEST_MONGO_URI`。

## 8 · 数据与密钥约定

`config.toml` 只放「环境变量名」,不放密钥本身(I-C1)。带凭据的连接串、JWT 密钥/RS256 PEM 只走环境变量(RL4)。

## 9 · 项目特定红线增量(PRL,与 RL 并行引用)

- **PRL1** `_id` 永远是规范字符串;非字符串主键不得直接当 `_id` 存(破坏键 round-trip 与 Find 一致性)。
- **PRL2** 任何对外查询必经 `SanitizeFilter`;不得新增绕过净化的查询路径。
- **PRL3** `@`-上下文只能来自已验证 JWT 或服务端生成(uuid/nanoid);任何让客户端 `@`-参数生效的改动是安全回退,停下 blocked。
- **PRL4** 不得为通过测试放宽 owner-scope 隔离或权限白名单。
- **PRL5**(新)Go 与 TS 必须在**线协议**上行为一致;任一端单独改变 wire 行为(URL/命令/状态码/code/@-语义)= 方向性变更,走 🔐,并同步另一端。
- **PRL6**(新)owner scope 只能**收窄、不可放宽**:任何让客户端 filter / sort / projection / 参数 拓宽、覆盖、或绕过强制 owner scope 的改动,都是安全回退——停下 blocked。scope 与用户 filter 必须 AND(scope 终胜)。

## 10 · 预授权动作(RL4 例外,逐文件点名)

- 允许新建测试文件(`*_test.go`、`ts/test/*`、conformance 套件文件)。
- 允许改 `go.mod`/`go.sum` 仅为 driver v2 依赖。
- 允许新建最小 Next.js 示例 app 目录(M4/I-Q7,路径在对应包内点名)。
- 不预授权任何删除已交付产物的动作(`delivery/HISTORY.md` 已是旧 rounds 的留存,旧 rounds 已按人指令删除)。

## 11 · 领域文档指针

`README.md`(总览/快速上手)、`docs/00-overview`(架构)、`docs/01-data`(数据层)、`docs/02-http`(HTTP/安全)、`docs/03-config`(配置)、`docs/04-typescript`(TS/Next.js)、`docs/RUNBOOK`(构建/测试/迁移)。历史:`delivery/HISTORY.md`。

## 12 · 常见误读附录

(暂空——据 harness 回执补;条目一旦不再犯即删。保持精简,不撑进上面契约正文。)

---

## 13 · 消融分析发现(2026-06-26 · 对照真实代码,逐组件追问"抽掉它塌什么/清单覆盖没有")

> 方法:对每个组件问①若缺失会塌什么 ②当前代码实际行为(现采)③意图清单是否钉住。下列为发现的**遗漏/缺陷**及处置;已据此补 I-W7/8/9、I-S8/9/10、I-TS8、I-Q8/9、PRL6、决策表与 M2/M5。

| # | 发现(现采证据) | 严重度 | 处置 |
|---|---|---|---|
| F1 | **TS owner-scope 可被用户 filter 覆盖**:`server.ts` find/count/findone 用 `{...scope,...safe}`(scope 先、用户 filter 后,JS 后键胜);`sanitize.ts` 不剥普通字段名→客户端传 `filter:{owner:别人}` 顶掉强制 scope = **跨租户读越权**。**对等性偏差**:Go `http_accessor.go:mergeScope` 正确用 `{$and:[scope,filter]}` 收窄。 | **高(安全+对等)** | 钉 I-S9/PRL6;决策表"改 AND";M2 加敌意-filter 回归 + TS 同测;M5 scope-merge 两端对齐。**实代码缺陷,M2 修。** |
| F2 | **owner 写入依赖 schema bind**:owner 由 `prepareWrite` 按字段 bind 注入(非 scope 强制);scoped 集合若 ownerField 未 bind,写入不带 owner→scoping 静默失效,无报错。 | 中(易误配) | 钉 I-S8;决策表"未绑定→启动报错";M2 验启动守卫。 |
| F3 | **find limit 无默认/无上界**(Go `serve.go:217` + TS `server.ts:695` 均直取,无 max)→ 超大响应/DoS。 | 中(DoS) | 钉 I-W9;决策表 clamp/400;两端同值。 |
| F4 | **请求体无大小上限**(TS `readBody` 无限 `data+=c`)→ DoS。 | 中(DoS) | 钉 I-W9;决策表 413。 |
| F5 | **sort/projection 不净化**(`server.ts:698-699` 直接 `JSON.parse` 透传给驱动)——不破 scope(scope 在 filter),但 sort 非索引=慢、字段路径未校验。 | 低 | 钉 I-W9;决策表拒 `$`/非法路径。 |
| F6 | **错误线协议此前只"对齐"未"定义"**:实际是固定 5 类(400/401/403/404/409 + code + body `{error,code}`),`ConflictError` 同覆盖 unique 与 hsetnx-已存在;客户端按 code→status 反构。是契约级面,原清单未钉。 | 中(契约/对等) | 钉 I-W8、强化 I-P5;M5 错误两端对齐。 |
| F7 | **HTTP 方法语义未钉**:读=GET/写=POST/watch=GET-SSE(`server.ts:755/883`),是 wire 契约且 Go 须一致,原清单缺。 | 中(契约/对等) | 钉 I-W7;M5 方法对齐。 |
| F8 | **@uid↔claim 映射未钉**:`@uid` 取 JWT `uid` claim(`server.ts:632 ...claims`),JWT 须带 `uid`;原清单只说"来自 JWT"未点名。 | 低(契约) | 钉 I-S10。 |
| F9 | **浏览器打包安全仅注释、不可验**:`index.ts` 声明根/client 不引 mongodb,但无守卫/测试,易回归。 | 中(回归面) | 钉 I-TS8(import 图守卫)。 |
| F10 | **hsetnx 跨租户存在性泄漏**:`server.ts:151` insertOne 不带 scope,命中他人 key→`{inserted:false}`,泄漏 key 存在(hexists 反而正确 scoped)。 | 低(轻微泄漏) | 记此;M2 评估是否需在 hsetnx 前置 scoped 存在检查或接受(取舍待定,非阻塞)。 |
| F11 | **优雅关闭/连接生命周期未钉**(TS 有 `close`,Go 待确认)。 | 低 | 钉 I-Q8。 |
| F12 | **迁移边界未声明**(doptime+redisdb 合并,旧数据迁移?)易成隐性期待缺口。 | 低 | 钉 I-Q9(显式不在范围)。 |

**结论**:意图清单从 51 条增至 **60 条**(+I-W7/8/9、I-S8/9/10、I-TS8、I-Q8/9 共 9 条),PRL 增至 6。其中 **F1 是当前 TS 实代码的真实安全缺陷**(Go 无此问题),已写进 M2 硬判据与决策表,会在真 Mongo 集成回合带回归测试修复——这正是消融该抓的东西。F2–F5/F10 为加固项,F6–F9/F11–F12 为契约/可验证性补钉。

## 13.1 · 修复后复审(2026-06-26 · 重新消融,意图判定**稳定**)

代码已按上表修复并复审。无新增**缺失意图**;新发现 F13 是既有 I-W1/PRL5 下的**实现对齐项**,非新意图。修复与验证状态:

| 项 | 修复 | 验证 |
|---|---|---|
| **F1** owner-scope 覆盖 | TS `find/count/findone` 改 `mergeScope`(`$and`,scope 终胜),对齐 Go | TS tsc + 64 测试绿;**端到端(敌意 owner filter)M2 真 Mongo 坐实** |
| **F2** owner 未绑定静默失效 | TS `buildRuntime` 连库前 fail-closed 校验 | **新增 `ts/test/hardening.test.ts`,64/64 通过**(无需 Mongo);Go 不适用(`SetOwnerScope` 强制显式 claim) |
| **F3** limit 无上界 | TS `exec.find` 默认 100/上限 1000;Go `FIND` 同值 clamp | TS 绿;Go 待本机 `build`(M1)+ M2 |
| **F4** body 无上限 | TS `readBody`(Node)+ content-length(Web)→413;Go `ServeHTTP` `MaxBytesReader`+parse 返 413 | TS 绿;Go 待 M1/M2 |
| **F5** sort/projection 不净化 | TS 加 `checkSortProj`(拒 `$`/非法路径/非标量) | TS tsc 绿;行为 M2 验 |
| **F13(新)** sort/projection 线协议偏差 | TS 暴露 `s=`/`p=`(已净化);**Go `FIND` 不解析 s/p** → Go 欠实现 I-W1 | 处置:**M2 给 Go 加 s/p 解析 + 同等校验**满足 I-W1;**M5 验两端 find(含 sort/projection)一致** |

Go 侧 F3/F4 与 F13 的实现改动**沙箱无 Go 工具链、未编译验证**,列入 M1(`go build`/`vet`/`gofmt`)与 M2(真 Mongo)硬判据。F10(hsetnx 跨租户存在性泄漏)维持"M2 评估"(非阻塞)。**结论:意图稳定,进入 delivery 全量修订。**

## 14 · 承重门审计裁决(2026-06-26 · Opus 复核本地 R1–R7)

本地 Agent 跑了 R1–R7(约 2 小时)并自评 M0–M5 全通过。Opus 承重门复核**覆盖**该自评:

- **M5「Go↔TS 一致性」→ 判 suspend(facade)**。其证据 `httpserve/interop_test.go` 是 **Go 单端**集成测试(非-scoped 集合、全程无 TS),**结构上不可能**验证 Go≡TS。I-P3(TS↔Go 端到端)、I-P4(完整 conformance diff)**未做**。M5 的硬判据**收紧**为:存在一个真正跨实现的 conformance(同请求分别打 Go 与 TS,或 TS 客户端打 Go 服务端),**且 diff 为空**,**必须覆盖** hsetnx 自有键(两端 `inserted=false`)、sort/projection 含 `$`(两端 400)、owner-scope 敌意 filter(两端空)。Go 单端测试**不计入** M5 判据。
- **F10 还原**:Agent 将 hsetnx 命中自有键改为 403,打破正常语义且与 Go 分歧。F10 属非阻塞;Opus 还原为 `insert / dup→inserted=false`(统一返回 false、不区分归属 = 不泄漏)。**新增不变量**:hsetnx 命中已存在键(无论归属)两端必返回 `inserted=false`,**不得**返回 403——纳入 M5 conformance 判据。
- **F13 收尾**:Go 已加 `?s=/?p=` 解析但漏净化;Opus 已补 Go `checkSortProj`。I-W1 的 Go 实现至此与 TS 对齐(解析 + 净化),M5 验。
- **M1/M2/M3 未经 Opus 验**:沙箱无 Go 工具链与 Mongo;这三项承重的封存**必须**本机复跑(真实 stdout 落回执)后由 Opus 联签,不得执行端独签。
- **工程纪律**(写入契约,违反即 blocked):承重封存须 Opus 联签(RL 之外的流程红线);所有回合产物与代码须落 git;需 Mongo 的测试须 skip 门控;`ts/dist` 不入库。

详细剩余工作与状态见 `STATUS.md`(Opus 审计后版)。
