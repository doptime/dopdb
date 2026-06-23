# v0 计划 · dopdb · R1(2026-06-23)

> 本地理解与拟建方案。只写不执行。V3 的自证判据写得最实——它是承重里程碑。

## 环境快照(本地实测)

- `go version` → go1.24.5 darwin/arm64 (≥ 1.22, 满足)
- `GOPROXY` → `https://proxy.golang.org,direct` (网络通畅)
- `grep mongo go.mod` → 无命中(驱动未拉)
- `mongod` → 未安装
- `DOPTIME_TEST_MONGO_URI` → 未设

## 包分解

### P-V0-toolchain 🟢

**目标**: Go 工具链可用, mongo-driver/v2 进 go.mod + go.sum, delivery 提交进 git。

**拟建**:
- `go get go.mongodb.org/mongo-driver/v2@latest` + `go mod tidy`
- `git add delivery/ go.mod go.sum` → commit

**自证**:
- `go version` 退出 0, 版本 ≥ 1.22
- `grep "go.mongodb.org/mongo-driver/v2" go.sum` 有命中
- `git log --oneline -1` 显示本包提交

---

### P-V1-build 🟢

**目标**: `go build ./...`(含 mongostore) 退出 0, vet 干净, gofmt 干净。

**拟建**:
- 全量构建 `go build ./...`
- 若 mongostore 因 v2 签名漂移失败,仅机械修正 `mongostore/mongostore.go` 的驱动调用(options builder 方法名、`mongo.Connect` 形参、`bson.Raw` 取值 API)
- `go vet ./...`, `gofmt -l .`

**自证**:
- `go build ./...` 退出 0, 产物 `go_build.txt` 留痕
- `go vet ./...` 退出 0
- `gofmt -l .` 空输出
- 若改了 mongostore: `git diff` 仅限驱动调用签名,未触 Store 接口 / BSONCodec

---

### P-V2-unit 🟢

**目标**: 无驱动测试全绿, 基线 34 测试本地坐实。

**拟建**:
- `go test -count=1 -v . ./api ./httpserve ./config ./memstore`
- 逐包数 PASS 行,与基线(数据 10 / api 7 / httpserve 11 / config 6)比对
- `config.Load` 冒烟: `DOPTIME_JWT_SECRET=smoke DOPTIME_MONGO_URI='mongodb://localhost:27017' go test -run TestLoadAndEnvOverride ./config`

**自证**:
- 四包均 `ok`, 合计退出 0
- PASS 数与基线一致(10+7+11+6=34);若不符,记异常
- config env 用例退出 0

---

### P-V3-mongostore-contract 🔴 承重

**目标**: 在真实 MongoDB 上跑通与 memstore 同构的契约集成测试。

**拟建**: 新建 `mongostore/integration_test.go`, 逐字按云端给的测试代码。测试覆盖 7 项契约:
1. Round-trip + 写入期 modifiers(trim/default/timestamps)
2. ErrNoDoc
3. HSetNX
4. 原子 HIncrBy(100 次 +1 → hits=100)
5. Find + 净化($where 被拒)
6. Unique 二级索引冲突
7. _id 恒字符串

**自证**(硬判据):
- 有 `DOPTIME_TEST_MONGO_URI` 且可连: `go test -run TestMongoContract ./mongostore` 退出 0 且输出含 `INTEGRATION OK`(非 SKIP)
- 关键数字: atomic hits 值 = 100, unique 冲突触发 yes/no, $where 被拒 yes/no
- 断言不过: 不改测试, 记 failed + 关键数字, suspend

**本地分支**: `DOPTIME_TEST_MONGO_URI` 未设 → 测试 t.Skip → 终态 B, 记 suspend。这是诚实负结果,不是失败。

**分支策略**:
- 若本地后续有 MongoDB( docker 或远程),可重跑验证 → 终态 A
- 若一直无: V3 suspend,不阻塞 V0/V1/V2/D1

---

### P-D1-docs-check 🟢 并行轨

**目标**: `docs/` 五篇中引用的路径、命令、测试数与仓库一致。

**拟建**:
- 抽取文档中 `path/file.go` 引用,逐个确认文件存在
- 核对回归命令(`go test . ./api ./httpserve ./config ./memstore`)可跑
- 核对测试数基线(34)
- 发现不符仅在 `docs/` 内修正

**自证**:
- `docs_paths.txt` 无 MISS
- 回归命令 EXIT 0, gofmt 空, vet EXIT 0
- `docs_counts.txt` 与文档数字一致
- `git diff --name-only` 仅含 docs/ 下文件

---

## 执行顺序与并行

```
承重主线: V0 → V1 → V3
并行轨:   V2(不依赖 V0/V1)、D1(完全独立)
```

- V0/V1 串行为 V3 铺路(驱动 → 编译 → 测试)
- V0 下发后立即并跑 V2(本地 34 测试不碰驱动)
- V3 卡住(无 Mongo / 编译问题)时转 D1
- V3 若走终态 B(suspend),其余 🟢 包全绿即 done

## 已知风险

1. **无测试 Mongo**: `DOPTIME_TEST_MONGO_URI` 未设,本地无 mongod。V3 必走终态 B。若人提供 URI,可切到终态 A。
2. **驱动 v2 签名漂移**: 沙箱从未编译 mongostore,`options.*` builder 和 `mongo.Connect` 签名可能有微小偏差。但这是机械修正,不涉语义。
3. **BSON vs JSON codec 分歧**: memstore 用 JSONCodec, mongostore 用 BSONCodec。`bson:"_id"` vs `json:"_id"` 若 struct tag 不一致,可能在 V4 暴露。本轮 V3 先把适配器契约坐实,codec 字段映射留 V4 专验。
