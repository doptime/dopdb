# 包: P-V3-mongostore-contract · 回合 R1(2026-06-23 · 真实 MongoDB 首次验证)

分级: 🔴 需判断/承重。**硬判据(本地据此自裁)**:设了 `DOPTIME_TEST_MONGO_URI` 时,`go test -run TestMongoContract ./mongostore` **退出 0 且输出无 `SKIP`**,即 done;无 URI/连不上 → 集成测试 skip 报因 → 终态 B,记 **suspend**;某断言不过 → failed,记关键数字 + 异常发现,**不改测试**,suspend 交云端。
上游: P-V1(mongostore 已编译通过)
回执写到: `delivery/rounds/R1/receipt-P-V3-mongostore-contract.md`(模板见 delivery/kit/00-protocol.md §3)

全景: STATUS.md 甘特 V3,本回合**唯一承重里程碑**。它没验过之前不切 V4。

任务一句话: 新建 `mongostore/integration_test.go`,对真实 MongoDB 跑通与 memstore 同构的契约(round-trip+写入期 modifiers、ErrNoDoc、SetNX、原子 Incr、Find+净化、unique 索引冲突、_id 恒字符串)。

## 1 背景 · 现在是什么情况

dopdb 至今只在内存 store(JSON codec)上验过。`mongostore`(BSON codec)在真 Mongo 上的行为**从未跑过**。本包用一个集成测试坐实:它只调 dopdb + mongostore 的**公开 API**(`mongostore.New`、`BSONCodec`、`dopdb.New[K,V]` 及其方法),不直接碰驱动,所以与驱动 v2 点版本无关。

测试需要一个隔离/一次性的 MongoDB(会建集合、建唯一索引、写删文档),连接串由人放进 `DOPTIME_TEST_MONGO_URI`。

## 2 意图 · 为什么做、什么算好

把「框架在真 Mongo 上行为正确」从假设变成证据。完成的精确定义见硬判据 + §4。

红线: RL1–RL8 全部适用。**RL2/RL5/RL6 重点**:断言不过就如实 failed,绝不改测试或门槛;退出码 0 还要看断言语义;skip 是诚实负结果,不许粉饰成 pass。本包追加 PRL1–PRL4(见项目卡)。
修改令: 允许**新建** `mongostore/integration_test.go`(项目卡预授权);不改任何现有文件。

## 3 任务 · 具体做什么

### 单元 1 · 创建集成测试文件

新建 `mongostore/integration_test.go`,内容如下(逐字):

```go
package mongostore_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/mongostore"
)

// Run against a disposable MongoDB by setting DOPTIME_TEST_MONGO_URI.
// Without it the test skips (honest terminal B), never a false pass.

type Member struct {
	UID       string    `bson:"_id" json:"_id"`
	Name      string    `bson:"name" json:"name" mod:"trim"`
	Role      string    `bson:"role" json:"role" mod:"default=member"`
	Hits      int64     `bson:"hits" json:"hits"`
	Age       int       `bson:"age" json:"age"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// Acct carries a UNIQUE secondary index on email (not on _id).
type Acct struct {
	ID    string `bson:"_id" json:"_id"`
	Email string `bson:"email" json:"email" index:"unique"`
}

func TestMongoContract(t *testing.T) {
	uri := os.Getenv("DOPTIME_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DOPTIME_TEST_MONGO_URI not set — skipping real-Mongo contract (terminal B)")
	}
	ctx := context.Background()
	st, err := mongostore.New(ctx, uri, "dopdb_integration_test")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	dopdb.SetDefaultStore(st)
	dopdb.SetDefaultCodec(mongostore.BSONCodec{})
	dopdb.SetValidator(nil)

	suffix := time.Now().UTC().Format("20060102T150405")
	memberColl := "it_members_" + suffix
	acctColl := "it_accts_" + suffix

	members := dopdb.New[string, *Member](dopdb.WithCollection(memberColl))
	t.Cleanup(func() {
		ks, _ := members.HKeys()
		_ = members.HDel(ks...)
	})

	// 1) round-trip + write-time modifiers (trim/default/timestamps)
	if err := members.HSet("u1", &Member{UID: "u1", Name: "  Alice  "}); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	got, err := members.HGet("u1")
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("trim on write failed: %q", got.Name)
	}
	if got.Role != "member" {
		t.Errorf("default on write failed: %q", got.Role)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps not set on write")
	}
	if got.UID != "u1" {
		t.Errorf("_id round-trip failed: %q", got.UID)
	}

	// 2) ErrNoDoc
	if _, err := members.HGet("ghost"); !errors.Is(err, dopdb.ErrNoDoc) {
		t.Errorf("expected ErrNoDoc, got %v", err)
	}

	// 3) HSetNX
	ins, err := members.HSetNX("u1", &Member{UID: "u1", Name: "Other"})
	if err != nil || ins {
		t.Errorf("SetNX on existing should not insert: ins=%v err=%v", ins, err)
	}

	// 4) atomic HIncrBy: 100 increments must total exactly 100
	for i := 0; i < 100; i++ {
		if err := members.HIncrBy("counter", "hits", 1); err != nil {
			t.Fatalf("HIncrBy: %v", err)
		}
	}
	c, err := members.HGet("counter")
	if err != nil {
		t.Fatalf("HGet counter: %v", err)
	}
	if c.Hits != 100 {
		t.Errorf("ATOMIC INCR FAILED: hits=%d want 100", c.Hits)
	}

	// 5) Find + sanitize
	_ = members.HSet("a", &Member{UID: "a", Name: "Ann", Age: 30})
	_ = members.HSet("b", &Member{UID: "b", Name: "Bo", Age: 17})
	adults, err := members.Find(dopdb.M{"age": dopdb.M{"$gte": 18}}, dopdb.FindOpt{})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	nAdult := 0
	for _, m := range adults {
		if m.Age >= 18 {
			nAdult++
		}
	}
	if nAdult < 2 { // u1(0 age won't count), a(30), counter(0)… at least a + Alice? Alice age 0
		// a(30) qualifies; ensure at least the explicit adult is found
		if nAdult < 1 {
			t.Errorf("Find $gte returned %d adults, want >=1", nAdult)
		}
	}
	if _, err := members.Find(dopdb.M{"$where": "while(true){}"}, dopdb.FindOpt{}); err == nil {
		t.Error("SANITIZER FAILED: $where was not rejected")
	}

	// 6) unique secondary index conflict
	accts := dopdb.New[string, *Acct](dopdb.WithCollection(acctColl))
	t.Cleanup(func() {
		ks, _ := accts.HKeys()
		_ = accts.HDel(ks...)
	})
	if err := accts.HSet("id1", &Acct{ID: "id1", Email: "dup@x.com"}); err != nil {
		t.Fatalf("first acct insert: %v", err)
	}
	// different _id, same unique email -> must error
	if err := accts.HSet("id2", &Acct{ID: "id2", Email: "dup@x.com"}); err == nil {
		t.Error("UNIQUE INDEX FAILED: duplicate email was accepted")
	}

	t.Logf("INTEGRATION OK against %s/dopdb_integration_test", uri)
}
```

### 单元 2 · 运行

```bash
go test -count=1 -run TestMongoContract -v ./mongostore 2>&1 | tee delivery/rounds/R1/mongostore_contract.txt
echo "EXIT: $?"
grep -E "SKIP|PASS|FAIL|INTEGRATION OK" delivery/rounds/R1/mongostore_contract.txt
```

照硬判据自裁:无 URI → 输出含 `SKIP` → 终态 B、suspend;有 URI 且退出 0 且无 `SKIP` → done(把 `INTEGRATION OK` 行与各断言现况抄进回执);有断言 FAIL → failed + 关键数字 + 异常发现,suspend。

## 4 验收 · 怎么算完成(harness 复跑,云端三层审计再复核)

- [ ] `mongostore/integration_test.go` 已按上文逐字创建
- [ ] `go test -run TestMongoContract ./mongostore` 退出 0
- [ ] 输出含 `INTEGRATION OK`(证明真跑了,非 skip)——**或** 明确含 `SKIP` 且回执标 suspend(终态 B)
- [ ] 关键数字抄:atomic hits 值(应 100)、unique 冲突是否触发、$where 是否被拒
- [ ] `mongostore_contract.txt` 留痕;进度账落 `delivery/rounds/R1/progress.md`

## 5 边界 · 不要做什么

可写:**新建** `mongostore/integration_test.go`、`delivery/rounds/R1/`。
禁改:`mongostore/mongostore.go` 逻辑、`store.go` 接口、任何现有测试、L0 冻结件。**绝不为让测试过而改 mongostore 的语义或删断言**(RL2/PRL2/PRL4)。越界登记 oob。

## 6 预算与换法 · 决策表

| 情况 | 动作 |
|---|---|
| URI 已设、连得上、全断言过 | done;抄 hits=100 等关键数字 |
| 无 URI | 测试 skip;终态 B;回执 suspend,写「无测试 Mongo,承重件证不了」 |
| 连接超时/认证失败 | 重试 1 次;仍失败 → 当作无 Mongo,终态 B、suspend,记错误一行 |
| atomic hits ≠ 100 | **不改测试**;failed,关键数字写实际值,异常发现写「$inc 非原子或驱动 Incr 适配有误」,suspend |
| unique 冲突未触发(第二次插入成功) | failed,异常发现写「EnsureIndex unique 未生效或 ReplaceOne 绕过了唯一约束」,suspend |
| $where 未被拒(Find 成功) | **严重**:净化器在真路径失效;failed + 异常发现,suspend |
| 编译期报 mongostore API 不匹配 | 说明 V1 未真正修好——回到 V1 决策表;本包 blocked 记明 |

整包 ~10 分钟(连库 + 跑)。不在本包内尝试「修 mongostore 让断言过」——那是 L0/语义,交云端。

## 7 收尾

按协议 §3 写回执;**关键数字必抄**:atomic hits 实际值、unique 冲突 yes/no、$where 被拒 yes/no、测试是 ran 还是 skip。「异常发现」必写:① 原子计数不等于 100;② 唯一索引未拦重复;③ 净化器在真 Mongo 上未拦 `$where`;④ BSON 落盘字段名与预期不符(如 `_id`/`createdAt` 对不上);⑤ 任何「退出 0 但语义不像完成」的迹象;⑥ 你被诱导去改 mongostore/测试的任何点。
