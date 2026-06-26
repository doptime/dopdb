包: P-R3-ts8 · 上游: 无 · 回合: R3
分级: 🟢 可客观自证
全景: 串行主线; I-TS8 浏览器打包安全守卫。
任务一句话: 为 TS 根入口与 client 入口写 import 图守卫,验证不引 mongodb/node:* 模块。
回执写到: delivery/rounds/R3/receipt-P-R3-ts8.md

## 1 背景 · 现在是什么情况

- `ts/src/index.ts` 声明"根 + /client 入口不引 node 或 mongodb",但仅靠注释,无守卫。
- `ts/src/client.ts` 只引 `./schema.js` 和 `./errors.js`——无 node-only。
- `ts/src/server.ts` 引 `node:*` + `mongodb`——但 server 入口不进入浏览器。
- **I-TS8** 要求一条**可验证守卫**(import 图/打包检查)。

## 2 意图 · 为什么做、什么算好

完成:新增一条 test,对 `index.ts`(根)和 `client.ts`(client)做静态 import 扫描,确认其导出链不传递性地引用 `mongodb` 或 `node:*` 模块。命中即 fail。红线: RL1–RL8 全部适用(见 kit/00-protocol.md §4);本包追加: 无。 修改令: 无。

## 3 任务 · 具体做什么

- **单元 1**: 在 `ts/test/` 下新建 `browser-safety.test.ts`。实现: ① 用 `node:module` 或手动 AST 扫描 `index.ts` 和 `client.ts` 的导出源文件,递归收集 import,检查不命中 `mongodb`/`node:*`/`crypto`/`http` 等 node-only 模块。 ② 断言 `server.ts` **会**命中 mongodb(反向验证守卫有效)。 ③ 跑 `npx tsx --test test/browser-safety.test.ts` 过。
- **单元 2**: 确保 `npx tsc --noEmit` 仍干净;`npx tsx --test test/*.test.ts` 仍全过(现在 12/12)。

铁顺序: 先落产物并确认非空 → 自检全过 → 最后记一行进度;记过的单元不回头改其产物。

## 4 验收 · 怎么算完成

- [ ] `ts/test/browser-safety.test.ts` 存在且非空
- [ ] `cd ts && npx tsx --test test/browser-safety.test.ts` → ok (EXIT:0)
- [ ] `cd ts && npx tsc --noEmit` → 干净 (EXIT:0)
- [ ] `cd ts && npx tsx --test test/*.test.ts` → 12/12 全过
- [ ] 进度账落 `delivery/rounds/R3/progress.md`

## 5 边界 · 不要做什么

- 只读区: `ts/src/server.ts`, `ts/src/schema.ts`, `ts/src/permission.ts`, `ts/src/sanitize.ts`, `ts/src/api.ts`, `ts/src/config.ts`, `ts/src/errors.ts`
- 可写区: `ts/test/browser-safety.test.ts`(新建)
- 明确不做: 不修改任何 `ts/src/` 下的源码;不碰 Go 代码;不改已有测试。

## 6 预算与换法

每单元最多 3 次: 第 1 次 直做; 第 2 次 缩范围定位(先只做 index.ts 的 import 扫描); 第 3 次 降批最小可证(用简单的 `fs.readFileSync` + 正则扫描 import/require 行,而非完整 AST)。连续两次产出一模一样仍未过 → 该单元记 failed。整包超 60 分钟 → 截断收尾。

## 7 收尾

按协议 §3 写回执;「异常发现」必写清单: ① 守卫误报(根入口意外引用了 node-only); ② 测试无法导入 tsx 编译产物; ③ 新增测试导致 tsc 报错。
