包: P-R3-q6docs · 上游: 无 · 回合: R3
分级: 🟢 可客观自证
全景: 与 P-R3-ts8 并排; I-Q6 schema-as-data 导出 + I-Q4 文档同步 + F10 评估。
任务一句话: 验证 schema-as-data 导出;核对文档与代码同步;评估 hsetnx 跨租户存在性泄漏。
回执写到: delivery/rounds/R3/receipt-P-R3-q6docs.md

## 1 背景 · 现在是什么情况

- **I-Q6**: `ts/bin/spec.ts` 已实现 `buildSpec(schema)` 导出 JSON,但从未对真实 schema 跑过。
- **I-Q4**: `README.md` + `docs/` 6 篇需在 R2(F13+错误码对齐)后重新核对。
- **F10**: `server.ts` hsetnx 的 `insertOne` 不带 scope,命中他人 key→`{inserted:false}`,泄漏 key 存在性。需评估"修 or 接受"。
- `package.json` `files` 字段: `["dist/src", "dist/bin"]`——需核对排除了 node_modules/dist 外的多余路径。

## 2 意图 · 为什么做、什么算好

完成: ① `dopdb-spec` CLI 跑通并产出合法 JSON;② docs 与代码 0 MISS;③ F10 评估结论落库。红线: RL1–RL8 全部适用;本包追加: 无。 修改令: 无。

## 3 任务 · 具体做什么

- **单元 1(I-Q6)**: ① 在 `ts/test/` 下新建 `spec-export.test.ts`,建一个示例 schema(`collection({...}).named("test")`),跑 `buildSpec(schema)`,断言产出 `{ version, collections: [{ name, fields, indexes, ownerField, bindings }] }` 结构。 ② 用 `npx tsx --test` 跑过。
- **单元 2(I-Q4)**: 逐文件核对: ① `README.md` 命令词表是否含全部 18 个;URL 方案写 `/api/<cmd>/<coll>`;错误码格式写 `{error, code}` 5 类。 ② `docs/02-http.md` 命令表、URL 方案、错误格式。 ③ `docs/00-overview.md` 包地图。 ④ `docs/04-typescript.md` 命令表与 Next.js 示例。 ⑤ `docs/RUNBOOK.md` 迁移说明。 ⑥ `docs/03-config.md` 无 `auto_auth`。 有 MISS → 直接修(L2 自由区);修完重核。
- **单元 3(F10 评估)**: ① 读 `server.ts` hsetnx 实现(`exec` case "hsetnx"),确认当前行为。 ② 写一段评估结论(3–5 句)进回执「经验」栏:是安全回退还是可接受的语义取舍。 ③ 若结论是"需修",写 suspend + 具体修复方案进回执。

铁顺序: 先落产物并确认非空 → 自检全过 → 最后记一行进度;记过的单元不回头改其产物。

## 4 验收 · 怎么算完成

- [ ] `ts/test/spec-export.test.ts` 存在且非空; `npx tsx --test` 通过
- [ ] 文档 MISS 列表(无则写"0 MISS")
- [ ] F10 评估结论落回执
- [ ] `npx tsc --noEmit` 仍干净(若改 docs 无影响;若改代码需验证)
- [ ] 进度账落 `delivery/rounds/R3/progress.md`

## 5 边界 · 不要做什么

- 只读区: `ts/src/` 全部源文件(不修改)。
- 可写区: `ts/test/spec-export.test.ts`(新建); `README.md`, `docs/*`(L2,可修 MISS)。
- 明确不做: 不修 F10 代码(只评估);不碰 Go;不改已有测试。

## 6 预算与换法

每单元最多 3 次: 第 1 次 直做; 第 2 次 缩范围(先核 README, 再核 docs); 第 3 次 降批(只写最关键的 MISS)。连续两次产出一模一样仍未过 → 该单元记 failed。整包超 60 分钟 → 截断收尾。

## 7 收尾

按协议 §3 写回执;「异常发现」必写清单: ① 文档 MISS 数量超预期; ② spec 导出结构与项目卡 §13 CollectionSpec 定义不符; ③ F10 评估撞语义判断(写 suspend)。
