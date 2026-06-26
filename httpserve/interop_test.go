package httpserve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/doptime/dopdb"
)

type interopUser struct {
	Name  string `json:"name" bson:"name"`
	Email string `json:"email" bson:"email"`
}

func setupInteropTest(t *testing.T) (*Handler, func()) {
	t.Helper()

	uri := os.Getenv("DOPDB_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set DOPDB_TEST_MONGO_URI to run interop tests")
	}

	users := dopdb.New[string, interopUser](dopdb.WithCollection("interop_users"))
	dopdb.RegisterHttp(users)

	perms := NewPermissions()
	perms.Grant("HGET", "interop_users")
	perms.Grant("HSET", "interop_users")
	perms.Grant("HSETNX", "interop_users")
	perms.Grant("HDEL", "interop_users")
	perms.Grant("FIND", "interop_users")
	perms.Grant("HEXISTS", "interop_users")
	perms.Grant("HKEYS", "interop_users")
	perms.Grant("HLEN", "interop_users")
	perms.Grant("HMGET", "interop_users")
	perms.Grant("HMSET", "interop_users")

	srv := NewServer(testSecret)
	h := NewHandler(srv, perms)

	ctx := context.Background()
	ds, err := dopdb.ConnectDatasources(ctx, []dopdb.DatasourceConfig{
		{Name: "default", URI: uri, DB: "dopdb_interop_" + t.Name()},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	dopdb.SetDatasources(ds)

	return h, func() {
		dopdb.SetDatasources(nil)
		_ = ds
	}
}

func jsonBody(v any) *strings.Reader {
	b, _ := json.Marshal(v)
	return strings.NewReader(string(b))
}

func requestJSON(t *testing.T, h *Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, jsonBody(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

func respBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rr.Body.String())
	}
	return m
}
func respAny(t *testing.T, rr *httptest.ResponseRecorder) any {
	t.Helper()
	var m any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rr.Body.String())
	}
	return m
}

func TestInteropHSetHGet(t *testing.T) {
	h, cleanup := setupInteropTest(t)
	defer cleanup()

	tok := tokenFor(t, "alice")

	// HSET
	rr := requestJSON(t, h, "POST", "/api/hset/interop_users?f=u1", tok,
		map[string]any{"name": "Alice", "email": "alice@example.com"})
	if rr.Code != 200 {
		t.Fatalf("HSET status=%d", rr.Code)
	}

	// HGET
	rr = requestJSON(t, h, "GET", "/api/hget/interop_users?f=u1", tok, nil)
	body := respBody(t, rr)
	if rr.Code != 200 {
		t.Fatalf("HGET status=%d body=%v", rr.Code, body)
	}
	if body["name"] != "Alice" {
		t.Fatalf("HGET name=%v want Alice", body["name"])
	}
}

func TestInteropHSetNX(t *testing.T) {
	h, cleanup := setupInteropTest(t)
	defer cleanup()

	tok := tokenFor(t, "alice")

	// HSETNX on absent key (use test-name-unique key)
	key1 := "nx_" + fmt.Sprint(time.Now().UnixNano())
	rr := requestJSON(t, h, "POST", "/api/hsetnx/interop_users?f="+key1, tok,
		map[string]any{"name": "Bob"})
	body := respBody(t, rr)
	if rr.Code != 200 {
		t.Fatalf("HSETNX status=%d", rr.Code)
	}
	if body["inserted"] != true {
		t.Fatalf("HSETNX inserted=%v want true", body["inserted"])
	}

	// HSETNX on existing key
	rr = requestJSON(t, h, "POST", "/api/hsetnx/interop_users?f="+key1, tok,
		map[string]any{"name": "Bob2"})
	body = respBody(t, rr)
	if body["inserted"] != false {
		t.Fatalf("HSETNX on existing: inserted=%v want false", body["inserted"])
	}
}

func TestInteropHDel(t *testing.T) {
	h, cleanup := setupInteropTest(t)
	defer cleanup()

	tok := tokenFor(t, "alice")

	// HSET first
	requestJSON(t, h, "POST", "/api/hset/interop_users?f=u3", tok,
		map[string]any{"name": "Charlie"})

	// HDEL
	rr := requestJSON(t, h, "POST", "/api/hdel/interop_users?f=u3", tok, nil)
	if rr.Code != 200 {
		t.Fatalf("HDEL status=%d", rr.Code)
	}

	// Verify deleted
	rr = requestJSON(t, h, "GET", "/api/hget/interop_users?f=u3", tok, nil)
	if rr.Code != 404 {
		t.Fatalf("HGET after HDEL status=%d want 404", rr.Code)
	}
}

func TestInteropFind(t *testing.T) {
	h, cleanup := setupInteropTest(t)
	defer cleanup()

	tok := tokenFor(t, "alice")

	// Insert two users
	requestJSON(t, h, "POST", "/api/hset/interop_users?f=f1", tok,
		map[string]any{"name": "Alice"})
	requestJSON(t, h, "POST", "/api/hset/interop_users?f=f2", tok,
		map[string]any{"name": "Bob"})

	// FIND with filter
	rr := requestJSON(t, h, "POST", "/api/find/interop_users", tok,
		map[string]any{"name": "Alice"})
	if rr.Code != 200 {
		t.Fatalf("FIND status=%d", rr.Code)
	}
	raw := respAny(t, rr)
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("FIND body not array: %v", raw)
	}
	if len(arr) != 1 {
		t.Fatalf("FIND count=%d want 1", len(arr))
	}

}
func TestInteropHExists(t *testing.T) {
	h, cleanup := setupInteropTest(t)
	defer cleanup()

	tok := tokenFor(t, "alice")

	// HSET first
	requestJSON(t, h, "POST", "/api/hset/interop_users?f=e1", tok,
		map[string]any{"name": "Eve"})

	// HEXISTS on existing
	rr := requestJSON(t, h, "GET", "/api/hexists/interop_users?f=e1", tok, nil)
	body := respBody(t, rr)
	if rr.Code != 200 {
		t.Fatalf("HEXISTS status=%d", rr.Code)
	}
	if body["exists"] != true {
		t.Fatalf("HEXISTS exists=%v want true", body["exists"])
	}

	// HEXISTS on absent
	rr = requestJSON(t, h, "GET", "/api/hexists/interop_users?f=NOPE", tok, nil)
	body = respBody(t, rr)
	if body["exists"] != false {
		t.Fatalf("HEXISTS on absent: exists=%v want false", body["exists"])
	}
}

func TestInteropErrorFormat(t *testing.T) {
	h, cleanup := setupInteropTest(t)
	defer cleanup()

	tok := tokenFor(t, "alice")

	// 404 case
	rr := requestJSON(t, h, "GET", "/api/hget/interop_users?f=MISSING", tok, nil)
	body := respBody(t, rr)
	if rr.Code != 404 {
		t.Fatalf("status=%d want 404", rr.Code)
	}
	if body["code"] != "not_found" {
		t.Fatalf("code=%v want not_found", body["code"])
	}
	if body["error"] == nil {
		t.Fatal("error field missing")
	}
}
func TestInteropNoKey(t *testing.T) {
	h, cleanup := setupInteropTest(t)
	defer cleanup()

	// interop_users is NOT owner-scoped, so no JWT is needed.
	// Requesting a non-existent key should return 404, not 401.
	rr := requestJSON(t, h, "GET", "/api/hget/interop_users?f=MISSING", "", nil)
	body := respBody(t, rr)
	if rr.Code != 404 {
		t.Fatalf("status=%d want 404", rr.Code)
	}
	if body["code"] != "not_found" {
		t.Fatalf("code=%v want not_found", body["code"])
	}
}
