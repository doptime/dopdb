# 执行简报 · dopdb · R2(2026-06-24)· Phase ② 执行交换

> 规划已对账定稿(见 `reconcile.md`)。本回合执行两个定稿 packet,做完写回执回传。

## 本回合两件事

| 包 | 分级 | 一句话 | 自证 |
|---|---|---|---|
| **P-V4-http-mongo-integration** | 🔴 承重 | 真 Mongo 上 httpserve 全栈端到端(JWT→`@`-绑定→权限→数据命令 + `/api/<name>`→BSON 往返 + codec 字段映射) | `DOPTIME_TEST_MONGO_URI` 已设时 `go test -run TestHTTPMongo -v ./httpserve` 退出 0 + 含 `INTEGRATION OK` + 无 `SKIP` |
| **P-W1-wasm-ts** | 🟢 并行 | 本地 1.24/node19 复跑 `make wasm` + `make ts` + smoke-test | 三步退出 0 + 产物齐 + 含 `ALL TS SDK INTEGRATION TESTS PASSED` |

## 顺序

两包**互不依赖,可并跑**。V4 是承重主线(站在 R1 封存的 V3 上);W1 是并行轨(不碰 Mongo)。V4 若卡在 codec/`@`-注入需云端裁 → 转去把 W1 封掉;W1 卡住(无 node/编译)→ 不阻塞 V4。

## 红线复读(全适用)

- **RL2/PRL2/PRL4**:绝不为让断言过而改测试、门槛、被测代码,或删/改断言。V4 唯一允许的代码改动 = 决策表里"驱动 API 不匹配"那行的机械签名适配(仅限 `mongo.Connect`/`FindOne().Decode`/`db.Drop`/`Disconnect` 4 类,参照已跑通的 mongostore.go)。
- **RL5**:exit 0 还要看语义——`INTEGRATION OK` / 成功串 真在,断言真过。
- **RL6**:1.24/node19 上任何差异、任何"像 facade"的迹象,如实记进「异常发现」。
- **PRL3**:`@uid` 只能来自 JWT,客户端伪造(query 或 body)必须被剥离——V4 子测试 2/5 专测。

## 交付

逐包写 receipt 到 `delivery/rounds/R2/receipt-P-<包名>.md`(状态 done/suspend/failed/blocked + 关键数字 + 异常发现),跑出的 `*.txt`(`http_mongo.txt` / `w1_*.txt`)留在 `delivery/rounds/R2/`,进度账写 `delivery/rounds/R2/progress.md`。越界改动登记 `delivery/rounds/R2/oob.md`。回传后云端三层审计 → 封 R2。
