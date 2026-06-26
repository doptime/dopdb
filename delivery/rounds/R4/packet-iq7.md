包: P-R4-iq7 · 上游: 无 · 回合: R4
分级: 🟢 可客观自证 — 硬判据: 最小 Next.js 示例 app 目录存在; route.ts 导出 {GET,POST,OPTIONS}; 编译通过
全景: 与 P-R4-f10/I-Q8 并排; I-Q7 最小示例 app
任务一句话: 建最小 Next.js 示例 app 供真实测试起步
回执写到: delivery/rounds/R4/receipt-P-R4-iq7.md

## 1 背景 · 现在是什么情况
- `ts/src/server.ts` 提供 `createNextHandler(cfg)` 返回 `{GET, POST, OPTIONS}`
- 无示例 app: 开发者需自己建 Next.js 项目
- 环境: Node 19 无 Next.js; 但可建目录结构并用 mock 验证形态

## 2 意图 · 为什么做、什么算好
完成: 建 `ts/examples/next-minimal/` 最小 app; route.ts 一行接管 /api/*; schema 带一个集合。红线: RL1-RL8 全部适用。 修改令: 无。

## 3 任务 · 具体做什么
**单元 1**: 建 `ts/examples/next-minimal/` 目录
- `package.json`: 依赖 `next`, `dopdb` (workspace 链接 `../../..`)
- `tsconfig.json`: 基础配置 (target ES2022, module NodeNext, strict)
- `app/layout.tsx`: 最小 layout
- `app/page.tsx`: 最小页面

**单元 2**: 建 `app/api/[...slug]/route.ts`
- import `createNextHandler` from `dopdb/server`
- 定义 schema (users 集合: name/email/age)
- `export const { GET, POST, OPTIONS } = createNextHandler({...})`
- `export const runtime = "nodejs"`

**单元 3**: 建 `dopdb-schema.ts` (独立 schema 文件, 供 route.ts import)
- 1 个 users 集合, ownerScope("owner")

**单元 4**: 加 1 个测试验证 route.ts 导出形态 (mock createNextHandler, 检查 {GET, POST, OPTIONS} 存在)

铁顺序: 先落产物 → 自检全过 → 记进度

## 4 验收 · 怎么算完成
- [ ] `ts/examples/next-minimal/` 目录存在且非空
- [ ] `app/api/[...slug]/route.ts` 导出 {GET, POST, OPTIONS}
- [ ] `dopdb-schema.ts` 有 1 个 users 集合
- [ ] `cd ts/examples/next-minimal && npx tsc --noEmit` 干净 (或用 mock 测试验证)
- [ ] 进度账落 `delivery/rounds/R4/progress.md`

## 5 边界 · 不要做什么
- 只读区: `ts/src/` 不改; Go 代码不改
- 可写区: `ts/examples/next-minimal/` 全部新建
- 明确不做: 不跑 `next dev`(环境不支持); 不装 next devDependency

## 6 预算与换法
每单元最多 3 次: 第 1 次直做; 第 2 次缩范围; 第 3 次降批。整包超 45 分钟 → 截断收尾。

## 7 收尾
按协议 §3 写回执; 「异常发现」必写清单: ① route.ts 编译报错; ② schema 字段定义与 docs 不一致
