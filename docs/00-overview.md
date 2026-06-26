# 00 · dopdb 总览(架构 · 取舍 · 包地图)

## 它是什么

dopdb 把 `doptime` + `redisdb` + `doptime-client` 合并重写,后端换成 MongoDB。核心主张:**一份 schema 同时产出类型、校验、带类型的客户端、服务端**,不生成代码、不前后端各写一遍。

## 两套对等实现

dopdb 不是“一个 Go 服务 + 一个 TS 客户端”,而是 **两套对等的完整实现**,共享同一线协议与全部特性:

- **Go**(根包 `dopdb` + `api/` + `httpserve/` + `config/`):服务端,直连 MongoDB 驱动。
- **TypeScript**(`ts/`):既能在 Node 跑同样的服务端,也能在浏览器做带类型客户端。

同样的 URL 方案(`/api/<cmd>/<coll>` + `?ds=`)、同样的命令词表、同样的 `@`-绑定 / 行级隔离 / 权限模型。两端可混用。

## 关键取舍(本轮确定)

| 取舍 | 说明 |
|---|---|
| 直连 Mongo,删 Store 抽象 | 删除 `Store`/`Codec` 接口与 `memstore`/`mongostore`;根包直接用 driver v2,把 Mongo 当 Mongo 用 |
| 多数据源 + `?ds=` | 配置可多个 `[[mongo]]`;请求用 `?ds=<name>` 选源,缺省 `default`;源不进路径 |
| 闭合命令词表 | 见 `02-http`;新增 `hmset/hmget/count/findone/watch` |
| `@`-绑定 | 服务端注入身份/上下文,客户端 `@`-键剥除(防伪造) |
| 行级隔离 | owner-scope:整集合读取强制 `{owner: 我}` |
| 权限默认拒绝 | `Grant/Deny/Allowed` + JSON 持久化;**无 AutoAuth** |
| JWT | HS256 + RS256;拒绝 `none` |
| watch | change stream → SSE(需副本集) |
| API 极简 | `decode → Validate → Func`,去掉钩子链 |
| WASM 退场 | TS 是独立等效实现,不再 WASM 桥接 |

## 包地图(Go)

```
dopdb.go          泛型 Collection[K,V]:原生可信 API(HGet/HSet/Find/HIncrBy/...)
types.go          M / FindOpt / SortKey / IndexSpec / ErrNoDoc / ErrForbidden
mongo.go          mongoBackend(直连 driver v2 的 CRUD/索引/watch)+ Datasources 注册表
http_accessor.go  HttpAccessor:类型擦除桥(把 V 装箱成 any 供 HTTP 派发)+ owner-scope 策略
modifiers.go      写入修饰器(时间戳、@-绑定字段)
sanitize.go       过滤器消毒(拒绝 $-运算符注入到键)
api/api.go        函数式 API 端点注册与派发
httpserve/        context(路由+JWT+@-解析) / serve(派发+watch) / permission / jwt / bootstrap(Serve)
config/config.go  TOML 读取(多 [[mongo]];密钥从环境变量解析,绝不入文件)
```

> 已删除的包/文件:`store.go`、`memstore/`、`mongostore/`(逻辑内联进 `mongo.go`)、`wasm/`、旧的 `clients/`(WASM 版)。

## 两个面(Go 与 TS 一致)

- **可信面**:服务端内部直接读写,无 scope、无 JWT。Go 用 `Collection` 原生方法;TS 用 `serverDb(schema, db)`。
- **受控面**:对外 HTTP,强制 JWT `@`-绑定、owner-scope、权限。Go 用 `httpserve`;TS 用 `serve(cfg)`。

继续阅读:`01-data`(数据层)、`02-http`(HTTP/安全)、`03-config`(配置)、`04-typescript`(TS 等效实现)、`RUNBOOK`。
