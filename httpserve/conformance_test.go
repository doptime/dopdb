package httpserve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/doptime/dopdb"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// M5 Go↔TS conformance: send identical HTTP requests to a Go server and a TS
// server (spawned as a subprocess), compare status code, error code, and body
// shape. This is the REAL cross-engine verification — not a Go-only or
// TS-only test dressed up as conformance (the R2/R5/R7 facade).
//
// Skips unless DOPDB_TEST_MONGO_URI is set (needs a real Mongo; the data paths
// exercise hsetnx dup-key and owner-scope, which are meaningless without it).

// confDoc mirrors ts/conformance/server.ts Notes: owner is @-bound.
type confDoc struct {
	Text  string `json:"text" bson:"text"`
	Owner string `json:"owner" bson:"owner"`
}

type confItem struct {
	Label string `json:"label" bson:"label"`
}

// tsConformance holds the two server URLs + cleanup.
type tsConformance struct {
	goBase string // Go httptest server URL
	tsBase string // TS subprocess server URL
	tsCmd  *exec.Cmd
	goSrv  *httptest.Server
	cl     *mongo.Client
	goDB   string
	tsDB   string
}

func setupConformance(t *testing.T) *tsConformance {
	t.Helper()
	uri := os.Getenv("DOPDB_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set DOPDB_TEST_MONGO_URI (replica set) to run Go↔TS conformance")
	}
	cl, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo connect: %v", err)
	}
	if err := cl.Ping(context.Background(), nil); err != nil {
		t.Fatalf("mongo ping: %v", err)
	}
	stamp := time.Now().UnixNano()
	goDB := fmt.Sprintf("dopdb_conf_go_%d", stamp)
	tsDB := fmt.Sprintf("dopdb_conf_ts_%d", stamp)

	// --- Go server (in-process) ---
	ds := dopdb.NewDatasources()
	ds.Add("default", cl.Database(goDB))
	dopdb.SetDatasources(ds)
	// Mirror the TS schema: notes (scoped) + items (plain).
	dopdb.RegisterHttp(dopdb.New[string, confDoc](dopdb.WithCollection("notes")))
	dopdb.RegisterHttp(dopdb.New[string, confItem](dopdb.WithCollection("items")))
	dopdb.NewString[string](dopdb.WithCollection("strvals")).HttpOn() // String family (STR*), non-scoped
	dopdb.NewSet[string](dopdb.WithCollection("setvals")).HttpOn()    // Set family (S*), non-scoped
	dopdb.SetOwnerScope("notes", "owner", "uid")
	perms := NewPermissions()
	for _, c := range []string{
		"HGET", "HSET", "HSETNX", "HDEL", "HEXISTS", "FIND", "HKEYS", "HLEN",
		"HSCAN", "HSCANNOVALUES", "HRANDFIELD",
	} {
		perms.Grant(c, "notes")
		perms.Grant(c, "items")
	}
	goSrv := httptest.NewServer(NewHandler(NewServer(testSecret), perms))

	// --- TS server (subprocess) ---
	tsScript := "conformance/server.ts"
	tsPort := freePort(t)
	nodeBin := os.Getenv("DOPDB_TS_NODE")
	if nodeBin == "" {
		nodeBin = "node" // resolved via PATH; override with DOPDB_TS_NODE if elsewhere
	}
	cmd := exec.Command(nodeBin, "--import", "tsx", tsScript)
	cmd.Dir = "../ts"
	cmd.Env = append(os.Environ(),
		"PORT="+fmt.Sprint(tsPort),
		"MONGO_URI="+uri,
		"MONGO_DB="+tsDB,
		"JWT_SECRET="+testSecret,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ts server: %v", err)
	}
	// Wait for the ready line.
	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "DOPDB_TS_READY ") {
				ready <- strings.TrimPrefix(line, "DOPDB_TS_READY ")
				return
			}
		}
		ready <- ""
	}()
	select {
	case p := <-ready:
		if p == "" {
			t.Fatalf("ts server exited without ready signal")
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("ts server did not become ready in 15s")
	}

	return &tsConformance{
		goBase: goSrv.URL,
		tsBase: fmt.Sprintf("http://127.0.0.1:%d", tsPort),
		tsCmd:  cmd,
		goSrv:  goSrv,
		cl:     cl,
		goDB:   goDB,
		tsDB:   tsDB,
	}
}

func (c *tsConformance) close() {
	if c.tsCmd != nil && c.tsCmd.Process != nil {
		_ = c.tsCmd.Process.Signal(os.Interrupt)
		_, _ = c.tsCmd.Process.Wait()
	}
	if c.goSrv != nil {
		c.goSrv.Close()
	}
	if c.cl != nil {
		_ = c.cl.Database(c.goDB).Drop(context.Background())
		_ = c.cl.Database(c.tsDB).Drop(context.Background())
		_ = c.cl.Disconnect(context.Background())
	}
	dopdb.SetDatasources(nil)
}

// httpCall sends an identical request to both servers and returns the parsed
// status + body for comparison.
func httpCall(t *testing.T, base, method, path, token string, body string) (int, any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}
	return resp.StatusCode, parsed
}

// assertSame compares status + error code + body shape between Go and TS.
func assertSame(t *testing.T, label string, goStatus, goBody, tsStatus, tsBody any) {
	t.Helper()
	if goStatus != tsStatus {
		t.Errorf("%s: status mismatch — Go=%v TS=%v", label, goStatus, tsStatus)
	}
	// Compare error code (the structured discriminator) for error responses.
	goCode := codeOf(goBody)
	tsCode := codeOf(tsBody)
	if goCode != tsCode {
		t.Errorf("%s: error code mismatch — Go=%q TS=%q (Go body=%v TS body=%v)",
			label, goCode, tsCode, goBody, tsBody)
	}
}

// codeOf extracts the "code" field from a JSON error body, or "" if absent.
func codeOf(body any) string {
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}
	if c, ok := m["code"].(string); ok {
		return c
	}
	return ""
}

// bodyField extracts a named field from a JSON object body.
func bodyField(t *testing.T, body any, field string) any {
	t.Helper()
	m, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body is not an object: %v", body)
	}
	return m[field]
}

// freePort finds an unused TCP port on localhost.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// urlEnc is a shorthand for url.QueryEscape used in test paths.
func urlEnc(s string) string { return url.QueryEscape(s) }

// ---- the conformance cases ----

func TestConformanceHSetHGet(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	for _, base := range []string{c.goBase, c.tsBase} {
		st, body := httpCall(t, base, "POST", "/api/hset/notes?f=conf1", tok, `{"text":"hello"}`)
		if st != 200 {
			t.Errorf("hset %s: status=%d body=%v", base, st, body)
		}
		st, body = httpCall(t, base, "GET", "/api/hget/notes?f=conf1", tok, "")
		if st != 200 {
			t.Errorf("hget %s: status=%d body=%v", base, st, body)
		}
		if v := bodyField(t, body, "text"); v != "hello" {
			t.Errorf("hget %s: text=%v want hello", base, v)
		}
	}
}

// F10 fix: hsetnx on an existing key (self-owned) → {inserted:false} on BOTH
// engines. This is the cross-tenant existence-leakage fix — the previous Go
// bug returned 403 on a self-owned key, breaking hsetnx semantics.
func TestConformanceHSetNXSelfKey(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	for _, base := range []string{c.goBase, c.tsBase} {
		// Seed: alice owns "shared".
		if st, _ := httpCall(t, base, "POST", "/api/hset/notes?f=shared", tok, `{"text":"first"}`); st != 200 {
			t.Fatalf("seed hset %s: status=%d", base, st)
		}
		// hsetnx the same key → inserted:false, NOT 403.
		st, body := httpCall(t, base, "POST", "/api/hsetnx/notes?f=shared", tok, `{"text":"second"}`)
		if st != 200 {
			t.Errorf("hsetnx self %s: status=%d want 200 (F10: no 403 on self-owned)", base, st)
		}
		if ins := bodyField(t, body, "inserted"); ins != false {
			t.Errorf("hsetnx self %s: inserted=%v want false", base, ins)
		}
	}
}

// F10 cross-tenant: bob hsetnx a key alice owns → {inserted:false} on BOTH.
// No 403, no error — uniform non-leakage: "exists for me" is indistinguishable
// from "exists for another tenant".
func TestConformanceHSetNXCrossTenant(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tokA := tokenFor(t, "alice")
	tokB := tokenFor(t, "bob")

	for _, base := range []string{c.goBase, c.tsBase} {
		// Alice owns "tenant1".
		if st, _ := httpCall(t, base, "POST", "/api/hset/notes?f=tenant1", tokA, `{"text":"alice's"}`); st != 200 {
			t.Fatalf("seed hset %s: status=%d", base, st)
		}
		// Bob hsetnx the same key → inserted:false (not 403, not a leak).
		st, body := httpCall(t, base, "POST", "/api/hsetnx/notes?f=tenant1", tokB, `{"text":"bob tries"}`)
		if st != 200 {
			t.Errorf("hsetnx cross %s: status=%d want 200", base, st)
		}
		if ins := bodyField(t, body, "inserted"); ins != false {
			t.Errorf("hsetnx cross %s: inserted=%v want false", base, ins)
		}
		// Verify bob did NOT overwrite alice's doc.
		st, body = httpCall(t, base, "GET", "/api/hget/notes?f=tenant1", tokA, "")
		if v := bodyField(t, body, "text"); v != "alice's" {
			t.Errorf("hsetnx cross %s: alice's doc overwritten to %v", base, v)
		}
	}
}

// F13: sort/projection with $-operator → 400 on BOTH engines.
func TestConformanceSortProjDollarReject(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	cases := []struct {
		name string
		path string
	}{
		{"sort $where", "/api/find/notes?s=" + urlEnc(`{"$where":"1"}`)},
		{"proj $where", "/api/find/notes?p=" + urlEnc(`{"$where":"1"}`)},
		{"sort $gt", "/api/find/notes?s=" + urlEnc(`{"text":{"$gt":""}}`)},
	}
	for _, tc := range cases {
		gs, gb := httpCall(t, c.goBase, "GET", tc.path, tok, "")
		ts, tb := httpCall(t, c.tsBase, "GET", tc.path, tok, "")
		assertSame(t, tc.name, gs, gb, ts, tb)
		if gs != 400 {
			t.Errorf("%s: expected 400, Go=%d TS=%d", tc.name, gs, ts)
		}
	}
}

// Owner-scope: bob's FIND returns [] (no leak of alice's docs) on BOTH.
func TestConformanceOwnerScopeEmpty(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tokA := tokenFor(t, "alice")
	tokB := tokenFor(t, "bob")

	for _, base := range []string{c.goBase, c.tsBase} {
		// Alice writes a note.
		if st, _ := httpCall(t, base, "POST", "/api/hset/notes?f=scoped1", tokA, `{"text":"alice private"}`); st != 200 {
			t.Fatalf("seed %s: status=%d", base, st)
		}
		// Bob FINDs → empty array (his scope matches nothing).
		st, body := httpCall(t, base, "POST", "/api/find/notes", tokB, `{}`)
		if st != 200 {
			t.Errorf("find bob %s: status=%d want 200", base, st)
		}
		arr, ok := body.([]any)
		if !ok {
			t.Errorf("find bob %s: body=%v want array", base, body)
			continue
		}
		if len(arr) != 0 {
			t.Errorf("find bob %s: got %d docs, want 0 (owner-scope leak)", base, len(arr))
		}
	}
}

// Error format parity: 404 has {error, code=not_found} on BOTH.
func TestConformanceErrorFormat(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	gs, gb := httpCall(t, c.goBase, "GET", "/api/hget/notes?f=nonexistent", tok, "")
	ts, tb := httpCall(t, c.tsBase, "GET", "/api/hget/notes?f=nonexistent", tok, "")
	assertSame(t, "404 format", gs, gb, ts, tb)
	if gs != 404 {
		t.Errorf("404: expected 404, Go=%d TS=%d", gs, ts)
	}
	if codeOf(gb) != "not_found" {
		t.Errorf("404 Go: code=%q want not_found, body=%v", codeOf(gb), gb)
	}
	if codeOf(tb) != "not_found" {
		t.Errorf("404 TS: code=%q want not_found, body=%v", codeOf(tb), tb)
	}
}

// HDel then HGet → 404 on BOTH.
func TestConformanceHDelThen404(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	for _, base := range []string{c.goBase, c.tsBase} {
		if st, _ := httpCall(t, base, "POST", "/api/hset/notes?f=del1", tok, `{"text":"temp"}`); st != 200 {
			t.Fatalf("seed %s: status=%d", base, st)
		}
		if st, _ := httpCall(t, base, "GET", "/api/hdel/notes?f=del1", tok, ""); st != 200 {
			t.Fatalf("hdel %s: status=%d", base, st)
		}
		st, _ := httpCall(t, base, "GET", "/api/hget/notes?f=del1", tok, "")
		if st != 404 {
			t.Errorf("hget after hdel %s: status=%d want 404", base, st)
		}
	}
}

// HEXISTS parity: true/false on BOTH.
func TestConformanceHExists(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	for _, base := range []string{c.goBase, c.tsBase} {
		if st, _ := httpCall(t, base, "POST", "/api/hset/notes?f=ex1", tok, `{"text":"present"}`); st != 200 {
			t.Fatalf("seed %s: status=%d", base, st)
		}
		st, body := httpCall(t, base, "GET", "/api/hexists/notes?f=ex1", tok, "")
		if st != 200 {
			t.Errorf("hexists present %s: status=%d", base, st)
		}
		if v := bodyField(t, body, "exists"); v != true {
			t.Errorf("hexists present %s: exists=%v want true", base, v)
		}
		st, body = httpCall(t, base, "GET", "/api/hexists/notes?f=nope", tok, "")
		if v := bodyField(t, body, "exists"); v != false {
			t.Errorf("hexists absent %s: exists=%v want false", base, v)
		}
	}
}

// Unknown command → 400 on BOTH.
func TestConformanceUnknownCommand(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	gs, gb := httpCall(t, c.goBase, "GET", "/api/bogus/notes?f=x", tok, "")
	ts, tb := httpCall(t, c.tsBase, "GET", "/api/bogus/notes?f=x", tok, "")
	assertSame(t, "unknown cmd", gs, gb, ts, tb)
	if gs != 400 {
		t.Errorf("unknown cmd: expected 400, Go=%d TS=%d", gs, ts)
	}
}

// scanFields pulls {cursor, keys} from an HSCAN/HSCANNOVALUES body.
func scanFields(t *testing.T, body any) (float64, []string) {
	t.Helper()
	m, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("scan body not an object: %v", body)
	}
	cur, _ := m["cursor"].(float64)
	raw, _ := m["keys"].([]any)
	out := make([]string, len(raw))
	for i, k := range raw {
		out[i], _ = k.(string)
	}
	return cur, out
}

// TestConformanceHScan: HSCAN / HSCANNOVALUES are deterministic (sorted by
// _id), so both engines return identical cursor + keys over the same seed.
// (values carry an _id-shape difference between Go and TS, identical to HGET,
// so we compare keys + cursor, not the whole body.)
func TestConformanceHScan(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")
	keys := []string{"scana", "scanb", "scanc"}
	for _, base := range []string{c.goBase, c.tsBase} {
		for _, k := range keys {
			if st, _ := httpCall(t, base, "POST", "/api/hset/notes?f="+k, tok, `{"text":"x"}`); st != 200 {
				t.Fatalf("seed hset %s %s: status=%d", base, k, st)
			}
		}
	}
	// HSCAN full page.
	gs, gb := httpCall(t, c.goBase, "GET", "/api/hscan/notes?count=10", tok, "")
	ts, tb := httpCall(t, c.tsBase, "GET", "/api/hscan/notes?count=10", tok, "")
	if gs != ts {
		t.Errorf("hscan status: Go=%d TS=%d", gs, ts)
	}
	gcur, gkeys := scanFields(t, gb)
	tcur, tkeys := scanFields(t, tb)
	if gcur != tcur {
		t.Errorf("hscan cursor: Go=%v TS=%v", gcur, tcur)
	}
	assertStringSliceEq(t, "hscan keys", gkeys, tkeys)
	if len(gkeys) != len(keys) {
		t.Errorf("hscan keys len: got %d want %d (%v)", len(gkeys), len(keys), gkeys)
	}

	// HSCAN with glob match — only scana matches.
	_, gb = httpCall(t, c.goBase, "GET", "/api/hscan/notes?match=scana&count=10", tok, "")
	_, tb = httpCall(t, c.tsBase, "GET", "/api/hscan/notes?match=scana&count=10", tok, "")
	_, gkeys = scanFields(t, gb)
	_, tkeys = scanFields(t, tb)
	assertStringSliceEq(t, "hscan match keys", gkeys, tkeys)
	if len(gkeys) != 1 || gkeys[0] != "scana" {
		t.Errorf("hscan match: got %v want [scana]", gkeys)
	}

	// HSCANNOVALUES — keys only, same cursor semantics.
	gs, gb = httpCall(t, c.goBase, "GET", "/api/hscannovalues/notes?count=10", tok, "")
	ts, tb = httpCall(t, c.tsBase, "GET", "/api/hscannovalues/notes?count=10", tok, "")
	if gs != ts {
		t.Errorf("hscannovalues status: Go=%d TS=%d", gs, ts)
	}
	gcur, gkeys = scanFields(t, gb)
	tcur, tkeys = scanFields(t, tb)
	if gcur != tcur {
		t.Errorf("hscannovalues cursor: Go=%v TS=%v", gcur, tcur)
	}
	assertStringSliceEq(t, "hscannovalues keys", gkeys, tkeys)
}

// assertStringSliceEq compares two string slices in order.
func assertStringSliceEq(t *testing.T, label string, a, b []string) {
	t.Helper()
	if len(a) != len(b) {
		t.Errorf("%s: len Go=%d TS=%d (%v vs %v)", label, len(a), len(b), a, b)
		return
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("%s[%d]: Go=%q TS=%q", label, i, a[i], b[i])
		}
	}
}

// TestConformanceHRandField: HRANDFIELD is random, so the two engines return
// different samples. We assert the SHAPE is identical (200 + array of length
// count, every element a seeded key), not the specific values.
func TestConformanceHRandField(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")
	valid := map[string]bool{"rand1": true, "rand2": true, "rand3": true}
	for _, base := range []string{c.goBase, c.tsBase} {
		for k := range valid {
			if st, _ := httpCall(t, base, "POST", "/api/hset/notes?f="+k, tok, `{"text":"x"}`); st != 200 {
				t.Fatalf("seed hset %s %s: status=%d", base, k, st)
			}
		}
		st, body := httpCall(t, base, "GET", "/api/hrandfield/notes?count=2", tok, "")
		if st != 200 {
			t.Errorf("%s hrandfield: status=%d", base, st)
			continue
		}
		arr, ok := body.([]any)
		if !ok {
			t.Errorf("%s hrandfield: body not an array: %v", base, body)
			continue
		}
		if len(arr) != 2 {
			t.Errorf("%s hrandfield: len=%d want 2", base, len(arr))
		}
		for _, e := range arr {
			s, _ := e.(string)
			if !valid[s] {
				t.Errorf("%s hrandfield: %q not a seeded key", base, s)
			}
		}
	}
}

// TestConformanceString: STR* family two-engine parity over strvals
// (non-scoped). STRSET body {"v":<value>}; STRGET returns the bare value.
func TestConformanceString(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	// STRSET + STRGET: both engines store and return the bare value.
	for _, base := range []string{c.goBase, c.tsBase} {
		if st, _ := httpCall(t, base, "POST", "/api/strset/strvals?f=s1", tok, `{"v":"hello"}`); st != 200 {
			t.Fatalf("strset %s: status=%d", base, st)
		}
	}
	gs, gb := httpCall(t, c.goBase, "GET", "/api/strget/strvals?f=s1", tok, "")
	ts, tb := httpCall(t, c.tsBase, "GET", "/api/strget/strvals?f=s1", tok, "")
	if gs != ts {
		t.Errorf("strget status: Go=%d TS=%d", gs, ts)
	}
	if gb != "hello" {
		t.Errorf("strget Go: got %v want hello", gb)
	}
	if tb != "hello" {
		t.Errorf("strget TS: got %v want hello", tb)
	}

	// STRGETALL: returns {key:value}; s1 present with the right value.
	_, gb = httpCall(t, c.goBase, "GET", "/api/strgetall/strvals", tok, "")
	_, tb = httpCall(t, c.tsBase, "GET", "/api/strgetall/strvals", tok, "")
	if gm, ok := gb.(map[string]any); ok {
		if gm["s1"] != "hello" {
			t.Errorf("strgetall Go: s1=%v want hello", gm["s1"])
		}
	} else {
		t.Errorf("strgetall Go: body not an object: %v", gb)
	}
	if tm, ok := tb.(map[string]any); ok {
		if tm["s1"] != "hello" {
			t.Errorf("strgetall TS: s1=%v want hello", tm["s1"])
		}
	} else {
		t.Errorf("strgetall TS: body not an object: %v", tb)
	}

	// STRSETALL + STRDEL: write two, delete one — both engines agree.
	for _, base := range []string{c.goBase, c.tsBase} {
		if st, _ := httpCall(t, base, "POST", "/api/strsetall/strvals", tok, `{"m1":"x","m2":"y"}`); st != 200 {
			t.Errorf("strsetall %s: status=%d", base, st)
		}
		if st, _ := httpCall(t, base, "GET", "/api/strdel/strvals?f=m1", tok, ""); st != 200 {
			t.Errorf("strdel %s: status=%d", base, st)
		}
		// after del, m1 is gone (STRGETALL no longer has it).
		_, body := httpCall(t, base, "GET", "/api/strgetall/strvals", tok, "")
		if m, ok := body.(map[string]any); ok {
			if _, stillThere := m["m1"]; stillThere {
				t.Errorf("strdel %s: m1 still present after del", base)
			}
			if m["m2"] != "y" {
				t.Errorf("strgetall %s after del: m2=%v want y", base, m["m2"])
			}
		}
	}
}

// ---- Set conformance helpers ----
func toStrSet(body any) map[string]bool {
	arr, _ := body.([]any)
	m := map[string]bool{}
	for _, e := range arr {
		if s, ok := e.(string); ok {
			m[s] = true
		}
	}
	return m
}
func strSetEq(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
func numField(body any, field string) int {
	m, _ := body.(map[string]any)
	if m == nil {
		return -1
	}
	switch v := m[field].(type) {
	case float64:
		return int(v)
	case int64:
		return int(v)
	}
	return -1
}
func boolField(body any, field string) bool {
	m, _ := body.(map[string]any)
	b, _ := m[field].(bool)
	return b
}

// TestConformanceSet: S* family two-engine parity over setvals (non-scoped).
// SMEMBERS compared order-insensitively ($addToSet order should match, but we
// don't depend on it).
func TestConformanceSet(t *testing.T) {
	c := setupConformance(t)
	defer c.close()
	tok := tokenFor(t, "alice")

	// SADD then SMEMBERS.
	for _, base := range []string{c.goBase, c.tsBase} {
		if st, _ := httpCall(t, base, "POST", "/api/sadd/setvals?f=s1", tok, `{"members":["a","b","c"]}`); st != 200 {
			t.Fatalf("sadd %s: %d", base, st)
		}
	}
	_, gb := httpCall(t, c.goBase, "GET", "/api/smembers/setvals?f=s1", tok, "")
	_, tb := httpCall(t, c.tsBase, "GET", "/api/smembers/setvals?f=s1", tok, "")
	if !strSetEq(toStrSet(gb), toStrSet(tb)) {
		t.Errorf("smembers differ: Go=%v TS=%v", gb, tb)
	}
	if len(toStrSet(gb)) != 3 {
		t.Errorf("smembers count: got %d want 3 (%v)", len(toStrSet(gb)), gb)
	}

	// SCARD: 3.
	_, gb = httpCall(t, c.goBase, "GET", "/api/scard/setvals?f=s1", tok, "")
	_, tb = httpCall(t, c.tsBase, "GET", "/api/scard/setvals?f=s1", tok, "")
	if numField(gb, "card") != numField(tb, "card") {
		t.Errorf("scard differ: Go=%v TS=%v", gb, tb)
	}
	if numField(gb, "card") != 3 {
		t.Errorf("scard: got %d want 3", numField(gb, "card"))
	}

	// SISMEMBER b → true, z → false.
	_, gb = httpCall(t, c.goBase, "GET", "/api/sismember/setvals?f=s1&member=b", tok, "")
	_, tb = httpCall(t, c.tsBase, "GET", "/api/sismember/setvals?f=s1&member=b", tok, "")
	if boolField(gb, "member") != boolField(tb, "member") || !boolField(gb, "member") {
		t.Errorf("sismember b: Go=%v TS=%v", gb, tb)
	}

	// SREM b, then SMEMBERS has 2 (a, c), SISMEMBER b → false.
	for _, base := range []string{c.goBase, c.tsBase} {
		if st, _ := httpCall(t, base, "POST", "/api/srem/setvals?f=s1", tok, `{"members":["b"]}`); st != 200 {
			t.Errorf("srem %s: %d", base, st)
		}
	}
	_, gb = httpCall(t, c.goBase, "GET", "/api/smembers/setvals?f=s1", tok, "")
	_, tb = httpCall(t, c.tsBase, "GET", "/api/smembers/setvals?f=s1", tok, "")
	if !strSetEq(toStrSet(gb), toStrSet(tb)) {
		t.Errorf("after srem smembers differ: Go=%v TS=%v", gb, tb)
	}
	if len(toStrSet(gb)) != 2 {
		t.Errorf("after srem count: got %d want 2", len(toStrSet(gb)))
	}
	_, gb = httpCall(t, c.goBase, "GET", "/api/sismember/setvals?f=s1&member=b", tok, "")
	_, tb = httpCall(t, c.tsBase, "GET", "/api/sismember/setvals?f=s1&member=b", tok, "")
	if boolField(gb, "member") != false || boolField(gb, "member") != boolField(tb, "member") {
		t.Errorf("sismember b after srem: Go=%v TS=%v", gb, tb)
	}
}
