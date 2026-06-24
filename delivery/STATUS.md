# STATUS · dopdb 项目总台账(云端每回合刷新)

> 三方想知道「项目到哪了」只读这一个文件。深究证据再按指针进对应回合目录。本文件云端独写,本地与人只读。

更新于:2026-06-24(R1 已封存;R2 规划交换下发)· 云端

## 甘特(阶段 × 回合)

```
阶段     R1          R2
V0 toolchain   ✓ 封存
V1 build       ✓ 封存
V2 unit        ✓ 封存
V3 mongostore  ✓ 封存(承重·真Mongo过)
V4 http@mongo  ·           ▣ 规划(承重)
W1 wasm/ts     ·           ▣ 规划(并行轨)
H1 atomic ups  ·                 → 待 V4 后
H2 scoped k/l  ·
H3 msgpack     ·
H4 perm persist·
D1 docs-check  ✓ 封存(并行轨)

▣=本回合在跑  ▽=已排期  ✓=已封存验过  ·=未排
```

## 阶段表

| 阶段 | 状态 | 证据指针 |
|---|---|---|
| 沙箱内核(数据+api+http+config) | ✓ 本地复跑验过(34 测试) | rounds/R1 SEALED |
| V0–V2(工具链/构建/无驱动测试,本地) | ✓ 封存 | rounds/R1/SEALED.md |
| V3(mongostore 对真实 Mongo) | ✓ 封存(承重·真Mongo 7 契约全过) | rounds/R1/SEALED.md |
| WASM/TS(api 核心→wasm + TS 客户端) | 作者沙箱已自测(go1.22);待 R2/W1 本地复跑(go1.24) | wasm/、clients/ts/、docs/04 |
| V4(http 端到端@真 Mongo) | R2 规划中(承重) | rounds/R2/plan-brief.md |
| W1(wasm/ts 本地构建+冒烟) | R2 规划中(并行轨) | rounds/R2/plan-brief.md |
| H1–H4(硬化) | 未排 | RUNBOOK §未竟 |

## 回合台账

| 回合 | 段 | 范围 | 状态 |
|---|---|---|---|
| R1 | 执行交换 | V0–V3 + D1 | **✓ 已封存**(SEALED.md);全 done,V3 终态 A |
| R2 | 规划交换 | V4 http@mongo(承重)+ W1 wasm/ts(并行) | plan-brief 已下发,待本地 v0-plan |

## 封存清单

| 回合 | 裁决 | 关键数字 | 指针 |
|---|---|---|---|
| R1 | PASS;V0/V1/V2/D1 done,V3 done 终态 A | driver v2.7.0;签名修 2 处;34+1 测试绿;V3 hits=100 / unique=yes / `$where`-拒=yes | rounds/R1/SEALED.md |

## 需要人拍板(🔐)

- R2 无 🔐。Mongo URI 本地已具备(R1 V3 用本地 Docker 跑通),V4 直接复用 `DOPTIME_TEST_MONGO_URI`。
- W1 需 node + npm(联网)。本地 R1 已证联网可用;若 node/npm 缺失,W1 走终态 B(suspend),不阻塞 V4。

## 能力画像(harness,据 R1 回执)

- **执行力**:高。自起 Docker Mongo 跑通承重件;v2.7.0 签名漂移定位准、机械修正一次过。
- **纪律性**:高(一处低危:`.gitignore` 越界替换未登记 oob;无害)。断言/门槛/冻结件/测试零触碰。
- **汇报质量**:高。回执数字与产物逐项一致。
