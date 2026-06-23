package dopdb_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/memstore"
)

type User struct {
	UID       string    `bson:"_id" json:"_id" index:"unique"`
	Name      string    `bson:"name" json:"name" mod:"trim" validate:"required"`
	Email     string    `bson:"email" json:"email" mod:"trim,lowercase" index:"1"`
	Role      string    `bson:"role" json:"role" mod:"default=member"`
	Token     string    `bson:"token" json:"token" mod:"nanoid"`
	Logins    int64     `bson:"logins" json:"logins" mod:"counter"`
	Age       int       `bson:"age" json:"age"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

func setup(t *testing.T) {
	t.Helper()
	dopdb.SetDefaultStore(memstore.New())
	dopdb.SetDefaultCodec(memstore.JSONCodec{})
	dopdb.SetValidator(func(v any) error {
		u, ok := v.(*User)
		if ok && strings.TrimSpace(u.Name) == "" {
			return errors.New("name required")
		}
		return nil
	})
}

func TestSetGetRoundTrip(t *testing.T) {
	setup(t)
	users := dopdb.New[string, *User](dopdb.WithCollection("users"))
	if users == nil {
		t.Fatal("New returned nil")
	}
	if err := users.HSet("u1", &User{UID: "u1", Name: " Alice ", Email: " ALICE@X.COM "}); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	got, err := users.HGet("u1")
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("trim failed: %q", got.Name)
	}
	if got.Email != "alice@x.com" {
		t.Errorf("trim,lowercase failed: %q", got.Email)
	}
	if got.Role != "member" {
		t.Errorf("default failed: %q", got.Role)
	}
	if len(got.Token) != 21 {
		t.Errorf("nanoid failed: %q (len %d)", got.Token, len(got.Token))
	}
	if got.Logins != 1 {
		t.Errorf("counter failed: %d", got.Logins)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps not set on write: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

func TestValidationOnWrite(t *testing.T) {
	setup(t)
	users := dopdb.New[string, *User](dopdb.WithCollection("users"))
	if err := users.HSet("bad", &User{UID: "bad", Name: "   "}); err == nil {
		t.Fatal("expected validation error on write (empty name)")
	}
}

func TestSaveDerivesKey(t *testing.T) {
	setup(t)
	users := dopdb.New[string, *User](dopdb.WithCollection("users"))
	if err := users.Save(&User{UID: "u7", Name: "Bob"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := users.HGet("u7")
	if err != nil {
		t.Fatalf("HGet after Save: %v", err)
	}
	if got.Name != "Bob" {
		t.Errorf("got %q", got.Name)
	}
}

func TestNotFound(t *testing.T) {
	setup(t)
	users := dopdb.New[string, *User](dopdb.WithCollection("users"))
	if _, err := users.HGet("nope"); !errors.Is(err, dopdb.ErrNoDoc) {
		t.Fatalf("expected ErrNoDoc, got %v", err)
	}
}

func TestSetNX(t *testing.T) {
	setup(t)
	users := dopdb.New[string, *User](dopdb.WithCollection("users"))
	ins, err := users.HSetNX("u1", &User{UID: "u1", Name: "First"})
	if err != nil || !ins {
		t.Fatalf("first SetNX should insert: ins=%v err=%v", ins, err)
	}
	ins, err = users.HSetNX("u1", &User{UID: "u1", Name: "Second"})
	if err != nil || ins {
		t.Fatalf("second SetNX should not insert: ins=%v err=%v", ins, err)
	}
	got, _ := users.HGet("u1")
	if got.Name != "First" {
		t.Errorf("value overwritten: %q", got.Name)
	}
}

func TestBatchAndEnumerate(t *testing.T) {
	setup(t)
	users := dopdb.New[string, *User](dopdb.WithCollection("users"))
	_ = users.HMSet(map[string]*User{
		"a": {UID: "a", Name: "A"},
		"b": {UID: "b", Name: "B"},
		"c": {UID: "c", Name: "C"},
	})
	if n, _ := users.HLen(); n != 3 {
		t.Fatalf("HLen=%d", n)
	}
	keys, _ := users.HKeys()
	if len(keys) != 3 {
		t.Fatalf("HKeys=%v", keys)
	}
	vals, err := users.HMGet("a", "zzz", "c")
	if err != nil {
		t.Fatalf("HMGet: %v", err)
	}
	if vals[0] == nil || vals[0].Name != "A" {
		t.Errorf("HMGet[0]=%v", vals[0])
	}
	if vals[1] != nil {
		t.Errorf("HMGet missing should be nil, got %v", vals[1])
	}
	if ok, _ := users.HExists("b"); !ok {
		t.Error("HExists(b) false")
	}
	_ = users.HDel("b")
	if ok, _ := users.HExists("b"); ok {
		t.Error("HDel didn't remove b")
	}
}

func TestIncrAtomicField(t *testing.T) {
	setup(t)
	type Counter struct {
		ID string `bson:"_id" json:"_id"`
		N  int64  `bson:"n" json:"n"`
	}
	c := dopdb.New[string, *Counter](dopdb.WithCollection("counters"))
	for i := 0; i < 5; i++ {
		if err := c.HIncrBy("hits", "n", 1); err != nil {
			t.Fatalf("HIncrBy: %v", err)
		}
	}
	got, err := c.HGet("hits")
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if got.N != 5 {
		t.Errorf("counter=%d want 5", got.N)
	}
}

func TestFindAndSanitize(t *testing.T) {
	setup(t)
	users := dopdb.New[string, *User](dopdb.WithCollection("users"))
	_ = users.HSet("a", &User{UID: "a", Name: "Ann", Age: 30})
	_ = users.HSet("b", &User{UID: "b", Name: "Bo", Age: 17})
	_ = users.HSet("c", &User{UID: "c", Name: "Cy", Age: 41})

	adults, err := users.Find(dopdb.M{"age": dopdb.M{"$gte": 18}}, dopdb.FindOpt{})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(adults) != 2 {
		t.Fatalf("expected 2 adults, got %d", len(adults))
	}
	if _, err = users.Find(dopdb.M{"$where": "while(true){}"}, dopdb.FindOpt{}); err == nil {
		t.Fatal("sanitizer let $where through")
	}
	if _, err = users.Find(dopdb.M{"x": dopdb.M{"$function": "..."}}, dopdb.FindOpt{}); err == nil {
		t.Fatal("sanitizer let $function through")
	}
}

func TestIntKey(t *testing.T) {
	setup(t)
	// _id is always a canonical string in dopdb, so a non-string id is kept as
	// an ordinary field; the int key still round-trips via HKeys.
	type Item struct {
		ID   int    `bson:"itemId" json:"itemId"`
		Name string `bson:"name" json:"name"`
	}
	items := dopdb.New[int, *Item](dopdb.WithCollection("items"))
	_ = items.HSet(42, &Item{ID: 42, Name: "answer"})
	got, err := items.HGet(42)
	if err != nil {
		t.Fatalf("HGet int key: %v", err)
	}
	if got.Name != "answer" {
		t.Errorf("got %q", got.Name)
	}
	keys, _ := items.HKeys()
	if len(keys) != 1 || keys[0] != 42 {
		t.Errorf("HKeys=%v", keys)
	}
}

func TestStructValue(t *testing.T) {
	setup(t)
	type Note struct {
		ID   string `bson:"_id" json:"_id"`
		Text string `bson:"text" json:"text" mod:"trim"`
	}
	notes := dopdb.New[string, Note](dopdb.WithCollection("notes"))
	_ = notes.HSet("n1", Note{ID: "n1", Text: "  hi  "})
	got, err := notes.HGet("n1")
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if got.Text != "hi" {
		t.Errorf("trim on struct value failed: %q", got.Text)
	}
}
