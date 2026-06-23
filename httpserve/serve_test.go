package httpserve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/memstore"
)

// Profile is the "self" pattern: keyed by uid, accessed via ?f=@uid. Not scoped
// because the key itself is the identity.
type Profile struct {
	UID  string `bson:"_id" json:"_id"`
	Name string `bson:"name" json:"name" mod:"trim"`
}

// Order is the "owned collection" pattern: keyed by orderId, every document
// carries owner; collection-wide and per-key access is scoped to owner==uid.
type Order struct {
	OrderID string `bson:"_id" json:"_id"`
	Owner   string `bson:"owner" json:"owner"`
	Item    string `bson:"item" json:"item"`
}

const testSecret = "test-secret-key"

func setupHandler() *Handler {
	dopdb.SetDefaultStore(memstore.New())
	dopdb.SetDefaultCodec(memstore.JSONCodec{})
	dopdb.SetValidator(nil)
	dopdb.RegisterHttp(dopdb.New[string, *Profile](dopdb.WithCollection("Profile")))
	dopdb.RegisterHttp(dopdb.New[string, *Order](dopdb.WithCollection("Order")))
	dopdb.SetOwnerScope("Order", "owner", "uid") // row isolation
	return NewHandler(NewServer(testSecret), NewPermissions(true /* AutoAuth: dev */))
}

func tokenFor(t *testing.T, uid string) string {
	t.Helper()
	tok, err := SignHS256(map[string]any{
		"uid": uid,
		"exp": time.Now().Add(time.Hour).Unix(),
	}, testSecret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func do(h *Handler, method, target, body, tok string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

func decodeObj(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode obj: %v (body=%s)", err, rr.Body.String())
	}
	return m
}

func decodeArr(t *testing.T, rr *httptest.ResponseRecorder) []any {
	t.Helper()
	var a []any
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode arr: %v (body=%s)", err, rr.Body.String())
	}
	return a
}

// 1. @-binding: HSET then HGET using ?f=@uid resolves the key from the JWT, and
// the write-time trim modifier runs.
func TestAtBindingRoundTrip(t *testing.T) {
	h := setupHandler()
	tok := tokenFor(t, "u1")

	rr := do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"  Alice  "}`, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("HSET status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = do(h, "GET", "/HGET-Profile?f=@uid", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("HGET status=%d body=%s", rr.Code, rr.Body.String())
	}
	obj := decodeObj(t, rr)
	if obj["name"] != "Alice" { // trimmed on write
		t.Errorf("name=%v want Alice", obj["name"])
	}
	if obj["_id"] != "u1" { // key bound from JWT
		t.Errorf("_id=%v want u1", obj["_id"])
	}
}

// 2. A forged @-param in the query cannot override the JWT-derived binding.
func TestForgedAtParamStripped(t *testing.T) {
	h := setupHandler()
	do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"Alice"}`, tokenFor(t, "u1"))
	do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"Bob"}`, tokenFor(t, "u2"))

	// u1 tries to read u2's profile by smuggling @uid=u2 in the query.
	rr := do(h, "GET", "/HGET-Profile?f=@uid&@uid=u2", "", tokenFor(t, "u1"))
	obj := decodeObj(t, rr)
	if obj["name"] != "Alice" {
		t.Errorf("forgery not blocked: got %v want Alice (u1's own)", obj["name"])
	}
}

// 3. The permission whitelist gates commands; an explicit Deny beats AutoAuth.
func TestPermissionDeny(t *testing.T) {
	h := setupHandler()
	h.Perms.Deny("HGETALL", "Profile")
	rr := do(h, "GET", "/HGETALL-Profile", "", tokenFor(t, "u1"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rr.Code)
	}
}

// 4. Row-level isolation on a scoped collection: a user cannot read or overwrite
// another user's document even knowing its id, and FIND returns only their rows.
func TestRowIsolation(t *testing.T) {
	h := setupHandler()
	u1, u2 := tokenFor(t, "u1"), tokenFor(t, "u2")

	// u1 creates order o1; owner is forced server-side.
	if rr := do(h, "POST", "/HSET-Order?f=o1", `{"item":"book"}`, u1); rr.Code != http.StatusOK {
		t.Fatalf("u1 HSET o1 status=%d body=%s", rr.Code, rr.Body.String())
	}

	// u2 cannot read o1.
	if rr := do(h, "GET", "/HGET-Order?f=o1", "", u2); rr.Code != http.StatusNotFound {
		t.Errorf("u2 read o1 status=%d want 404", rr.Code)
	}
	// u1 can read o1.
	rr := do(h, "GET", "/HGET-Order?f=o1", "", u1)
	if rr.Code != http.StatusOK {
		t.Fatalf("u1 read o1 status=%d body=%s", rr.Code, rr.Body.String())
	}
	obj := decodeObj(t, rr)
	if obj["item"] != "book" || obj["owner"] != "u1" {
		t.Errorf("o1=%v want item=book owner=u1", obj)
	}

	// u2 cannot overwrite u1's o1.
	if rr := do(h, "POST", "/HSET-Order?f=o1", `{"item":"hijacked"}`, u2); rr.Code != http.StatusForbidden {
		t.Errorf("u2 overwrite o1 status=%d want 403", rr.Code)
	}
	// confirm o1 is intact.
	rr = do(h, "GET", "/HGET-Order?f=o1", "", u1)
	if decodeObj(t, rr)["item"] != "book" {
		t.Error("o1 was hijacked")
	}

	// u1 adds o2; FIND returns only the caller's rows.
	do(h, "POST", "/HSET-Order?f=o2", `{"item":"pen"}`, u1)
	do(h, "POST", "/HSET-Order?f=o3", `{"item":"other"}`, u2)

	if got := len(decodeArr(t, do(h, "GET", "/FIND-Order", "", u1))); got != 2 {
		t.Errorf("u1 FIND returned %d rows, want 2", got)
	}
	if got := len(decodeArr(t, do(h, "GET", "/FIND-Order", "", u2))); got != 1 {
		t.Errorf("u2 FIND returned %d rows, want 1", got)
	}
}

// 5. A scoped collection denies access to an unauthenticated caller.
func TestScopedRequiresAuth(t *testing.T) {
	h := setupHandler()
	rr := do(h, "GET", "/HGET-Order?f=o1", "", "") // no token
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rr.Code)
	}
}

// 6. The filter sanitizer rejects code-execution operators over HTTP.
func TestFindSanitizeOverHTTP(t *testing.T) {
	h := setupHandler()
	target := "/FIND-Profile?" + url.Values{"q": {`{"$where":"while(true){}"}`}}.Encode()
	rr := do(h, "GET", target, "", tokenFor(t, "u1"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

// 7. Commands outside the closed vocabulary are rejected.
func TestUnknownCommand(t *testing.T) {
	h := setupHandler()
	rr := do(h, "GET", "/FOO-Profile", "", tokenFor(t, "u1"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rr.Code)
	}
}

// 8. An invalid JWT signature is rejected.
func TestBadJWT(t *testing.T) {
	h := setupHandler()
	rr := do(h, "GET", "/HGET-Profile?f=@uid", "", "not.a.token")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 (body=%s)", rr.Code, rr.Body.String())
	}
}
