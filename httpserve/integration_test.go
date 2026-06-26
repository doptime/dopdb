package httpserve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/doptime/dopdb"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// HTTP-layer integration tests against a real MongoDB. They self-skip unless
// DOPDB_TEST_MONGO_URI is set; each uses a throwaway database dropped on cleanup.
// They reuse the Mongo-free helpers (do/tokenFor/decodeObj/testSecret).

type itDoc struct {
	Owner string `json:"owner" bson:"owner"`
	Note  string `json:"note" bson:"note"`
}

func mongoOrSkip(t *testing.T) *mongo.Client {
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
	return cl
}

func decodeArr(t *testing.T, rr *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var a []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode array: %v (body=%s)", err, rr.Body.String())
	}
	return a
}

// setupMongoHandler wires a single "default" datasource to a throwaway db, runs
// register (which should dopdb.RegisterHttp the test collections), grants the
// given (cmd, coll) pairs, and returns a Handler + cleanup.
func setupMongoHandler(t *testing.T, grants [][2]string, register func()) (*Handler, func()) {
	t.Helper()
	cl := mongoOrSkip(t)
	dbName := fmt.Sprintf("dopdb_it_%d", time.Now().UnixNano())
	ds := dopdb.NewDatasources()
	ds.Add("default", cl.Database(dbName))
	dopdb.SetDatasources(ds)
	register()
	p := NewPermissions()
	for _, g := range grants {
		p.Grant(g[0], g[1])
	}
	h := NewHandler(NewServer(testSecret), p)
	return h, func() {
		_ = cl.Database(dbName).Drop(context.Background())
		_ = cl.Disconnect(context.Background())
		dopdb.SetDatasources(nil)
	}
}

func TestIntegrationHTTPRoundTrip(t *testing.T) {
	coll := "it_notes"
	h, done := setupMongoHandler(t,
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

	// FIND returns an array.
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
	h, done := setupMongoHandler(t,
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
}

func TestIntegrationMultiDatasource(t *testing.T) {
	cl := mongoOrSkip(t)
	dbA := fmt.Sprintf("dopdb_it_a_%d", time.Now().UnixNano())
	dbB := fmt.Sprintf("dopdb_it_b_%d", time.Now().UnixNano())
	ds := dopdb.NewDatasources()
	ds.Add("default", cl.Database(dbA))
	ds.Add("other", cl.Database(dbB))
	dopdb.SetDatasources(ds)
	defer func() {
		_ = cl.Database(dbA).Drop(context.Background())
		_ = cl.Database(dbB).Drop(context.Background())
		_ = cl.Disconnect(context.Background())
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
