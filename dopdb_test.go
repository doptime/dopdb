package dopdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Integration tests run against a real MongoDB (the Store abstraction was
// removed, so there is no in-memory backend to test against). They self-skip
// unless DOPDB_TEST_MONGO_URI is set. Each test uses a throwaway database that is
// dropped on cleanup.

type itUser struct {
	Name  string `json:"name" bson:"name"`
	Email string `json:"email" bson:"email" index:"unique"`
	Age   int    `json:"age" bson:"age" index:"1"`
}

func withTestDS(t *testing.T) func() {
	t.Helper()
	uri := os.Getenv("DOPDB_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set DOPDB_TEST_MONGO_URI to run integration tests")
	}
	cl, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := cl.Ping(context.Background(), nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
	dbName := fmt.Sprintf("dopdb_it_%d", time.Now().UnixNano())
	ds := NewDatasources()
	ds.Add("default", cl.Database(dbName))
	SetDatasources(ds)
	return func() {
		_ = cl.Database(dbName).Drop(context.Background())
		_ = cl.Disconnect(context.Background())
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
	if ins, _ := users.HSetNX("u1", &itUser{Name: "Other"}); ins {
		t.Error("HSetNX on existing key should not insert")
	}
	if ins, _ := users.HSetNX("u2", &itUser{Name: "Bob", Email: "bob@x.io", Age: 25}); !ins {
		t.Error("HSetNX on new key should insert")
	}

	if n, _ := users.HLen(); n != 2 {
		t.Errorf("HLen=%d want 2", n)
	}
	keys, _ := users.HKeys()
	if len(keys) != 2 {
		t.Errorf("HKeys=%v", keys)
	}

	if err := users.HDel("u1"); err != nil {
		t.Fatalf("HDel: %v", err)
	}
	if _, err := users.HGet("u1"); err != ErrNoDoc {
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

	// HIncrBy is a true atomic $inc on a numeric field.
	if err := users.HIncrBy("k0", "age", 5); err != nil {
		t.Fatalf("HIncrBy: %v", err)
	}
	g, _ := users.HGet("k0")
	if g.Age != 25 { // 20 + 5
		t.Errorf("age after HIncrBy=%d want 25", g.Age)
	}
}
