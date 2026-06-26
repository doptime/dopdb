包: P-R6-m3-go · 上游: 无 · 回合: R6
分级: 🔴 承重 — 硬判据: DOPDB_TEST_MONGO_URI="mongodb://localhost:27017" 下 watch 集成测试全过(insert→事件/update→事件/resume 不漏/scoped delete 不投递); 回归基线不坏
全景: 串行主线; 与 P-R6-m3-ts 并排
任务一句话: 建 Go watch 集成测试, 验证 change stream → emit 往返
回执写到: delivery/rounds/R6/receipt-P-R6-m3-go.md

## 1 背景 · 现在是什么情况
- MongoDB 副本集已启动 `mongodb://localhost:27017=PRIMARY`
- Go `mongo.go:377-427` 已有 `watch()` 实现, 支持 resume token 自动恢复
- Go `httpserve/serve.go:299-318` 已有 WATCH SSE dispatch
- 无 watch 集成测试——M3 一直 suspend

## 2 意图 · 为什么做、什么算好
完成: 新增 3 个 watch 集成测试, 覆盖 insert/update 事件推送、resume token 续传、scoped delete 不投递。红线: RL1-RL8 + PRL1-6 全部适用。 修改令: 无(只加新测试, 不改已有测试)。

## 3 任务 · 具体做什么
**单元 1 (insert/update 事件)**:
- 在 `dopdb_test.go` 新增 `TestIntegrationWatchInsertUpdate`
- 用 `withTestDS` 建一次性库, 建 Collection, 开 watch goroutine, 写文档 → 验证收到 insert 事件 → 更新文档 → 验证收到 update 事件
- 关键断言: 收到 2 个事件, op 分别为 "insert" 和 "update"

**单元 2 (resume token 续传)**:
- 新增 `TestIntegrationWatchResume`
- 开 watch → 等 1 个事件 → 取消 ctx 停流 → 等 resume token → 重开 watch 用 resume → 再写文档 → 验证收到断线后的事件
- 关键断言: 续传后收到新事件, 不断漏

**单元 3 (scoped delete 不投递)**:
- 新增 `TestIntegrationWatchScopedDelete`
- 建 scoped collection, 开 scoped watch → 插入文档 → 收到 insert → 删除文档 → 验证 emit 没被调用
- 关键断言: delete 事件不触发 emit (I-WA2 已知限制)

铁顺序: 先落产物并确认非空 → 自检全过 → 最后记一行进度

## 4 验收 · 怎么算完成
- [ ] `dopdb_test.go` 新增 3 个 TestIntegrationWatch 测试
- [ ] `DOPDB_TEST_MONGO_URI="mongodb://localhost:27017" go test -run IntegrationWatch ./...` 全过
- [ ] `go test -count=1 ./...` 回归基线不坏
- [ ] 进度账落 `delivery/rounds/R6/progress.md`

## 5 边界 · 不要做什么
- 只读区: 已有 `*_test.go` 不改（只在 `dopdb_test.go` 追加新测试）
- 可写区: `dopdb_test.go` (追加新测试函数)
- 明确不做: 不改 mongo.go/watch() 逻辑; 不改 serve.go; 不碰 TS

## 6 预算与换法
每单元最多 3 次: 第 1 次直做; 第 2 次缩范围; 第 3 次降批。整包超 90 分钟 → 截断收尾。

## 7 收尾
按协议 §3 写回执; 「异常发现」必写清单: ① watch 超时收不到事件; ② resume token 续传失败; ③ 已有集成测试被新测试影响
