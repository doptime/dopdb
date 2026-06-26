# 回合规划 plan · dopdb · R4(2026-06-26)

## 包 A: P-R4-f10 — F10 hsetnx 跨租户存在性修复(🔴)

**目标**: 修 TS `server.ts` exec hsetnx 分支用 filtered scope + Go `http_accessor.go` HttpSetNX scoped 存在检查,消除"他人 key 存在性被泄漏"的缺陷。

**拟建什么**:
1. TS `ts/src/server.ts` exec hsetnx: 改 `insertOne(doc)` → 先 `countDocuments({_id: key, ...scope})`, 非零→返回 `{inserted: false}`; 零→`insertOne({_id: key})`。
2. Go `http_accessor.go` HttpSetNX: scoped 时先 `HttpExistsScoped(ctx, ds, key, scope)` → true 返回 false; false 走 `HSetNX`。
3. 新增集成测试: TS 端在 `server.test.ts` 加 scoped hsetnx 测试; Go 端在 `dopdb_test.go` 或新建 `httpserve/integration_test.go` 追加。

**打算怎么自证**:
- 写 1 个 "scoped hsetnx 不能写他人 key 且不泄漏存在性" 集成测试: 用户 A 写 key K → 用户 B 对 key K hsetnx → B 收到 `{inserted: false}` 或 403(取决于设计选择), 但关键是不让 B 确定 K 被谁占了。
- 更精确的验证: B 对 K hsetnx 应被拒(403)或返回 `{inserted: false}` 但不能让 B 区分"K 被 A 占了"vs"K 不存在"——实际上 `{inserted: false}` 本身就是存在性信号。真正该做的是: scoped hsetnx 应先检查 owner scope,不匹配直接拒绝(403),而非返回 "inserted: false"。
- `tsc --noEmit` 干净; `go build/vet/test` 全绿。

## 包 B: P-R4-iq8 — 优雅关闭/连接生命周期(🟢)

**目标**: Go `httpserve.Serve` 返回可关停 handle; 关闭释放 Mongo 连接与 change stream。

**拟建什么**:
1. 读当前 `httpserve/bootstrap.go` Serve 签名。
2. 新增 `ServeWithListener` 或改 `Serve` 返回 `Closer` interface。
3. 加 1 个单元测试验证 close 后端口释放。

**打算怎么自证**:
- Go `go test ./httpserve/...` 全绿。
- 新建测试跑 Serve → close → 验证 listener 关闭。
- `go build/vet` 干净。

## 包 C: P-R4-iq7 — 最小 Next.js 示例 app(🟢)

**目标**: 建一个最小可运行 Next.js 示例 app,供真实测试起步。

**拟建什么**:
1. 在 `ts/examples/next-minimal/` 建目录。
2. 写 `package.json` (依赖 next, dopdb 用 workspace 链接), `tsconfig.json`, `app/api/[...slug]/route.ts`, `app/page.tsx`。
3. schema 文件 `dopdb-schema.ts` 带一个 users 集合。

**打算怎么自证**:
- `cd ts/examples/next-minimal && npx next dev` 能启动不 crash。
- 或: 写 1 个 E2E 测试打示例 app。
- 如果 next 环境太重,改为写 `ts/examples/next-minimal/app/api/[...slug]/route.ts` 并用 mock 验证导出形态。
