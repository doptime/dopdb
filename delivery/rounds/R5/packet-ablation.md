包: P-R5-ablation · 上游: 无 · 回合: R5
分级: 🟢 可客观自证
全景: 与 P-R5-conformance 并排; 消融复审 + M3 suspend 落盘
任务一句话: 消融复审查新缺陷; M3 suspend 正式落盘
回执写到: delivery/rounds/R5/receipt-P-R5-ablation.md

## 1 背景
- R1-R4 共发现 F1-F13 缺陷,全部已修
- R4 后代码有新增(bootstrap.go ServeWithHandle, http_accessor.go HttpSetNXScoped, serve.go scoped HSETNX 分支)
- 需复审: 新代码是否引入新缺陷; 旧修复是否退步

## 2 意图
完成: 逐组件审查 R4 新增代码,问"抽掉它塌什么"; 在 STATUS.md 记录 M3 suspend。红线: RL1-RL8 全部适用。 修改令: 无。

## 3 任务
**单元 1**: 消融复审 R4 新增代码:
- `httpserve/bootstrap.go` ServeWithHandle: 是否安全关停? Serve 签名是否兼容?
- `http_accessor.go` HttpSetNXScoped: scoped 存在性检查是否正确? 非 scoped 路径是否退步?
- `httpserve/serve.go` HSETNX scoped 分支: 403 是否正确?

**单元 2**: 检查 R4 前修复是否退步:
- F1 mergeScope $and → 仍在
- F2 buildRuntime fail-closed → 仍在
- F3 limit clamp 100/1000 → 仍在
- F13 s=/p= 解析 → 仍在
- 错误码 5 类 → 仍在

**单元 3**: STATUS.md 记录 M3 suspend:
- M3 状态: suspend (原因: 无副本集,无 mongosh)
- 写 1-2 句说明

铁顺序: 先落产物→自检全过→记进度

## 4 验收
- [ ] 消融复审结论(有/无新 Fxx)
- [ ] STATUS.md 更新 M3 为 suspend
- [ ] 进度账落 `delivery/rounds/R5/progress.md`

## 5 边界
- 只读区: 全部源码不改(只读审查)
- 可写区: `delivery/STATUS.md`(L2)
- 明确不做: 不修任何代码

## 6 预算与换法
每单元最多 2 次。整包超 30 分钟 → 截断。

## 7 收尾
按协议 §3 写回执。
