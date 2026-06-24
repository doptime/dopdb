# SEALED · R2(2026-06-24 执行 / 审计封存)· 云端

> 协议 §2.1 封存章。含本文件的回合目录即档案;封存目录内文件谁也不许改。

裁决:**R2 通过(PASS)。V4 = done(承重·真 Mongo 六契约全过);W1 = done。** STATUS.md 经核**非篡改**(详见末)。

## 三层审计

- **L1 回执完备**:V4/W1 两份 receipt + progress + 产物齐;无 `oob.md`。
- **L2 直读产物**:逐项核对 receipt = 产物。
- **L3 红线/范围**:比对执行前后,确认未触冻结件/测试/语义。

## 裁决表

| 包 | 状态 | 关键数字(产物核对) | 范围/红线核对 |
|---|---|---|---|
| **P-V4** http@mongo 🔴 承重 | ✅ done | `http_mongo.txt`:六子测试全 PASS、`INTEGRATION OK`、无 SKIP、0.25s。JWT(401×3/200);`@`-绑定(_id=u1,伪造剥离=yes);权限(deny=403,auto=200);数据命令(HSET→HGET→HDEL→404);API(saved=hello,owner=u1,**body 伪造 @uid 剥离=yes**,Mongo 落盘 text=hello);codec(**HTTP 字段集 == BSON 字段集 == `[_id createdAt name role updatedAt]`**,`_id`=string,`role`=member 两层一致,`createdAt`=Date 非 string) | `http_integration_test.go` 与包内嵌代码**逐字一致**(无篡改断言/门槛);仅新建该文件;✓ |
| **P-W1** wasm/ts 🟢 | ✅ done | go1.24.5/node v19.0.0;`make wasm` EXIT 0(`dopdb.wasm` 2,876,177 B,wasm_exec.js 刷新自 1.24);`make ts` EXIT 0(dist 3×.js+3×.d.ts);smoke EXIT 0 含 `ALL TS SDK INTEGRATION TESTS PASSED`(fetch ExperimentalWarning 属预期) | 仅重建构建产物(wasm/dist/wasm_exec.js);未改 src/*.ts 与 Go wasm 源;✓ |

## 承重里程碑结论(V4)

dopdb 的 `httpserve` 全栈在**真实 MongoDB**(本地 Docker)上端到端验过:JWT(拒 none/坏签名/无 token,放行合法)→ `@`-绑定(key 从 JWT 注入,query+body 伪造均剥离,**PRL3 守住**)→ `command::collection` 权限(deny=403/首用授予)→ 数据命令往返 → `/api/<name>` 跑通 api 流水线且落 Mongo。**V3 遗留的 codec 字段映射坑已坐实关闭**:JSON-进/BSON-落盘/JSON-出 字段一一对齐,`_id` 恒 string、时间戳为 Date。框架核心至此在真 Mongo 上全绿。

## 关键数字汇总

- V4:六子测试全 PASS,INTEGRATION OK,0.25s;codec 两层字段集相等;`@uid` 伪造(query+body)双双剥离。
- W1:wasm 2,876,177 B;tsc 干净;SDK 端到端过(go1.24/node19)。
- 云端独立复跑:无驱动 34 全绿、httpserve baseline 11 全绿(把驱动测试暂移后跑)。

## 指针

- 回执:`receipt-P-{V4,W1}.md` · 进度:`progress.md`
- 产物:`http_mongo.txt`(六子测试 + INTEGRATION OK)、`w1_{wasm,ts,smoke}.txt`
- 新代码:`httpserve/http_integration_test.go`(逐字按 V4 包);重建产物 `clients/ts/wasm/*`、`clients/ts/dist/*`

## STATUS.md 核验(回应「疑被本地篡改」)

**结论:非内容篡改。** 工作树的 `delivery/STATUS.md` 与云端 R2 定稿版**逐字相同**(diff 为空)。`git status` 显示 `M delivery/STATUS.md` 的真因:本地应用了云端 R2-reconcile 交付(其中含更新后的 STATUS.md),却只 `git add` 了新增的 R2 文件、**未把 STATUS.md 的更新一并提交**,于是它以「已修改/未提交」漂着——那处「修改」恰是**云端自己交付的内容**。另 `ddf02e6 "updates r1 r2"` 以本地署名提交了云端 R1-seal 的 STATUS,内容同样是云端的(属「本地提交云端交付」的工作流)。

**规矩重申(低危纪律提示)**:STATUS.md 云端独写。本地应用每次交付时,应把云端交付的 STATUS.md **原样一并提交**(verbatim),既不手改、也别让它以未提交状态漂着。本轮云端已刷新 STATUS 到 R3 版,旧的漂移自然被取代。

## 能力画像(累计 R1+R2)

- **执行力 高**:真 Mongo http 全栈六契约一次全过;wasm/TS 在 1.24/node19 复跑全绿;承重件零返工。
- **纪律性 高(累计 2 处低危,均无害)**:R1 的 `.gitignore` 越界未登记;R2 的 STATUS.md 未提交漂移。两次都不涉及内容篡改、冻结件、测试、密钥。断言/门槛零篡改。
- **汇报质量 高**:回执数字与产物逐项一致,异常发现栏诚实(无虚报)。

— 封存于 2026-06-24,云端。R3(收尾硬化,单回合打包全部余量)规划已开,见 `rounds/R3/plan-brief.md`。
