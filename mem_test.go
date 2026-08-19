package dopdb

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Peak RETAINED memory is what bounded retention fixes; benchmem reports total
// allocation, which is dominated by the transient per-document decode and so
// hides the difference. This measures the heap while the result is still alive.
func TestMemoryBoundOnSortedQuery(t *testing.T) {
	uri := os.Getenv("DOPDB_TEST_KVROCKS_URI")
	if uri == "" {
		t.Skip("no server")
	}
	opt, _ := redis.ParseURL(uri)
	applyPoolDefaults(opt)
	cl := redis.NewClient(opt)
	defer cl.Close()
	ns := "memtest"
	ds := NewDatasources()
	ds.Add("default", cl, ns)
	SetDatasources(ds)
	defer SetDatasources(nil)

	c := New[string, *benchDoc](WithCollection("mc"))
	ctx := context.Background()
	const n = 20000
	if cnt, _ := cl.HLen(ctx, ns+":mc").Result(); cnt < n {
		m := map[string]*benchDoc{}
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("k%06d", i)] = &benchDoc{Name: fmt.Sprintf("u%06d", i), Age: i % 90, Email: fmt.Sprintf("u%d@x.io", i), Tag: "t"}
			if len(m) == 1000 {
				_ = c.HMSet(m)
				m = map[string]*benchDoc{}
			}
		}
		if len(m) > 0 {
			_ = c.HMSet(m)
		}
	}

	heapWhileHolding := func(limit int64) uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		out, err := c.Find(M{"tag": "t"}, FindOpt{SortKeys: []SortKey{{Field: "age", Asc: false}}, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC() // collect the transient decodes; only the result survives
		runtime.ReadMemStats(&after)
		got := after.HeapAlloc
		runtime.KeepAlive(out)
		if before.HeapAlloc > got {
			return 0
		}
		return got - before.HeapAlloc
	}

	bounded := heapWhileHolding(10)
	// The contrast: with no limit there is nothing to bound, so every matching
	// document is retained. That is exactly what a limited query used to do.
	unbounded := heapWhileHolding(0)

	t.Logf("sorted over %d matches: limit 10 retains %d KiB, no limit retains %d KiB",
		n, bounded/1024, unbounded/1024)
	if bounded > 2<<20 {
		t.Errorf("a limit-10 query retained %d KiB; bounded retention is not working", bounded/1024)
	}
	if unbounded < bounded*10 {
		t.Errorf("the bounded and unbounded queries retained similar memory (%d vs %d KiB) — the bound is not taking effect",
			bounded/1024, unbounded/1024)
	}
}
