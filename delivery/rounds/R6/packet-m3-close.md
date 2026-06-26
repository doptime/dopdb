包: P-R6-m3-close · 上游: P-R6-m3-go, P-R6-m3-ts (两者都 done) · 回合: R6
分级: 🟢 可客观自证 — 硬判据: STATUS.md M3 改通过; SEALED.md 落位
全景: 等 A+B done 后执行; M3 封存
任务一句话: M3 测试全过 → 更新 STATUS + 写 SEALED.md
回执写到: delivery/rounds/R6/receipt-P-R6-m3-close.md

## 1 背景 · 现在是什么情况
- 若 P-R6-m3-go 和 P-R6-m3-ts 都 done, M3 watch E2E 通过
- 需要更新 STATUS.md M3 状态为 ✓
- 需要写 R6 SEALED.md

## 2 意图 · 为什么做、什么算好
完成: STATUS.md 刷新 + SEALED.md 落位。红线: 全部适用。 修改令: 无。

## 3 任务 · 具体做什么
**单元 1**: 更新 `delivery/STATUS.md`:
- M3 watch E2E 改 ✓ 通过(R6)
- 回合台账加 R6 行
- 验证快照更新
- 里程碑链 M3 改 ✓

**单元 2**: 写 `delivery/rounds/R6/SEALED.md`

铁顺序: 先落产物 → 记进度

## 4 验收 · 怎么算完成
- [ ] STATUS.md 已刷新 (M3=✓)
- [ ] SEALED.md 已落位
- [ ] 进度账落 `delivery/rounds/R6/progress.md`

## 5 边界 · 不要做什么
- 只读区: 全部代码不改
- 可写区: STATUS.md, SEALED.md, progress.md
- 明确不做: 不碰代码

## 6 预算与换法
每单元最多 2 次。整包超 15 分钟 → 截断。

## 7 收尾
按协议 §3 写回执。
