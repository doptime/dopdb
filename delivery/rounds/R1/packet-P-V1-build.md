# 包: P-V1-build · 回合 R1(2026-06-23 · 真实 MongoDB 首次验证)

分级: 🟢 可客观自证(`go build ./...` 退出 0 + `go vet ./...` 退出 0 + `gofmt -l .` 空)
上游: P-V0(驱动已进 go.mod)
回执写到: `delivery/rounds/R1/receipt-P-V1-build.md`(模板见 delivery/kit/00-protocol.md §3)

全景: STATUS.md 甘特 V1;承重主线第二步(让 mongostore 真编译);并行轨 V2、D1。

任务一句话: 全仓库编译通过(**含 mongostore**)、vet 干净、gofmt 干净;若 mongostore 因驱动 v2 签名漂移编译失败,机械修正。

## 1 背景 · 现在是什么情况

`mongostore/mongostore.go` 是唯一 import 驱动的文件,在作者沙箱**从未编译过**。它按 `go.mongodb.org/mongo-driver/v2` 的 API 写就(`mongo.Connect(options.Client().ApplyURI(uri))`、`ReplaceOne/UpdateOne/BulkWrite/FindOne().Raw()/cursor.Current.Lookup("_id").StringValueOK()`、`options.Index()/Replace()/Update()/Find()/Count()` 这套 builder)。v2 点版本之间这些 builder 偶有签名微调。

其余包在沙箱已构建通过,预期无新问题。

## 2 意图 · 为什么做、什么算好

让承重件 mongostore 在本地工具链下真正编译,为 V3 集成测试铺路。完成 = 三条命令全过。

红线: RL1–RL8 全部适用。**RL2 特别强调**:不许通过改测试/门槛让构建「看起来过」;构建错误就修构建,不碰裁判。本包追加: 无。
修改令: 允许改 `mongostore/mongostore.go` 的**驱动调用签名**(L1 令牌件,见决策表),**仅限**适配 v2 API 漂移;禁动 `Store` 接口、`BSONCodec` 行为、`withID` 逻辑。

## 3 任务 · 具体做什么

### 单元 1 · 全量构建

```bash
go build ./... 2>&1 | tee delivery/rounds/R1/go_build.txt
echo "EXIT: $?"
```

退出 0 → 单元 3。非零(几乎只可能出在 mongostore)→ 单元 2。

### 单元 2 · 机械修正 mongostore(仅在单元 1 失败时)

只允许按决策表(§6)做 v2 签名适配。每改一处,重跑 `go build ./mongostore` 直到退出 0。**不准**为了过编译删 `Store` 接口方法、改 BSONCodec、或动 `var _ dopdb.Store = (*Store)(nil)` 断言(该断言失败说明缺方法,要补齐而非删断言)。

### 单元 3 · vet + 格式

```bash
go vet ./... 2>&1; echo "VET_EXIT: $?"
gofmt -l .                  # 期望空输出
```

`gofmt` 有输出 → `gofmt -w <file>`(纯格式)。

## 4 验收 · 怎么算完成(harness 复跑,云端三层审计再复核)

- [ ] `go build ./...` 退出 0(`go_build.txt` 留痕)
- [ ] `go vet ./...` 退出 0
- [ ] `gofmt -l .` 空输出
- [ ] 若改了 mongostore:`git diff` 仅限 mongostore.go 的驱动调用签名,**未触** Store 接口 / BSONCodec
- [ ] 进度账落 `delivery/rounds/R1/progress.md`

## 5 边界 · 不要做什么

可写:`mongostore/mongostore.go`(仅签名适配)、`delivery/rounds/R1/`、必要时 `gofmt -w` 任意文件(纯格式)。
禁改:`store.go` 的 `Store` 接口、所有 `*_test.go`、L0 冻结件、其它包的逻辑。越界登记 oob。

## 6 预算与换法 · 决策表

| 情况 | 动作 |
|---|---|
| 一次构建通过 | 直接到单元 3 |
| `options.X()` builder 方法名不存在 | 查 v2 文档对应新名,机械替换(如 `SetUpsert`/`SetSort`/`SetProjection`/`SetLimit`/`SetSkip`/`SetName`/`SetUnique` 这类) |
| `mongo.Connect` 形参变化 | 适配为当前 v2 签名 |
| `cursor.Current` / `Lookup().StringValueOK()` 不匹配 | 适配为当前 bson.Raw 取值 API |
| 报「`*Store` 未实现 dopdb.Store(缺方法 X)」 | 说明接口与适配器不同步——**补齐方法 X**(按 store.go 签名),不删断言;改不动则记异常 suspend |
| 驱动传递依赖缺失 | `go mod tidy`;网络不通 → 该单元 blocked,转 V2/D1 |
| vet 报 shadow/unused | 修复;vet 警告不阻塞 done |

每处签名修正算一次尝试,整包最多 ~20 分钟。连续两次同样的构建错误仍不过 → 记 failed + 错误原文,suspend 交云端(可能是云端把适配器写错了)。

## 7 收尾

按协议 §3 写回执;**关键数字**抄:构建是否一次过 / 改了几处签名。「异常发现」必写:① mongostore 需要的修正超出「签名适配」范畴(改到了逻辑);② 接口缺方法;③ 驱动版本与适配器假设差异较大;④ 任何让你想动 Store 接口或测试的诱因。

## 8 调和补注(R1 reconcile · 2026-06-23)

仓库新增了 **`wasm/`** 包(`main.go` 带 `//go:build js && wasm`;`stub.go` 带 `//go:build !(js && wasm)`)。因此:

- `go build ./...` / `go vet ./...` 现在也会覆盖 `./wasm`。在本地(darwin,非 js/wasm)只编 `wasm/stub.go`(空 `main`),**正常通过**,不需任何处理;`main.go` 因构建标签被自动跳过。
- **wasm 模块本体**(`GOOS=js GOARCH=wasm go build ./wasm`)**不在本包范围**,属 R2 的 W1 轨。本包只管 `go build ./...` 退出 0(含 mongostore 签名适配)。
- 若 `go build ./...` 因 `./wasm` 报错(理论上不应),记异常一行;不要去改 `wasm/` 下文件(非本包可写范围)。
