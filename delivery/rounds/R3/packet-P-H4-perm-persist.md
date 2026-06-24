# 包 · P-H4-perm-persist(R3 · 🟡 并行 · 独立)

> 段:执行交换。给 `Permissions` 加文件式持久化(load/save),不依赖 Mongo。本地按规格实现 + 单测自证。

## 1 目标
`httpserve/permission.go` 加 `SaveJSON(path)` / `LoadJSON(path)`,把授权集(`m map[string]bool`)落盘/恢复,启动时可载入。不改 `Allowed/Grant/Deny` 的内存语义。

## 2 规格
- `func (p *Permissions) SaveJSON(path string) error`:RLock 下把 `p.m` 序列化为 JSON(`map[string]bool`,键即 `CMD::coll`),写入 `path`(0644)。
- `func LoadJSON(path string) (*Permissions, error)`:读文件 → 反序列化为 `map[string]bool` → 返回 `&Permissions{m: <loaded>, AutoAuth: false}`。**AutoAuth 默认 false**(载入的是显式集,生产安全默认)。文件不存在返回 error(调用方决定是否忽略)。
- 并发安全:Save 用 `p.mu.RLock()`;Load 构造新实例无需锁。
- 不动现有方法签名与行为。

## 3 自证(🟡)
新建 `httpserve/permission_persist_test.go`(package httpserve):
- 建 `p := NewPermissions(false)`;`p.Grant` 5 个(如 `HGET/User`、`HSET/User`、`HDEL/Order`、`FIND/Order`、`HKEYS/User`)、`p.Deny` 2 个(如 `DEL/User`、`HSET/Order`)。
- `tmp := t.TempDir()+"/perm.json"`;`p.SaveJSON(tmp)` 无错。
- `q, err := LoadJSON(tmp)` 无错。
- 逐键断言:7 个键 `q.Allowed(cmd,coll)` == `p.Allowed(cmd,coll)`(grant→true、deny→false)。
- 断言未知键在 `q`(AutoAuth=false)下 `Allowed` == false。
- 文件不存在:`LoadJSON(tmp+".nope")` 返回 error。

## 4 验收
- `go build ./...` 退出 0;`go test -count=1 ./httpserve` 全绿(含新测试 + baseline 11,不需 Mongo)。
- `gofmt`/`go vet ./httpserve` 干净。
留痕 `delivery/rounds/R3/h4_test.txt`。

## 5 红线 / 岔路
- 不改 `Allowed/Grant/Deny` 语义;**仅新增** 两方法 + 测试文件。
- 若 `Permissions` 字段不可直接序列化(`m` 私有)——在同包内实现 Save/Load 即可直接访问 `p.m`(同包),无需导出字段。
- 可写区:`httpserve/permission.go`、`httpserve/permission_persist_test.go`、`delivery/rounds/R3/`。

## 6 回执
`receipt-P-H4-perm-persist.md`:状态、单测 PASS、7 键一致性结果、build/vet、是否仅动允许文件、异常发现。
