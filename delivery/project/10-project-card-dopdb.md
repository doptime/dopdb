# 项目卡 · dopdb(v1 · 2026-06-23 · 云端维护)

> 本项目慢变事实。回合简报只放切片,全景与冻结看这里。本卡正文是**契约**;针对某台 harness 的临时澄清进文末「常见误读附录」,不许撑进正文。

## 一句话与终态(A / B,两者皆 done)

把 doptime+redisdb 合并重写、数据后端换成 MongoDB 的 Go 框架(`github.com/doptime/dopdb`)。当前阶段目标是**把已在内存 store 上验证的框架,在真实 MongoDB 上验证通过**。

- **终态 A(达成)**:`mongostore` 在真实 MongoDB 上跑通与 `memstore` 同构的契约(CRUD/ErrNoDoc/SetNX/原子 Incr/Find 净化/unique 索引冲突),且无驱动那批测试维持全绿。
- **终态 B(诚实负结果)**:本地无法起 MongoDB / 拿不到测试 URI → `mongostore` 集成测试 **skip 并明确报告原因**,其余 🟢 全绿;承重件 `suspend` 交云端裁。B 按设计走完即 done,不许为了好看把 skip 伪装成 pass(RL5/RL6)。

## 阶段编号表(本项目用字母 V / H / D)

| 阶段 | 一句话 |
|---|---|
| V0 | 工具链体检 + 拉 mongo 驱动 + 锁 go.sum |
| V1 | `go build ./...`(含 mongostore)+ vet + gofmt 干净 |
| V2 | 无驱动测试全绿(数据/api/httpserve/config/memstore) |
| **V3**(承重) | mongostore 对真实 MongoDB 跑通契约集成测试 |
| **V4**(承重,依赖 V3) | httpserve 端到端在真 Mongo 后端复跑(bson tag 下 owner-scope/@-绑定正确) |
| H1 | 原子 scoped-write 原语(filtered upsert)替换 check-then-act |
| H2 | scoped 集合的 HKEYS/HLEN(带 filter count/distinct) |
| H3 | msgpack 请求/响应 |
| H4 | `_permissions` 用 dopdb 集合持久化 |
| D* | 文档/清理(并行 🟢 安全轨) |

阶段(V/H/D)跨回合;回合用 R 编号。承重里程碑(V3、V4)必须云端 L2/L3 验过才往上建,故分回合。

## 目录地图(实测,2026-06-23)

```
go.mod                      module github.com/doptime/dopdb,go 1.22
store.go                    Store 接口 + Codec + ErrNoDoc/ErrForbidden + M/FindOpt/IndexSpec
dopdb.go                    Collection[K,V] + New + 选项 + 键编解码 + index 标签 + H* 方法
modifiers.go                写入期 modifiers + 时间戳 + RunValidate/SetValidator
sanitize.go                 SanitizeFilter(过滤器净化,安全核心)
http_accessor.go            HttpAccessor 类型擦除桥 + 注册表 + owner-scope 策略
api/api.go                  api.Api + CallByMap 流水线 + 命名 + 注册表
httpserve/jwt.go            stdlib JWT(HS256/RS256,拒 none)+ token 缓存
httpserve/context.go        ReqCtx 解析 + @-替换 + 防伪 + param 注入
httpserve/permission.go     command::collection::on/off 白名单 + AutoAuth
httpserve/serve.go          Handler:解析→权限→命令派发→响应;serveAPI
config/config.go            TOML 配置读取 + env 密钥解析 + 校验
memstore/memstore.go        内存 Store + JSONCodec(测试用)
mongostore/mongostore.go    MongoDB Store 适配器 + BSONCodec(唯一 import 驱动)
docs/                       00-overview / 01-data / 02-http / 03-config / RUNBOOK
config.toml.example         配置样例
*_test.go                   见验收清单
```

## 验收命令清单(★ = 回归基线)

```bash
★ go test . ./api ./httpserve ./config ./memstore   # 无驱动全套,期望 ok×4(memstore 无测试)
  go test ./mongostore                               # 需 mongo 驱动 + DOPTIME_TEST_MONGO_URI
  go test ./...                                       # 全部
★ go vet ./...                                        # 静态检查(mongostore 需驱动)
★ gofmt -l .                                          # 空输出 = 干净
```

无驱动那批当前基线:**数据 10 + api 7 + httpserve 11 + config 6 = 34 测试全过**。

## 三级冻结(L0 / L1 / L2)

**L0 绝对冻结(谁让改谁有问题)** —— 安全语义核心 + 裁判:
- `sanitize.go` 的 `allowedQueryOps` / `forbiddenOps` 名单与递归走查逻辑。
- `httpserve/context.go` 里「先剥离客户端 `@`-键、再注入服务端 `@`-上下文」的顺序与 `replaceTags` 的 fail-closed(缺 claim 报错)。
- `http_accessor.go` 的 owner-scope 注入与 scoped 读/写隔离逻辑(`OwnerScope`/`HttpGetScoped`/`HttpSetScoped`/`mergeScope`)。
- `httpserve/jwt.go` 拒 `none`、强制 exp 的逻辑。
- 一切 `*_test.go`(裁判,RL2)。

**L1 云端令牌件(仅带显式「修改令」可改)**:
- `mongostore/mongostore.go` 的驱动调用签名(v2 适配,允许机械修正,见决策表)。
- `config/config.go` 的 schema 字段与校验规则。
- `store.go` 的 `Store` 接口方法集(加方法属方向性变更,走 🔐)。

**L2 自由工作区(按包指令改,不许顺手优化)**:其余实现文件、docs、新增测试文件。

## 决策表(承重岔路,依真实数字/现实自动选择)

| 情况 | 动作 |
|---|---|
| `go get` mongo 驱动成功 | 继续 V1 |
| `go get` 因网络失败 | V0 该单元 blocked,记环境事实;V2/V5/D1 仍可跑 |
| `go build ./mongostore` 因 v2 options 签名漂移失败 | **机械修正**:仅限 `options.Client()/Replace()/Update()/Find()/Index()/Count()/BulkWrite()` 的 builder 方法名或 `mongo.Connect` 形参;改完 build exit 0;git diff 限于 mongostore.go;**禁动 `Store` 接口与 BSONCodec 行为** |
| mongostore.go 出现接口未实现(缺方法) | 说明 `Store` 接口或适配器不同步——记异常,**不要删接口方法让它过**,suspend 交云端 |
| V3:有 `DOPTIME_TEST_MONGO_URI` 且 Mongo 可连 | 跑集成测试,硬判据见 V3 包;过→done,关键数字抄断言值 |
| V3:无 URI 或 Mongo 连不上 | 集成测试 `t.Skip` 并打印原因 → 终态 B,记 **suspend**(承重件证不了),其余 🟢 照跑 |
| V3:集成测试某断言不过(如原子 Incr ≠ N、unique 未报冲突) | **不改测试**(RL2);记 failed + 关键数字 + 异常发现,suspend 交云端裁 |
| gofmt 有输出 | `gofmt -w` 修复(纯格式,非语义) |

## 环境事实(来源:作者沙箱体检 + 待 R1 本地坐实)

- Go 1.22.2(沙箱 apt 装);本地需 Go 1.22+。
- **沙箱网络到不了 `go.mongodb.org` / `golang.org/x` / `proxy.golang.org`**,故 `mongostore` 与驱动相关代码**未在沙箱编译/测试过**——这正是 R1 要本地坐实的头号事项。
- 无驱动那批 34 测试在沙箱全过(`go test . ./api ./httpserve ./config ./memstore`)。

## 数据与密钥约定

- 密钥(JWT secret)与带凭据的 Mongo 连接串**只走环境变量**(RL4):`DOPTIME_JWT_SECRET`、`DOPTIME_MONGO_URI`;测试用 `DOPTIME_TEST_MONGO_URI`。
- 配置文件 `config.toml` 只放「环境变量名」,不放密钥本身。
- 测试用的 Mongo 应是一次性 / 隔离库(集成测试会建集合、建索引、写删文档)。

## 项目特定红线增量(PRL,与 RL 并行引用)

- **PRL1**:`_id` 永远是规范字符串;不得为了方便把非字符串主键直接当 `_id` 存(会破坏键 round-trip 与 Find 一致性)。
- **PRL2**:任何对外暴露的查询都必须经 `SanitizeFilter`;不得新增绕过净化器的查询路径。
- **PRL3**:`@`-上下文只能来自已验证 JWT(或服务端生成的 uuid/nanoid);任何让客户端 `@`-参数生效的改动都是安全回退,停下写 blocked。
- **PRL4**:不得为通过测试而放宽 owner-scope 隔离或权限白名单逻辑。

## 预授权动作(RL4 例外,逐文件点名)

- 允许**新建**测试文件:`mongostore/integration_test.go`、`mongostore/http_integration_test.go`(V4)。
- 允许修改 `go.mod`/`go.sum` 以添加 mongo 驱动依赖。
- 不预授权任何删除已交付产物的动作。

## 领域文档指针

`docs/00-overview.md`(架构)、`docs/01-data.md`(数据层)、`docs/02-http.md`(HTTP/安全)、`docs/03-config.md`(配置)、`docs/RUNBOOK.md`(构建/测试/迁移/未竟)。

## 常见误读附录

(暂空——R1 后据 harness 回执补;条目一旦不再犯即删。)
