package httpserve

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/redis/go-redis/v9"
)

// HttpOn-Go gate behavior (R8 §3 criterion 5). Mirrors the TS e2e gate test
// (ts/test/server.test.ts "httpOn gates data commands without a permit
// function"): the HttpOn(...) bitmask is the SOLE gate, with NO Grant /
// WithPermissions configured. This proves the bitmask stands on its own and
// the legacy Permissions map is not carrying it.
//
// Skips unless DOPDB_TEST_KVROCKS_URI is set, like the conformance tests.

// hpDoc is a minimal non-scoped document for the gate test collections.
type hpDoc struct {
	Text string `json:"text"`
}

func setupHttpOnGate(t *testing.T) (srv *httptest.Server, cl *redis.Client, ns string) {
	t.Helper()
	cl = kvOrSkip(t)
	ns = fmt.Sprintf("dopdb_httpon_%d", time.Now().UnixNano())
	ds := dopdb.NewDatasources()
	ds.Add("default", cl, ns)
	dopdb.SetDatasources(ds)

	// Collection A: read-only via HttpOn. Collection B: everything on (debug
	// default). NEITHER uses Grant / WithPermissions — the empty Permissions
	// below proves HttpOn is the sole gate.
	dopdb.New[string, hpDoc](dopdb.WithCollection("httponA")).HttpOn(dopdb.ReadOnly)
	dopdb.New[string, hpDoc](dopdb.WithCollection("httponB")).HttpOn()

	emptyPerms := NewPermissions() // no grants at all
	srv = httptest.NewServer(NewHandler(NewServer(testSecret), emptyPerms))
	return srv, cl, ns
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
	srv, cl, ns := setupHttpOnGate(t)
	t.Cleanup(func() {
		srv.Close()
		dropNS(cl, ns)
		_ = cl.Close()
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
