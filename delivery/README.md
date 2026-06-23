# delivery/ — Grounded Delivery 协作运行库(v3.0)

> 本目录是云端 × 本地 harness × 人三方协作的**唯一**组织信息落点(取代旧的隐藏目录
> `.harness-run-time/` 与仓库根 `agent-kit/`)。它对三方都可见、可读:
> 人看甘特与拍板点,本地看当前回合的包,云端看痕迹与台账。
> **任何一方开工前,先读 `ROLES.md` 认领自己的角色,再读 `STATUS.md` 找到"现在该做什么"。**

## 目录结构(寿命从长到短,目录即模块)

```
delivery/
├─ README.md        本文件:目录定位与放置规则
├─ ROLES.md         三方角色卡(人 / 本地 harness / 云端),静态
├─ STATUS.md        项目总台账:甘特图、阶段表、回合台账、封存清单(云端每回合刷新)
├─ kit/             方法论本体,跨项目通用 ── 迁移到新项目时整个目录拷走即可
│  ├─ 00-protocol.md      双端契约:格式、红线、冻结、打包(唯一权威)
│  ├─ 01-cloud-manual.md  云端手册:审计 SOP、切包、冷启动
│  └─ 02-local-manual.md  本地手册:执行循环、回执纪律
├─ project/         本项目慢变事实(每项目一份,换项目时换掉这一格)
│  └─ 10-project-card-dopdb.md
└─ rounds/          回合目录:一回合一目录,这就是"基于目录的模块化"
   └─ R1/           当前回合:规划交换 plan-brief.md(云↓)+ v0-plan.md(本地↑);
                    执行交换 brief.md + packet-*.md(云↓)+ receipt-*.md + progress.md + oob.md(本地↑)
                    (尚无封存回合;审计完成后该回合会获得 SEALED.md)
```

## 防重复机制(为什么两端都能"快进")

1. **回合目录 + SEALED.md**:云端审计完一个回合,在该回合目录写 `SEALED.md`
   (裁决 + 关键数字)。此后该目录是档案:云端下回合不再重审,本地不再翻阅,
   人不必重看。结论已抄进 `STATUS.md`。
2. **STATUS.md 是唯一滚动摘要**:三方想知道"项目到哪了",只读这一个文件;
   想深究证据,再按指针进对应回合目录。
3. 因此每个回合,云端只需读:`STATUS.md` + 未封存的回合目录;本地只需读:
   `ROLES.md`(首次)+ `STATUS.md` + 当前回合目录。

## 人(信使)的循环(v3.0:一个回合两段交换,纯整理回合退化为一段)

一个回合现在有两次往返。**规划交换**(先对齐理解,极便宜):
1. 云端下行一份薄 `plan-brief.md`(只圈本回合范围)给你,你放进 `delivery/rounds/R<n>/`,启动 harness。
2. harness 写出 `v0-plan.md`(它对任务的分解与理解,**不执行**)。你按 §7 打包上传,云端**对账**:diff 出理解分歧 → 改清楚契约 / 写死澄清 → 定稿 `brief.md` + `packet-*.md` 给你下载。

**执行交换**(再动手):

3. 你把定稿 `brief`+包按原路径放进 `delivery/`,启动 harness 执行(🟢 放手做、🔴 证不了就 suspend)。
4. harness 跑完,你按 §7 全量打包(必须含 `.git/`、`delivery/`、`go.mod` 等)上传,说"  "。
5. 云端重扫 + 三层审计 + 封存上回合 → 产出新一回合的 `plan-brief.md`(或快路径下直接给定稿包)与刷新后的 `STATUS.md`、`SEALED.md` 给你下载 → 回到第 1 步。

**快路径**:若某回合纯属 🟢 整理(归档、改文档,无承重里程碑),云端会跳过规划交换,直接下定稿包——那个回合就只有一次往返。

## 写入权属(单写者原则,避免冲突)

| 路径 | 谁写 | 谁读 |
|---|---|---|
| `kit/`、`project/`、`STATUS.md`、`ROLES.md`、`SEALED.md`、`plan-brief.md`、`brief.md`、`packet-*` | 云端产、人放置、harness 落库提交 | 三方 |
| `rounds/<当前>/v0-plan.md`(规划交换)、`receipt-* · progress.md · oob.md`(执行交换) | 本地 harness | 三方 |
| 已封存回合目录 | 谁也不写 | 原则上不再读 |
