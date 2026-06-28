package httpserve

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/doptime/dopdb"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// HttpOn-Go gate behavior (R8 §3 criterion 5). Mirrors the TS e2e gate test
// (ts/test/server.test.ts "httpOn gates data commands without a permit
// function"): the HttpOn(...) bitmask is the SOLE gate, with NO Grant /
// WithPermissions configured. This proves the bitmask stands on its own and
// the legacy Permissions map is not carrying it.
//
// Skips unless DOPDB_TEST_MONGO_URI is set, like the conformance tests.

// hpDoc is a minimal non-scoped document for the gate test collections.
type hpDoc struct {
	Text string `json:"text" bson:"text"`
}

func setupHttpOnGate(t *testing.T) (srv *httptest.Server, cl *mongo.Client, db string) {
	t.Helper()
	uri := os.Getenv("DOPDB_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set DOPDB_TEST_MONGO_URI (replica set) to run the HttpOn gate test")
	}
	var err error
	cl, err = mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo connect: %v", err)
	}
	if err := cl.Ping(context.Background(), nil); err != nil {
		t.Fatalf("mongo ping: %v", err)
	}
	db = "dopdb_httpon_" + t.Name()
	ds := dopdb.NewDatasources()
	ds.Add("default", cl.Database(db))
	dopdb.SetDatasources(ds)

	// Collection A: read-only via HttpOn. Collection B: everything on (debug
	// default). NEITHER uses Grant / WithPermissions — the empty Permissions
	// below proves HttpOn is the sole gate.
	dopdb.New[string, hpDoc](dopdb.WithCollection("httponA")).HttpOn(dopdb.ReadOnly)
	dopdb.New[string, hpDoc](dopdb.WithCollection("httponB")).HttpOn()

	emptyPerms := NewPermissions() // no grants at all
	srv = httptest.NewServer(NewHandler(NewServer(testSecret), emptyPerms))
	return srv, cl, db
}

func hpReq(t *testing.T, base, method, path, body string) int {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestHttpOnGate(t *testing.T) {
	srv, cl, db := setupHttpOnGate(t)
	t.Cleanup(func() {
		srv.Close()
		_ = cl.Database(db).Drop(context.Background())
		_ = cl.Disconnect(context.Background())
		dopdb.SetDatasources(nil)
	})
	base := srv.URL

	// Collection A (HttpOn(ReadOnly)): HSET must be forbidden by the bitmask
	// gate; HGET must NOT be forbidden (the key is missing, so 404 — the point
	// is it passes the permission gate, which is what ReadOnly is about).
	if st := hpReq(t, base, "POST", "/api/hset/httponA?f=k1", `{"text":"x"}`); st != http.StatusForbidden {
		t.Errorf("httponA HSET: got %d, want 403 (HttpOn(ReadOnly) forbids writes; no Grant configured)", st)
	}
	if st := hpReq(t, base, "GET", "/api/hget/httponA?f=missing", ""); st == http.StatusForbidden {
		t.Errorf("httponA HGET: got 403, want non-403 (read is allowed by ReadOnly; got %d)", st)
	}

	// Collection B (HttpOn() = all): HSET must succeed — the bitmask alone
	// authorizes the write, with still no Grant configured.
	if st := hpReq(t, base, "POST", "/api/hset/httponB?f=k1", `{"text":"x"}`); st != http.StatusOK {
		t.Errorf("httponB HSET: got %d, want 200 (HttpOn() = all on; no Grant configured)", st)
	}
}
