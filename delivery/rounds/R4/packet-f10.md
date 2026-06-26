包: P-R4-f10 · 上游: 无 · 回合: R4
分级: 🔴 承重 — 硬判据: 真 Mongo 下 scoped hsetnx 对他人 key 返回 403 (非 `{inserted:false}`); 对空 key 正常插入; 回归基线不坏
全景: 串行主线; 与 P-R4-iq8/I-Q7 并排
任务一句话: 修 TS/Go 两端 hsetnx scoped 存在性泄漏: 他人 key → 403 forbidden
回执写到: delivery/rounds/R4/receipt-P-R4-f10.md

## 1 背景 · 现在是什么情况
- `ts/src/server.ts` exec hsetnx (L179-188): `insertOne(doc)` 不带 scope → 他人 key E11000 → `{inserted:false}` 泄漏存在性
- Go `http_accessor.go` `HttpSetNX` (L189-199): 无 scoped 变体,直接调 `HSetNX`
- hset 已正确处理 scope: `replaceOne({_id: key, ...scope}, doc, upsert:true)`
- **GLM 写死澄清**: scoped hsetnx 对他人 key 应返回 **403 forbidden** (非 `{inserted:false}`), 语义: "你不是这个 key 的主人,无权操作"

## 2 意图 · 为什么做、什么算好
完成: TS exec hsetnx 先检查 scope, 不匹配 → 403; Go HttpSetNX scoped 时先 exists, 不存在 → 403。红线: RL1-RL8 + PRL1-6 全部适用。 修改令: 无(改 exec 逻辑非 L0; 加测试预授权)。

## 3 任务 · 具体做什么
**单元 1 (TS 修复)**:
- `ts/src/server.ts` hsetnx case: 改 `insertOne(doc)` → ① 先 `countDocuments({_id: a.key, ...scope})` ② n > 0 → `throw new ForbiddenError()` (403) ③ n == 0 → `insertOne({_id: a.key, ...a.value})` 成功返回 `{inserted: true}`
- 注意: 非 scoped 集合 (scope == {}) 时行为不变 (insertOne 直接走)

**单元 2 (Go 修复)**:
- `http_accessor.go` `HttpSetNX`: 在 scoped 时, 先调 `HttpExistsScoped(ctx, ds, key, scope)` → true → 返回 `errors.New("forbidden")` 或 `dopdb.ErrForbidden`; false → 走 `HSetNX`
- `httpserve/serve.go` dispatch: hsetnx 返回 forbidden 时, `writeErr` 输出 403 + `forbidden` code

**单元 3 (集成测试)**:
- TS: `server.test.ts` 加 1 个 "scoped hsetnx 对他人 key → 403" 测试
- Go: `httpserve/integration_test.go` 加 1 个 "scoped hsetnx 对他人 key → 403" 集成测试
- 回归: `go test ./...` + `tsc --noEmit` + `npx tsx --test test/server.test.ts` 全绿

铁顺序: 先落产物并确认非空 → 自检全过 → 最后记一行进度

## 4 验收 · 怎么算完成
- [ ] `ts/src/server.ts` hsetnx 分支改过 (grep `ForbiddenError` 命中 hsetnx)
- [ ] `http_accessor.go` HttpSetNX scoped 逻辑改过
- [ ] 新增 2 个测试 (TS 1 + Go 1)
- [ ] `go build/vet/test` 全绿
- [ ] `cd ts && npx tsc --noEmit` 干净
- [ ] 进度账落 `delivery/rounds/R4/progress.md`

## 5 边界 · 不要做什么
- 只读区: `sanitize.go`, `httpserve/context.go` @-顺序, `jwt.go` 拒 none, 全部已有 `*_test.go` (不改已有测试)
- 可写区: `ts/src/server.ts`, `http_accessor.go`, `httpserve/serve.go`, `ts/test/server.test.ts`, `httpserve/integration_test.go`
- 明确不做: 不改 hset/hget/hdel 逻辑; 不加新命令; 不改已有测试

## 6 预算与换法
每单元最多 3 次: 第 1 次直做; 第 2 次缩范围定位; 第 3 次降批最小可证。整包超 90 分钟 → 截断收尾。

## 7 收尾
按协议 §3 写回执; 「异常发现」必写清单: ① hsetnx 改后现有测试过不了(需排查); ② 403 语义与现有 forbidden 处理不一致; ③ 集成测试跑不了(无 Mongo 需 skip)
