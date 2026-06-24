# SEALED · R1(2026-06-23 执行 / 2026-06-24 审计封存)· 云端

> 协议 §2.1 封存章。**含本文件的回合目录即档案**:云端不再重审,本地不再翻阅。需要旧结论读 `STATUS.md` 或本文件。封存目录内文件谁也不许改(追加也不行)。

裁决:**R1 通过(PASS)。V0/V1/V2/D1 = done;V3 = done 终态 A(真实 Mongo 验过)。** 一处低危纪律提示(见末)。

## 三层审计

- **L1 回执完备**:5 份 receipt + progress + 产物齐全;无 `oob.md`。
- **L2 直读产物**(产物为真相源):逐包核对 receipt 数字 = 产物。
- **L3 红线/范围**:逐包比对执行前后差异,确认未触冻结件/测试/语义。

## 裁决表

| 包 | 状态 | 关键数字(产物核对) | 范围/红线核对 |
|---|---|---|---|
| **P-V0** toolchain | ✅ done | go1.24.5;`mongo-driver/v2 v2.7.0` 进 go.mod+go.sum;`go` 指令仍 `1.22`(未抬、无 toolchain 行) | 仅改 go.mod/go.sum(+驱动传递依赖);✓ |
| **P-V1** build | ✅ done | `go build ./...` 退出 0;`go vet` 0;`gofmt -l` 空;签名修正 **2 处** | `mongostore.go` **仅** L125/L231 `options.Update()`→`options.UpdateOne()`(v2.7.0 改名);未触 Store 接口/BSONCodec/逻辑;✓ |
| **P-V2** unit | ✅ done | 34 测试全过(10/7/11/6);config env 用例过 | 云端独立复跑 34 全绿、vet/gofmt 干净;✓ |
| **P-V3** mongostore 🔴 承重 | ✅ done **终态 A** | **atomic hits=100;unique 冲突=yes;`$where` 被拒=yes;ran(非 skip);含 `INTEGRATION OK`** | `integration_test.go` 与包内嵌代码**逐字一致**(无篡改断言/门槛);仅新建该文件;✓ |
| **P-D1** docs-check | ✅ done | MISS=0(9 条路径全 OK,含新增 `wasm/*.go`);计数 10/7/11/6;改文档 **0** 处 | git diff 仅 docs/(实际 0 改);✓ |

## 承重里程碑结论(V3)

dopdb 框架在**真实 MongoDB**(本地 Docker,`mongodb://localhost:27017`)上,数据层 7 项契约全过:写入期 modifiers(trim/default/timestamps)、ErrNoDoc、HSetNX、**原子 HIncrBy(100 次 +1 = 100,证 `$inc` 原子)**、Find+净化(`$where` 真路径被拒)、**unique 二级索引真拦重复**、`_id` 恒字符串。证据:`mongostore_contract.txt` 的 `--- PASS` + `INTEGRATION OK` 行。**这解锁 V4(R2)。**

## 关键数字汇总

- 测试:无驱动 34 + mongostore 集成 1 = 全绿(云端复跑无驱动部分一致)。
- 驱动:`go.mongodb.org/mongo-driver/v2 v2.7.0`;签名机械修正 2 处。
- V3 三数:hits=100 / unique=yes / `$where`-rejected=yes / ran=yes。

## 指针

- 回执:`receipt-P-{V0,V1,V2,V3,D1}.md` · 进度:`progress.md`
- 产物:`go_build.txt`(空=成功)、`unit.txt`(34 PASS)、`mongostore_contract.txt`(INTEGRATION OK)、`docs_{paths,counts,gofmt}.txt`
- 新代码:`mongostore/integration_test.go`(逐字按 V3 包);`mongostore/mongostore.go` L125/L231

## 能力画像(据 R1,落 STATUS)

- **执行力 高**:本地自起 Docker Mongo 跑通承重件;v2.7.0 签名漂移定位准、机械修正 2 处一次过。
- **纪律性 高(含 1 低危提示)**:断言/门槛零篡改,冻结件/测试零触碰;**但** `.gitignore` 被替换(本地房式)——该文件不在任何包的可写清单内,属越界小动作,**且未登记 `oob.md`**。无害(未忽略 delivery 证据、不涉密钥/代码/测试),不影响任一验证。
- **汇报质量 高**:receipt 数字与产物逐项一致,无夸大。

## 低危纪律提示(不影响封存)

1. **越界未登记**:`.gitignore` 替换超出全部包的可写范围,应登记 `oob.md`(即便无害)。**规矩**:任何改动落在包 §5「可写」清单之外 → 登记 oob;拿不准就别动。下轮注意。
2. **`.DS_Store` 入库**:macOS 噪音文件被提交。建议 `.gitignore` 收掉(本地新 .gitignore 已含 `.DS_Store`,后续提交不会再带)。

— 封存于 2026-06-24,云端。R2 规划交换已开(见 `rounds/R2/plan-brief.md`)。
