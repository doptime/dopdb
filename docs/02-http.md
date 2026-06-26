# 02 · HTTP 层与安全模型(URL · `@`-绑定 · 命令词表 · 权限 · 行级隔离 · watch)

HTTP 层只把 schema 暴露成一组**闭合**的数据命令 + 函数式 API,并在边界强制 JWT `@`-绑定、行级隔离、权限。Go 在 `httpserve/`,TS 在 `ts/src/server.ts`,行为一致。

## URL 方案

- **数据命令**:`/api/<cmd>/<coll>` —— `/api/` 后两段,首段是命令、次段是集合。
- **函数式 API**:`/api/<name>` —— `/api/` 后一段。
- **数据源**:查询参数 `?ds=<name>`,缺省 `default`。**数据源不进路径**。
- **键**:查询参数 `?f=`(可多值,用于 hmget/hdel 等);`?f=@uid` 触发 `@`-解析。
- **查询**:`find/findone/count` 的过滤器走 **POST body**(JSON);`?limit= &skip= &s=<排序JSON> &p=<投影JSON>`。

区分规则:`/api/` 后 ≥2 段且首段在命令词表内 → 数据命令;否则按 `/api/<name>` 当函数式 API。

## 命令词表(闭合)

```
hget hset hsetnx hdel del hexists hgetall hkeys hvals hlen
hincrby hincrbyfloat hmset hmget count find findone watch
```

| 命令 | 方法 | 语义 |
|---|---|---|
| `hget` | GET | 取一条(scoped:仅自己的,否则 404) |
| `hset` | POST | upsert 一条(body 为值;scoped 写他人 id → 403) |
| `hsetnx` | POST | 不存在才写 |
| `hdel`/`del` | POST/GET | 删一/多条(`?f=` 多值) |
| `hexists` | GET | 是否存在 |
| `hgetall` | GET | 全部值(scoped 仅自己的) |
| `hkeys`/`hvals`/`hlen` | GET | 键 / 值 / 计数 |
| `hincrby`/`hincrbyfloat` | GET | 原子 `$inc`(整数 / 浮点) |
| `hmset` | POST | 批量写,body 为 `{id: {字段...}, ...}` |
| `hmget` | GET | 批量取,`?f=` 多值,按输入顺序对齐(缺失为 null) |
| `count` | POST | 计数(可带过滤器 body;scoped 叠加) |
| `find` | POST | 查询数组(过滤器 body + `limit/skip/s/p`) |
| `findone` | POST | 查询首条(无则 404) |
| `watch` | GET | change stream → SSE 实时订阅 |

## `@`-绑定(防伪造的身份注入)

服务端为每个请求构造上下文:已验证的 JWT claims + 服务端注入的 `@key`(集合名)、`@field`(默认=记录键)、`@remoteAddr`、`@host`、`@method`、`@path`、`@rawQuery`。

- 写入时,值里标了 `@uid` 等的字段由上下文填充,**客户端传来的 `@`-键一律剥除**——身份无法伪造。
- 键里的 `@`-记号:`@uuid`/`@nanoid` 现场生成;`@<claim>` 取自 JWT。所以 `?f=@uid` 表示“我自己的那条记录”;对应 claim 缺失则**失败关闭**(拒绝)。

## 行级隔离(owner scope)

Redis 时代靠 key 命名(如 `userInfo<uid>`)天然隔离;Mongo 没有键命名空间,隔离必须是显式谓词。

- 声明:`dopdb.SetOwnerScope("orders", "owner", "uid")`(文档 `owner` 字段 == JWT claim `uid`)。TS:集合 `.ownerScope("owner")`。
- 整集合读取(hgetall/hkeys/hlen/find/...)被强制 AND 上 `{owner: 我}`,客户端无法放宽。
- 按键操作(hget/hset/hdel 的 scoped 版)用 `{_id, owner}` 交集 + 原子过滤 upsert,杜绝“先查再写”竞态;写他人 id → 403,读他人 id → 404。
- 若集合声明了 scope 但请求无对应 claim → 一律拒绝。

## 权限(默认拒绝)

键为 `COMMAND::collection`,**默认拒绝**:未显式授权的组合一律 403。函数式 API 用 `API::<name>` 同样受门控。

```go
p := httpserve.NewPermissions()      // 空集 = 全拒
p.Grant("HGET", "users"); p.Grant("HSET", "users")
p.Deny("DEL", "users")               // 显式拒(覆盖 Grant)
p.SaveJSON("perm.json"); q, _ := httpserve.LoadJSON("perm.json")
```

> 已**移除 AutoAuth**(首用即授权)。授权一律显式;集群可用共享集合承载同样的 Grant/Deny/Allowed。

## watch(change stream → SSE)

`GET /api/watch/<coll>` 返回 `text/event-stream`,每个变更一行 `data: {"type","id","doc"}`。

- owner-scope:管道按 `fullDocument.<owner>` 过滤;**delete 无 fullDocument,scoped 下不投递**。
- 断线续传:Go 用 resume token 自动重连;TS 客户端发 `Last-Event-ID`,服务端 `resumeAfter`。
- **需要 MongoDB 以副本集运行**(change stream 的前提)。

## JWT

`Authorization: Bearer <token>`。支持 **HS256**(HMAC 密钥)与 **RS256**(PEM/SPKI 公钥验签),拒绝 `none` 与未知算法;校验 `exp`。

## 一行起服务(Go)

```go
cfg, _ := config.Load("config.toml")            // 读所有 [[mongo]]
log.Fatal(httpserve.Serve(cfg, httpserve.WithPermissions(perms)))
// 连接所有数据源 → SetDatasources → NewHandler → CORS → ListenAndServe
```
