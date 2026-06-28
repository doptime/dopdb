# 回执 receipt · dopdb · R8(本机实证,2026-06-28)

> 本回合 = R8 收尾验证。逐条抄录 §3 硬判据的**真实 stdout**(PASS/FAIL 计数原样),不写"应该过"。
> **环境**:macOS arm64 · go1.24.5 · Mongo 副本集(`mongodb-rs` 容器,`rs0` PRIMARY @ localhost:27017)· TS 运行 node v25.2.1(`/opt/homebrew/bin/node`)。

## 环境/收尾发现(诚实记录,非代码缺陷)

1. **`dopdb.go` 本地工作树被误删**(`git status` 显示 `D dopdb.go`,但 HEAD 内文件完好)。已 `git checkout HEAD -- dopdb.go` 恢复——这是恢复受跟踪内容,非代码改动。**若不恢复,M1 编译会报 `http_accessor.go: undefined: Collection`**。恢复后一切正常。该文件含 opus 盲写的 `HttpOn` 方法。
2. **默认 `node` 是 v19.0.0,`--import tsx` 加载器不注册**(报 `ERR_UNKNOWN_FILE_EXTENSION`)→ 直接导致 `npm test`(#6)与 Go conformance 的 TS 子进程(M5)启动失败。改用 `/opt/homebrew/bin/node`(**v25.2.1**)后两者均正常——正是 directive §4 预置的 `DOPDB_TS_NODE` 覆盖场景。**这不是代码缺陷**,是本机 node 版本问题;不修代码,只换 node 路径。

## §3 逐条真实输出

### M1 — `go build ./... && go vet ./... && gofmt -l .`(恢复 dopdb.go 后)
```
go build ./...   →  exit 0
go vet ./...     →  exit 0
gofmt -l .       →  (空输出 = 全部已格式化)
```
**结论:PASS**(opus 盲写的 `perms.go` + `dopdb.go::HttpOn` + `httpserve/serve.go` 门 **编译通过**,含 `Perm` 常量块、`HashAll = All`、门调用——directive §4 点名的预期风险点**均无报错**)。

### M2 — `go test ./httpserve -run Integration -v`(DOPDB_TEST_MONGO_URI=副本集)
```
=== RUN   TestIntegrationHTTPRoundTrip       --- PASS (0.21s)
=== RUN   TestIntegrationOwnerScope          --- PASS (0.14s)
=== RUN   TestIntegrationMultiDatasource     --- PASS (0.27s)
PASS
ok  github.com/doptime/dopdb/httpserve  1.756s
```
**结论:PASS(3/3)**。

### M3 — directive 写的 `go test ./httpserve -run IntegrationWatch -v`
```
PASS
ok  github.com/doptime/dopdb/httpserve  0.561s [no tests to run]
```
**诚实标注:这条按字面是"无可运行测试"**——httpserve 包里没有 watch 测试(`go test -run X` 无匹配 → 退出 0 + "no tests to run",**不等于 PASS**)。
**但 Go 侧 watch 有真覆盖**,只是测试在**根包**(不在 httpserve):directive 把包指错了。
真正的 M3(Go watch @ 副本集,change stream)实测:
```
=== RUN   TestIntegrationWatchInsertUpdate   --- PASS (2.34s)
=== RUN   TestIntegrationWatchScopedDelete   --- PASS (2.18s)
PASS
ok  github.com/doptime/dopdb  5.246s
```
**结论:PASS(2/2,根包)**——M3 的**意图**(Go watch 对真副本集工作)已验证;directive 命令的包路径有误,已在此如实记。
> 注:Go 端 watch 实现于 `httpserve/serve.go:333`(SSE → `acc.HttpWatch`),其行为由上述根包测试覆盖。httpserve 包无独立 watch 测试(非缺陷,仅覆盖率分布)。

### M5 — `go test ./httpserve -run Conformance -v`(DOPDB_TS_NODE=v25,两端实比对)
```
=== RUN   TestConformanceHSetHGet            --- PASS (0.63s)
=== RUN   TestConformanceHSetNXSelfKey       --- PASS (0.59s)
=== RUN   TestConformanceHSetNXCrossTenant   --- PASS (0.59s)
=== RUN   TestConformanceSortProjDollarReject --- PASS (0.35s)
=== RUN   TestConformanceOwnerScopeEmpty     --- PASS (0.54s)
=== RUN   TestConformanceErrorFormat         --- PASS (0.36s)
=== RUN   TestConformanceHDelThen404         --- PASS (0.93s)
=== RUN   TestConformanceHExists             --- PASS (0.63s)
=== RUN   TestConformanceUnknownCommand      --- PASS (0.34s)
PASS
ok  github.com/doptime/dopdb/httpserve  5.874s
```
**结论:PASS(9/9)**——证实 **HttpOn 改门后仍逐命令两端一致**(directive §3 判据 3 的核心)。Go conformance 用 `Grant` 授权 notes/items,OR 门(`!HttpAllowed && !Perms.Allowed`)不受影响,如 directive 预测。

### §3.5 HttpOn-Go 门行为(新增 `httpserve/httpon_test.go`,镜像 TS 端到端门测试)
```
=== RUN   TestHttpOnGate                     --- PASS (0.20s)
PASS
ok  github.com/doptime/dopdb/httpserve  0.622s
```
**结论:PASS**。测试断言(零 `Grant`/`WithPermissions`,证明 HttpOn 位掩码**独立成门**):
- `httponA.HttpOn(ReadOnly)` → `HSET` **403**、`HGET` **非 403**(missing key → 404,过门);
- `httponB.HttpOn()`(无参)→ `HSET` **200**。

### §3.6 TS 回归 — `( cd ts && npm test )`(node v25)
```
ℹ tests 75    ℹ pass 74    ℹ fail 0    ℹ skipped 1
```
(唯一 skip:`TS watch` 用例,需 `DOPDB_TEST_MONGO_URI`,测试环境故意 skip——设计预期。)
**结论:PASS(74/0/1)**,与 directive 期望完全一致。

## 全量 Go 套件汇总(`go test ./...`,副本集 + node v25)
```
ok  github.com/doptime/dopdb        5.529s   (根包 4 测试,含 2 watch)
ok  github.com/doptime/dopdb/api    (cached)  (7 测试)
ok  github.com/doptime/dopdb/config (cached)  (6 测试)
ok  github.com/doptime/dopdb/httpserve 8.148s (18 测试:dispatch×3 + conformance×9 + httpon×1 + integration×3 + persist×1)
```
**零 FAIL。**

## 自分类
- 🟢 **已实证**:§3 全部六条本机真跑过,真实输出如上。
- 🔴 **承重(交 Opus 终判)**:M1/M2/M3/M5/HttpOn-Go 的"是否真符合预期"是承重终判——本机实证全部 PASS,但**终判归 Opus**,不自盖。本回执置 `pending-opus`,等上传后 Opus 据此真实输出落终判联签。
- ⚠️ **两个环境性发现(非代码缺陷,需 Opus 知悉)**:① `dopdb.go` 曾本地误删(已恢复);② 默认 node v19 坏 `--import tsx`,需用 v25+ 或等价(已用 `DOPDB_TS_NODE` 解决)。**均非 dopdb 代码问题**。
