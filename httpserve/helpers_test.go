package httpserve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Shared test helpers for the httpserve package. API-dispatch tests do not touch
// MongoDB (the echo endpoint is pure), so no datasource wiring is needed here.
// Data-command integration tests live separately and gate on a real MongoDB.

const testSecret = "test-secret-0123456789"

// setupHandler builds a Handler with an HS256 server and a default-deny
// permission set, granting the API endpoints the dispatch tests exercise.
func setupHandler() *Handler {
	s := NewServer(testSecret)
	p := NewPermissions()
	p.Grant("API", "echo")  // a real, registered endpoint
	p.Grant("API", "ghost") // granted but NOT registered -> exercises the 404 path
	return NewHandler(s, p)
}

// tokenFor mints an HS256 bearer token carrying uid.
func tokenFor(t *testing.T, uid string) string {
	t.Helper()
	tok, err := SignHS256(map[string]any{"uid": uid}, testSecret)
	if err != nil {
		t.Fatalf("SignHS256: %v", err)
	}
	return tok
}

// do issues a request against h and returns the recorded response.
func do(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
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

// decodeObj decodes a JSON object response body.
func decodeObj(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rr.Body.String())
	}
	return m
}
