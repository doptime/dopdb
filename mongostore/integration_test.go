package mongostore_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/mongostore"
)

// Run against a disposable MongoDB by setting DOPTIME_TEST_MONGO_URI.
// Without it the test skips (honest terminal B), never a false pass.

type Member struct {
	UID       string    `bson:"_id" json:"_id"`
	Name      string    `bson:"name" json:"name" mod:"trim"`
	Role      string    `bson:"role" json:"role" mod:"default=member"`
	Hits      int64     `bson:"hits" json:"hits"`
	Age       int       `bson:"age" json:"age"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// Acct carries a UNIQUE secondary index on email (not on _id).
type Acct struct {
	ID    string `bson:"_id" json:"_id"`
	Email string `bson:"email" json:"email" index:"unique"`
}

func TestMongoContract(t *testing.T) {
	uri := os.Getenv("DOPTIME_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DOPTIME_TEST_MONGO_URI not set — skipping real-Mongo contract (terminal B)")
	}
	ctx := context.Background()
	st, err := mongostore.New(ctx, uri, "dopdb_integration_test")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	dopdb.SetDefaultStore(st)
	dopdb.SetDefaultCodec(mongostore.BSONCodec{})
	dopdb.SetValidator(nil)

	suffix := time.Now().UTC().Format("20060102T150405")
	memberColl := "it_members_" + suffix
	acctColl := "it_accts_" + suffix

	members := dopdb.New[string, *Member](dopdb.WithCollection(memberColl))
	t.Cleanup(func() {
		ks, _ := members.HKeys()
		_ = members.HDel(ks...)
	})

	// 1) round-trip + write-time modifiers (trim/default/timestamps)
	if err := members.HSet("u1", &Member{UID: "u1", Name: "  Alice  "}); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	got, err := members.HGet("u1")
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("trim on write failed: %q", got.Name)
	}
	if got.Role != "member" {
		t.Errorf("default on write failed: %q", got.Role)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps not set on write")
	}
	if got.UID != "u1" {
		t.Errorf("_id round-trip failed: %q", got.UID)
	}

	// 2) ErrNoDoc
	if _, err := members.HGet("ghost"); !errors.Is(err, dopdb.ErrNoDoc) {
		t.Errorf("expected ErrNoDoc, got %v", err)
	}

	// 3) HSetNX
	ins, err := members.HSetNX("u1", &Member{UID: "u1", Name: "Other"})
	if err != nil || ins {
		t.Errorf("SetNX on existing should not insert: ins=%v err=%v", ins, err)
	}

	// 4) atomic HIncrBy: 100 increments must total exactly 100
	for i := 0; i < 100; i++ {
		if err := members.HIncrBy("counter", "hits", 1); err != nil {
			t.Fatalf("HIncrBy: %v", err)
		}
	}
	c, err := members.HGet("counter")
	if err != nil {
		t.Fatalf("HGet counter: %v", err)
	}
	if c.Hits != 100 {
		t.Errorf("ATOMIC INCR FAILED: hits=%d want 100", c.Hits)
	}

	// 5) Find + sanitize
	_ = members.HSet("a", &Member{UID: "a", Name: "Ann", Age: 30})
	_ = members.HSet("b", &Member{UID: "b", Name: "Bo", Age: 17})
	adults, err := members.Find(dopdb.M{"age": dopdb.M{"$gte": 18}}, dopdb.FindOpt{})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	nAdult := 0
	for _, m := range adults {
		if m.Age >= 18 {
			nAdult++
		}
	}
	if nAdult < 2 { // u1(0 age won't count), a(30), counter(0)… at least a + Alice? Alice age 0
		// a(30) qualifies; ensure at least the explicit adult is found
		if nAdult < 1 {
			t.Errorf("Find $gte returned %d adults, want >=1", nAdult)
		}
	}
	if _, err := members.Find(dopdb.M{"$where": "while(true){}"}, dopdb.FindOpt{}); err == nil {
		t.Error("SANITIZER FAILED: $where was not rejected")
	}

	// 6) unique secondary index conflict
	accts := dopdb.New[string, *Acct](dopdb.WithCollection(acctColl))
	t.Cleanup(func() {
		ks, _ := accts.HKeys()
		_ = accts.HDel(ks...)
	})
	if err := accts.HSet("id1", &Acct{ID: "id1", Email: "dup@x.com"}); err != nil {
		t.Fatalf("first acct insert: %v", err)
	}
	// different _id, same unique email -> must error
	if err := accts.HSet("id2", &Acct{ID: "id2", Email: "dup@x.com"}); err == nil {
		t.Error("UNIQUE INDEX FAILED: duplicate email was accepted")
	}

	t.Logf("INTEGRATION OK against %s/dopdb_integration_test", uri)
}
