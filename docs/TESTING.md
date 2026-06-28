# 测试布局与运行(标准套件)

回合期累积的测试已收敛为下述标准项目测试文件(被取代的 `httpserve/interop_test.go`——Go 单端、已被 `conformance_test.go` 取代——已删除)。

## Go 测试文件

| 文件 | 覆盖 | 需 Mongo |
|---|---|---|
| `dopdb_test.go` | 根包单测:Collection 方法、键编解码、编码 | 否 |
| `api/api_test.go` | 函数式 API 流水线(decode→validate→func) | 否 |
| `config/config_test.go` | 配置加载/解析 | 否 |
| `httpserve/helpers_test.go` | 共享测试 helper(`setupHandler`/`tokenFor`/`do`/`decodeObj`) | — |
| `httpserve/api_dispatch_test.go` | HTTP 路由/分发、命令词表、错误形状 | 否 |
| `httpserve/permission_persist_test.go` | 权限 `SaveJSON`/`LoadJSON` | 否 |
| `httpserve/integration_test.go` | 真 Mongo 端到端 CRUD/原子/owner 隔离 | **是** |
| `httpserve/conformance_test.go` | **Go↔TS 一致性**:起 TS 子进程对打 Go,逐命令比对 status+code+body | **是** |

## TypeScript 测试文件(`ts/test/`)

`schema` `client` `server` `permission` `sanitize` `prepare` `indexes` `config` `hardening` `browser-safety` `spec-export` `next-handler` `types.test-d` `watch-e2e` `watch-reconnect`。
其中 `watch-e2e` 需真 Mongo(副本集),无 `DOPDB_TEST_MONGO_URI` 自动 skip;其余用内存替身,无需 Mongo。

## 运行

```bash
# TS:全套(无 Mongo 的自动 skip)
( cd ts && npm test )

# Go:不需 Mongo 的部分(单测 + 路由 + 配置 + 权限持久化)
go test ./... -short    # 或不带 -short 但不设 DOPDB_TEST_MONGO_URI:需 Mongo 的会 skip

# Go:需真 Mongo(副本集)的集成 + 一致性
export DOPDB_TEST_MONGO_URI="mongodb://127.0.0.1:27017/?replicaSet=rs0"
# 若 node 不在 PATH 默认位置(conformance 起 TS 子进程):export DOPDB_TS_NODE=/path/to/node
go test ./httpserve -run Integration -v
go test ./httpserve -run Conformance -v
go test ./httpserve -run IntegrationWatch -v
```

## 约定

- 需真 Mongo 的测试**一律**用 `DOPDB_TEST_MONGO_URI` 门控(缺则 `t.Skip` / TS `{ skip }`),不得在无 Mongo 环境硬挂。
- **两端一致性只认 `conformance_test.go`**:同一组请求分别打 Go 与 TS、diff 为空。单端集成测试(如旧 interop)**不能**当作一致性证据。
- 测试不改被测的门槛/守卫来凑过;关键数字以真实 stdout 为准。
