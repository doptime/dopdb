包: P-R4-iq8 · 上游: 无 · 回合: R4
分级: 🟢 可客观自证 — 硬判据: Serve 返回可关停 handle; 单元测试过; 回归基线不坏
全景: 与 P-R4-f10 并排; I-Q8 优雅关闭
任务一句话: Go httpserve.Serve 返回可关停 handle; 关闭释放 Mongo 连接
回执写到: delivery/rounds/R4/receipt-P-R4-iq8.md

## 1 背景 · 现在是什么情况
- `httpserve/bootstrap.go` Serve 当前签名: `func Serve(cfg *config.Config, opts ...ServeOption) error` → 阻塞监听
- TS 侧 `DopdbServer.close()` 已实现 (server.ts L852-855)
- Go 无优雅关闭入口: `log.Fatal(Serve(cfg))` 只能 os.Signal 杀进程

## 2 意图 · 为什么做、什么算好
完成: 新增 `ServeWithHandle(cfg, opts...)` 返回 `(*http.Server, error)` 或改 `Serve` 返回 `(*ServerHandle, error)`; 关闭调用 `Shutdown(ctx)`。红线: RL1-RL8 全部适用。 修改令: 无(新增函数不改现有 Serve 签名)。

## 3 任务 · 具体做什么
**单元 1**: `httpserve/bootstrap.go` 新增 `ServeWithHandle(cfg, opts...) (*http.Server, error)` —— 与 `Serve` 内部逻辑一致, 但返回 `*http.Server` 供调用者关停
**单元 2**: `httpserve/bootstrap.go` 保持 `Serve(cfg, opts...)` 签名不变, 内部调 `ServeWithHandle` 并 `http.ListenAndServe` 阻塞
**单元 3**: 加 1 个测试验证 ServeWithHandle 返回的 server 可 Shutdown

铁顺序: 先落产物 → 自检全过 → 记进度

## 4 验收 · 怎么算完成
- [ ] `httpserve/bootstrap.go` 新增 `ServeWithHandle` 函数
- [ ] 原 `Serve` 签名不变, 内部调 `ServeWithHandle`
- [ ] 新增测试验证 server.Shutdown 成功
- [ ] `go build/vet/test ./...` 全绿
- [ ] 进度账落 `delivery/rounds/R4/progress.md`

## 5 边界 · 不要做什么
- 只读区: 已有测试不改; `config.go` schema 不改
- 可写区: `httpserve/bootstrap.go`, `httpserve/bootstrap_test.go`(新建)
- 明确不做: 不改 Serve 签名; 不改 TS 侧

## 6 预算与换法
每单元最多 3 次: 第 1 次直做; 第 2 次缩范围; 第 3 次降批。整包超 45 分钟 → 截断收尾。

## 7 收尾
按协议 §3 写回执; 「异常发现」必写清单: ① ServeWithHandle 返回类型与预期不符; ② Shutdown 测试需真实端口
