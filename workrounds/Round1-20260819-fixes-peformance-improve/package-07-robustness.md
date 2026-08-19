# package-07 · 健壮性与 DoS 面

> 严重度 🟠。对应审计 S2-12、S2-14、S2-15、S2-17、S2-19、S2-22。

## 问题与修复

### 7.1 SQL 解析器无嵌套深度上限（S2-12）

`parsePrimary` 对每个 `(` 递归 `parseOr → parseAnd → parseNot → parsePrimary`
（每层约 4 个栈帧），没有深度计数。请求体上限 1 MiB ≈ 25 万个括号。

持读权限者（SQL 在 ReadOnly 位）一个 POST 就能让单个 goroutine 栈涨到数十 MB，
并发几十个即 OOM。FIND 的 JSON filter 一直有深度限制，SQL 没有。

**修复**：`maxSQLDepth = 64`，超限返回语法错误（400）而不是栈溢出。

### 7.2 服务器无超时 + 监听失败静默（S2-14）

```go
srv := &http.Server{Addr: ..., Handler: ...}   // 无任何超时
go func() {
    if err := srv.ListenAndServe(); err != http.ErrServerClosed {
        // 注释写 "log but don't panic"，函数体是空的
    }
}()
```

- 无 `ReadHeaderTimeout` → 建连不发头的客户端可以挂住连接数小时 → FD 耗尽
- 端口被占时 `ServeWithHandle` **正常返回**，进程存活、不服务任何请求、零日志

**修复**：
- `ReadHeaderTimeout=10s`、`ReadTimeout=60s`、`IdleTimeout=120s`。
  **`WriteTimeout` 保持 0** —— watch 是长连接，写超时会把它切断。
- 监听错误经 `ServeHandle.ListenErr` 暴露；启动后短暂等待，端口冲突时
  `ServeWithHandle` **直接返回 error** 而不是假装成功。

### 7.3 watch 订阅失败被静默吞掉（S2-15）

```go
_ = ha.HttpWatch(ctx, c.DB, scope, emit)   // 错误显式丢弃
```

KVRocks 抖动使 Subscribe 失败 → SSE 头已写 + **永无事件永无错误**。客户端以为在监听，
事件全丢。这比 500 更危险：500 会触发告警，静默不会。

**修复**：出错时在流内发 `event: error`；ping 协程用 WaitGroup join（迟到的 ping 写
可能落到已被复用的 keep-alive 连接上）。

### 7.4 TS 按 content-length 限流，chunked 不受限（S2-17）

```ts
Number(req.headers.get("content-length") ?? "0") > MAX_BODY
```

chunked / HTTP2 / 被代理重新分帧的请求没有 content-length → 读到 `0` → 放行 →
随后全量读入 → **未认证请求造成无界分配**。Node 独立 `serve()` 一直是按累计字节截断的，
两套 handler 不一致。

**修复**：web 适配器改为 `getReader()` **按到达字节计数**，超限 413 并 cancel。

### 7.5 TS `$regex` 走回溯引擎（S2-19）

`new RegExp(pattern).test(v)` 运行时构造、无超时、无长度限制，而 `find` 是 **O(全集合)**
扫描。ReadOnly 可达的
`{"name":{"$regex":"(a+)+$"}}` → 每文档指数回溯 × 全集合 → **事件循环冻结，整进程停摆**
（含 watch 和其它所有请求）。Go 侧走 RE2，线性时间，没有这个问题。

**修复**（与 package-08 的性能优化同一处代码）：
- 模式预编译并缓存（顺带是性能修复）
- 长度上限 512
- 拒绝嵌套量词 `\([^)]*[+*][^)]*\)\s*[+*{]` —— 保守，会误拒一些合法模式，
  **对共享事件循环来说这是正确的取舍**

### 7.6 后端故障被吞成"不存在"（S2-22）

`checkOwner` 把 owner 哈希的**真实读取错误**和 `ErrForbidden` 一样上抛，而所有非 Hash
读路径把任何 err 当"不存在"：LRange → `[]`、LLen → 0、SMembers → `[]`、
SIsMember → false、ZScore → ErrNoDoc、StrGet → ErrNoDoc。

KVRocks 抖动期间客户端收到 **200 + 空列表**；缓存层会把"数据没了"缓存下来。而写路径
同类错误返回 500 —— 读写表现不一致，排障极难。

**修复**：只有 `ErrForbidden` 映射为"不存在"，redis 错误原样上抛 → 500。

## 判断为"不改"的一条

**watch 续传 / 客户端 `Last-Event-ID` 死代码。**

服务端从不发 `id:` 行，所以客户端的重连解析恒为 null、头永远不发。报告建议"清理或
补齐"。

**我选择两者都不做，理由是**：发 `id:` 却没有回放缓冲，等于给客户端一个**假的续传承诺**
——比现在"明确没有"更糟。真做续传需要环形缓冲区 + 单调序号，那是一个功能，不是一次
清理。删客户端代码则会让未来加回放时需要改客户端。

处理方式：把现状**如实写进文档**（`docs/02-http.md`、`docs/04-typescript.md`）：
不发 `id:`、忽略 `Last-Event-ID`、重连从头开始、断开期间事件丢失、只能看到经 dopdb
的写入。

## 涉及文件

| 文件 | 类型 |
|---|---|
| `sql.go` | 修改（深度上限） |
| `httpserve/bootstrap.go` | 修改（超时、ListenErr） |
| `httpserve/serve.go` | 修改（watch 错误上抛、ping join） |
| `ts/src/server.ts` | 修改（流式 body 限流） |
| `ts/src/query.ts` | 修改（正则缓存 + ReDoS 守卫） |
| `list.go` `set.go` `zset.go` `string.go` `kvrocks.go` | 修改（错误不吞） |

## 验证

```bash
go test -count=1 ./...
cd ts && npm test
```

手工：

```bash
# SQL 深度
python3 -c "print('SELECT * FROM t WHERE ' + '('*200 + 'a=1' + ')'*200)" \
  | curl -XPOST --data-binary @- ".../api/sql/t" -H "Authorization: $TOK"   # 期望 400

# 端口冲突
# 占用端口后调用 ServeWithHandle → 期望返回 error，而不是"成功"

# ReDoS（TS 引擎）
curl -XPOST ".../api/find/<coll>" -d '{"name":{"$regex":"(a+)+$"}}'         # 期望 400/无结果，进程不冻结
```

## 验收标准

1. 深嵌套 SQL 返回 400，进程不崩。
2. 端口被占用时 `ServeWithHandle` 返回 error。
3. 慢速不发头的连接在 ReadHeaderTimeout 后被断开；**watch 长连接不受影响**。
4. watch 订阅失败时客户端收到 `event: error`，不是静默空流。
5. TS 的 chunked 大请求体返回 413，进程内存不飙升。
6. 嵌套量词正则被拒，事件循环不冻结。
7. 后端不可用时读路径返回 5xx，**不是** 200 + 空结果。

## 风险与影响

- ReadTimeout=60s 对超大请求体的慢速上传可能偏紧；1 MiB 上限下应无影响。
- ReDoS 守卫是保守的，会误拒少数合法模式（例如 `(ab+)+`）。已在代码注释中写明这是
  刻意的取舍。
