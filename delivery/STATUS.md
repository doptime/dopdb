# STATUS · dopdb 项目总台账(云端每回合刷新)

> 三方想知道「项目到哪了」只读这一个文件。深究证据再按指针进对应回合目录。本文件云端独写,本地与人只读。

更新于:2026-06-23(R1 调和定稿;本地 go1.24.5)· 云端

## 甘特(阶段 × 回合)

```
阶段     R1          R2
V0 toolchain   ▣ 定稿
V1 build       ▣ 定稿
V2 unit        ▣ 定稿
V3 mongostore  ▣ 定稿(承重)
V4 http@mongo  ·           ▽ 排期(依赖 V3 验过)
W1 wasm/ts     ·           ▽ 排期(make wasm + make ts + SDK 冒烟)
H1 atomic ups  ·                 → 待 V4 后
H2 scoped k/l  ·
H3 msgpack     ·
H4 perm persist·
D1 docs-check  ▣ 定稿(并行轨)

▣=本回合在跑  ▽=已排期  ✓=已封存验过  ·=未排
```

## 阶段表

| 阶段 | 状态 | 证据指针 |
|---|---|---|
| 沙箱内核(数据+api+http+config) | 已自测(34 测试,memstore/JSON) | 作者沙箱;待 R1 本地复跑 |
| V0–V2(工具链/构建/无驱动测试,本地) | R1 调和定稿,执行中 | rounds/R1/ |
| V3(mongostore 对真实 Mongo) | R1 调和定稿,执行中(承重) | rounds/R1/packet-P-V3-* |
| WASM/TS(api 核心→wasm + TS 客户端) | 作者沙箱已自测(wasm 桥 + tsc + SDK 端到端);待 R2 本地复跑 | wasm/、clients/ts/、docs/04 |
| V4(http 端到端@真 Mongo) | 排期 → R2 | — |
| W1(wasm/ts 本地构建+冒烟) | 排期 → R2 | rounds/R2(待建) |
| H1–H4(硬化) | 未排 | RUNBOOK §未竟 |

## 回合台账

| 回合 | 段 | 范围 | 状态 |
|---|---|---|---|
| R1 | 执行交换(冷启动直发) | V0–V3 + D1 并行轨 | **已调和定稿**(reconcile.md),本地执行中,待回执 |
| R2 | (待排) | V4 http@mongo + W1 wasm/ts | 未下发 |

## 封存清单

(暂无——R1 尚未封存。)

## 需要人拍板(🔐)

- R1 无 🔐。
- 提供测试用 MongoDB:请人给一个**隔离/一次性**库的连接串,设进 `DOPTIME_TEST_MONGO_URI`(集成测试会建集合/索引、写删文档)。无则 V3 走终态 B(suspend),不阻塞 🟢。

## 能力画像(harness,据回执维护)

(R1 为本台 harness 首跑,回执回来后填:执行力 / 纪律性 / 汇报质量。)
