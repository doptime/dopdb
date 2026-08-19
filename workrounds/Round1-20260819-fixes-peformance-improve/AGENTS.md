# AGENTS.md · Round1-20260819-fixes-peformance-improve

本文件是本工作轮的**执行与回执协议**。按 AGENTS.md 约定编写，任何读取 AGENTS.md 的
agent runner（含 oh-my-pi）都应先读本文件再动手。

> 若你的 runner 有额外约定（回执文件名前缀、状态字段枚举、目录深度限制等），以你的
> runner 为准，但**回执的必填字段一个都不能少**——见第四节。

---

## 一、你要做什么

1. 把 `files/` 应用到仓库。
2. 逐个 package 验证。
3. **每个 package 写一份 `receipt-XX-<name>.md`。**
4. 全部完成后写 `receipt-summary.md`。

没有回执的 package 视为未完成。回执是本轮的交付物，不是附属品。

## 二、应用

`files/` 镜像仓库根目录结构：

```bash
cp -r workrounds/Round1-20260819-fixes-peformance-improve/files/. <repo-root>/
```

37 个文件：29 修改 + 8 新增。**不要**手工挑拣——包与包之间有依赖（例如
package-08 的有界检索依赖 package-06 定下的响应形状）。

## 三、验证

### 3.1 环境

需要一个 Redis 协议服务器。KVRocks 是目标运行环境；redis-server 在测试用途上等价
（dopdb 只用公共命令集）。

```bash
kvrocks -c kvrocks.conf            # 或 redis-server --port 6666
export DOPDB_TEST_KVROCKS_URI="redis://127.0.0.1:6666"
```

### 3.2 全量门禁

```bash
gofmt -l .            # 必须无输出
go vet ./...
go test -count=1 ./...
( cd ts && npm ci && npm run typecheck && npm test && npm run build )
```

### 3.3 两种运行都要跑

| 运行 | 期望 |
|---|---|
| **有服务器** | Go 4/4 包通过；TS 全部通过；conformance 全部 PASS |
| **无服务器** | 全部通过，需要服务器的用例 **skip 而非 fail** |

第二种是硬要求：环境缺失导致 CI 变红，最终结果是没人看 CI。

### 3.4 每个 package 的专属验证

各 `package-XX-*.md` 的"验证"一节给了可直接执行的命令。**必须逐个跑，不能只跑全量
门禁就签字**——全量绿灯不能证明某一条修复真的生效（本轮就有一个反例：有界 top-N 堆
的比较方向写反了，只有专属的等价测试和 conformance 能抓到）。

## 四、回执格式（必填字段）

每个 package 一份，文件名 `receipt-XX-<name>.md`，`XX` 与 package 编号一一对应。

```markdown
# Receipt · package-XX-<name>

- **状态**: PASS | FAIL | PARTIAL | SKIPPED
- **执行时间**: <ISO 8601>
- **执行环境**: Go <version> / Node <version> / 服务器 <KVRocks x.y.z 或 redis x.y>
- **应用的文件**: <本包声明的文件清单，逐个确认存在且已覆盖>

## 验证记录

对本包"验证"一节的每条命令：

| 命令 | 期望 | 实际 | 结论 |
|---|---|---|---|
| ... | ... | **粘贴真实输出片段** | PASS/FAIL |

## 验收标准逐条核对

对本包"验收标准"的每一条，写明达成与否，**不达成必须说明原因**。

## 偏差

与本包描述不符的任何事情。没有就写"无"。

## 备注

复现出的新问题、对修复方案的异议、后续建议。
```

### 四条硬规则

1. **实际输出必须是真实粘贴的**，不是复述、不是"符合预期"。没跑就写 SKIPPED，
   不要写 PASS。
2. **FAIL 不是失败，隐瞒才是。** 一个如实的 FAIL 回执比一个粉饰的 PASS 有价值得多。
3. **不要为了让测试通过而修改被测对象的门禁**（放宽断言、跳过用例、调低阈值）。
   如果测试挡路，在"偏差"里写清楚，不要绕过去。
4. **不确定就标 PARTIAL 并说明**，不要向上取整成 PASS。

## 五、汇总回执

全部 package 完成后写 `receipt-summary.md`：

```markdown
# Receipt Summary · Round1-20260819-fixes-peformance-improve

- **总体状态**: PASS | FAIL | PARTIAL
- **完成时间**: <ISO 8601>
- **环境**: <同上>

## 逐包状态

| Package | 主题 | 状态 | 备注 |
|---|---|---|---|
| 01 | JWT 认证 | | |
| ... | | | |

## 门禁结果

| 门禁 | 结果 | 输出片段 |
|---|---|---|
| gofmt -l . | | |
| go vet ./... | | |
| go test（有服务器） | | |
| go test（无服务器，须 skip） | | |
| npm run typecheck | | |
| npm test（有服务器） | | |
| npm test（无服务器，须 skip） | | |

## 性能复测

粘贴 `go test -run XXX -bench . -benchmem .` 的真实输出，并与
package-08 记录的基线对比。**硬件不同数字必然不同，要看的是比例关系是否成立**
（例如 `count` 空过滤应比 `find` 快三个数量级以上）。

## 未解决 / 新发现

开题报告第七节列了四条本轮做不到的事。逐条确认现状，并追加你新发现的问题。

## 结论

一段话：这一轮能不能合并，还差什么。
```

## 六、边界

- **不要扩大范围。** 发现新问题写进回执的"新发现"，不要顺手改——本轮的因果链要
  保持可追溯。
- **不要删除或弱化任何测试。** 本轮新增的测试是修复的证据；删掉测试等于删掉证据。
- **不要改这些行为**，它们是本轮的刻意决定，不是遗漏：
  - watch 不发 SSE `id:` 行、忽略 `Last-Event-ID`（package-07 说明理由）
  - TS 端 hash 命令打在非 Hash 集合上返回空而非报错（package-06 说明理由）
  - 写命令仅 POST，GET 返回 405（package-05）
- **发版不在本轮范围内。** package-09 修了 dist-tag 推导逻辑，但执行 `npm publish`
  需要人来决定，且必须标 breaking change（开题报告第七节）。
