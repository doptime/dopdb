# 对账 · dopdb · R3(2026-06-24)· 云端

## 裁决:v0-plan **SOUND**(优秀),据此定稿 4 包,进执行交换。

本地 v0 对五轨理解准确:H1 的 `PutScoped`/dup-key→`ErrForbidden`、H2 的逐用户键集不泄漏、T1/H4/D2 的自证法都对路;并确认 `mongo.IsDuplicateKeyError` 在 v2.7.0 可用(`mongo/errors.go:174`)。承重轨(H1/H2)框架实现由云端写定并在 memstore 上自测(build+vet 干净、旧测试全绿、`-race` 并发干净),本地套用 + 逐字新建测试 + 真 Mongo 跑;T1/H4/D2 本地按规格实现 + 自证。

## 云端对 v0 的 4 处细化(均已自测,已并入定稿包)

1. **H1 并发用例下沉到 `Collection.HSetScoped` 层**(测试用例 2/3),不走 HTTP。原因:HTTP 并发会卷入 AutoAuth 授权 map 与 jwt LRU 的无关并发路径,污染 H1 原子性判据。HTTP 层的**跨主单请求守卫**仍由用例 1 验(403)。这样 H1 的原子性判据是干净的:直接打 `PutScoped` 原语,40 同主并发全 ok / 16+16 跨主竞态 u2 全拒。
2. **`mongostore.PutScoped` 用单个 `$set`(内部强制 owner),不用 `$set`+`$setOnInsert`**。后者会与过滤器在 owner 字段冲突;upsert 在 insert 时已从过滤器自带 `_id`。
3. **`PutScoped` 强制 owner(非仅携带)**:两 store 都把 owner 字段写死成 `ownerVal`,直接调用(非 HTTP 路径)也安全——`-race` 直测验证 30 同主并发终值 owner=u1 一致、跨主 `ErrForbidden`。
4. **`mongostore` 的 `options.Update()→options.UpdateOne()` 同步**:你 R1 在 HSetNX/Incr 做的修正已并入本交付的 `mongostore.go`(连同新 `PutScoped`),套用不会回退。

## v0 风险逐条回应
- **#1(Store 接口加方法 = 方向性变更,是否走 🔐)**:plan-brief 已授权此演进(scoped 安全语义收尾的必要原语),**无需 🔐**。memstore 实现已含,`var _ dopdb.Store = (*Store)(nil)` 断言通过(云端 build 已确认)。
- **#2(dup-key API)**:确认可用;若仍漂移,仅机械适配 mongostore 那一处判定,记 oob。
- **#3(旧测试挂)**:云端已在 memstore 上验 `TestRowIsolation` 在重写后的 `HttpSetScoped` 上**仍 PASS**(隔离语义不变)。真 Mongo 上若旧测试挂 = 语义回归 → 停 + suspend,不改测试。
- **#4(并发稳定性)**:同意——用例 2/3 验「终值一致 + 无错 + 跨主全拒」,不验「竞态是否触发」;原子性由 updateOne 保证。

## 定稿包(执行交换)
| 包 | 类 | 轨 | 验收硬线 |
|---|---|---|---|
| P-H1H2-scoped-hardening | 🔴 承重 | H1+H2 | 真 Mongo:`go test ./...` 全绿(含旧测试)+ `TestScopedHardening` 四子测试 + `SCOPED HARDENING OK` + `-race` 干净 |
| P-T1-ts-data-client | 🟢 | T1 | tsc 0 + data-client mock 测试 PASS + smoke 仍过 |
| P-H4-perm-persist | 🟡 | H4 | `go test ./httpserve` 全绿(含新单测,无需 Mongo) |
| P-D2-docs-runbook | 🟢 | D2 | 路径核对 0 MISS + git diff 仅 docs |

并行:H1H2(承重)与 T1/H4/D2 可并跑;H1H2 卡住转封后三者(不阻塞)。H3 不做。
