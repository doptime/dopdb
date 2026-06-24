package httpserve

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/api"
	"github.com/doptime/dopdb/mongostore"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Run against a disposable MongoDB by setting DOPTIME_TEST_MONGO_URI. Without it
// the test skips (honest terminal B), never a false pass. Each run uses a unique
// database that is dropped on cleanup.
//
// This exercises the full httpserve stack on a real Mongo backend: JWT -> @-binding
// -> permission -> data commands + /api/<name> -> BSON round-trip, and verifies the
// JSON-in / BSON-at-rest / JSON-out field mapping (the codec concern V3 deferred).

// CodecProfile uses identical bson/json field names; timestamps fill by Go field
// name (CreatedAt/UpdatedAt); role defaults on write.
type CodecProfile struct {
	UID       string    `bson:"_id" json:"_id"`
	Name      string    `bson:"name" json:"name" mod:"trim"`
	Role      string    `bson:"role" json:"role" mod:"default=member"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

type NoteDoc struct {
	ID   string `bson:"_id" json:"_id"`
	Text string `bson:"text" json:"text"`
}

// SaveNote: @uid is injected from the JWT through the api pipeline; note from body.
type SaveNote struct {
	UID  string `json:"@uid"`
	Note string `json:"note"`
}
type NoteOut struct {
	Saved string `json:"saved"`
	Owner string `json:"owner"`
}

var httpMongoAPIOnce sync.Once

func registerHTTPMongoAPIs() {
	notes := dopdb.New[string, *NoteDoc](dopdb.WithCollection("Notes"))
	api.Api(func(in *SaveNote) (*NoteOut, error) {
		if err := notes.HSet(in.UID, &NoteDoc{ID: in.UID, Text: in.Note}); err != nil {
			return nil, err
		}
		got, err := notes.HGet(in.UID)
		if err != nil {
			return nil, err
		}
		return &NoteOut{Saved: got.Text, Owner: in.UID}, nil
	}, api.WithName("savenote"))
}

func freshHandlerMongo() *Handler {
	return NewHandler(NewServer(testSecret), NewPermissions(true /* AutoAuth: dev */))
}

func keysSorted(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// craftNoneToken builds a forged "alg":"none" JWT (no signature) that must be rejected.
func craftNoneToken(uid string) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	pl := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"uid":%q,"exp":%d}`, uid, time.Now().Add(time.Hour).Unix())))
	return hdr + "." + pl + "."
}

func TestHTTPMongo(t *testing.T) {
	uri := os.Getenv("DOPTIME_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DOPTIME_TEST_MONGO_URI not set — skipping real-Mongo http e2e (terminal B)")
	}
	ctx := context.Background()
	dbName := "dopdb_http_it_" + time.Now().UTC().Format("20060102T150405")

	st, err := mongostore.New(ctx, uri, dbName)
	if err != nil {
		t.Fatalf("mongostore.New: %v", err)
	}
	dopdb.SetDefaultStore(st)
	dopdb.SetDefaultCodec(mongostore.BSONCodec{})
	dopdb.SetValidator(nil)
	dopdb.RegisterHttp(dopdb.New[string, *Profile](dopdb.WithCollection("Profile")))
	dopdb.RegisterHttp(dopdb.New[string, *Order](dopdb.WithCollection("Order")))
	dopdb.RegisterHttp(dopdb.New[string, *CodecProfile](dopdb.WithCollection("CodecProfile")))
	dopdb.SetOwnerScope("Order", "owner", "uid")
	httpMongoAPIOnce.Do(registerHTTPMongoAPIs)

	// Separate driver client for raw-BSON inspection + teardown (drops the DB).
	cli, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("driver connect: %v", err)
	}
	db := cli.Database(dbName)
	t.Cleanup(func() {
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})

	// 1) JWT: rejection paths -> 401, valid -> 200.
	t.Run("1-jwt", func(t *testing.T) {
		h := freshHandlerMongo()
		if rr := do(h, "GET", "/HGET-Order?f=o1", "", ""); rr.Code != http.StatusUnauthorized {
			t.Errorf("no-token (scoped) status=%d want 401", rr.Code)
		}
		if rr := do(h, "GET", "/HGET-Profile?f=@uid", "", "not.a.token"); rr.Code != http.StatusUnauthorized {
			t.Errorf("malformed-token status=%d want 401", rr.Code)
		}
		if rr := do(h, "GET", "/HGET-Order?f=o1", "", craftNoneToken("u1")); rr.Code != http.StatusUnauthorized {
			t.Errorf("alg:none token status=%d want 401", rr.Code)
		}
		if rr := do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"Jwt"}`, tokenFor(t, "u1")); rr.Code != http.StatusOK {
			t.Errorf("valid-token HSET status=%d want 200 (body=%s)", rr.Code, rr.Body.String())
		}
	})

	// 2) @-binding from JWT + forgery stripped (query and body).
	t.Run("2-at-binding-forgery", func(t *testing.T) {
		h := freshHandlerMongo()
		u1 := tokenFor(t, "u1")
		if rr := do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"  Alice  "}`, u1); rr.Code != http.StatusOK {
			t.Fatalf("HSET status=%d body=%s", rr.Code, rr.Body.String())
		}
		do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"Bob"}`, tokenFor(t, "u2"))
		obj := decodeObj(t, do(h, "GET", "/HGET-Profile?f=@uid", "", u1))
		if obj["_id"] != "u1" {
			t.Errorf("_id=%v want u1 (key bound from JWT)", obj["_id"])
		}
		if obj["name"] != "Alice" {
			t.Errorf("name=%v want Alice (trim on write)", obj["name"])
		}
		// u1 smuggles @uid=u2 in the query -> must still read u1's own.
		obj = decodeObj(t, do(h, "GET", "/HGET-Profile?f=@uid&@uid=u2", "", u1))
		if obj["name"] != "Alice" {
			t.Errorf("query forgery not blocked: name=%v want Alice", obj["name"])
		}
	})

	// 3) Permission whitelist: explicit Deny -> 403; AutoAuth first-use -> allowed.
	t.Run("3-permission", func(t *testing.T) {
		h := freshHandlerMongo()
		h.Perms.Deny("HGETALL", "Profile")
		if rr := do(h, "GET", "/HGETALL-Profile", "", tokenFor(t, "u1")); rr.Code != http.StatusForbidden {
			t.Errorf("denied HGETALL status=%d want 403", rr.Code)
		}
		h2 := freshHandlerMongo()
		do(h2, "POST", "/HSET-Profile?f=@uid", `{"name":"x"}`, tokenFor(t, "u1"))
		if rr := do(h2, "GET", "/HGET-Profile?f=@uid", "", tokenFor(t, "u1")); rr.Code != http.StatusOK {
			t.Errorf("auto-auth HGET status=%d want 200", rr.Code)
		}
	})

	// 4) Data commands @ real Mongo: HSET -> HGET -> HDEL -> HGET(404).
	t.Run("4-data-commands", func(t *testing.T) {
		h := freshHandlerMongo()
		u1 := tokenFor(t, "u1")
		do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"Alice"}`, u1)
		obj := decodeObj(t, do(h, "GET", "/HGET-Profile?f=@uid", "", u1))
		if obj["name"] != "Alice" || obj["_id"] != "u1" {
			t.Errorf("HGET obj=%v want name=Alice _id=u1", obj)
		}
		if rr := do(h, "POST", "/HDEL-Profile?f=@uid", "", u1); rr.Code != http.StatusOK {
			t.Errorf("HDEL status=%d want 200", rr.Code)
		}
		if rr := do(h, "GET", "/HGET-Profile?f=@uid", "", u1); rr.Code != http.StatusNotFound {
			t.Errorf("HGET after HDEL status=%d want 404", rr.Code)
		}
	})

	// 5) /api/<name> @ real Mongo: handler writes+reads a collection; @uid from JWT
	// (forged body @uid stripped); verify the doc actually landed in Mongo.
	t.Run("5-api-at-mongo", func(t *testing.T) {
		h := freshHandlerMongo()
		rr := do(h, "POST", "/api/savenote", `{"note":"hello","@uid":"u2"}`, tokenFor(t, "u1"))
		if rr.Code != http.StatusOK {
			t.Fatalf("api status=%d body=%s", rr.Code, rr.Body.String())
		}
		obj := decodeObj(t, rr)
		if obj["saved"] != "hello" {
			t.Errorf("saved=%v want hello", obj["saved"])
		}
		if obj["owner"] != "u1" {
			t.Errorf("owner=%v want u1 (forged body @uid must be stripped)", obj["owner"])
		}
		raw := bson.M{}
		if err := db.Collection("Notes").FindOne(ctx, bson.M{"_id": "u1"}).Decode(&raw); err != nil {
			t.Fatalf("note not persisted to Mongo: %v", err)
		}
		if raw["text"] != "hello" {
			t.Errorf("persisted text=%v want hello", raw["text"])
		}
	})

	// 6) Codec field mapping (V3 deferred): HTTP JSON <-> BSON-at-rest <-> JSON.
	t.Run("6-codec-mapping", func(t *testing.T) {
		h := freshHandlerMongo()
		u1 := tokenFor(t, "u1")
		want := []string{"_id", "createdAt", "name", "role", "updatedAt"}

		// layer 1 — HTTP JSON in -> HTTP JSON out
		if rr := do(h, "POST", "/HSET-CodecProfile?f=@uid", `{"name":"  Alice  ","role":""}`, u1); rr.Code != http.StatusOK {
			t.Fatalf("HSET status=%d body=%s", rr.Code, rr.Body.String())
		}
		obj := decodeObj(t, do(h, "GET", "/HGET-CodecProfile?f=@uid", "", u1))
		if got := keysSorted(obj); !sameSet(got, want) {
			t.Errorf("HTTP field set=%v want %v", got, want)
		}
		if obj["_id"] != "u1" || obj["name"] != "Alice" || obj["role"] != "member" {
			t.Errorf("HTTP values: _id=%v name=%v role=%v (want u1/Alice/member)", obj["_id"], obj["name"], obj["role"])
		}
		if s, _ := obj["createdAt"].(string); s == "" {
			t.Errorf("HTTP createdAt empty")
		}
		if s, _ := obj["updatedAt"].(string); s == "" {
			t.Errorf("HTTP updatedAt empty")
		}

		// layer 2 — raw BSON at rest
		raw := bson.M{}
		if err := db.Collection("CodecProfile").FindOne(ctx, bson.M{"_id": "u1"}).Decode(&raw); err != nil {
			t.Fatalf("raw find: %v", err)
		}
		if got := keysSorted(map[string]any(raw)); !sameSet(got, want) {
			t.Errorf("BSON field set=%v want %v", got, want) // layer 3: same set as HTTP => aligned
		}
		if id, ok := raw["_id"].(string); !ok || id != "u1" {
			t.Errorf("BSON _id=%v (%T) want string \"u1\"", raw["_id"], raw["_id"])
		}
		if raw["role"] != "member" {
			t.Errorf("BSON role=%v want member (default persisted through BSON)", raw["role"])
		}
		if _, isStr := raw["createdAt"].(string); isStr {
			t.Errorf("BSON createdAt stored as string, want a Date")
		}
		if raw["createdAt"] == nil {
			t.Errorf("BSON createdAt missing")
		}
	})

	t.Logf("INTEGRATION OK against %s/%s", uri, dbName)
}
