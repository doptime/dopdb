# 03 · 配置(TOML · 环境变量密钥 · 装配)

> `config` 包从 TOML 文件读运行配置,**密钥与带凭据的连接串只从环境变量取**(RL4),文件里只写「持有它的环境变量名」。无外部依赖(内置一个覆盖本 schema 的小 TOML reader),所以能在无 Mongo 环境下编译与测试。

## 文件格式

完整示例见仓库根 `config.toml.example`:

```toml
[http]
addr           = ":8080"
jwt_secret_env = "DOPTIME_JWT_SECRET"    # 持有 HS256 密钥 / RS256 PEM 的环境变量名
auto_auth      = false                   # 仅开发可 true;生产必须 false
cors_origins   = ["https://app.example.com"]

[[mongo]]
name    = "default"                      # 必须有一个 name="default"
uri_env = "DOPTIME_MONGO_URI"            # 连接串环境变量(可含凭据);env 优先于 uri
uri     = "mongodb://localhost:27017"    # 字面回退,仅开发
db      = "appdb"

[[mongo]]
name = "analytics"
uri  = "mongodb://localhost:27017"
db   = "analytics"
```

## 解析规则

- `jwt_secret_env` 指向的环境变量被读入 `HTTP.JWTSecret`;若该变量未设,回退到文件里的 `jwt_secret`(不推荐,仅开发)。
- 每个 `[[mongo]]` 的 `uri_env` 指向的环境变量覆盖 `uri`;未设则用字面 `uri`。
- 注释 `#` 在引号外才生效(`addr = "host#x:80"` 里的 `#` 不会被当注释)。
- 支持的值类型:字符串、整数、浮点、布尔、字符串数组。

## 校验(Load 时强制)

- 至少一个 `[[mongo]]`,且必须有 `name="default"`。
- 数据源名不重复,每个都有非空 `uri` 与 `db`。
- 解析密钥后 `JWTSecret` 非空,否则报错(点名该填哪个环境变量)。

## 非致命告警(`cfg.Warnings()`)

启动时建议打印:`auto_auth` 开着会告警(生产别开);`uri` 字面串里含 `@`(疑似把凭据写进了文件)会告警建议改走 `uri_env`。

## 装配进框架

```go
cfg, err := config.Load("config.toml")
if err != nil { log.Fatal(err) }
for _, w := range cfg.Warnings() { log.Println("[config]", w) }

ctx := context.Background()
st, err := mongostore.NewFromSource(ctx, cfg.Default())   // 需 mongo 驱动
if err != nil { log.Fatal(err) }
dopdb.SetDefaultStore(st)
dopdb.SetDefaultCodec(mongostore.BSONCodec{})

// 注册集合、声明 owner-scope……(见 02-http.md)

h := httpserve.NewHandler(
    httpserve.NewServer(cfg.HTTP.JWTSecret),
    httpserve.NewPermissions(cfg.HTTP.AutoAuth),
)
log.Fatal(http.ListenAndServe(cfg.HTTP.Addr, h))
```

多数据源(迁移期):`src, _ := cfg.Source("analytics")` → `mongostore.NewFromSource(ctx, src)` → 给某集合 `dopdb.WithStore(...)`。

## 与 doptime 配置的差异

- doptime 走 `github.com/doptime/config`(cfgredis/cfghttp),数据源是 Redis 服务器表;dopdb 的数据源是 Mongo `{uri, db}`。
- doptime 部分配置用「环境变量里塞 JSON」(如 `HTTP={"AutoAuth":true}`);dopdb 统一用 TOML 文件 + 环境变量只放密钥,边界更清楚。
