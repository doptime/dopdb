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
