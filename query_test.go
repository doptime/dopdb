package dopdb

import (
	"testing"
	"time"
)

// The query engine replaced MongoDB's query planner, so it needs tests of its
// own — these are the only tests in the repo that pin the FILTER DIALECT itself,
// and they run without a server.

func doc(pairs ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i].(string)] = pairs[i+1]
	}
	return m
}

func TestMatchEquality(t *testing.T) {
	d := doc("name", "Ada", "age", int64(30), "tags", []any{"a", "b"})

	cases := []struct {
		name   string
		filter M
		want   bool
	}{
		{"implicit eq", M{"name": "Ada"}, true},
		{"implicit eq miss", M{"name": "Bob"}, false},
		{"$eq", M{"name": M{"$eq": "Ada"}}, true},
		{"$ne", M{"name": M{"$ne": "Bob"}}, true},
		{"int64 vs float64 filter", M{"age": 30.0}, true},
		{"array contains element", M{"tags": "b"}, true},
		{"array whole match", M{"tags": []any{"a", "b"}}, true},
		{"array miss", M{"tags": "z"}, false},
		{"absent field eq null", M{"nope": nil}, true},
		{"absent field eq value", M{"nope": "x"}, false},
		{"two clauses AND implicitly", M{"name": "Ada", "age": 30.0}, true},
		{"one clause fails", M{"name": "Ada", "age": 31.0}, false},
	}
	for _, c := range cases {
		if got := matchFilter(d, c.filter); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestMatchComparison(t *testing.T) {
	d := doc("age", int64(30), "score", 4.5)
	cases := []struct {
		filter M
		want   bool
	}{
		{M{"age": M{"$gt": 29.0}}, true},
		{M{"age": M{"$gt": 30.0}}, false},
		{M{"age": M{"$gte": 30.0}}, true},
		{M{"age": M{"$lt": 31.0}}, true},
		{M{"age": M{"$lte": 29.0}}, false},
		{M{"age": M{"$gt": 20.0, "$lt": 40.0}}, true},
		{M{"age": M{"$gt": 20.0, "$lt": 25.0}}, false},
		{M{"score": M{"$gte": 4.5}}, true},
		{M{"missing": M{"$gt": 1.0}}, false},
	}
	for i, c := range cases {
		if got := matchFilter(d, c.filter); got != c.want {
			t.Errorf("case %d %v: got %v want %v", i, c.filter, got, c.want)
		}
	}
}

func TestMatchLogical(t *testing.T) {
	d := doc("a", int64(1), "b", "x")
	cases := []struct {
		name   string
		filter M
		want   bool
	}{
		{"$and both", M{"$and": []any{map[string]any{"a": 1.0}, map[string]any{"b": "x"}}}, true},
		{"$and one fails", M{"$and": []any{map[string]any{"a": 1.0}, map[string]any{"b": "y"}}}, false},
		{"$or one hits", M{"$or": []any{map[string]any{"a": 9.0}, map[string]any{"b": "x"}}}, true},
		{"$or none", M{"$or": []any{map[string]any{"a": 9.0}, map[string]any{"b": "y"}}}, false},
		{"$nor none hit", M{"$nor": []any{map[string]any{"a": 9.0}}}, true},
		{"$nor one hit", M{"$nor": []any{map[string]any{"a": 1.0}}}, false},
		{"$not", M{"a": M{"$not": map[string]any{"$gt": 5.0}}}, true},
		{"$not negates", M{"a": M{"$not": map[string]any{"$lt": 5.0}}}, false},
	}
	for _, c := range cases {
		if got := matchFilter(d, c.filter); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestMatchElementAndArrayOps(t *testing.T) {
	d := doc(
		"tags", []any{"red", "blue"},
		"items", []any{
			map[string]any{"n": "a", "q": int64(2)},
			map[string]any{"n": "b", "q": int64(5)},
		},
		"n", int64(10),
		"name", "Hello World",
	)
	cases := []struct {
		name   string
		filter M
		want   bool
	}{
		{"$exists true", M{"tags": M{"$exists": true}}, true},
		{"$exists false", M{"nope": M{"$exists": false}}, true},
		{"$exists false on present", M{"tags": M{"$exists": false}}, false},
		{"$size", M{"tags": M{"$size": 2.0}}, true},
		{"$size wrong", M{"tags": M{"$size": 3.0}}, false},
		{"$all", M{"tags": M{"$all": []any{"blue", "red"}}}, true},
		{"$all missing one", M{"tags": M{"$all": []any{"blue", "green"}}}, false},
		{"$in", M{"n": M{"$in": []any{1.0, 10.0}}}, true},
		{"$nin", M{"n": M{"$nin": []any{1.0, 2.0}}}, true},
		{"$elemMatch", M{"items": M{"$elemMatch": map[string]any{"q": map[string]any{"$gt": 4.0}}}}, true},
		{"$elemMatch miss", M{"items": M{"$elemMatch": map[string]any{"q": map[string]any{"$gt": 9.0}}}}, false},
		{"$mod", M{"n": M{"$mod": []any{3.0, 1.0}}}, true},
		{"$mod miss", M{"n": M{"$mod": []any{3.0, 2.0}}}, false},
		{"$type string", M{"name": M{"$type": "string"}}, true},
		{"$type number", M{"name": M{"$type": "number"}}, false},
		{"$regex", M{"name": M{"$regex": "^Hello"}}, true},
		{"$regex case-sensitive", M{"name": M{"$regex": "^hello"}}, false},
		{"$regex with i option", M{"name": M{"$regex": "^hello", "$options": "i"}}, true},
		{"dot path into array", M{"items.n": "b"}, true},
		{"dot path miss", M{"items.n": "z"}, false},
	}
	for _, c := range cases {
		if got := matchFilter(d, c.filter); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// An operator outside the allowlist must never widen a result set: a filter the
// engine does not understand matches nothing rather than everything.
func TestUnknownOperatorMatchesNothing(t *testing.T) {
	d := doc("a", int64(1))
	if matchFilter(d, M{"a": M{"$where": "true"}}) {
		t.Error("$where must not match")
	}
	if matchFilter(d, M{"$expr": map[string]any{"$eq": []any{1.0, 1.0}}}) {
		t.Error("$expr must not match")
	}
}

func TestSortRows(t *testing.T) {
	rows := []row{
		{id: "c", doc: doc("age", int64(30), "name", "carol")},
		{id: "a", doc: doc("age", int64(40), "name", "alice")},
		{id: "b", doc: doc("age", int64(30), "name", "bob")},
	}
	sortRows(rows, []SortKey{{Field: "age", Asc: true}, {Field: "name", Asc: true}})
	got := []string{rows[0].id, rows[1].id, rows[2].id}
	want := []string{"b", "c", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort = %v want %v", got, want)
		}
	}

	sortRows(rows, []SortKey{{Field: "age", Asc: false}})
	if rows[0].id != "a" {
		t.Errorf("desc sort head = %q want a", rows[0].id)
	}
}

func TestSortIsTypeStable(t *testing.T) {
	// mixed types must not panic and must produce a deterministic order
	rows := []row{
		{id: "1", doc: doc("v", "text")},
		{id: "2", doc: doc("v", int64(5))},
		{id: "3", doc: doc("v", nil)},
	}
	sortRows(rows, []SortKey{{Field: "v", Asc: true}})
	if rows[0].id != "3" || rows[1].id != "2" || rows[2].id != "1" {
		t.Errorf("type order = %s,%s,%s want 3,2,1 (null < number < string)", rows[0].id, rows[1].id, rows[2].id)
	}
}

func TestApplyFindOptSkipLimit(t *testing.T) {
	mk := func(ids ...string) []row {
		out := make([]row, len(ids))
		for i, id := range ids {
			out[i] = row{id: id, doc: doc("k", id)}
		}
		return out
	}
	got, err := applyFindOpt(mk("a", "b", "c", "d"), FindOpt{Skip: 1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].id != "b" || got[1].id != "c" {
		t.Errorf("skip/limit = %v", got)
	}

	got, err = applyFindOpt(mk("a", "b"), FindOpt{Skip: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("skip past end should be empty, got %d", len(got))
	}
}

func TestProjection(t *testing.T) {
	d := doc("_id", "k1", "a", int64(1), "b", int64(2), "c", int64(3))

	inc := applyProjection(d, M{"a": 1.0})
	if _, ok := inc["a"]; !ok {
		t.Error("include mode dropped a")
	}
	if _, ok := inc["b"]; ok {
		t.Error("include mode kept b")
	}
	if inc["_id"] != "k1" {
		t.Error("include mode should keep _id by default")
	}

	incNoID := applyProjection(d, M{"a": 1.0, "_id": 0.0})
	if _, ok := incNoID["_id"]; ok {
		t.Error("_id:0 should drop _id")
	}

	exc := applyProjection(d, M{"b": 0.0})
	if _, ok := exc["b"]; ok {
		t.Error("exclude mode kept b")
	}
	if exc["a"] == nil || exc["c"] == nil {
		t.Error("exclude mode dropped fields it should keep")
	}
}

func TestSortKeysFromMap(t *testing.T) {
	keys := sortKeysFromMap(M{"b": -1.0, "a": 1.0})
	if len(keys) != 2 {
		t.Fatalf("got %d keys", len(keys))
	}
	// field names are ordered so an unordered Sort map is at least deterministic
	if keys[0].Field != "a" || !keys[0].Asc {
		t.Errorf("keys[0]=%+v", keys[0])
	}
	if keys[1].Field != "b" || keys[1].Asc {
		t.Errorf("keys[1]=%+v", keys[1])
	}
}

func TestTimeComparison(t *testing.T) {
	early := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := doc("at", early)
	if !matchFilter(d, M{"at": M{"$lt": late}}) {
		t.Error("time $lt failed")
	}
	if matchFilter(d, M{"at": M{"$gt": late}}) {
		t.Error("time $gt should fail")
	}
	if !matchFilter(d, M{"at": early}) {
		t.Error("time equality failed")
	}
}
