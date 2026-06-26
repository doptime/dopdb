# 03 · 配置(TOML · 环境变量密钥 · 装配)

`config/`(Go)与 `ts/src/config.ts`(TS)读取同一套 TOML schema。两者都**不引入第三方依赖**(自带刚好覆盖本 schema 的小解析器),密钥与连接串一律从**环境变量**解析,绝不从文件读取明文。

## Schema

```toml
[http]
addr           = ":8080"
jwt_secret_env = "DOPTIME_JWT_SECRET"    # 持有 HS256 密钥 / RS256 PEM 公钥的环境变量名
cors_origins   = ["https://app.example.com"]

# 可写多个 [[mongo]];必须有一个 name = "default"
[[mongo]]
name    = "default"
uri_env = "DOPTIME_MONGO_URI"            # 持有连接串(可含账号密码)的环境变量名
uri     = "mongodb://localhost:27017"    # 字面回退(仅开发);env 优先
db      = "appdb"

[[mongo]]
name = "analytics"
uri  = "mongodb://localhost:27017"
db   = "analytics"
```

> 已**移除 `auto_auth`**(随 AutoAuth 一起删除)。权限默认拒绝,授权一律显式(见 `02-http`)。

## 解析与校验

- `jwt_secret`:由 `jwt_secret_env` 指定的环境变量解析;为空则报错。
- 每个 `[[mongo]]` 的 `uri`:`uri_env` 指定的环境变量优先,否则用字面 `uri`。
- 校验:至少一个 `[[mongo]]`;必须存在 `name="default"`;名字唯一;每个源都要有 `uri` 与 `db`。
- `Warnings()`:把“字面 uri 里带账号密码(应改用 `uri_env`)”等非致命风险列出,供启动时打印。

## 装配(Go)

```go
cfg, err := config.Load("config.toml")
if err != nil { log.Fatal(err) }
for _, w := range cfg.Warnings() { log.Println("warn:", w) }

// 一行起服务:连接所有数据源 → SetDatasources → Handler → CORS → 监听
log.Fatal(httpserve.Serve(cfg, httpserve.WithPermissions(perms)))
```

`httpserve.Serve` 内部用 `cfg.Mongo` 构造 `[]dopdb.DatasourceConfig` 调 `ConnectDatasources`,再 `SetDatasources`。请求用 `?ds=<name>` 选源,缺省 `default`。

## 装配(TS)

```ts
import { serveFromConfig, Permissions } from "dopdb/server";
const perms = new Permissions().grant("HGET", "users").grant("HSET", "users");
await serveFromConfig("config.toml", { schema, permissions: perms });
// serveFromConfig 会把所有 [[mongo]] 作为数据源载入,并传入权限
```

## 相关环境变量

- `DOPTIME_JWT_SECRET`(示例名):HS256 密钥或 RS256 PEM 公钥。
- `DOPTIME_MONGO_URI`(示例名):默认数据源连接串。
- `DOPDB_TEST_MONGO_URI`:**仅测试用**;设置后运行集成测试(watch 需副本集)。
