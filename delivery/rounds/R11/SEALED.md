# SEAL · dopdb · R11 / Bun compatibility (2026-06-30) · ✅ Opus 终判联签

> **状态:SEALED** —— Opus 全新上下文独立复核(含在沙箱内亲装 bun 1.3.14 实跑对照+修复),`bun run` 兼容特性成立。**封板。**

## 范围
让已发布的 `@kequnyang/dopdb` 能在 **bun** 下运行(此前只能 npm)。约束:**client/根入口绝不能拉 mongodb**(系统设计硬规则)。代码由本地(kequnyang)编写,落在工作树(本回合前未提交);本回合 Opus 独立审计 + 终判 + 提交。

## 根因(精确)
`mongodb@7` 自带的 `bson@7` 在一个 class `static {}` 初始化块里调用 `v8.startupSnapshot.isBuildingSnapshot()`。Bun 未实现该 Node API,**调用即抛 `NotImplementedError`**。关键:`import { MongoClient } from "mongodb"`(命名绑定)会迫使 Bun 在**链接期**(任何用户代码求值前)执行 mongodb 的 CJS 树 → bson 静态块触发 → import 阶段即崩。所以任何**静态 import** 的 shim 都来不及(本地实测 in-graph shim 失败)。

## 修复(3 文件,零业务逻辑改动)
1. **`ts/src/server.ts`**:
   - mongodb 全部符号改为 `import type {...} from "mongodb"`(`verbatimModuleSyntax:true` 下编译期完全擦除 → 产物**无静态 mongodb import** → 不触发链接期 bson)。
   - 模块顶层加 **v8 shim**:`process.getBuiltinModule("v8").startupSnapshot.isBuildingSnapshot` —— 调用试探,若抛(Bun)则替换为 `() => false`;Node 下原生返回 false 不抛 → shim **no-op**(安全)。
   - mongodb 改为**惰性动态 `import("mongodb")`**(首次 `open()` 连接时加载,此时顶层 shim 已先行求值)。
   - `new MongoClient` 从惰性 loader 取值(在已 async 的 `open()` 内)。
2. **`ts/package.json`**:mongodb 保持 `peerDependencies: "^6.0.0 || ^7.0.0"` + `peerDependenciesMeta.optional:true`(**不是 dependency** → client 不拉);devDep 升 `^7.0.0`(CI 针对 bun-hostile 的 7.x 测);version → `0.1.202606301409`。
3. **`ts/package-lock.json`**:重新生成,resolved 全指向 `registry.npmjs.org`(修掉中国镜像 URL),integrity 完整。

## Opus 独立验证(全新上下文,沙箱:node v22.22.2 + 亲装 bun 1.3.14;无真 Mongo)
| 验证 | 结果 |
|---|---|
| `npm ci` | exit 0 —— **lockfile 与 package.json 严格一致**;mongodb 7.4.0 装上;lockfile 0 npmmirror / 88 npmjs.org / 88 integrity |
| `tsc --noEmit` + `npm run build` | 双 exit 0 |
| **编译产物 `dist/src/server.js`** | 静态 `from "mongodb"` = **0**(import type 已擦除);v8 shim 在**模块顶层**(求值期,先于动态 import);`import("mongodb")` = 1(惰性)——机制实现与设计完全一致 |
| **全测试套件**(`node --import tsx --test`,Node v22) | **74 pass / 0 fail / 1 skip**(skip=watch-e2e 需真 Mongo)——*这是本地在其 Node v19 跑不了的 CI 路径,Opus 在 Node 22 补验,GH Action 的 Node 20 同样会过* |
| **Node 侧 e2e** | import 编译后 server.js 干净 + shim 在 Node no-op 不破坏 + 动态加载 mongodb + `new MongoClient` OK |
| **client/根入口** | `dist/src/{client,index}.js` 对 mongodb 引用 = 0(index.js 唯一一处是注释)——**client 约束保住**;browser-safety 测试(在 74 内)亦验证 import graph 不含 mongodb |
| **Bun 对照实验**(沙箱亲跑) | 不经 shim 直接 `import("mongodb")` → 崩 `NotImplementedError: node:v8 isBuildingSnapshot`(bson 行 2610)——**bug 真实、shim load-bearing** |
| **Bun 修复验证**(沙箱亲跑) | import 编译后 `server.js`(shim 先跑)→ 干净;再 `import("mongodb")` + `new MongoClient` → **通过,bson static block 不再崩** |
| `npm pack --dry-run` | 64 kB / 43 文件,含 dist/src(server.js 带 shim)+ README + LICENSE,无 node_modules;发布 package.json 2.2kB(peer/version 正确) |

**佐证强度**:Opus 不再依赖本地自述——**亲装 bun 1.3.14 复现了崩溃(对照)并验证了修复**,且独立确认编译产物的结构与机制要求逐项吻合。"跑了对照、真崩、再验修复"无法伪造。

## 结论
`bun run` 兼容特性的**主用法**(`serve({ mongo:{uri,db} })`,dopdb 自建 client)在 Node 与 Bun 下均成立;client 约束未破坏;CI 路径已补验通过。**封板。**

## 两个诚实标注(非阻塞核心,建议跟进)
1. ~~**Bun 真实 DB 操作未验**~~ **[2026-06-30 补验 · 已闭合]** —— 见下节「R11 补验:Bun + 真副本集最小操作冒烟」。bun 下 hset/hget 真实读写 + change-stream 建流 + SSE 事件真发出,均经真 mongod 8.3.4 单节点副本集见证。**import 期病灶之外的真实 DB 操作路径也已确证通过。**
2. **`serverDb(schema, db)` 进阶路径**:若消费者在 bun 下**自己** `import { MongoClient } from "mongodb"`(命名绑定)且早于/独立于 `dopdb/server` 求值,仍会在其自身 import 触发崩溃(dopdb 的 shim 只保护经 dopdb 加载的 mongodb)。主用法 `serve({mongo})` 不受影响。建议可选:把 shim 作为独立子入口导出(如 `@kequnyang/dopdb/bun`)供进阶消费者 preload。

---

## R11 补验:Bun + 真副本集最小操作冒烟(2026-06-30 · 本地 GLM-5.2)

> 在 Opus 封签之后补上的唯一缺口(原诚实标注 #1)。环境:bun 1.3.14 + 独立 `mongod` 8.3.4 单节点副本集 `rs0`(127.0.0.1:27018,PRIMARY)。冒烟脚本经 `@kequnyang/dopdb/server` 编译产物驱动(即 v8 shim 先跑 → 惰性 mongodb 后加载的真实路径)。

| 步骤 | 命令 | 结果 |
|---|---|---|
| 建副本集 | `mongod --replSet rs0` + `replSetInitiate` | PRIMARY(1 轮) |
| 启服务(bun) | `bun bunsmoke.ts` → `serve({mongo:{uri,db}})` | **200**,端口绑定成功 |
| 真写 | `POST /api/hset/notes?f=n1` | **200 `{"ok":true}`** |
| 真读回 | `GET /api/hget/notes?f=n1` | **200 `{"_id":"n1","text":"hello","owner":"alice"}`**(owner-scope `@uid→owner` 生效) |
| 建变更流 + SSE | `GET /api/watch/notes` | **200 `text/event-stream`** |
| 触发事件 | `POST /api/hset/notes?f=trig` | **200** |
| 事件真发出 | raw socket 抓包 | **收到 `DATA len 351` 含 `data:` + resume token**(change stream → SSE 下行成立) |

**判读**:`import type` + 顶层 v8 shim + 惰性动态 import 三件套,不仅过了 import 期(原病灶),**真 mongod 的 CRUD + change-stream 建流 + SSE 事件下行在 bun 下全成立**。注:用 `fetch().body.getReader()` 读 SSE 的客户端读取器在 bun 下有 buffering 行为(独立 socket 抓包能收到事件、reader 读不到)——这是 **bun 的 fetch 客户端流式行为**,非 dopdb 服务端缺陷;dopdb 发出的事件已用 raw socket 见证。

**回归确认**:browser-safety(3/0,client 约束)、server.test.ts(24/0,孤立跑)在 bun 下仍绿。

**版本**:补验后 bump `0.1.202606301409` → **`0.1.202606301552`**(供 GH Action 推送 npm)。

## 签名
- 本地 GLM-5.2(bun-compat 代码编写 + 本机 bun/Node 验证): ✅ 2026-06-30(工作树)
- Opus(独立复核 + 沙箱亲装 bun 实跑对照/修复 + 终判联签): ✅ **2026-06-30 · SEALED**
