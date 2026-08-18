package dopdb

import (
	"reflect"
	"strings"
	"testing"
)

// The SQL layer is a front end that compiles to the same (filter, sort, page)
// shape FIND uses, so these tests check the COMPILATION — no server needed. The
// resulting filters are then run through the query engine to prove the two agree.

func mustParse(t *testing.T, s string) *SQLQuery {
	t.Helper()
	q, err := ParseSQL(s)
	if err != nil {
		t.Fatalf("ParseSQL(%q): %v", s, err)
	}
	return q
}

func TestSQLCompilesToFilters(t *testing.T) {
	cases := []struct {
		sql  string
		want M
	}{
		{"SELECT * FROM t WHERE a = 1", M{"a": 1.0}},
		{"SELECT * FROM t WHERE a = 'x'", M{"a": "x"}},
		{"SELECT * FROM t WHERE a != 1", M{"a": M{"$ne": 1.0}}},
		{"SELECT * FROM t WHERE a <> 1", M{"a": M{"$ne": 1.0}}},
		{"SELECT * FROM t WHERE a > 1", M{"a": M{"$gt": 1.0}}},
		{"SELECT * FROM t WHERE a >= 1", M{"a": M{"$gte": 1.0}}},
		{"SELECT * FROM t WHERE a < 1", M{"a": M{"$lt": 1.0}}},
		{"SELECT * FROM t WHERE a <= 1", M{"a": M{"$lte": 1.0}}},
		{"SELECT * FROM t WHERE a = TRUE", M{"a": true}},
		{"SELECT * FROM t WHERE a = -3", M{"a": -3.0}},
		{"SELECT * FROM t WHERE a IN (1, 2)", M{"a": M{"$in": []any{1.0, 2.0}}}},
		{"SELECT * FROM t WHERE a NOT IN ('x')", M{"a": M{"$nin": []any{"x"}}}},
		{"SELECT * FROM t WHERE a BETWEEN 1 AND 5", M{"a": M{"$gte": 1.0, "$lte": 5.0}}},
		{"SELECT * FROM t WHERE a IS NULL", M{"a": nil}},
		{"SELECT * FROM t WHERE a IS NOT NULL", M{"a": M{"$ne": nil}}},
		{"SELECT * FROM t WHERE a LIKE 'ab%'", M{"a": M{"$regex": "^ab.*$"}}},
		{"SELECT * FROM t WHERE a ILIKE 'ab%'", M{"a": M{"$regex": "^ab.*$", "$options": "i"}}},
		{"SELECT * FROM t WHERE addr.city = 'NY'", M{"addr.city": "NY"}},
		{"SELECT * FROM t WHERE _id = 'k1'", M{"_id": "k1"}},
	}
	for _, c := range cases {
		q := mustParse(t, c.sql)
		if !reflect.DeepEqual(q.Filter, c.want) {
			t.Errorf("%s\n got %#v\nwant %#v", c.sql, q.Filter, c.want)
		}
	}
}

func TestSQLLogicalStructure(t *testing.T) {
	q := mustParse(t, "SELECT * FROM t WHERE a = 1 AND b = 2")
	and, ok := q.Filter["$and"].([]any)
	if !ok || len(and) != 2 {
		t.Fatalf("AND => %#v", q.Filter)
	}
	q = mustParse(t, "SELECT * FROM t WHERE a = 1 OR b = 2")
	if or, ok := q.Filter["$or"].([]any); !ok || len(or) != 2 {
		t.Fatalf("OR => %#v", q.Filter)
	}
	q = mustParse(t, "SELECT * FROM t WHERE NOT a = 1")
	if nor, ok := q.Filter["$nor"].([]any); !ok || len(nor) != 1 {
		t.Fatalf("NOT => %#v", q.Filter)
	}
	// parentheses bind tighter than the surrounding OR
	q = mustParse(t, "SELECT * FROM t WHERE (a = 1 OR a = 2) AND b = 3")
	if _, ok := q.Filter["$and"]; !ok {
		t.Fatalf("parenthesised => %#v", q.Filter)
	}
}

// Two predicates on the SAME column must both survive — key-merging would
// silently drop one and widen the result.
func TestSQLSameColumnTwiceKeepsBoth(t *testing.T) {
	q := mustParse(t, "SELECT * FROM t WHERE a > 1 AND a < 5")
	d3 := map[string]any{"a": int64(3)}
	d9 := map[string]any{"a": int64(9)}
	if !matchFilter(d3, q.Filter) {
		t.Error("a=3 should match 1 < a < 5")
	}
	if matchFilter(d9, q.Filter) {
		t.Error("a=9 must not match 1 < a < 5")
	}
}

func TestSQLShaping(t *testing.T) {
	q := mustParse(t, "SELECT name, age FROM t ORDER BY age DESC, name ASC LIMIT 10 OFFSET 5")
	if q.Table != "t" {
		t.Errorf("table=%q", q.Table)
	}
	if len(q.Opt.Projection) != 2 || q.Opt.Projection["name"] != 1 {
		t.Errorf("projection=%#v", q.Opt.Projection)
	}
	want := []SortKey{{Field: "age", Asc: false}, {Field: "name", Asc: true}}
	if !reflect.DeepEqual(q.Opt.SortKeys, want) {
		t.Errorf("sort=%#v", q.Opt.SortKeys)
	}
	if q.Opt.Limit != 10 || q.Opt.Skip != 5 {
		t.Errorf("limit=%d skip=%d", q.Opt.Limit, q.Opt.Skip)
	}

	c := mustParse(t, "SELECT COUNT(*) FROM t WHERE a = 1")
	if !c.Count {
		t.Error("COUNT(*) not recognised")
	}
}

// The compiled filter must behave identically to the equivalent hand-written
// one — SQL is a front end, not a second dialect.
func TestSQLAgreesWithTheQueryEngine(t *testing.T) {
	rows := []map[string]any{
		{"_id": "1", "name": "ada", "age": int64(30), "tags": []any{"x"}},
		{"_id": "2", "name": "bob", "age": int64(40)},
		{"_id": "3", "name": "cid", "age": int64(50), "tags": []any{"y"}},
	}
	check := func(sql string, wantIDs ...string) {
		t.Helper()
		q := mustParse(t, sql)
		var got []string
		for _, r := range rows {
			if matchFilter(r, q.Filter) {
				got = append(got, r["_id"].(string))
			}
		}
		if len(got) != len(wantIDs) {
			t.Errorf("%s => %v want %v", sql, got, wantIDs)
			return
		}
		for i := range got {
			if got[i] != wantIDs[i] {
				t.Errorf("%s => %v want %v", sql, got, wantIDs)
				return
			}
		}
	}
	check("SELECT * FROM t WHERE age >= 40", "2", "3")
	check("SELECT * FROM t WHERE age > 30 AND age < 50", "2")
	check("SELECT * FROM t WHERE name IN ('ada','cid')", "1", "3")
	check("SELECT * FROM t WHERE name LIKE 'a%'", "1")
	check("SELECT * FROM t WHERE name ILIKE 'A%'", "1")
	check("SELECT * FROM t WHERE tags IS NULL", "2")
	check("SELECT * FROM t WHERE tags IS NOT NULL", "1", "3")
	check("SELECT * FROM t WHERE NOT age = 40", "1", "3")
	check("SELECT * FROM t WHERE _id = '2'", "2")
	check("SELECT * FROM t WHERE name = 'nobody'")
}

// Everything dopdb deliberately does not implement must be REJECTED with a clear
// message, never silently ignored — a quietly dropped JOIN or GROUP BY would
// return a plausible-looking wrong answer.
func TestSQLRejectsWhatItDoesNotSupport(t *testing.T) {
	cases := []struct {
		sql  string
		want string
	}{
		{"INSERT INTO t VALUES (1)", "read-only"},
		{"UPDATE t SET a = 1", "read-only"},
		{"DELETE FROM t WHERE a = 1", "read-only"},
		{"DROP TABLE t", "DDL"},
		{"SELECT * FROM t JOIN u ON t.a = u.a", "JOIN"},
		{"SELECT * FROM t, u", "multiple tables"},
		{"SELECT * FROM t GROUP BY a", "GROUP BY"},
		{"SELECT SUM(a) FROM t", "COUNT(*)"},
		{"SELECT a AS b FROM t", "aliases"},
		{"SELECT * FROM t WHERE a = NULL", "IS NULL"},
		{"SELECT * FROM t; SELECT * FROM t", "one statement"},
		{"SELECT * FROM t WHERE a = 1 extra", "unexpected"},
		{"SELECT * FROM t LIMIT -1", "non-negative"},
		{"SELECT * FROM `t$evil`", "illegal identifier"},
		{"SELECT * FROM t WHERE a = \"x\"", "literal value"}, // double quotes are identifiers, not strings
	}
	for _, c := range cases {
		_, err := ParseSQL(c.sql)
		if err == nil {
			t.Errorf("%s: expected an error", c.sql)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.sql, err, c.want)
		}
	}
}

// The FROM clause is a security boundary: each collection is authorized
// separately, so a statement must not name a different one.
func TestSQLTableMustMatchCollection(t *testing.T) {
	q := mustParse(t, "SELECT * FROM secrets WHERE a = 1")
	if err := q.CheckTable("notes"); err == nil {
		t.Fatal("FROM secrets must be rejected when serving collection notes")
	}
	if err := q.CheckTable("secrets"); err != nil {
		t.Fatalf("matching table rejected: %v", err)
	}
}

// The compiled filter still has to pass the operator allowlist — SQL is not a
// way around the sanitizer.
func TestSQLOutputPassesSanitizer(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM t WHERE a = 1 AND (b > 2 OR c LIKE 'x%')",
		"SELECT * FROM t WHERE a IN (1,2,3) AND d IS NOT NULL",
		"SELECT * FROM t WHERE NOT a BETWEEN 1 AND 2",
	} {
		q := mustParse(t, sql)
		if _, err := SanitizeFilter(q.Filter); err != nil {
			t.Errorf("%s: sanitizer rejected the compiled filter: %v", sql, err)
		}
	}
}

func TestSQLStringLiteralEscaping(t *testing.T) {
	q := mustParse(t, "SELECT * FROM t WHERE name = 'O''Brien'")
	if q.Filter["name"] != "O'Brien" {
		t.Errorf("escaped quote => %#v", q.Filter["name"])
	}
}

func TestLikeToRegexEscapesLiterals(t *testing.T) {
	// a '.' in a LIKE pattern is literal, not a wildcard
	q := mustParse(t, "SELECT * FROM t WHERE a LIKE 'a.c'")
	if matchFilter(map[string]any{"a": "axc"}, q.Filter) {
		t.Error("'.' in LIKE must be literal")
	}
	if !matchFilter(map[string]any{"a": "a.c"}, q.Filter) {
		t.Error("'a.c' should match LIKE 'a.c'")
	}
}
