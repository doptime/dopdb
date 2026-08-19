package dopdb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

type benchDoc struct {
	Name  string `json:"name"`
	Age   int    `json:"age"`
	Email string `json:"email"`
	Tag   string `json:"tag"`
}

func benchSetup(b *testing.B, n int) (*Collection[string, *benchDoc], func()) {
	b.Helper()
	uri := os.Getenv("DOPDB_TEST_KVROCKS_URI")
	if uri == "" {
		b.Skip("no server")
	}
	opt, _ := redis.ParseURL(uri)
	applyPoolDefaults(opt)
	cl := redis.NewClient(opt)
	ns := fmt.Sprintf("bench_%d", n)
	ds := NewDatasources()
	ds.Add("default", cl, ns)
	SetDatasources(ds)
	c := New[string, *benchDoc](WithCollection("bc"))
	ctx := context.Background()
	if cnt, _ := cl.HLen(ctx, ns+":bc").Result(); cnt < int64(n) {
		ids := make([]string, 0, 1000)
		docs := make([]*benchDoc, 0, 1000)
		m := map[string]*benchDoc{}
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("k%06d", i)] = &benchDoc{
				Name: fmt.Sprintf("user%06d", i), Age: i % 90, Email: fmt.Sprintf("u%d@x.io", i), Tag: "t",
			}
			if len(m) == 1000 {
				_ = c.HMSet(m)
				m = map[string]*benchDoc{}
			}
		}
		if len(m) > 0 {
			_ = c.HMSet(m)
		}
		_, _ = ids, docs
	}
	return c, func() { _ = cl.Close(); SetDatasources(nil) }
}

func BenchmarkFindSelective(b *testing.B) {
	c, done := benchSetup(b, 20000)
	defer done()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Find(M{"name": "user010000"}, FindOpt{Limit: 10}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFindRegex(b *testing.B) {
	c, done := benchSetup(b, 20000)
	defer done()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Find(M{"name": M{"$regex": "^user0100"}}, FindOpt{Limit: 10}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCountAll(b *testing.B) {
	c, done := benchSetup(b, 20000)
	defer done()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.backend("").countFilter(ctx, c.coll, M{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCountFiltered(b *testing.B) {
	c, done := benchSetup(b, 20000)
	defer done()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.backend("").countFilter(ctx, c.coll, M{"age": 42.0}); err != nil {
			b.Fatal(err)
		}
	}
}

// The pathological shape: a filter that matches everything, with a small limit.
// Before bounded retention this materialised every matching document just to
// return ten of them.
func BenchmarkFindWideMatchSmallLimit(b *testing.B) {
	c, done := benchSetup(b, 20000)
	defer done()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := c.Find(M{"tag": "t"}, FindOpt{Limit: 10})
		if err != nil {
			b.Fatal(err)
		}
		if len(out) != 10 {
			b.Fatalf("got %d rows", len(out))
		}
	}
}

// Paging with no filter: nothing needs decoding to answer it.
func BenchmarkFindEmptyFilterPage(b *testing.B) {
	c, done := benchSetup(b, 20000)
	defer done()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Find(M{}, FindOpt{Limit: 50}); err != nil {
			b.Fatal(err)
		}
	}
}

// Sorting still needs every matching document, but only the best N are retained.
func BenchmarkFindSortedTopN(b *testing.B) {
	c, done := benchSetup(b, 20000)
	defer done()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := c.Find(M{"tag": "t"}, FindOpt{SortKeys: []SortKey{{Field: "age", Asc: false}}, Limit: 10})
		if err != nil {
			b.Fatal(err)
		}
		if len(out) != 10 {
			b.Fatalf("got %d rows", len(out))
		}
	}
}
