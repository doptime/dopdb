package httpserve

import (
	"net/http"
	"testing"

	"github.com/doptime/dopdb/api"
)

// EchoIn mixes a body field (msg) with an @-context field (@uid) filled from the
// verified JWT — the same non-forgeable binding the data layer uses.
type EchoIn struct {
	UID string `json:"@uid"`
	Msg string `json:"msg"`
}

// Registered once at package init (api.Api panics on duplicate names).
var _ = api.Api(func(in *EchoIn) (map[string]any, error) {
	return map[string]any{"uid": in.UID, "msg": in.Msg}, nil
}, api.WithName("echo"))

func TestAPIDispatch(t *testing.T) {
	h := setupHandler()
	rr := do(h, "POST", "/api/echo", `{"msg":"hi"}`, tokenFor(t, "u1"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	obj := decodeObj(t, rr)
	if obj["uid"] != "u1" || obj["msg"] != "hi" {
		t.Errorf("echo=%v want uid=u1 msg=hi", obj)
	}
}

func TestAPIPermissionDeny(t *testing.T) {
	h := setupHandler()
	h.Perms.Deny("API", "echo")
	rr := do(h, "POST", "/api/echo", `{"msg":"hi"}`, tokenFor(t, "u1"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rr.Code)
	}
}

func TestAPINotFound(t *testing.T) {
	h := setupHandler()
	rr := do(h, "GET", "/api/ghost", "", tokenFor(t, "u1"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}
