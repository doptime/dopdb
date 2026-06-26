包: P-R3-ts2 · 上游: 无 · 回合: R3
分级: 🟢 可客观自证
全景: 与 P-R3-ts8 和 P-R3-q6docs 并排; I-TS2 Pages Router 验证 + I-Q5 打包核对。
任务一句话: 验证 DopdbServer.listener 可用作 Pages Router 入口;核对打包配置。
回执写到: delivery/rounds/R3/receipt-P-R3-ts2.md

## 1 背景 · 现在是什么情况

- **I-TS2**: `serve()` 返回 `DopdbServer` 接口,有 `listener` 字段;文档写 `export default (req, res) => srv.listener(req, res)`。
- **I-Q5**: `package.json` `files` = `["dist/src", "dist/bin"]`;需核对是否排除了测试文件和 node_modules。
- `DopdbServer` 接口定义在 `server.ts` `export interface DopdbServer`: 含 `close()`, `listener`, `http`(http.Server), `port`。

## 2 意图 · 为什么做、什么算好

完成: ① 写一条 test 验证 `serve()` 返回的 `listener` 是有效的 Node 请求处理函数; ② 核对 `package.json` files 字段正确。红线: RL1–RL8 全部适用;本包追加: 无。 修改令: 无。

## 3 任务 · 具体做什么

- **单元 1(I-TS2)**: 在 `ts/test/server.test.ts` 已有测试中,或新建独立 test 文件,验证: ① `serve()` 返回 `DopdbServer` 有 `.listener` 属性; ② `.listener` 是可调用函数; ③ 用它处理一个假请求能返回 2xx/4xx/5xx(不崩溃)。 若 `server.test.ts` 已有相关断言,则只需确认覆盖,无需新增。
- **单元 2(I-Q5)**: 核对 `ts/package.json`: ① `files` 只含 `dist/src`/`dist/bin`; ② `exports` 正确指向 `dist/src/`; ③ `bin` 指向 `dist/bin/spec.js`; ④ 无 `node_modules`/`test/` 泄露。 有 MISS → 修。
- **单元 3(I-TS2 续)**: 确认 `server.ts` 的 `createNextHandler` 也返回了 `{ GET, POST, OPTIONS }`(已在 R2/M4 验证,本次只核对代码未退步)。

铁顺序: 先落产物并确认非空 → 自检全过 → 最后记一行进度;记过的单元不回头改其产物。

## 4 验收 · 怎么算完成

- [ ] `listener` 验证: 断言存在且可调用(已有或新增 test 均算)
- [ ] `package.json` 打包核对: 列出 MISS 列表(无则"0 MISS")
- [ ] `npx tsc --noEmit` 仍干净
- [ ] `npx tsx --test test/*.test.ts` 全过(含本包可能新增的测试)
- [ ] 进度账落 `delivery/rounds/R3/progress.md`

## 5 边界 · 不要做什么

- 只读区: `ts/src/server.ts`(不修改逻辑,只读验证)。
- 可写区: `ts/test/`(可新建或追加测试); `ts/package.json`(仅修正 files/exports MISS)。
- 明确不做: 不改 server.ts;不碰 Go。

## 6 预算与换法

每单元最多 3 次: 第 1 次 直做; 第 2 次 缩范围(先核对 package.json, 再写 listener 验证); 第 3 次 降批(最小断言: `typeof srv.listener === "function"`)。连续两次产出一模一样仍未过 → 该单元记 failed。整包超 45 分钟 → 截断收尾。

## 7 收尾

按协议 §3 写回执;「异常发现」必写清单: ① listener 类型不对(非函数); ② package.json files 泄露敏感路径; ③ 现有 server.test.ts 已有覆盖,本包无新增。
