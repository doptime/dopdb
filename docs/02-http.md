# 02 · HTTP 层与安全模型(`@`-绑定 · 命令词表 · 权限 · 行级隔离)

> 这一层让 CRUD 端点消失:前端直接说 `HGET-User?f=@uid`,框架在服务端解析、鉴权、注入身份、隔离行、再落到 MongoDB。本文件讲 URL 文法、安全三件套、API 端点,以及一个请求走完的全程。

## 一个请求的全程

```
GET /HGET-User?f=@uid   (Authorization: Bearer <jwt>)
  │
  1. 解析 CMD-KEY:CMD=HGET, Coll=User;读 ?f= 字段、?ds= 数据源
  2. 验 JWT(HS256/RS256,拒 none;LRU 缓存)
  3. @-替换:?f=@uid → uid=<jwt.uid>(缺 claim 即 fail-closed)
  4. 建 param 表:query+header → 剥离客户端 @-键(防伪) → 注入 @key/@field/@<claim>
  5. 权限闸:Allowed("HGET","User")?(白名单 + AutoAuth)
  6. 查注册表:LookupHttp("User")
  7. owner 作用域:OwnerScope("User", claims) —— 被 scope 但无 claim ⇒ 401
  8. 派发到 dopdb 方法 → JSON 响应
```

## URL 文法

路径最后一段 = `CMD-KEY`;字段走 `?f=`;数据源走 `?ds=`(默认 `default`)。

- `CMD-KEY` 里 **KEY = 集合名**,**`?f=` 的值 = 文档主键**(经 `@`-解析)。
- 段内**没有 `-`** ⇒ 当作 **API 调用**(`Cmd=API`,见末节)。
- 主键 / 字段里可含 `@tag`。

例:
- `GET /HGET-User?f=@uid` → 取调用者自己的 User 文档(self 模式)。
- `POST /HSET-Order?f=o1`(body `{"item":"book"}`)→ 写 Order o1。
- `GET /FIND-Order?q=<urlencoded json>&limit=50` → 查询(净化 + scope)。

## 闭合命令词表

前端只能调这组固定动词,**不是任意查询**——这就是从 Redis 继承的安全属性:

```
HGET HSET HSETNX HDEL DEL HEXISTS HGETALL HKEYS HVALS HLEN HINCRBY HINCRBYFLOAT FIND
```

词表外(且非 API)的命令 → 400。`HINCRBY` 取 `?f=`(文档主键)+ `?field=`(数值字段点路径)+ `?n=`(增量)。

## 安全件一 · `@`-绑定(上下文注入 + 防伪)

`@`-标记在主键 / 字段 / API 入参里被服务端替换:

- `@uuid` / `@nanoid[N]`:服务端生成。
- 其余 `@name`:从**已验证 JWT claim** 取;缺失即报错(fail-closed)。
- 数字 claim 渲染成整数(规避科学计数法)。

**防伪**:建 param 表时**先删掉所有客户端送来的 `@`-前缀键**,再注入服务端的 `@key/@field/@remoteAddr/@host/@method/@path/@rawQuery` 与每个 JWT claim(`@<claim>`)。所以客户端无法用 `?@uid=别人` 顶替身份。

入参结构体里 `json:"@uid"` 的字段,就从注入的 `@uid` 填充。`mergeBody()` 把请求体的非 `@` 字段并进 param,`@`-上下文永远盖过它。

## 安全件二 · 权限白名单

`command::collection::on/off`,键为 `"CMD::Coll"`,对应 doptime 的 `_permissions`(这里 key 是集合名):

```go
perms := httpserve.NewPermissions(false /* 生产:AutoAuth 关 */)
perms.Grant("HGET", "User")
perms.Deny("HGETALL", "User")     // 显式 off 压过 AutoAuth
```

- AutoAuth=true(仅开发):首次见到的 `(cmd,coll)` 自动授予——白名单随开发自然长成「恰好用到的样子」。
- API 调用也过白名单(`API::<endpoint>`)。
- v1 内存实现;集群可用一个 dopdb 的 `_permissions` 集合做后端,只换 `Allowed/Grant` 的实现。

## 安全件三 · owner 行级隔离

Redis 靠 key 命名免费拿到隔离(`userInfo<uid>`);Mongo 没有 key 命名空间,隔离必须是显式谓词。两种模式:

### self 模式(个人数据,不需要 scope)
集合按 uid 做主键,前端用 `?f=@uid`。`@uid` 来自 JWT,你只能读写自己的 `_id`,天然隔离。

### owned-collection 模式(一对多,需要 scope)
```go
dopdb.SetOwnerScope("Order", "owner", "uid")  // 文档字段 owner == JWT claim uid
```
声明后:
- **集合级读**(`HGETALL/HVALS/FIND`):自动注入 `{owner: <uid>}` 谓词,且客户端无法放宽(与其 filter 取 `$and`)。
- **每键操作**(`HGET/HSET/HDEL/HEXISTS/HINCRBY`)对 scoped 集合:
  - 读 → 经 `Find({_id:key} ∧ scope)`,别人的文档即使知道 id 也读不到(404)。
  - 写 → **owner 字段由框架从 scope 强制注入**(客户端伪造不了、也漏不掉),且若该 id 已被他人占有则拒(403)。
  - `HKEYS/HLEN` 对 scoped 集合暂直接 403(安全默认)。
- 被 scope 但请求未带对应 claim ⇒ 401。

> caveat:scoped 写入的防劫持检查目前是 check-then-act(非原子);原子版待 `Store` 加 filtered-upsert。

## 错误 → HTTP 状态

`ErrNoDoc`→404,`ErrForbidden`→403,词表外命令/坏 filter/坏入参→400,JWT 无效/scoped 未鉴权→401,集合未注册→404,API 端点未找到→404。

## 装配

```go
dopdb.RegisterHttp(dopdb.New[string, *User](dopdb.WithCollection("User")))
dopdb.RegisterHttp(dopdb.New[string, *Order](dopdb.WithCollection("Order")))
dopdb.SetOwnerScope("Order", "owner", "uid")

h := httpserve.NewHandler(httpserve.NewServer(jwtSecret), httpserve.NewPermissions(autoAuth))
http.ListenAndServe(addr, h)
```

## API 端点(无 `-` 的路径)

`api.Api(...)` 定义的端点,经 HTTP 走同一个 Handler:

```go
type EchoIn struct {
    UID string `json:"@uid"`   // 从 JWT 注入
    Msg string `json:"msg"`    // 从请求体
}
api.Api(func(in *EchoIn) (map[string]any, error) {
    return map[string]any{"uid": in.UID, "msg": in.Msg}, nil
}, api.WithName("echo"))
```

- 命名:`InDemo`→`api:demo`;客户端用 `/demo` 或 `/api:demo`。
- 流水线(逐字保留 doptime):`解码 → ParamEnhancer → Validate → Func → ResultSaver → ResponseModifier`。
- 入参经同一套 `@`-绑定(`json:"@uid"` 从 JWT 填)。
- 也过权限白名单(`API::echo`)。
- 本地直调:`ep.Func(&EchoIn{...})`,零 HTTP 开销。

详见 `api/api.go` 与 `02` 配套测试 `httpserve/serve_test.go`、`httpserve/api_dispatch_test.go`。
