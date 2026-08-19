package dopdb

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// The bounded retention must return EXACTLY what a full sort-then-slice would.
// It is an optimisation, so any difference is a correctness bug, not a trade-off.
func TestTopNMatchesFullSort(t *testing.T) {
	orders := [][]SortKey{
		nil,
		{{Field: "age", Asc: true}},
		{{Field: "age", Asc: false}},
		{{Field: "age", Asc: true}, {Field: "name", Asc: false}},
		{{Field: "_id", Asc: false}},
	}
	for _, keys := range orders {
		for _, n := range []int{1, 3, 10, 50, 500} {
			rows := make([]row, 0, 300)
			for i := 0; i < 300; i++ {
				rows = append(rows, row{
					id:  fmt.Sprintf("k%03d", i),
					doc: map[string]any{"age": int64(rand.Intn(20)), "name": fmt.Sprintf("n%03d", rand.Intn(50))},
				})
			}
			order := rowOrder{keys: keys}

			// reference: sort everything, take the first n
			want := make([]row, len(rows))
			copy(want, rows)
			sort.SliceStable(want, func(i, j int) bool { return order.less(want[i], want[j]) })
			if len(want) > n {
				want = want[:n]
			}

			h := newTopN(n, order)
			for _, r := range rows {
				h.push(r)
			}
			got := h.sorted()

			if len(got) != len(want) {
				t.Fatalf("keys=%v n=%d: got %d rows want %d", keys, n, len(got), len(want))
			}
			for i := range want {
				if got[i].id != want[i].id {
					t.Fatalf("keys=%v n=%d: row %d = %s want %s", keys, n, i, got[i].id, want[i].id)
				}
			}
		}
	}
}
