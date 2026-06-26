包: P-R5-conformance · 上游: 无 · 回合: R5
分级: 🟢 可客观自证
全景: 串行主线; I-P4 conformance 套件
任务一句话: 建 conformance 套件,对核心命令验证 Go↔TS 语义一致
回执写到: delivery/rounds/R5/receipt-P-R5-conformance.md

## 1 背景
- Go 和 TS 是两套对等实现,共享同一线协议
- 但无自动化验证两端一致——靠人工目视
- I-P4 要求 "conformance 套件对核心命令两端 diff 为空"
- 无真 Mongo → 基于 TS server.test.ts 的内存假 Mongo

## 2 意图
完成: conformance 套件验证 hget/hset/hsetnx/hdel/find/hmget/hexists/hkeys/hlen/hincrby 共 10 个核心命令,Go↔TS 响应一致。红线: RL1-RL8 全部适用。 修改令: 无。

## 3 任务
**单元 1**: 在 `ts/test/` 新建 `conformance.test.ts`,实现:
- 用 TS 的 fakeCollection(已在 server.test.ts)执行命令
- 对照 Go 侧相同命令的预期行为(从 Go 集成测试推导)
- 验证: 返回 shape 一致、错误码一致、403/404 场景一致

**单元 2**: 覆盖以下命令(各 1-2 断言):
- hget: 存在→200+doc; 不存在→404
- hset: upsert 成功→200
- hsetnx: 不存在→{inserted:true}; 已存在→{inserted:false}; scoped 他人 key→403
- hdel: 删除→200
- find: 过滤→数组; scoped 敌意 filter→空
- hexists: 存在→true; 不存在→false
- hmget: 批量→对齐数组(null 填缺失)

**单元 3**: 错误模型对齐:
- 400/validation: 无效输入
- 401/unauthorized: 无 JWT
- 403/forbidden: scoped 跨租户
- 404/not_found: 文档不存在
- 409/conflict: unique 冲突/hsetnx 已存在

铁顺序: 先落产物→自检全过→记进度

## 4 验收
- [ ] `ts/test/conformance.test.ts` 存在且非空
- [ ] `npx tsx --test test/conformance.test.ts` 通过
- [ ] `npx tsc --noEmit` 干净
- [ ] 进度账落 `delivery/rounds/R5/progress.md`

## 5 边界
- 只读区: `ts/src/` 全部源文件不改; Go 代码不改
- 可写区: `ts/test/conformance.test.ts`
- 明确不做: 不跑真 Mongo; 不改服务端逻辑

## 6 预算与换法
每单元最多 3 次。整包超 60 分钟 → 截断。

## 7 收尾
按协议 §3 写回执。
