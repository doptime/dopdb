# 回执 · P-H4-perm-persist(R3 · 🟡 并行 · 独立)

状态:**done**

## 单测结果

`TestSaveLoadJSON` PASS:
- 5 Grant(HGET/User, HSET/User, HDEL/Order, FIND/Order, HKEYS/User) → Save → Load → 逐键 `Allowed` 一致
- 2 Deny(DEL/User, HSET/Order) → 同上,一致性一致
- 未知键(AutoAuth=false) → `Allowed` == false
- 文件不存在 → `LoadJSON` 返回 error

`go test -count=1 ./httpserve` 全绿(含 baseline 11 + 新测试)
`go build ./...` 退出 0

## 仅动允许文件

- `httpserve/permission.go`:加 `SaveJSON` 方法 + `LoadJSON` 函数(仅加 `encoding/json`/`os` import)
- `httpserve/permission_persist_test.go`:新建

未改 `Allowed/Grant/Deny` 语义。

## 异常发现

无。
