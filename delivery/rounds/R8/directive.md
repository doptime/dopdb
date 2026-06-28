# 回合指令 directive · dopdb · R8(2026-06-28,Opus↓ · v4.0 两模型首个回合)

## 0 一句话 + 本回合承重里程碑
**本机实证坐实"系统符合预期",把 M1/M2/M3/M5 + 新 HttpOn 的 Go 侧从"Opus 盲写/未亲验"转成"真实输出可终判"。** 这是收尾前的最后一个承重回合——没有新功能,只有**真跑 + 留痕**。

承重里程碑:**M1(Go 编译)· M2(真 Mongo)· M3(watch)· M5(Go↔TS 一致性)· HttpOn-Go(门行为)** 一次性本机验证。

## 1 范围与不在范围
- **范围**:仅"运行验证 + 必要的编译修复 + 补一个 Go 门测试"。
- **不在范围**:不加新功能、不重构、不动文档(README/AGENTS 已定稿)、不改 schema/线协议。若编译报错,**只做让它编译通过的最小修复**,并在回执如实记录改了什么、为什么。

## 2 上回(Opus 本轮)裁决与交付
- 三件事已做:① 测试标准化(删被取代的 `interop_test.go`,加 httpOn 覆盖,`docs/TESTING.md`);② 恢复 `HttpOn(...)` 权限(debug 全开默认 + 可收紧);③ 文档拆 `README.md`(人)+ `AGENTS.md`(机器)。
- **TS 侧 Opus 已亲验**:tsc 干净,75 测试 / 74 过 / 1 skip(含 httpOn 端到端门测试)。
- **Go 侧 Opus 未亲验**(沙箱无 Go):`perms.go`(新)、`dopdb.go` 的 `HttpOn` 方法、`httpserve/serve.go` 的 `HttpAllowed` 门——**一行未编译**。这是 R8 第一要务。
- 异常发现:无未回应项。

## 3 硬判据(逐条客观、可独立复核;真实 stdout 落回执)
1. **M1**:`go build ./... && go vet ./... && gofmt -l .` —— 编译/vet 零错、`gofmt -l` 输出为空。
2. **M2**:`go test ./httpserve -run Integration -v`(`DOPDB_TEST_MONGO_URI` 指向副本集)—— 全 PASS。
3. **M5**:`go test ./httpserve -run Conformance -v` —— 全 PASS(确认 HttpOn 改门后**仍**逐命令两端一致;若 Go conformance 用 Grant/permit 授权,OR 门应不受影响,验证之)。
4. **M3**:`go test ./httpserve -run IntegrationWatch -v` —— 全 PASS。
5. **HttpOn-Go 门行为**(补测):新增 `httpserve/httpon_test.go`,镜像 TS 的端到端门测试——
   - 集合 A `.HttpOn(dopdb.ReadOnly)`:`HSET` → 403、`HGET` → 非 403;
   - 集合 B `.HttpOn()`(无参):`HSET` → 200;
   - **不配置任何 `Grant`/`WithPermissions`**,证明 HttpOn 位掩码独立成门。
   需 Mongo,用 `DOPDB_TEST_MONGO_URI` 门控、缺则 skip(对齐 conformance)。
6. **TS 回归**:`( cd ts && npm test )` —— 仍 74 过 / 0 败 / 1 skip。

## 4 已知约束
- RL2/RL5/RL6 适用:**不许改测试/门槛来凑过**;退出码 0 不等于完成,语义指标(各 PASS 计数)以真实 stdout 为准;诚实失败优于假装成功。
- L0 冻结:线协议(URL/命令词表/错误 5 类)、`@`-绑定语义、owner-scope 语义 —— 不得借"修复编译"改动。
- conformance 的 node 路径已改 PATH 解析;若本机 node 不在默认位置,导出 `DOPDB_TS_NODE`。
- 编译若因 `Perm` 常量块、`HashAll = All`、门调用等报错,做最小修复并记录(这是 Opus 盲写的预期风险点)。

## 5 实证要求(本回合的全部价值在此)
把 §3 每条命令的**完整真实 stdout** 抄进 `rounds/R8/receipt-verify.md`(各 PASS/FAIL 计数原样抄,不写"应该过")。任一条 FAIL:如实记 failed + 报错原文,**不往上盖 done**。全部 PASS 后,产 `rounds/R8/SEALED.md` 草稿(逐里程碑 + 证据指针)置 **pending-opus**,等操作者上传 → Opus 凭真实输出落终判联签。

## 6 自分类提醒
- 🟢 已实证:§3 每条本机真跑过、真实输出在回执里。
- 🔴 承重:M1/M2/M3/M5/HttpOn-Go 的"是否真符合预期"是承重终判 —— 你做完 + 实证 + 起草 SEALED + `pending-opus`,**终判归 Opus**,不自盖。
- 提醒:`HttpOn()` 无参 = 全开仅供 debug;这是设计预期,不是缺陷。
