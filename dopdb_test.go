package dopdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Integration tests run against a real KVRocks (or any Redis-protocol server —
// the command set dopdb uses is the common one). They self-skip unless
// DOPDB_TEST_KVROCKS_URI is set. Each test gets a throwaway key namespace that
// is deleted on cleanup, so a shared server is safe.

type itUser struct {
	Name  string `json:"name"`
	Email string `json:"email" index:"unique"`
	Age   int    `json:"age" index:"1"`
}

// testNamespace returns a namespace nobody else is using.
func testNamespace(tag string) string {
	return fmt.Sprintf("dopdb_it_%s_%d", tag, time.Now().UnixNano())
}

// connectOrSkip dials the test server, skipping the test when none is configured.
func connectOrSkip(t *testing.T) *redis.Client {
	t.Helper()
	uri := os.Getenv("DOPDB_TEST_KVROCKS_URI")
	if uri == "" {
		t.Skip("set DOPDB_TEST_KVROCKS_URI (e.g. redis://localhost:6666) to run integration tests")
	}
	opt, err := redis.ParseURL(uri)
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	applyPoolDefaults(opt)
	cl := redis.NewClient(opt)
	if err := cl.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return cl
}

// dropNamespace removes every key dopdb wrote under ns.
func dropNamespace(cl *redis.Client, ns string) {
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

func withTestDS(t *testing.T) func() {
	t.Helper()
	cl := connectOrSkip(t)
	ns := testNamespace("go")
	ds := NewDatasources()
	ds.Add("default", cl, ns)
	SetDatasources(ds)
	return func() {
		dropNamespace(cl, ns)
		_ = cl.Close()
		SetDatasources(nil)
	}
}

func TestIntegrationCRUD(t *testing.T) {
	defer withTestDS(t)()
	users := New[string, *itUser](WithCollection("it_users"))

	if err := users.HSet("u1", &itUser{Name: "Ada", Email: "ada@x.io", Age: 30}); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	got, err := users.HGet("u1")
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if got.Name != "Ada" || got.Age != 30 {
		t.Errorf("got=%+v", got)
	}

	if ok, _ := users.HExists("u1"); !ok {
		t.Error("HExists u1 should be true")
	}
	if ok, _ := users.HExists("missing"); ok {
		t.Error("HExists missing should be false")
	}

	// HSetNX: first wins, second is a no-op.
	if ins, _ := users.HSetNX("u1", &itUser{Name: "Other", Email: "other@x.io"}); ins {
		t.Error("HSetNX on existing key should not insert")
	}
	if ins, err := users.HSetNX("u2", &itUser{Name: "Bob", Email: "bob@x.io", Age: 25}); !ins {
		t.Errorf("HSetNX on new key should insert (err=%v)", err)
	}

	if n, _ := users.HLen(); n != 2 {
		t.Errorf("HLen=%d want 2", n)
	}
	keys, _ := users.HKeys()
	if len(keys) != 2 {
		t.Errorf("HKeys=%v", keys)
	}
	vals, _ := users.HVals()
	if len(vals) != 2 {
		t.Errorf("HVals len=%d want 2", len(vals))
	}
	all, _ := users.HGetAll()
	if len(all) != 2 || all["u1"].Name != "Ada" {
		t.Errorf("HGetAll=%v", all)
	}

	if err := users.HDel("u1"); err != nil {
		t.Fatalf("HDel: %v", err)
	}
	if _, err := users.HGet("u1"); !errors.Is(err, ErrNoDoc) {
		t.Errorf("HGet after delete err=%v want ErrNoDoc", err)
	}
}

func TestIntegrationMGetMSet(t *testing.T) {
	defer withTestDS(t)()
	users := New[string, *itUser](WithCollection("it_users_m"))

	if err := users.HMSet(map[string]*itUser{
		"a": {Name: "A", Email: "a@x.io"},
		"b": {Name: "B", Email: "b@x.io"},
	}); err != nil {
		t.Fatalf("HMSet: %v", err)
	}
	got, err := users.HMGet("a", "missing", "b")
	if err != nil {
		t.Fatalf("HMGet: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("HMGet len=%d want 3", len(got))
	}
	if got[0] == nil || got[0].Name != "A" {
		t.Errorf("got[0]=%+v", got[0])
	}
	if got[1] != nil {
		t.Errorf("got[1] should be nil (missing), got %+v", got[1])
	}
	if got[2] == nil || got[2].Name != "B" {
		t.Errorf("got[2]=%+v", got[2])
	}
}

func TestIntegrationFindAndAtomicIncr(t *testing.T) {
	defer withTestDS(t)()
	users := New[string, *itUser](WithCollection("it_users_f"))

	for i, u := range []*itUser{
		{Name: "x", Email: "x@x.io", Age: 20},
		{Name: "y", Email: "y@x.io", Age: 40},
		{Name: "z", Email: "z@x.io", Age: 40},
	} {
		if err := users.HSet(fmt.Sprintf("k%d", i), u); err != nil {
			t.Fatalf("HSet: %v", err)
		}
	}

	out, err := users.Find(M{"age": 40}, FindOpt{SortKeys: []SortKey{{Field: "name", Asc: true}}})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(out) != 2 || out[0].Name != "y" || out[1].Name != "z" {
		t.Errorf("Find age=40 => %+v", out)
	}

	// FindOne returns the first match; a filter with no match is ErrNoDoc.
	one, err := users.FindOne(M{"name": "x"})
	if err != nil || one.Age != 20 {
		t.Errorf("FindOne=%+v err=%v", one, err)
	}
	if _, err := users.FindOne(M{"name": "nobody"}); !errors.Is(err, ErrNoDoc) {
		t.Errorf("FindOne miss err=%v want ErrNoDoc", err)
	}

	// Richer operators go through the same in-process evaluator.
	adults, err := users.Find(M{"age": M{"$gte": 40}}, FindOpt{})
	if err != nil || len(adults) != 2 {
		t.Errorf("Find $gte => %d docs, err=%v", len(adults), err)
	}

	// HIncrBy is a WATCH-guarded atomic increment on a field inside the document.
	if err := users.HIncrBy("k0", "age", 5); err != nil {
		t.Fatalf("HIncrBy: %v", err)
	}
	g, _ := users.HGet("k0")
	if g.Age != 25 { // 20 + 5
		t.Errorf("age after HIncrBy=%d want 25", g.Age)
	}
	if err := users.HIncrByFloat("k0", "age", 2); err != nil {
		t.Fatalf("HIncrByFloat: %v", err)
	}
	if g, _ = users.HGet("k0"); g.Age != 27 {
		t.Errorf("age after HIncrByFloat=%d want 27", g.Age)
	}
}

// Concurrent increments must all land: this is the property the Mongo build got
// from $inc and the KVRocks build gets from WATCH/MULTI.
func TestIntegrationIncrIsAtomic(t *testing.T) {
	defer withTestDS(t)()
	counters := New[string, *itUser](WithCollection("it_counter"))

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = counters.HIncrBy("c1", "age", 1)
		}()
	}
	wg.Wait()

	got, err := counters.HGet("c1")
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if got.Age != n {
		t.Errorf("concurrent HIncrBy = %d want %d (increments were lost)", got.Age, n)
	}
}

// index:"unique" has no server-side enforcement on KVRocks, so dopdb enforces it.
func TestIntegrationUniqueIndex(t *testing.T) {
	defer withTestDS(t)()
	users := New[string, *itUser](WithCollection("it_unique"))

	if err := users.HSet("u1", &itUser{Name: "Ada", Email: "dup@x.io"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// a different document claiming the same unique value is rejected
	err := users.HSet("u2", &itUser{Name: "Bob", Email: "dup@x.io"})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate email err=%v want ErrDuplicate", err)
	}
	// re-writing the SAME document with the same value is fine
	if err := users.HSet("u1", &itUser{Name: "Ada2", Email: "dup@x.io"}); err != nil {
		t.Errorf("rewrite of the same doc: %v", err)
	}
	// once the holder releases the value, another document may take it
	if err := users.HSet("u1", &itUser{Name: "Ada2", Email: "moved@x.io"}); err != nil {
		t.Fatalf("change email: %v", err)
	}
	if err := users.HSet("u2", &itUser{Name: "Bob", Email: "dup@x.io"}); err != nil {
		t.Errorf("value should have been released: %v", err)
	}
	// deleting a document releases its claims too
	if err := users.HDel("u2"); err != nil {
		t.Fatal(err)
	}
	if err := users.HSet("u3", &itUser{Name: "Cid", Email: "dup@x.io"}); err != nil {
		t.Errorf("claim after delete: %v", err)
	}
}

// A struct declaring `json:"_id"` gets the document key filled in on read. The
// Mongo build forced _id into every stored document; on KVRocks the id lives in
// the hash field instead, so nothing would populate that struct field unless the
// decode path does it. Regression found by the post-migration audit.
func TestIntegrationIDFieldIsFilledOnRead(t *testing.T) {
	defer withTestDS(t)()
	type idDoc struct {
		ID   string `json:"_id"`
		Text string `json:"text"`
	}
	c := New[string, *idDoc](WithCollection("it_idfield"))
	if err := c.HSet("k1", &idDoc{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	got, err := c.HGet("k1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "k1" {
		t.Errorf("HGet _id = %q want k1", got.ID)
	}

	// every read path that knows the key must fill it, not just HGet
	all, _ := c.HGetAll()
	if all["k1"].ID != "k1" {
		t.Errorf("HGetAll _id = %q want k1", all["k1"].ID)
	}
	vals, _ := c.HVals()
	if len(vals) != 1 || vals[0].ID != "k1" {
		t.Errorf("HVals _id = %+v", vals)
	}
	many, _ := c.HMGet("k1")
	if len(many) != 1 || many[0].ID != "k1" {
		t.Errorf("HMGet _id = %+v", many)
	}
	found, _ := c.Find(M{"text": "hello"}, FindOpt{})
	if len(found) != 1 || found[0].ID != "k1" {
		t.Errorf("Find _id = %+v", found)
	}
	_, fvals, _, _ := c.HScan(0, "*", 10)
	if len(fvals) != 1 || fvals[0].ID != "k1" {
		t.Errorf("HScan _id = %+v", fvals)
	}
}

func TestIntegrationScanAndRandField(t *testing.T) {
	defer withTestDS(t)()
	users := New[string, *itUser](WithCollection("it_scan"))
	for i := 0; i < 5; i++ {
		if err := users.HSet(fmt.Sprintf("k%d", i), &itUser{Name: fmt.Sprintf("n%d", i), Email: fmt.Sprintf("e%d@x.io", i)}); err != nil {
			t.Fatal(err)
		}
	}

	// walk the whole collection, honouring the cursor protocol
	seen := map[string]bool{}
	var cursor uint64
	for i := 0; i < 20; i++ {
		keys, vals, next, err := users.HScan(cursor, "*", 2)
		if err != nil {
			t.Fatalf("HScan: %v", err)
		}
		if len(keys) != len(vals) {
			t.Fatalf("HScan returned %d keys but %d values", len(keys), len(vals))
		}
		for _, k := range keys {
			seen[k] = true
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(seen) != 5 {
		t.Errorf("HScan saw %d keys want 5 (%v)", len(seen), seen)
	}

	// glob match is applied by the server
	keys, _, err := users.HScanNoValues(0, "k1", 10)
	if err != nil {
		t.Fatalf("HScanNoValues: %v", err)
	}
	_ = keys

	rnd, err := users.HRandField(3)
	if err != nil {
		t.Fatalf("HRandField: %v", err)
	}
	if len(rnd) != 3 {
		t.Errorf("HRandField returned %d want 3", len(rnd))
	}
}

func TestIntegrationScopedWrite(t *testing.T) {
	defer withTestDS(t)()
	type owned struct {
		Owner string `json:"owner"`
		Note  string `json:"note"`
	}
	notes := New[string, *owned](WithCollection("it_scoped"))

	if err := notes.HSetScoped("n1", &owned{Owner: "alice", Note: "hi"}, "owner", "alice"); err != nil {
		t.Fatalf("alice write: %v", err)
	}
	// bob cannot overwrite alice's row even knowing its id
	err := notes.HSetScoped("n1", &owned{Owner: "bob", Note: "stolen"}, "owner", "bob")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("bob overwriting alice err=%v want ErrForbidden", err)
	}
	got, _ := notes.HGet("n1")
	if got.Note != "hi" {
		t.Errorf("row was modified: %+v", got)
	}
}

// ---- watch integration tests -------------------------------------------------
//
// Mongo change streams are gone; watch now consumes dopdb's own publication
// channel. The observable contract is unchanged: writes are delivered, and a
// scoped watcher never sees deletes (they carry no document to scope on).

func TestIntegrationWatchInsertUpdate(t *testing.T) {
	defer withTestDS(t)()
	users := New[string, itUser](WithCollection("users_it_watch"))

	b, ok := defaultDatasources.get("")
	if !ok {
		t.Fatal("no backend")
	}

	var mu sync.Mutex
	var ops []string
	done := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = b.watch(ctx, "users_it_watch", nil, func(op, id string, raw []byte) error {
			mu.Lock()
			ops = append(ops, op)
			mu.Unlock()
			return nil
		})
		close(done)
	}()

	// let the subscription establish before writing
	time.Sleep(300 * time.Millisecond)

	_ = users.HSet("k1", itUser{Name: "Alice", Email: "alice@x.io", Age: 30})
	_ = users.HSet("k1", itUser{Name: "Alice", Email: "alice@x.io", Age: 31})

	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	t.Logf("watch events: %v", ops)
	if len(ops) < 2 {
		t.Fatalf("expected at least 2 events, got %d: %v", len(ops), ops)
	}
	if ops[0] != "insert" {
		t.Errorf("first event = %q want insert", ops[0])
	}
	if ops[1] != "replace" {
		t.Errorf("second event = %q want replace", ops[1])
	}
}

func TestIntegrationWatchScopedDelete(t *testing.T) {
	defer withTestDS(t)()
	users := New[string, itUser](WithCollection("users_it_watch_del"))

	b, ok := defaultDatasources.get("")
	if !ok {
		t.Fatal("no backend")
	}

	var mu sync.Mutex
	var ops []string
	done := make(chan struct{})

	scope := M{"name": "Alice"}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = b.watch(ctx, "users_it_watch_del", scope, func(op, id string, raw []byte) error {
			mu.Lock()
			ops = append(ops, op)
			mu.Unlock()
			return nil
		})
		close(done)
	}()

	time.Sleep(300 * time.Millisecond)
	_ = users.HSet("k1", itUser{Name: "Alice", Email: "alice@x.io", Age: 30})
	_ = users.Del("k1")
	time.Sleep(500 * time.Millisecond)

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	t.Logf("scoped watch events: %v", ops)

	foundInsert, foundDelete := false, false
	for _, op := range ops {
		if op == "insert" || op == "replace" {
			foundInsert = true
		}
		if op == "delete" {
			foundDelete = true
		}
	}
	if !foundInsert {
		t.Fatal("expected to see insert event through scoped watch")
	}
	if foundDelete {
		t.Fatal("scoped watch should NOT deliver delete events (no document to scope on)")
	}
}

// The namespace is what keeps two datasources apart on one server, so it needs
// a test of its own: the same collection name in two namespaces is two hashes.
func TestIntegrationNamespaceIsolation(t *testing.T) {
	cl := connectOrSkip(t)
	nsA, nsB := testNamespace("a"), testNamespace("b")
	ds := NewDatasources()
	ds.Add("default", cl, nsA)
	ds.Add("other", cl, nsB)
	SetDatasources(ds)
	defer func() {
		dropNamespace(cl, nsA)
		dropNamespace(cl, nsB)
		_ = cl.Close()
		SetDatasources(nil)
	}()

	inA := New[string, *itUser](WithCollection("shared"))
	inB := New[string, *itUser](WithDB("other"), WithCollection("shared"))

	if err := inA.HSet("k", &itUser{Name: "in-a", Email: "a@x.io"}); err != nil {
		t.Fatal(err)
	}
	if err := inB.HSet("k", &itUser{Name: "in-b", Email: "b@x.io"}); err != nil {
		t.Fatal(err)
	}
	a, _ := inA.HGet("k")
	b, _ := inB.HGet("k")
	if a.Name != "in-a" || b.Name != "in-b" {
		t.Errorf("namespaces leaked into each other: a=%q b=%q", a.Name, b.Name)
	}
}
