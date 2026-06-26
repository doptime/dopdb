# RUNBOOK · dopdb 构建 / 测试 / 运行 / 迁移

## 前置

- **Go** ≥ 1.22(driver `go.mongodb.org/mongo-driver/v2`)。
- **Node** ≥ 20(TS 实现;`mongodb` ^6 为 peer 依赖)。
- **MongoDB**:普通实例即可跑大部分集成测试;**`watch`(change stream)需副本集**。

## Go:构建与测试

```bash
make build         # go build ./...(根包直连 driver,需联网拉取一次 driver 模块)
make vet           # go vet ./...
make fmt-check     # gofmt 校验
make test          # go test ./...(集成测试在未设 DOPDB_TEST_MONGO_URI 时自动跳过)
make test-mongo    # DOPDB_TEST_MONGO_URI=mongodb://... 跑集成测试(watch 需副本集)
```

- **单测**(`api/`、`config/`、`httpserve/` 的 api-dispatch/permission):无需数据库。
- **集成测试**(根包 `dopdb_test.go`、`httpserve/integration_test.go`):需 `DOPDB_TEST_MONGO_URI`,各自用一次性数据库,结束即 drop。

```bash
# 例:本地副本集(单节点)便于跑 watch
DOPDB_TEST_MONGO_URI="mongodb://localhost:27017/?replicaSet=rs0" make test-mongo
```

## TypeScript:构建与测试

```bash
make ts            # cd ts && npm install && npm run build
make ts-typecheck  # tsc -p tsconfig.json --noEmit(strict)
make ts-test       # node --import tsx --test test/*.test.ts
```

TS 单测多数无需真库(`server.test.ts` 用注入的内存假 Mongo;`config.test.ts` 仅解析 TOML)。

## 运行(Go)

```toml
# config.toml(密钥从环境变量解析)
[http]
addr = ":8080"
jwt_secret_env = "DOPTIME_JWT_SECRET"
cors_origins = ["https://app.example.com"]
[[mongo]]
name = "default"
uri_env = "DOPTIME_MONGO_URI"
db = "appdb"
```

```bash
export DOPTIME_JWT_SECRET="...HS256 密钥或 RS256 PEM..."
export DOPTIME_MONGO_URI="mongodb://user:pw@host:27017/?authSource=admin"
./your-server   # 内部:config.Load → httpserve.Serve(cfg, WithPermissions(perms))
```

请求示例:

```bash
TOKEN="Bearer <jwt>"
# 写(默认数据源)
curl -XPOST "localhost:8080/api/hset/users?f=u1" -H "Authorization: $TOKEN" -d '{"name":"Ada","email":"ada@x.io","age":30}'
# 读(指定数据源)
curl "localhost:8080/api/hget/users?ds=analytics&f=u1" -H "Authorization: $TOKEN"
# 我自己的记录
curl "localhost:8080/api/hget/profiles?f=@uid" -H "Authorization: $TOKEN"
# 查询(过滤器走 body)
curl -XPOST "localhost:8080/api/find/users?limit=20" -H "Authorization: $TOKEN" -d '{"age":{"$gte":18}}'
# 实时订阅(SSE)
curl -N "localhost:8080/api/watch/users" -H "Authorization: $TOKEN"
```

## 从旧版本迁移(要点)

- **URL**:数据命令从旧的 `CMD-KEY` 段改为 `/api/<cmd>/<coll>`;数据源从路径段改为 `?ds=<name>`。
- **Store/Codec**:已删除;若曾依赖 `memstore`/`mongostore`/`WithStore`,改为直接 `New[...]` + `SetDatasources`/`ConnectDatasources`。
- **权限**:`auto_auth` 已移除;改为显式 `Grant`(或加载 JSON)。默认拒绝。
- **WASM**:已退场;TS 是独立等效实现,直接用 `dopdb/client` 与 `dopdb/server`。
- **API 钩子**:`ParamEnhancer/ResultSaver/ResponseModifier` 已去除,仅保留 `Validate`。

> 注意:本仓库 Go 代码绑定 driver v2,请在本机 `go build ./...` 确认 API 细节;TS 已通过严格类型检查与全部单测。
