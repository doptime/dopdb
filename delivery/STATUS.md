# STATUS · dopdb 项目总台账(云端每回合刷新)

> 三方想知道「项目到哪了」只读这一个文件。深究证据再按指针进对应回合目录。本文件云端独写,本地与人只读。

更新于:2026-06-24(R1+R2+R3 已封存;**项目收尾完结**)· 云端

## 甘特(阶段 × 回合)

```
阶段            R1     R2     R3
V0–V3 (核+真Mongo) ✓封存
D1 docs-check      ✓封存(并行)
V4 http@mongo      ·      ✓封存(承重)
W1 wasm/ts         ·      ✓封存(并行)
H1 atomic-scoped   ·      ·      ✓封存(承重)
H2 scoped HKEYS/LEN·      ·      ✓封存(承重)
T1 TS 数据命令客户端·      ·      ✓封存(并行)
H4 perm 持久化     ·      ·      ✓封存
D2 docs/RUNBOOK 收尾·     ·      ✓封存(并行)
H3 msgpack-at-rest ·      ·      ✗不做(最低价值,除非人要求)

▣=本回合在跑  ✓=已封存验过  ·=未排  ✗=不做
R3 为最后收尾回合,已全部封存。**dopdb 项目交付完结(无 R4)。**
```

## 阶段表

| 阶段 | 状态 | 证据指针 |
|---|---|---|
| 沙箱内核(数据+api+http+config) | ✓ 本地复跑验过(34 测试) | rounds/R1 SEALED |
| V0–V2(工具链/构建/无驱动测试,本地) | ✓ 封存 | rounds/R1/SEALED.md |
| V3(mongostore 对真实 Mongo) | ✓ 封存(承重·真Mongo 7 契约全过) | rounds/R1/SEALED.md |
| WASM/TS(api 核心→wasm + TS 客户端) | ✓ 封存(go1.24/node19 复跑全绿) | rounds/R2/SEALED.md |
| V4(http 端到端@真 Mongo) | ✓ 封存(承重·真 Mongo 六契约全过) | rounds/R2/SEALED.md |
| W1(wasm/ts 本地构建+冒烟) | ✓ 封存(并行轨) | rounds/R2/SEALED.md |
| H1+H2(scoped 原子写 + scoped 键/计数) | ✓ 封存(承重·真 Mongo 四子测试 + -race) | rounds/R3/SEALED.md |
| T1(TS 数据命令客户端) | ✓ 封存(10/10 请求成形) | rounds/R3/SEALED.md |
| H4(权限持久化) | ✓ 封存(7 键一致) | rounds/R3/SEALED.md |
| D2(docs/RUNBOOK 收尾) | ✓ 封存(0 MISS) | rounds/R3/SEALED.md |
| H3(msgpack-at-rest) | 不做(最低价值,除非人要求) | — |

## 回合台账

| 回合 | 段 | 范围 | 状态 |
|---|---|---|---|
| R1 | 执行交换 | V0–V3 + D1 | **✓ 已封存**(SEALED.md);全 done,V3 终态 A |
| R2 | 执行交换 | V4 http@mongo(承重)+ W1 wasm/ts(并行) | **✓ 已封存**(SEALED.md);V4/W1 全 done |
| R3 | 执行交换 | H1+H2(scoped 硬化,承重)+ T1 + H4 + D2 | **✓ 已封存**(SEALED.md);4 包全 done;**项目收尾完结** |

## 封存清单

| 回合 | 裁决 | 关键数字 | 指针 |
|---|---|---|---|
| R1 | PASS;V0/V1/V2/D1 done,V3 done 终态 A | driver v2.7.0;签名修 2 处;34+1 测试绿;V3 hits=100 / unique=yes / `$where`-拒=yes | rounds/R1/SEALED.md |
| R2 | PASS;V4 done(承重)、W1 done | V4 六子测试全过、INTEGRATION OK;codec HTTP==BSON 字段集;`@uid` 伪造(query+body)剥离;wasm 2.88MB;SDK 端到端过 | rounds/R2/SEALED.md |
| R3 | PASS;H1+H2/T1/H4/D2 全 done | scoped 硬化四子测试 + -race 干净;TS 数据客户端 10/10;权限持久化 7 键一致;docs 0 MISS;**项目完结** | rounds/R3/SEALED.md |

## 需要人拍板(🔐)

- R3 无放宽类 🔐。H1/H2 承重件复用本地 Docker Mongo(`DOPTIME_TEST_MONGO_URI`)。T1 需 node/npm(已证可用)。
- 一处产品取舍待人确认(非阻塞):H3 msgpack-at-rest 价值最低,云端默认**不做**——若要做请明示。

## 能力画像(harness,累计 R1+R2)

- **执行力**:高。真 Mongo http 全栈六契约一次全过;wasm/TS 在 1.24/node19 复跑全绿;承重件零返工。
- **纪律性**:高(累计 2 处低危、均无害:R1 `.gitignore` 越界未登记;R2 STATUS.md 未提交漂移——**非篡改**,内容是云端交付的)。断言/门槛/冻结件/测试零触碰。
- **汇报质量**:高。回执与产物逐项一致,异常发现栏诚实。
