package httpserve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/redis/go-redis/v9"
)

// HTTP-layer integration tests against a real KVRocks (or any Redis-protocol
// server). They self-skip unless DOPDB_TEST_KVROCKS_URI is set; each uses a
// throwaway key namespace dropped on cleanup. They reuse the database-free
// helpers (do/tokenFor/decodeObj/testSecret).

type itDoc struct {
	Owner string `json:"owner"`
	Note  string `json:"note"`
}

func kvOrSkip(t *testing.T) *redis.Client {
	t.Helper()
	uri := os.Getenv("DOPDB_TEST_KVROCKS_URI")
	if uri == "" {
		t.Skip("set DOPDB_TEST_KVROCKS_URI (e.g. redis://localhost:6666) to run integration tests")
	}
	opt, err := redis.ParseURL(uri)
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	opt.PoolSize = 64
	cl := redis.NewClient(opt)
	if err := cl.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return cl
}

// dropNS removes every key dopdb wrote under ns.
func dropNS(cl *redis.Client, ns string) {
	ctx := context.Background()
	var cursor uint64
	for {
		keys, next, err := cl.Scan(ctx, cursor, ns+":*", 500).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			_ = cl.Del(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}

func decodeArr(t *testing.T, rr *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var a []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode array: %v (body=%s)", err, rr.Body.String())
	}
	return a
}

// setupKVHandler wires a single "default" datasource to a throwaway namespace,
// runs register (which should dopdb.RegisterHttp the test collections), grants
// the given (cmd, coll) pairs, and returns a Handler + cleanup.
func setupKVHandler(t *testing.T, grants [][2]string, register func()) (*Handler, func()) {
	t.Helper()
	cl := kvOrSkip(t)
	ns := fmt.Sprintf("dopdb_it_%d", time.Now().UnixNano())
	ds := dopdb.NewDatasources()
	ds.Add("default", cl, ns)
	dopdb.SetDatasources(ds)
	register()
	p := NewPermissions()
	for _, g := range grants {
		p.Grant(g[0], g[1])
	}
	h := NewHandler(NewServer(testSecret), p)
	return h, func() {
		dropNS(cl, ns)
		_ = cl.Close()
		dopdb.SetDatasources(nil)
	}
}

func TestIntegrationHTTPRoundTrip(t *testing.T) {
	coll := "it_notes"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}, {"HGET", coll}, {"FIND", coll}, {"HDEL", coll}},
		func() { dopdb.RegisterHttp(dopdb.New[string, *itDoc](dopdb.WithCollection(coll))) },
	)
	defer done()

	if rr := do(h, "POST", "/api/hset/"+coll+"?f=k1", `{"note":"hello"}`, tokenFor(t, "u1")); rr.Code != 200 {
		t.Fatalf("hset status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr := do(h, "GET", "/api/hget/"+coll+"?f=k1", "", tokenFor(t, "u1"))
	if rr.Code != 200 {
		t.Fatalf("hget status=%d body=%s", rr.Code, rr.Body.String())
	}
	if obj := decodeObj(t, rr); obj["note"] != "hello" {
		t.Errorf("note=%v want hello", obj["note"])
	}

	// FIND returns an array — evaluated in-process now, same wire shape.
	rr = do(h, "POST", "/api/find/"+coll, `{"note":"hello"}`, tokenFor(t, "u1"))
	if rr.Code != 200 {
		t.Fatalf("find status=%d", rr.Code)
	}
	if arr := decodeArr(t, rr); len(arr) != 1 {
		t.Errorf("find returned %d docs want 1", len(arr))
	}

	// Permission gate: HKEYS was not granted -> 403.
	if rr := do(h, "GET", "/api/hkeys/"+coll, "", tokenFor(t, "u1")); rr.Code != 403 {
		t.Errorf("ungranted HKEYS expected 403, got %d", rr.Code)
	}
}

func TestIntegrationOwnerScope(t *testing.T) {
	coll := "it_owned"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}, {"HGET", coll}},
		func() {
			dopdb.RegisterHttp(dopdb.New[string, *itDoc](dopdb.WithCollection(coll)))
			dopdb.SetOwnerScope(coll, "owner", "uid") // doc.owner == claim "uid"
		},
	)
	defer done()

	// alice writes her own record: ?f=@uid resolves the key to "alice".
	if rr := do(h, "POST", "/api/hset/"+coll+"?f=@uid", `{"note":"secret"}`, tokenFor(t, "alice")); rr.Code != 200 {
		t.Fatalf("alice hset=%d body=%s", rr.Code, rr.Body.String())
	}
	// alice reads her own.
	if rr := do(h, "GET", "/api/hget/"+coll+"?f=@uid", "", tokenFor(t, "alice")); rr.Code != 200 {
		t.Fatalf("alice hget=%d", rr.Code)
	}
	// bob, knowing alice's id, still cannot read it (row-level isolation) -> 404.
	if rr := do(h, "GET", "/api/hget/"+coll+"?f=alice", "", tokenFor(t, "bob")); rr.Code != 404 {
		t.Errorf("bob reading alice's record expected 404, got %d", rr.Code)
	}
	// nor overwrite it: the scoped write is a compare-and-set on the owner.
	if rr := do(h, "POST", "/api/hset/"+coll+"?f=alice", `{"note":"stolen"}`, tokenFor(t, "bob")); rr.Code != 403 {
		t.Errorf("bob overwriting alice's record expected 403, got %d", rr.Code)
	}
}

func TestIntegrationMultiDatasource(t *testing.T) {
	cl := kvOrSkip(t)
	nsA := fmt.Sprintf("dopdb_it_a_%d", time.Now().UnixNano())
	nsB := fmt.Sprintf("dopdb_it_b_%d", time.Now().UnixNano())
	ds := dopdb.NewDatasources()
	ds.Add("default", cl, nsA)
	ds.Add("other", cl, nsB)
	dopdb.SetDatasources(ds)
	defer func() {
		dropNS(cl, nsA)
		dropNS(cl, nsB)
		_ = cl.Close()
		dopdb.SetDatasources(nil)
	}()

	coll := "it_ds"
	dopdb.RegisterHttp(dopdb.New[string, *itDoc](dopdb.WithCollection(coll)))
	p := NewPermissions()
	p.Grant("HSET", coll)
	p.Grant("HGET", coll)
	h := NewHandler(NewServer(testSecret), p)
	tok := tokenFor(t, "u1")

	// Same key, different datasource selected by ?ds=.
	if rr := do(h, "POST", "/api/hset/"+coll+"?f=k1", `{"note":"in-default"}`, tok); rr.Code != 200 {
		t.Fatalf("default hset=%d", rr.Code)
	}
	if rr := do(h, "POST", "/api/hset/"+coll+"?ds=other&f=k1", `{"note":"in-other"}`, tok); rr.Code != 200 {
		t.Fatalf("other hset=%d", rr.Code)
	}

	rr := do(h, "GET", "/api/hget/"+coll+"?f=k1", "", tok)
	if obj := decodeObj(t, rr); obj["note"] != "in-default" {
		t.Errorf("default note=%v want in-default", obj["note"])
	}
	rr = do(h, "GET", "/api/hget/"+coll+"?ds=other&f=k1", "", tok)
	if obj := decodeObj(t, rr); obj["note"] != "in-other" {
		t.Errorf("other note=%v want in-other", obj["note"])
	}
}

// FIND's shaping (sort, skip, limit, projection) moved from the database into
// the query engine, so the HTTP surface of it needs a test of its own.
func TestIntegrationFindShaping(t *testing.T) {
	type rec struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	coll := "it_shape"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}, {"FIND", coll}, {"COUNT", coll}},
		func() { dopdb.RegisterHttp(dopdb.New[string, *rec](dopdb.WithCollection(coll))) },
	)
	defer done()
	tok := tokenFor(t, "u1")

	for i, r := range []rec{{"carol", 30}, {"alice", 40}, {"bob", 30}} {
		body := fmt.Sprintf(`{"name":%q,"age":%d}`, r.Name, r.Age)
		if rr := do(h, "POST", fmt.Sprintf("/api/hset/%s?f=k%d", coll, i), body, tok); rr.Code != 200 {
			t.Fatalf("hset %d = %d", i, rr.Code)
		}
	}

	// sort by age asc then name asc, limit 2
	rr := do(h, "POST", "/api/find/"+coll+`?s={"age":1}&limit=2`, `{}`, tok)
	if rr.Code != 200 {
		t.Fatalf("find status=%d body=%s", rr.Code, rr.Body.String())
	}
	arr := decodeArr(t, rr)
	if len(arr) != 2 {
		t.Fatalf("limit ignored: %d docs", len(arr))
	}
	if arr[0]["age"].(float64) != 30 || arr[1]["age"].(float64) != 30 {
		t.Errorf("sort by age asc failed: %v", arr)
	}

	// projection: only name
	rr = do(h, "POST", "/api/find/"+coll+`?p={"name":1}`, `{"age":40}`, tok)
	arr = decodeArr(t, rr)
	if len(arr) != 1 {
		t.Fatalf("projection query returned %d docs", len(arr))
	}
	if arr[0]["name"] != "alice" {
		t.Errorf("projected name=%v", arr[0]["name"])
	}
	if age, ok := arr[0]["age"]; ok && age.(float64) != 0 {
		t.Errorf("projection leaked age: %v", arr[0])
	}

	// $-operator smuggling in ?s= is still rejected before it reaches the engine
	if rr := do(h, "POST", "/api/find/"+coll+`?s={"$where":1}`, `{}`, tok); rr.Code != 400 {
		t.Errorf("sort with $ key expected 400, got %d", rr.Code)
	}

	// COUNT goes through the same evaluator
	rr = do(h, "POST", "/api/count/"+coll, `{"age":30}`, tok)
	if obj := decodeObj(t, rr); obj["count"].(float64) != 2 {
		t.Errorf("count=%v want 2", obj["count"])
	}
}

// A unique-index violation is a 409 now: KVRocks has no server-side unique
// index, so dopdb raises it and the HTTP layer maps it.
func TestIntegrationUniqueConflictIs409(t *testing.T) {
	type uniqRec struct {
		Email string `json:"email" index:"unique"`
	}
	coll := "it_uniq_http"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}},
		func() { dopdb.RegisterHttp(dopdb.New[string, *uniqRec](dopdb.WithCollection(coll))) },
	)
	defer done()
	tok := tokenFor(t, "u1")

	if rr := do(h, "POST", "/api/hset/"+coll+"?f=a", `{"email":"x@x.io"}`, tok); rr.Code != 200 {
		t.Fatalf("first hset=%d body=%s", rr.Code, rr.Body.String())
	}
	rr := do(h, "POST", "/api/hset/"+coll+"?f=b", `{"email":"x@x.io"}`, tok)
	if rr.Code != 409 {
		t.Errorf("duplicate hset=%d want 409 (body=%s)", rr.Code, rr.Body.String())
	}
	if obj := decodeObj(t, rr); obj["code"] != "conflict" {
		t.Errorf("error code=%v want conflict", obj["code"])
	}
}

// ---- SQL ---------------------------------------------------------------------
//
// SQL is a front end over the same query engine FIND uses, exposed only on Hash
// collections. These tests cover the three things the HTTP path adds on top of
// the parser: the FROM check, the owner scope, and the limit clamp.

func TestIntegrationSQL(t *testing.T) {
	type rec struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	coll := "it_sql"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}, {"SQL", coll}},
		func() { dopdb.RegisterHttp(dopdb.New[string, *rec](dopdb.WithCollection(coll))) },
	)
	defer done()
	tok := tokenFor(t, "u1")

	for i, r := range []rec{{"carol", 30}, {"alice", 40}, {"bob", 30}} {
		body := fmt.Sprintf(`{"name":%q,"age":%d}`, r.Name, r.Age)
		if rr := do(h, "POST", fmt.Sprintf("/api/hset/%s?f=k%d", coll, i), body, tok); rr.Code != 200 {
			t.Fatalf("hset %d = %d", i, rr.Code)
		}
	}

	// a raw statement as the body
	rr := do(h, "POST", "/api/sql/"+coll, "SELECT * FROM "+coll+" WHERE age >= 40", tok)
	if rr.Code != 200 {
		t.Fatalf("sql status=%d body=%s", rr.Code, rr.Body.String())
	}
	if arr := decodeArr(t, rr); len(arr) != 1 || arr[0]["name"] != "alice" {
		t.Errorf("WHERE age >= 40 => %v", arr)
	}

	// the {"sql": "..."} wrapper works too
	rr = do(h, "POST", "/api/sql/"+coll, `{"sql":"SELECT * FROM `+coll+` WHERE age = 30 ORDER BY name ASC"}`, tok)
	arr := decodeArr(t, rr)
	if len(arr) != 2 || arr[0]["name"] != "bob" || arr[1]["name"] != "carol" {
		t.Errorf("ORDER BY name => %v", arr)
	}

	// projection: only the named column comes back
	rr = do(h, "POST", "/api/sql/"+coll, "SELECT name FROM "+coll+" WHERE name = 'alice'", tok)
	arr = decodeArr(t, rr)
	if len(arr) != 1 || arr[0]["name"] != "alice" {
		t.Fatalf("projection => %v", arr)
	}
	if age, ok := arr[0]["age"]; ok && age.(float64) != 0 {
		t.Errorf("projection leaked age: %v", arr[0])
	}

	// COUNT(*) answers a number, like the COUNT command
	rr = do(h, "GET", "/api/sql/"+coll+"?q="+url.QueryEscape("SELECT COUNT(*) FROM "+coll+" WHERE age = 30"), "", tok)
	if obj := decodeObj(t, rr); obj["count"].(float64) != 2 {
		t.Errorf("COUNT(*) => %v", obj["count"])
	}

	// LIKE
	rr = do(h, "POST", "/api/sql/"+coll, "SELECT * FROM "+coll+" WHERE name LIKE 'a%'", tok)
	if arr := decodeArr(t, rr); len(arr) != 1 || arr[0]["name"] != "alice" {
		t.Errorf("LIKE => %v", arr)
	}

	// a bad statement is the caller's mistake: 400, not 500
	if rr := do(h, "POST", "/api/sql/"+coll, "SELECT * FROM", tok); rr.Code != 400 {
		t.Errorf("bad SQL status=%d want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	if rr := do(h, "POST", "/api/sql/"+coll, "DELETE FROM "+coll, tok); rr.Code != 400 {
		t.Errorf("DELETE status=%d want 400", rr.Code)
	}

	// FROM naming a DIFFERENT collection must be refused: each collection is
	// authorized separately, so SQL must not be a way to read another one.
	if rr := do(h, "POST", "/api/sql/"+coll, "SELECT * FROM other_secret_collection", tok); rr.Code != 400 {
		t.Errorf("cross-collection FROM status=%d want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

// The owner scope is AND-ed into the compiled filter and cannot be widened by
// the statement — the same guarantee FIND gives.
func TestIntegrationSQLOwnerScope(t *testing.T) {
	coll := "it_sql_scoped"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}, {"SQL", coll}},
		func() {
			dopdb.RegisterHttp(dopdb.New[string, *itDoc](dopdb.WithCollection(coll)))
			dopdb.SetOwnerScope(coll, "owner", "uid")
		},
	)
	defer done()

	if rr := do(h, "POST", "/api/hset/"+coll+"?f=@uid", `{"note":"alice-secret"}`, tokenFor(t, "alice")); rr.Code != 200 {
		t.Fatalf("alice hset=%d", rr.Code)
	}
	if rr := do(h, "POST", "/api/hset/"+coll+"?f=@uid", `{"note":"bob-secret"}`, tokenFor(t, "bob")); rr.Code != 200 {
		t.Fatalf("bob hset=%d", rr.Code)
	}

	// alice sees only her own row
	rr := do(h, "POST", "/api/sql/"+coll, "SELECT * FROM "+coll, tokenFor(t, "alice"))
	arr := decodeArr(t, rr)
	if len(arr) != 1 || arr[0]["note"] != "alice-secret" {
		t.Errorf("scoped SELECT * => %v", arr)
	}

	// and cannot widen the scope by naming another owner in WHERE
	rr = do(h, "POST", "/api/sql/"+coll, "SELECT * FROM "+coll+" WHERE owner = 'bob'", tokenFor(t, "alice"))
	if arr := decodeArr(t, rr); len(arr) != 0 {
		t.Errorf("alice reading bob's rows via SQL => %v", arr)
	}

	// nor by an OR that would otherwise match everything
	rr = do(h, "POST", "/api/sql/"+coll, "SELECT * FROM "+coll+" WHERE owner = 'bob' OR owner = 'alice'", tokenFor(t, "alice"))
	if arr := decodeArr(t, rr); len(arr) != 1 || arr[0]["note"] != "alice-secret" {
		t.Errorf("OR-widened scope => %v", arr)
	}

	// COUNT(*) is scoped too
	rr = do(h, "GET", "/api/sql/"+coll+"?q="+url.QueryEscape("SELECT COUNT(*) FROM "+coll), "", tokenFor(t, "alice"))
	if obj := decodeObj(t, rr); obj["count"].(float64) != 1 {
		t.Errorf("scoped COUNT(*) => %v", obj["count"])
	}
}

// SQL is Hash-only: a String/List/Set/ZSet collection is not a table.
func TestIntegrationSQLIsHashOnly(t *testing.T) {
	coll := "it_sql_str"
	h, done := setupKVHandler(t,
		[][2]string{{"SQL", coll}},
		func() { dopdb.NewString[string](dopdb.WithCollection(coll)).HttpOn() },
	)
	defer done()

	rr := do(h, "POST", "/api/sql/"+coll, "SELECT * FROM "+coll, tokenFor(t, "u1"))
	if rr.Code != 404 {
		t.Errorf("SQL on a string collection status=%d want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}

// The HTTP path clamps LIMIT exactly as FIND's ?limit= is clamped.
func TestIntegrationSQLLimitIsClamped(t *testing.T) {
	type rec struct {
		N int `json:"n"`
	}
	coll := "it_sql_limit"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}, {"SQL", coll}},
		func() { dopdb.RegisterHttp(dopdb.New[string, *rec](dopdb.WithCollection(coll))) },
	)
	defer done()
	tok := tokenFor(t, "u1")
	for i := 0; i < 5; i++ {
		do(h, "POST", fmt.Sprintf("/api/hset/%s?f=k%d", coll, i), fmt.Sprintf(`{"n":%d}`, i), tok)
	}

	// an absurd LIMIT is clamped, not honoured
	rr := do(h, "POST", "/api/sql/"+coll, "SELECT * FROM "+coll+" LIMIT 999999", tok)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if arr := decodeArr(t, rr); len(arr) != 5 {
		t.Errorf("got %d rows want 5", len(arr))
	}
	// an explicit small LIMIT is honoured
	rr = do(h, "POST", "/api/sql/"+coll, "SELECT * FROM "+coll+" ORDER BY n ASC LIMIT 2", tok)
	if arr := decodeArr(t, rr); len(arr) != 2 || arr[0]["n"].(float64) != 0 {
		t.Errorf("LIMIT 2 => %v", arr)
	}
}

// ---- post-migration audit regressions ---------------------------------------
//
// Four defects the KVRocks migration introduced, each reproduced before it was
// fixed. None had a test, which is why they survived; these are those tests.

// A scoped increment used to check ownership in the dispatcher and then run an
// UNSCOPED increment. An owner racing their own delete could land the increment
// after the delete, where it upserted a document with NO owner field — a row
// invisible to every scoped read and delete afterwards. A third party could also
// have the increment land on their freshly recreated document.
func TestIntegrationScopedIncrIsAtomic(t *testing.T) {
	type ctr struct {
		Owner string `json:"owner"`
		N     int    `json:"n"`
	}
	coll := "it_incr_scoped"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}, {"HGET", coll}, {"HDEL", coll}, {"HINCRBY", coll}, {"HGETALL", coll}},
		func() {
			dopdb.RegisterHttp(dopdb.New[string, *ctr](dopdb.WithCollection(coll)))
			dopdb.SetOwnerScope(coll, "owner", "uid")
		},
	)
	defer done()
	alice, bob := tokenFor(t, "alice"), tokenFor(t, "bob")

	// a scoped increment must never create a document
	if rr := do(h, "POST", "/api/hincrby/"+coll+"?f=ghost&field=n&n=1", "", alice); rr.Code != 403 {
		t.Errorf("scoped incr on an absent document = %d want 403 (body=%s)", rr.Code, rr.Body.String())
	}
	if rr := do(h, "GET", "/api/hget/"+coll+"?f=ghost", "", alice); rr.Code != 404 {
		t.Errorf("an unowned ghost row was created: hget = %d", rr.Code)
	}

	// and must not touch another tenant's document
	if rr := do(h, "POST", "/api/hset/"+coll+"?f=@uid", `{"n":0}`, alice); rr.Code != 200 {
		t.Fatalf("alice hset = %d", rr.Code)
	}
	if rr := do(h, "POST", "/api/hincrby/"+coll+"?f=alice&field=n&n=100", "", bob); rr.Code != 403 {
		t.Errorf("bob incrementing alice's row = %d want 403", rr.Code)
	}
	rr := do(h, "GET", "/api/hget/"+coll+"?f=@uid", "", alice)
	if obj := decodeObj(t, rr); obj["n"].(float64) != 0 {
		t.Errorf("alice's counter was modified by bob: n=%v", obj["n"])
	}

	// the owner's own increment still works
	if rr := do(h, "POST", "/api/hincrby/"+coll+"?f=@uid&field=n&n=3", "", alice); rr.Code != 200 {
		t.Fatalf("alice incrementing her own row = %d", rr.Code)
	}
	rr = do(h, "GET", "/api/hget/"+coll+"?f=@uid", "", alice)
	if obj := decodeObj(t, rr); obj["n"].(float64) != 3 {
		t.Errorf("n=%v want 3", obj["n"])
	}
}

// Incrementing a field that holds a string used to overwrite it with a number
// and answer 200. Mongo's $inc refused; silently destroying the value is worse
// than an error.
func TestIntegrationIncrRefusesNonNumericField(t *testing.T) {
	type doc struct {
		V string `json:"v"`
		N int    `json:"n"`
	}
	coll := "it_incr_type"
	h, done := setupKVHandler(t,
		[][2]string{{"HSET", coll}, {"HGET", coll}, {"HINCRBY", coll}},
		func() { dopdb.RegisterHttp(dopdb.New[string, *doc](dopdb.WithCollection(coll))) },
	)
	defer done()
	tok := tokenFor(t, "u1")

	if rr := do(h, "POST", "/api/hset/"+coll+"?f=k", `{"v":"abc","n":10}`, tok); rr.Code != 200 {
		t.Fatalf("hset = %d", rr.Code)
	}
	rr := do(h, "POST", "/api/hincrby/"+coll+"?f=k&field=v&n=5", "", tok)
	if rr.Code != 409 {
		t.Errorf("incr on a string field = %d want 409 (body=%s)", rr.Code, rr.Body.String())
	}
	got := do(h, "GET", "/api/hget/"+coll+"?f=k", "", tok)
	if obj := decodeObj(t, got); obj["v"] != "abc" {
		t.Errorf("the string value was overwritten: v=%v", obj["v"])
	}
	// a numeric field still increments
	if rr := do(h, "POST", "/api/hincrby/"+coll+"?f=k&field=n&n=5", "", tok); rr.Code != 200 {
		t.Fatalf("incr on a numeric field = %d", rr.Code)
	}
	if obj := decodeObj(t, do(h, "GET", "/api/hget/"+coll+"?f=k", "", tok)); obj["n"].(float64) != 15 {
		t.Errorf("n=%v want 15", obj["n"])
	}
}

// Redis deletes a list/set/zset key when its last element goes, but the owner
// claim used to survive — so the first user to touch a key name owned it
// forever and everyone else got a permanent 403 for a key that did not exist.
func TestIntegrationOwnerClaimIsReleasedWhenKeyEmpties(t *testing.T) {
	coll := "it_owner_release"
	h, done := setupKVHandler(t,
		[][2]string{{"LPUSH", coll}, {"RPOP", coll}, {"LLEN", coll}},
		func() {
			dopdb.NewList[string, string](dopdb.WithCollection(coll)).HttpOn()
			dopdb.SetOwnerScope(coll, "owner", "uid")
		},
	)
	defer done()
	u1, u2 := tokenFor(t, "u1"), tokenFor(t, "u2")

	if rr := do(h, "POST", "/api/lpush/"+coll+"?f=q", `{"items":["a"]}`, u1); rr.Code != 200 {
		t.Fatalf("u1 lpush = %d body=%s", rr.Code, rr.Body.String())
	}
	// a live claim blocks another user
	if rr := do(h, "POST", "/api/lpush/"+coll+"?f=q", `{"items":["b"]}`, u2); rr.Code != 403 {
		t.Errorf("u2 pushing to u1's live list = %d want 403", rr.Code)
	}
	// empty it: the key disappears, and so must the claim
	for i := 0; i < 2; i++ {
		do(h, "POST", "/api/rpop/"+coll+"?f=q", "", u1)
	}
	if rr := do(h, "POST", "/api/lpush/"+coll+"?f=q", `{"items":["b"]}`, u2); rr.Code != 200 {
		t.Errorf("u2 claiming the freed key name = %d want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

// User entries and dopdb's bookkeeping share one keyspace, so an entry named
// "__owner" resolved to the collection's isolation index. On KVRocks a SET over
// a hash key converts the type instead of raising WRONGTYPE, so one such write
// destroyed the index and broke every later scoped write in the collection.
func TestIntegrationReservedKeyNamesAreRefused(t *testing.T) {
	coll := "it_reserved"
	h, done := setupKVHandler(t,
		[][2]string{{"STRSET", coll}, {"STRGET", coll}},
		func() {
			dopdb.NewString[string](dopdb.WithCollection(coll)).HttpOn()
			dopdb.SetOwnerScope(coll, "owner", "uid")
		},
	)
	defer done()
	u1, u2 := tokenFor(t, "u1"), tokenFor(t, "u2")

	for _, bad := range []string{"__owner", "__events"} {
		if rr := do(h, "POST", "/api/strset/"+coll+"?f="+bad, `{"v":"evil"}`, u2); rr.Code != 400 {
			t.Errorf("strset %s = %d want 400 (body=%s)", bad, rr.Code, rr.Body.String())
		}
	}
	// the collection is still usable — the isolation index was not corrupted
	if rr := do(h, "POST", "/api/strset/"+coll+"?f=normal", `{"v":"ok"}`, u1); rr.Code != 200 {
		t.Errorf("a normal key after the attack = %d want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if rr := do(h, "GET", "/api/strget/"+coll+"?f=normal", "", u1); rr.Code != 200 {
		t.Errorf("strget after the attack = %d want 200", rr.Code)
	}
}
