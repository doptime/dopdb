# dopdb

> `doptime` + `redisdb` 合并重写,数据后端换成 **MongoDB**。先以 Go 落地,最终用 TypeScript 重写以消除前后端分离。
>
> 模块路径:`github.com/doptime/dopdb` · Go 1.22+

把 doptime 的三个招牌——**`@`-绑定上下文模型**、**闭合命令词表**、**API 钩子流水线**——原样搬到 MongoDB 上;丢掉 Redis 才有的东西(ZSet/Set 抽象、阻塞列表、Redis-stream RPC);顺手修掉 redisdb 几处旧缺陷(读路径跑 modifier、键编解码不一致、非原子计数、msgpack-at-rest)。

核心架构:整个类型层只依赖一个窄接口 `dopdb.Store`,MongoDB 驱动**只出现在 `mongostore/` 一个文件**——所以核心逻辑用内存 store 就能全量测试,换引擎只重写一个适配器。

## 目录结构

```
.                          模块根 = package dopdb(数据核心)
├─ store.go                Store 接口 + Codec + ErrNoDoc/ErrForbidden + M/FindOpt/IndexSpec
├─ dopdb.go                Collection[K,V] + New + 选项 + 键编解码 + index 标签 + H* 方法
├─ modifiers.go            写入期 modifiers + 时间戳 + SetValidator/RunValidate
├─ sanitize.go             SanitizeFilter(查询净化,安全核心)
├─ http_accessor.go        HttpAccessor 类型擦除桥 + 注册表 + owner-scope 策略
├─ api/                    api.Api + CallByMap 钩子流水线 + 命名 + 注册表
├─ httpserve/              HTTP 层:JWT、@-绑定、权限白名单、owner 行级隔离、命令/API 派发
├─ config/                 TOML 配置读取 + 环境变量解析密钥 + 校验(无外部依赖)
├─ memstore/               内存 Store + JSONCodec(应用单测用)
├─ mongostore/             MongoDB Store 适配器 + BSONCodec(唯一 import 驱动)
├─ wasm/                   Go→WASM 桥:把 api 核心暴露给 JS(createApi/callApi);stub.go 保非 wasm 平台可构建
├─ clients/ts/             dopdb-client:TS/JS 客户端(WASM 运行时 + 远程调用器),发布 .ts + dist/*.js + *.d.ts
├─ config.toml.example     配置样例
├─ Makefile                常用命令(make test / vet / fmt / build / wasm / ts)
├─ docs/                   设计与使用文档(从这里读起)
│  ├─ 00-overview.md         架构 / 取舍 / 包地图
│  ├─ 01-data.md             数据层:集合 / 方法 / modifiers / 索引 / Find
│  ├─ 02-http.md             HTTP 与安全:@-绑定 / 命令词表 / 权限 / 行级隔离
│  ├─ 03-config.md           配置:schema / env 密钥 / 装配
│  ├─ 04-wasm-ts.md          WASM 桥 / TS·JS 客户端 / /api/<name> 路由
│  └─ RUNBOOK.md             构建 / 测试矩阵 / 部署 / 迁移 / 未竟项
└─ delivery/               云×本地×人 协作运行库(给 agent harness 派发测试任务)
   ├─ kit/                   方法论本体(协议 + 两份手册,跨项目通用)
   ├─ project/               dopdb 项目卡(慢变事实)
   ├─ STATUS.md / ROLES.md / README.md
   └─ rounds/R1/             首回合:在真实 MongoDB 上验证框架的测试派发
```

## 安装

```bash
go get github.com/doptime/dopdb
# 用到 mongostore 时,额外拉驱动:
go get go.mongodb.org/mongo-driver/v2
```

> 说明:`go.mod` 默认不声明 mongo 驱动——只有 `mongostore` 包用它。核心包(`dopdb`/`api`/`httpserve`/`config`/`memstore`)零外部依赖。

## 快速上手

```go
// 1) 定义集合(bson 与 json 标签同名;@uid 类字段从已验证 JWT 注入)
type User struct {
    UID   string `bson:"_id"  json:"_id"`
    Name  string `bson:"name" json:"name" mod:"trim" validate:"required"`
    Email string `bson:"email" json:"email" mod:"trim,lowercase" index:"unique"`
}

// 2) 启动时装配(生产用 mongostore;测试用 memstore)
st, _ := mongostore.New(ctx, uri, "appdb")   // 或 memstore.New()
dopdb.SetDefaultStore(st)
dopdb.SetDefaultCodec(mongostore.BSONCodec{}) // 测试用 memstore.JSONCodec{}

dopdb.RegisterHttp(dopdb.New[string, *User](dopdb.WithCollection("User")))

// 3) HTTP(数据命令 + API 走同一个 Handler)
h := httpserve.NewHandler(httpserve.NewServer(jwtSecret), httpserve.NewPermissions(autoAuth))
http.ListenAndServe(":8080", h)
```

配置驱动的装配见 `docs/03-config.md`;`@`-绑定与 owner 行级隔离见 `docs/02-http.md`。

## 测试

```bash
make test        # 无驱动全套:数据 10 + api 7 + httpserve 11 + config 6 = 34
make vet fmt     # 静态检查 + 格式检查
make build       # 构建无驱动包
make build-mongo # 构建含 mongostore(需先 go get 驱动)
make test-mongo  # mongostore 集成测试(需 DOPTIME_TEST_MONGO_URI)
```

完整测试矩阵、部署与迁移见 `docs/RUNBOOK.md`。

## WASM / TypeScript(用 TS 函数创建 API)

api 核心可编译成 WebAssembly,在任意 JS 运行时里加载;**传入一个带接口类型的 TS/JS 函数**即创建 API,路由默认 `/api/<名称>`(名称取自函数名或显式指定)。

```ts
import { loadDopdb, createApi, configure } from "dopdb-client";

// 服务端/同构:用 TS 函数定义并伺服 API
const db = await loadDopdb();
const greet = db.api((input: { name: string }) => ({ msg: "hi " + input.name })); // -> /api/greet
import { createServer } from "node:http";
createServer(db.nodeListener).listen(8080);

// 前端:调远端 dopdb 服务的 /api/<name>
configure({ baseUrl: "https://api.example.com" });
await createApi("greet")({ name: "Ada" });   // POST {baseUrl}/api/greet
```

```bash
make wasm   # 编译 dopdb.wasm 到 clients/ts/wasm/
make ts     # 上面 + 编译 TS 客户端到 clients/ts/dist/
```

细节(WASM 桥、命名规则、服务端适配器、运行时边界)见 `docs/04-wasm-ts.md`。

## 状态

核心(数据 + api + http + config)在内存 store 上已全量自测(34 测试)。`mongostore` 对真实 MongoDB 的验证由 `delivery/rounds/R1/` 派发给本地 harness 执行(见该目录)。未竟硬化项(原子 scoped-write、scoped HKEYS/HLEN、msgpack、权限持久化)与 TS 重写见 `docs/RUNBOOK.md §未竟`。
