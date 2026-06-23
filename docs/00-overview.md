# 00 · dopdb 总览(架构 · 取舍 · 包地图)

> dopdb 是 `doptime` + `redisdb` 的合并重写,把数据后端从 Redis 换成 MongoDB,先以 Go 落地,最终用 TS 重写以消除前后端分离。本文件讲**整体形状**;数据层看 `01-data.md`,HTTP/安全层看 `02-http.md`,配置看 `03-config.md`,构建/运维看 `RUNBOOK.md`。

## 一句话

把 doptime 的三个招牌——**`@`-绑定上下文模型**、**闭合命令词表**、**API 钩子流水线**——原样搬到 MongoDB 上,丢掉 Redis 才有的东西(ZSet/Set 抽象、阻塞列表、Redis-stream RPC 交换机),并顺手修掉 redisdb 的几处旧设计缺陷。

## 核心架构决定:窄 `Store` 接口解耦

整个类型层只依赖一个窄接口 `dopdb.Store`(`EnsureIndex/Put/PutIfAbsent/PutMany/Get/GetMany/Delete/Exists/IDs/All/Count/Incr/Find`)。MongoDB 驱动**只出现在一个文件** `mongostore/mongostore.go` 里。带来三点:

1. **可测**:核心逻辑用内存 `Store`(`memstore`)就能全量跑,不需要起 Mongo。
2. **可换**:换 DocumentDB/FerretDB/未来的 TS 实现,只重写一个适配器。
3. **格式即契约**:文档以 BSON 落盘(可查询、可被 Compass/mongosh 检视、语言中立),不再像 redisdb 那样把 msgpack 串成全框架的隐式契约。

```
应用代码
  │  api.Api[…] / dopdb.New[K,V] / httpserve.Handler
  ▼
dopdb(类型层:Collection[K,V]、modifiers、sanitize、http_accessor)
  │  依赖:dopdb.Store 接口(窄)
  ▼
mongostore(唯一 import 驱动)  ── 或 ──  memstore(内存,测试用)
  ▼
MongoDB                                      内存 map
```

## 包地图

| 包 | 职责 | 是否 import 驱动 | 沙箱可测 |
|---|---|---|---|
| `dopdb` | 类型化 `Collection[K,V]` + 写入期 modifiers + 过滤器净化 + 类型擦除的 `HttpAccessor` 桥 + owner-scope 策略 | 否 | ✅ |
| `api` | `api.Api(...)` 端点定义 + `CallByMap` 钩子流水线 + 注册表 | 否 | ✅ |
| `httpserve` | HTTP 层:URL 解析、JWT、`@`-绑定、权限白名单、owner 行级隔离、命令派发、API 派发 | 否 | ✅ |
| `config` | TOML 配置读取 + 环境变量解析密钥 + 校验 | 否 | ✅ |
| `memstore` | 内存 `Store` + JSON codec(给应用做单测) | 否 | ✅ |
| `mongostore` | MongoDB `Store` 适配器 + `BSONCodec` | **是** | ❌(需驱动) |

## 三种调用路径(RPC 已去掉)

doptime 有四种(本地、HTTP、Redis-stream RPC、HTTP RPC)。dopdb 保留两种:

- **本地函数调用**:`ep := api.Api(fn)` 后 `ep.Func(in)`,零开销。
- **HTTP**:数据命令(`HGET-Coll?f=...`)与 API(`/endpoint`),经同一个 `httpserve.Handler`。

Redis-stream「数据交换机」RPC 与 HTTP-直连 RPC **都不保留**。

## 从 doptime / redisdb 保留了什么

| 保留(引擎无关) | 在哪 |
|---|---|
| `@`-绑定:`@id`/`@uuid`/`@nanoid` 从已验证 JWT 注入,客户端伪造件先剥离 | `httpserve/context.go` |
| 闭合命令词表(前端只能调固定动词,不是任意查询) | `httpserve/serve.go` `dataCommands` |
| `command::collection::on/off` 权限白名单 + AutoAuth | `httpserve/permission.go` |
| API 钩子流水线 `ParamEnhancer→Validate→Func→ResultSaver→ResponseModifier` | `api/api.go` `CallByMap` |
| API 命名规则(`InDemo`→`api:demo`,剥前后缀) | `api/api.go` `apiName` |

## 相对 redisdb 改了什么(修掉的旧缺陷)

| 旧缺陷(redisdb) | dopdb 的处理 |
|---|---|
| modifiers/校验/时间戳跑在**读**路径、且只对 HTTP 生效 | 一律在**写**路径、对所有路径生效(`modifiers.go`) |
| key 编解码三套不一致(struct key 无法 round-trip) | 单一规范:string→自身、整数→base10、其余→JSON;`_id` 永远是字符串 |
| 批量读静默丢解码失败项 | 失败即返回 error(fail-fast) |
| `redis.Nil` 错误泄漏到所有调用点 | 自有哨兵 `ErrNoDoc` / `ErrForbidden` |
| 非原子计数器(Go 端 read-modify-write) | `HIncrBy` 走 `$inc`,真原子 |
| msgpack-at-rest(不可查询) | BSON-at-rest(可查询、语言中立) |

## 丢掉了什么(Mongo 不擅长 / 用不上)

- **ZSet / Set 作为存储抽象** → 由「带索引的集合 + 受限 `Find` 查询」取代。
- **阻塞列表操作**(BLPOP 等) → Mongo 无对应,去掉。
- **Redis-stream RPC 交换机** → 去掉(见上)。

## 已知 caveat(收尾项,见 RUNBOOK §未竟)

- owner-scoped 写入的「防劫持」检查目前是 check-then-act(非原子);原子版需给 `Store` 加 filtered-upsert 原语。
- scoped 集合的 `HKEYS/HLEN` 暂直接 403(安全默认)。
- HTTP 响应/请求 v1 仅 JSON(未做 msgpack)。
