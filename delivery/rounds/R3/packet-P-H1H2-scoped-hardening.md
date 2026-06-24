# 包 · P-H1H2-scoped-hardening(R3 · 🔴 承重)

> 段:执行交换。owner-scoped 集合安全语义收尾(H1 原子写 + H2 scoped 键/计数),在**真实 Mongo** 上验。框架实现由云端写定并在 memstore 上自测过(build+vet 干净、全旧测试绿、`-race` 并发干净);本地**套用本交付内的 6 个框架文件** + **逐字新建测试** + 跑。

## 1 目标
- **H1**:把 `HttpSetScoped` 的 check-then-act 换成原子 `Store.PutScoped`(过滤式 upsert)。跨主 id → dup-key/owner-mismatch → `ErrForbidden`,关掉 TOCTOU 窗口。
- **H2**:scoped 集合的 `HKEYS`/`HLEN` 由「一律 403」改为「只回调用者本人的键/计数」,不泄漏。

## 2 框架改动(已在本交付内,套用即可,勿手改)
本交付的 zip 覆盖仓库根后,以下 6 个文件即为定稿实现:
- `store.go`:`Store` 接口加 `PutScoped(ctx, coll, id string, doc []byte, ownerField, ownerVal string) error`。
- `memstore/memstore.go`:`PutScoped` 实现(mutex 内:查现存 `_id` 的 owner,不匹配→`ErrForbidden`;否则写并**强制** owner 字段)。
- `mongostore/mongostore.go`:`PutScoped` 实现(`UpdateOne({_id, ownerField:ownerVal}, {$set: doc(强制 owner, 去 _id)}, upsert)`;`mongo.IsDuplicateKeyError`→`dopdb.ErrForbidden`)。**注**:同时把既有 HSetNX/Incr 的 `options.Update()` 一并对齐为 `options.UpdateOne()`(你 R1 已做的修正,已并入本文件,套用不会回退)。
- `dopdb.go`:`Collection.HSetScoped(key, value, ownerField, ownerVal)`(serializeKey+encode→`store.PutScoped`)。
- `http_accessor.go`:`HttpSetScoped` 重写为走 `HSetScoped`(删 check-then-act);`HttpAccessor` 接口 + `Collection` 加 `HttpKeysScoped`/`HttpLenScoped`(经 `keysByScope` = `Find(scope)` 取 `_id`)。加了 `fmt` import。
- `httpserve/serve.go`:`HKEYS`/`HLEN` 的 scoped 分支由 403 改为调 `HttpKeysScoped`/`HttpLenScoped`。

> 设计取舍(与你 v0 的差异,均已自测验证):① `mongostore.PutScoped` 用**单个 `$set`(内部强制 owner)**,不用 `$set`+`$setOnInsert`——后者会和过滤器在 owner 上冲突;过滤器在 insert 时自带 `_id`。② **并发用例改在 `Collection.HSetScoped` 层**跑(下方测试用例 2/3),而非 HTTP 层——避免 AutoAuth 授权 map / jwt LRU 在并发下的无关噪声污染 H1 判据;HTTP 层的跨主单请求守卫仍由用例 1 验。③ `PutScoped` **强制** owner(非仅携带),直接调用也安全。

## 3 逐字新建测试
新建 `httpserve/scoped_integration_test.go`,内容与下方**逐字一致**(零改动):

```go
package httpserve

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/mongostore"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Run against a disposable MongoDB via DOPTIME_TEST_MONGO_URI; otherwise skips
// (honest terminal B). Each run uses a unique database dropped on cleanup.
//
// Validates the R3 scoped-collection hardening on a real backend:
//   H1 - atomic scoped upsert (PutScoped): cross-owner writes refused even under
//        concurrency; same-owner concurrent writes all succeed; no hijack.
//   H2 - scoped HKEYS/HLEN return only the caller's keys/count, no leakage.

func scopedSortStrs(a []any) []string {
	out := make([]string, 0, len(a))
	for _, x := range a {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func scopedSameStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScopedHardening(t *testing.T) {
	uri := os.Getenv("DOPTIME_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DOPTIME_TEST_MONGO_URI not set — skipping scoped hardening on real Mongo (terminal B)")
	}
	ctx := context.Background()
	dbName := "dopdb_scoped_it_" + time.Now().UTC().Format("20060102T150405")

	st, err := mongostore.New(ctx, uri, dbName)
	if err != nil {
		t.Fatalf("mongostore.New: %v", err)
	}
	dopdb.SetDefaultStore(st)
	dopdb.SetDefaultCodec(mongostore.BSONCodec{})
	dopdb.SetValidator(nil)
	dopdb.RegisterHttp(dopdb.New[string, *Profile](dopdb.WithCollection("Profile")))
	dopdb.RegisterHttp(dopdb.New[string, *Order](dopdb.WithCollection("Order")))
	dopdb.SetOwnerScope("Order", "owner", "uid")

	cli, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("driver connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cli.Database(dbName).Drop(ctx)
		_ = cli.Disconnect(ctx)
	})

	orders := dopdb.New[string, *Order](dopdb.WithCollection("Order"))

	// 1) HTTP-level atomic guard: u2 cannot overwrite u1's row (single request).
	t.Run("1-atomic-cross-owner-http", func(t *testing.T) {
		h := NewHandler(NewServer(testSecret), NewPermissions(true))
		u1, u2 := tokenFor(t, "u1"), tokenFor(t, "u2")
		if rr := do(h, "POST", "/HSET-Order?f=o1", `{"item":"book"}`, u1); rr.Code != http.StatusOK {
			t.Fatalf("u1 create o1 status=%d", rr.Code)
		}
		if rr := do(h, "POST", "/HSET-Order?f=o1", `{"item":"hijack"}`, u2); rr.Code != http.StatusForbidden {
			t.Errorf("u2 overwrite o1 status=%d want 403", rr.Code)
		}
		obj := decodeObj(t, do(h, "GET", "/HGET-Order?f=o1", "", u1))
		if obj["item"] != "book" || obj["owner"] != "u1" {
			t.Errorf("o1=%v want item=book owner=u1 (hijack must not apply)", obj)
		}
	})

	// 2) Atomic primitive under concurrency: N same-owner writers to one key all
	// succeed; final doc is owned by the caller and equals one of the writes.
	t.Run("2-same-owner-concurrent", func(t *testing.T) {
		const N = 40
		items := make(map[string]bool, N)
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for i := 0; i < N; i++ {
			it := "v" + strconv.Itoa(i)
			items[it] = true
			wg.Add(1)
			go func(it string) {
				defer wg.Done()
				if err := orders.HSetScoped("shared", &Order{OrderID: "shared", Item: it}, "owner", "u1"); err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				}
			}(it)
		}
		wg.Wait()
		if ok != N {
			t.Errorf("same-owner concurrent: %d/%d ok, want all", ok, N)
		}
		got, err := orders.HGet("shared")
		if err != nil {
			t.Fatalf("HGet shared: %v", err)
		}
		if got.Owner != "u1" {
			t.Errorf("final owner=%q want u1", got.Owner)
		}
		if !items[got.Item] {
			t.Errorf("final item=%q not one of the concurrent writes", got.Item)
		}
	})

	// 3) Atomic isolation under contention: key pre-owned by u1; concurrent u1
	// and u2 writers -> every u1 succeeds, every u2 is refused; never hijacked.
	t.Run("3-cross-owner-contention", func(t *testing.T) {
		if err := orders.HSetScoped("raced", &Order{OrderID: "raced", Item: "base"}, "owner", "u1"); err != nil {
			t.Fatalf("seed raced: %v", err)
		}
		const each = 16
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok, forbidden, other := 0, 0, 0
		shoot := func(owner string) {
			defer wg.Done()
			err := orders.HSetScoped("raced", &Order{OrderID: "raced", Item: "x"}, "owner", owner)
			mu.Lock()
			switch err {
			case nil:
				ok++
			case dopdb.ErrForbidden:
				forbidden++
			default:
				other++
			}
			mu.Unlock()
		}
		for i := 0; i < each; i++ {
			wg.Add(2)
			go shoot("u1")
			go shoot("u2")
		}
		wg.Wait()
		if ok != each || forbidden != each || other != 0 {
			t.Errorf("contention: ok=%d forbidden=%d other=%d want ok=%d forbidden=%d other=0", ok, forbidden, other, each, each)
		}
		got, _ := orders.HGet("raced")
		if got.Owner != "u1" {
			t.Errorf("raced final owner=%q want u1 (never hijacked)", got.Owner)
		}
	})

	// 4) Scoped HKEYS/HLEN over HTTP: only the caller's keys/count, no leak.
	t.Run("4-scoped-keys-no-leak", func(t *testing.T) {
		h := NewHandler(NewServer(testSecret), NewPermissions(true))
		// fresh owners isolate this subtest from rows written above.
		a, b := tokenFor(t, "u3"), tokenFor(t, "u4")
		do(h, "POST", "/HSET-Order?f=k1", `{"item":"a"}`, a)
		do(h, "POST", "/HSET-Order?f=k2", `{"item":"b"}`, a)
		do(h, "POST", "/HSET-Order?f=k3", `{"item":"c"}`, b)

		ka := scopedSortStrs(decodeArr(t, do(h, "GET", "/HKEYS-Order", "", a)))
		kb := scopedSortStrs(decodeArr(t, do(h, "GET", "/HKEYS-Order", "", b)))
		if !scopedSameStrs(ka, []string{"k1", "k2"}) {
			t.Errorf("u3 keys=%v want [k1 k2]", ka)
		}
		if !scopedSameStrs(kb, []string{"k3"}) {
			t.Errorf("u4 keys=%v want [k3]", kb)
		}
		for _, x := range ka {
			for _, y := range kb {
				if x == y {
					t.Errorf("leak: %q in both key sets", x)
				}
			}
		}
		if n := decodeObj(t, do(h, "GET", "/HLEN-Order", "", a))["len"]; n != float64(2) {
			t.Errorf("u3 HLEN=%v want 2", n)
		}
		if n := decodeObj(t, do(h, "GET", "/HLEN-Order", "", b))["len"]; n != float64(1) {
			t.Errorf("u4 HLEN=%v want 1", n)
		}
	})

	t.Logf("SCOPED HARDENING OK against %s/%s", uri, dbName)
}
```

## 4 执行
```
export DOPTIME_TEST_MONGO_URI=mongodb://localhost:27017   # 复用 R1/R2 的本地 Docker
cd <repo> && export PATH=$PATH:<go1.24>
go build ./...                                            # 含改后的 mongostore/PutScoped,需驱动在 go.mod(你已有)
go test -count=1 ./...                                    # 全量回归
go test -count=1 -run TestScopedHardening -v ./httpserve  # 本承重测试详跑
go test -count=1 -race -run TestScopedHardening ./httpserve  # 并发用例加 -race
```
留痕到 `delivery/rounds/R3/scoped_mongo.txt`。

## 5 硬验收(全绿才 done)
- `go build ./...` 退出 0。
- `go test ./...` 全绿——**含 R1/R2 既有全部测试**(`TestHTTPMongo` 六子测试、httpserve baseline 11、根/api/config/memstore)。**旧测试一个都不许挂**。
- `TestScopedHardening` 四子测试全 PASS,输出含 `SCOPED HARDENING OK`,无 SKIP:
  - `1-atomic-cross-owner-http`:u2 覆盖 u1 的 o1 → 403;o1 仍 item=book/owner=u1。
  - `2-same-owner-concurrent`:40 并发同主写 → 全 ok;终值 owner=u1 且 item∈写入集。
  - `3-cross-owner-contention`:raced 预属 u1;16 u1 + 16 u2 并发 → u1 全 nil、u2 全 `ErrForbidden`、other=0;终 owner=u1。
  - `4-scoped-keys-no-leak`:u3 键集={k1,k2}、u4={k3}、交集空;HLEN u3=2 / u4=1。
- `-race` 跑 `TestScopedHardening` 无 WARNING。

## 6 红线 / 岔路
- **RL2**:不许改测试/门槛/语义来凑过。**旧测试挂 = 语义回归 → 停、suspend、记哪个测试 + 现象**(别动代码/测试)。
- dup-key 判定若 v2.7.0 API 漂移:**仅**机械适配 `mongostore.go` 那一处判定调用(参照同文件既有用法),不动其它。记 oob。
- scoped HKEYS 漏别人的键(用例 4 失败)= 严重 PRL → failed + 异常发现 + suspend。
- 并发用例 2/3 若偶发不稳:记现象 + 复现率,别把 flaky 当 pass。
- **无 Mongo**:`TestScopedHardening` 自动 skip → 终态 B suspend(本包),**不阻塞** T1/H4/D2。
- 可写区:新建 `httpserve/scoped_integration_test.go`、本交付覆盖的 6 个框架文件、`delivery/rounds/R3/`。其余任何改动登记 `oob.md`。

## 7 回执
`receipt-P-H1H2-scoped-hardening.md`:状态(done|suspend|failed|blocked)、四子测试关键数字(各成功/拒绝计数、终 owner、键集、交集大小)、build/全量回归结果、是否 -race 干净、测试与本包是否逐字一致、异常发现。
