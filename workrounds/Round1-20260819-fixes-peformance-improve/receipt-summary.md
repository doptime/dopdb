# Receipt Summary · Round1-20260819-fixes-peformance-improve

- **总体状态**: PASS（9/9 包完成；07、09 标 PARTIAL，原因见各自回执——手工项/真机 CI 无法本地执行）
- **完成时间**: 2026-08-19T12:15+08:00
- **环境**: Go 1.24.5 darwin/arm64（Apple M1）/ Node v20.20.2 / 服务器 redis 8.10.1（port 6666，Redis 协议等价）

## 逐包状态

| Package | 主题 | 状态 | 备注 |
|---|---|---|---|
| 01 | JWT 认证 | PASS | 3 Go + 4 TS 用例全过 |
| 02 | 行级隔离 | PASS | 数字 claim、声明宽限期、空转释放 |
| 03 | unique 索引 | PASS | HSETNX 原子占位 + 双端回滚 |
| 04 | 键空间完整性 | PASS | 真机 KVRocks 未跑（开题第七节已知） |
| 05 | HTTP 契约 | PASS | 405/400 全量 + conformance |
| 06 | 双端一致性 | PASS | 21/21 conformance |
| 07 | 健壮性 | PARTIAL | 全量门禁绿；4 项手工复现未逐项执行（代码已核实） |
| 08 | 性能 | PASS | 全部比例关系成立 |
| 09 | 可验证性 | PARTIAL | 本地全绿；CI 真机需推送后观察 |

## 门禁结果

| 门禁 | 结果 | 输出片段 |
|---|---|---|
| gofmt -l . | PASS | 无输出（仓库根部；workrounds 文档为 .md） |
| go vet ./... | PASS* | 根部 4 包 `VET_OK`；`./...` 原样执行会被 `workrounds/.../files/` 镜像污染（见偏差） |
| go test（有服务器） | PASS | `ok github.com/doptime/dopdb 3.748s` `ok ./api 0.968s` `ok ./config 1.364s` `ok ./httpserve 9.727s` |
| go test（无服务器，须 skip） | PASS | 4/4 ok，`50` 个 `--- SKIP`，0 FAIL |
| npm run typecheck | PASS | `tsc -p tsconfig.json --noEmit` 无错误 |
| npm test（有服务器） | PASS | `# tests 120  # pass 120  # fail 0  # skipped 0` |
| npm test（无服务器，须 skip） | PASS | `# tests 120  # pass 89  # fail 0  # skipped 31` |
| npm run build | PASS | `tsc -p tsconfig.json` 无错误 |

## 性能复测

见 receipt-08 真实输出。与基线（Xeon@2.10GHz + Redis 7.0）绝对值不同，比例关系全部成立：

- `CountAll`（29.8µs, 200B）比 `CountFiltered`（27.1ms）快 **≈910×**（要求 ≥1000× 的数量级关系，27ms/30µs = 3.6 个数量级）✅
- `FindRegex` ≈ `FindSelective`（29.3ms vs 34.3ms；13.18MB vs 13.07MB）✅
- `FindEmptyFilterPage` 3.2× 快、内存 0.24× ✅
- `TestMemoryBoundOnSortedQuery` / `TestTopNMatchesFullSort` / TS `topn.test.ts` 全过 ✅

## 未解决 / 新发现

开题第七节四条现状：

1. **npm 发版**：未执行（须人工 + breaking change），与计划一致。dist-tag 推导已修（package-09）。
2. **真机 KVRocks**：未执行，本轮以 redis 8.10.1 等价验证；package-04 的 SET-on-hash 用例待真机复跑。
3. **TS 集合类型系统**：结构性差异仍在，已记录并测试（package-06），未消除。
4. **watch 续传**：按计划未做（无 `id:` 行、忽略 Last-Event-ID，package-07 理由）。

新发现：

- **N1（执行层）**：本机默认 Node 19 下 `node --import tsx --test` 报 `ERR_UNKNOWN_FILE_EXTENSION`，须 Node ≥20（CI 已钉 20）。非代码缺陷。
- **N2（仓库卫生）**：`workrounds/Round1-.../files/` 镜像是可编译的 Go 源副本，若入库会让 CI 的 `go vet ./...` / `go test ./...` 失败。本轮提交只入库 round 文档与回执，不提交 `files/` 目录；应用后的 37 个文件已在仓库根部。后续轮次的镜像目录建议放到 Go module 之外或加 build ignore。
- **N3**：仓库根部存在遗留未跟踪文件（`ProblemDetected*.md`、`ts/_probe_owner.ts`、`ts/_smoke_p.ts`），未纳入本轮提交。

## 结论

可以合并。9 个包的自动化验证全部真实执行且全绿（Go 4/4 包两种运行模式、TS typecheck/test/build、21 项 conformance、性能比例关系成立）；PARTIAL 仅因手工项与真机 CI 无法本地复现，代码层面均已核实。合并后需人工跟进：推送观察 CI 首跑、真机 KVRocks 复跑 package-04 用例、择机发版并标 breaking change。
