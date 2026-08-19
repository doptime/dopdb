# package-08 · 性能：实测驱动的查询引擎优化

> 对应审计 P11/P17（"全集合扫描"）。审计只做了推理，本包**先测量再优化再复测**。

## 方法

新建 `bench_test.go`（20,000 文档基准集）与 `mem_test.go`（驻留内存测量）。
所有数字来自单核 Intel Xeon @ 2.10GHz + Redis 7.0，`-benchtime=5x`。

**硬件不同数字必然不同。验收要看的是比例关系，不是绝对值。**

## 基线（修复前）

```
BenchmarkFindSelective    64 ms   13.0 MB   380k allocs
BenchmarkFindRegex       121 ms   57.7 MB   760k allocs
BenchmarkCountAll         53 ms   12.7 MB   360k allocs
BenchmarkCountFiltered    58 ms   13.1 MB   380k allocs
```

三个刺眼的地方：
1. `CountAll` —— "有多少条文档"花了 53 ms 和 12.7 MB
2. `FindRegex` 是 `FindSelective` 的 **1.9 倍时间、4.4 倍内存**
3. 每 20k 文档 380k 次分配 ≈ 每文档 19 次

## 修复

### 8.1 正则每文档重编译一次

`matchRegex` 在**每文档循环内部** `regexp.Compile`。20k 文档就是编译 20k 次同一个模式。
过滤器是按文档求值的，**模式不是**。

**修复**：`compileRegex` 缓存（Go `sync.Map`，TS `Map`），键为 `flags\0pattern`。
顺带承载 package-07 的 ReDoS 守卫（长度上限 + 嵌套量词拒绝）。

### 8.2 `count` 空过滤走全扫描

"有多少条文档"是**一条命令**。扫描并解码整个集合来回答它是纯浪费。

**修复**：`countFilter` / `countDocs` 在过滤器为空时直接 `HLEN`。

### 8.3 空过滤 + 无排序时根本不需要解码

`find({})` 分页时，id 和原始字节**就是全部答案**——解码每个文档只是为了把结果扔掉。

**修复**：`skipDecode := len(filter) == 0 && !needDoc`。
（`needDoc` = 有排序键或有投影。）

### 8.4 无界结果物化 —— 峰值驻留内存

这一条 `benchmem` **看不见**：`B/op` 统计的是**总分配**，被瞬时解码淹没。真正的问题是
**留在内存里的量**。

原来的 `find` 把所有匹配文档物化成 `[]row`，**然后**才应用 skip/limit。
`find({}, limit 10)` 打在百万文档集合上，要留住一百万个解码文档来返回十条。

**修复**：有界 top-N 堆。`retainCap(opt) = limit + skip`，只保留还有可能进入答案的行，
O(n) 内存、O(log n) 每行。扫描仍是 O(集合)（KVRocks 没有索引可查，**这改不了**），
但**内存由调用方实际要的量决定**。

无排序时行的解码 map 立即置 nil（`needDoc=false`），让 GC 立刻回收。

## 结果（修复后）

```
BenchmarkFindSelective            57 ms   13.1 MB   380k allocs
BenchmarkFindRegex                64 ms   13.2 MB   400k allocs   ← 121ms/57.7MB
BenchmarkCountAll              0.030 ms    206 B      8 allocs    ← 53ms/12.7MB
BenchmarkCountFiltered            58 ms   13.1 MB   380k allocs   （必然是扫描）
BenchmarkFindWideMatchSmallLimit  61 ms   13.1 MB   380k allocs
BenchmarkFindEmptyFilterPage      20 ms    3.1 MB    61k allocs   ← ~55ms/13MB
BenchmarkFindSortedTopN           82 ms   18.2 MB   700k allocs
```

| 场景 | 前 | 后 | 倍数 |
|---|---|---|---|
| `count` 空过滤 | 53 ms / 12.7 MB | **0.030 ms / 206 B** | **~1700×** |
| `find` + `$regex` | 121 ms / 57.7 MB | **64 ms / 13.2 MB** | 1.9× / 4.4× |
| `find` 空过滤分页 | ~55 ms / 13 MB | **20 ms / 3.1 MB** | 2.7× / 4.2× |

### 驻留内存（`mem_test.go`）

```
sorted over 20000 matches: limit 10 retains 0 KiB, no limit retains 1870 KiB
```

同一个排序查询，`limit 10` 驻留 ≈0 KiB，无 limit 驻留 1.87 MiB。这就是有界检索的
全部意义，也是 `benchmem` 看不出来的那一半。

## 我在这里写错过一次

**有界 top-N 堆的第一版，sift 比较方向反了**，保留的是**最差**的 N 条而不是最好的。
是 conformance 和集成测试抓出来的：

```
LIMIT 2 => [n:3, n:4]      # 应为 [n:0, n:1]
sort by age asc => [30 bob, 40 alice]   # 应为 [30 bob, 30 carol]
```

修复后补了 `topn_test.go` / `ts/test/topn.test.ts`：随机 300 行 × 5 种排序 × 5 种 n，
断言**与"全排序再切片"逐行相同**。

这是优化，**任何差异都是 bug 而不是取舍**——所以它需要等价测试，不是性能测试。

## 没有优化的部分，以及为什么

- **`find` 带过滤器仍是 O(集合)**。KVRocks 没有二级索引，没有可以下推的东西。
  这是架构事实，不是可以调优的常数。`docs/01-data.md` 把它写在最显眼的位置。
- **`countFiltered` 仍需全扫描**。同上。
- **每文档解码分配**（380k allocs / 20k 文档）是 CBOR → `map[string]any` 的固有成本。
  要降只能引入 schema 感知的惰性解码，那是重构不是优化。

## 涉及文件

| 文件 | 类型 |
|---|---|
| `query.go` | 修改（正则缓存、rowOrder、topN、retainCap） |
| `kvrocks.go` | 修改（有界 find、HLEN 短路、skipDecode） |
| `ts/src/query.ts` | 修改（正则缓存、rowLess、TopN） |
| `ts/src/kvrocks.ts` | 修改（findDocs 有界、countDocs 短路） |
| `ts/src/server.ts` | 修改（retainCap / effectiveSortKeys 接线） |
| `bench_test.go` | **新增** |
| `mem_test.go` | **新增** |
| `topn_test.go` | **新增** |
| `ts/test/topn.test.ts` | **新增** |
| `Makefile` | 修改（`make bench`） |
| `docs/01-data.md` | 修改（写入实测代价模型） |

## 验证

```bash
export DOPDB_TEST_KVROCKS_URI="redis://127.0.0.1:6666"

make bench                                          # 或
go test -run XXX -bench . -benchmem -benchtime=5x .

go test -count=1 -run 'TopNMatches|MemoryBound' -v .
cd ts && node --import tsx --test test/topn.test.ts
```

## 验收标准

1. `BenchmarkCountAll` 比 `BenchmarkCountFiltered` **快三个数量级以上**，
   且分配 < 1 KB。
2. `BenchmarkFindRegex` 的时间和内存与 `BenchmarkFindSelective` **同量级**
   （不再是 2 倍时间 / 4 倍内存）。
3. `BenchmarkFindEmptyFilterPage` 明显快于 `BenchmarkFindSelective` 且内存约为其 1/4。
4. `TestMemoryBoundOnSortedQuery` 通过：limit 查询驻留 < 2 MiB，且**至少比无 limit
   小一个数量级**。
5. `TestTopNMatchesFullSort` / `topn.test.ts` 通过——有界检索与全排序**逐行相同**。
6. 全量测试仍绿（优化不能改变语义）。

## 风险与影响

- 语义**不变**：top-N 与全排序等价，有测试钉住。
- `retainCap` 在 `skip` 极大时（> 2^20）回落到无界，避免过度预分配。
- 正则缓存在 Go 是 `sync.Map`（无上限，模式集通常很小）；TS 超过 1000 条清空。
  若有用户构造海量不同模式，这是可预见的内存增长点——已在代码注释中标注。
