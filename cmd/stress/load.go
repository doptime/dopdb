package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doptime/dopdb/httpserve"
)

type clientFlags struct {
	base        string
	secret      string
	users       int
	docs        int
	concurrency int
	time        string
	requests    int64
	scenario    string
	out         string
}

var cli clientFlags

// ---- report types (the JSON document) ----

type Report struct {
	Host        string              `json:"host"`
	Scenario    string              `json:"scenario"`
	Users       int                 `json:"users"`
	DocsPerUser int                 `json:"docs_per_user"`
	Concurrency int                 `json:"concurrency"`
	DurationMs  float64             `json:"duration_ms"`
	StartedAt   string              `json:"started_at"`
	Total       ReportTotal         `json:"total"`
	PerOp       map[string]OpReport `json:"per_op"`
}

type ReportTotal struct {
	Requests  int     `json:"requests"`
	Errors    int     `json:"errors"`
	OpsPerSec float64 `json:"ops_per_sec"`
}

type OpReport struct {
	Requests  int           `json:"requests"`
	Errors5xx int           `json:"errors_5xx"`
	NetErrors int           `json:"net_errors"`
	Status    map[int]int   `json:"status"`
	OpPerSec  float64       `json:"ops_per_sec"`
	Latency   LatencyReport `json:"latency"`
}

type LatencyReport struct {
	MinUs  float64 `json:"min_us"`
	AvgUs  float64 `json:"avg_us"`
	P50Us  float64 `json:"p50_us"`
	P90Us  float64 `json:"p90_us"`
	P95Us  float64 `json:"p95_us"`
	P99Us  float64 `json:"p99_us"`
	P999Us float64 `json:"p999_us"`
	MaxUs  float64 `json:"max_us"`
}

// ---- raw per-operation accumulator ----

type perOp struct {
	count  int
	err5xx int
	netErr int
	status map[int]int
	durs   []time.Duration
}

// runLoad is the client: mint one HS256 token per user, probe the server,
// run the requested scenario(s) for the configured wall time, print the report.
//
// Every request is authenticated through the real verification path (the
// server verifies the JWT and serves owner-scoped data for the caller's
// claims); tokens come from httpserve.SignHS256, the same signer the login
// endpoint would use.
func runLoad(args []string) int {
	fs := loadFlags()
	_ = fs.Parse(args)

	dur, err := time.ParseDuration(cli.time)
	if err != nil {
		log.Fatalf("bad --time: %v", err)
	}
	if cli.users < 1 || cli.docs < 1 {
		log.Fatal("--users and --docs-per-user must be >= 1")
	}

	tokens := make([]string, cli.users)
	for u := 0; u < cli.users; u++ {
		tok, err := httpserve.SignHS256(map[string]any{"uid": fmt.Sprintf("user%04d", u)}, cli.secret)
		if err != nil {
			log.Fatalf("mint token for user %d: %v", u, err)
		}
		tokens[u] = tok
	}
	if err := probe(cli.base, tokens[0]); err != nil {
		log.Fatalf("server at %s unreachable: %v", cli.base, err)
	}

	scen := strings.ToLower(cli.scenario)
	// Scenarios are independent: each runs for its own --time window, so the
	// report's per-op numbers are not contaminated by other scenarios' state
	// churn (e.g. CRUD deletes racing a counter's incr).
	all := []struct {
		name string
		fn   func(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration)
	}{
		{"crud", runCRUD},
		{"find", runFind},
		{"fanout", runFanout},
		{"list", runList},
		{"zset", runZSet},
		{"set", runSet},
		{"str", runStr},
		{"incr", runIncr},
	}
	var runners []struct {
		name string
		fn   func(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration)
	}
	for _, s := range all {
		if scen == s.name || scen == "mix" {
			runners = append(runners, s)
		}
	}
	if len(runners) == 0 {
		log.Fatalf("unknown scenario %q (crud|find|fanout|list|zset|set|str|incr|mix)", scen)
	}

	raw := map[string]*perOp{}
	totalDur := time.Duration(0)
	for _, r := range runners {
		name := scen
		if len(runners) > 1 {
			name = scen + "/" + r.name
		}
		log.Printf("scenario %-12s: %d workers x %s", name, cli.concurrency, dur)
		ops, d := r.fn(tokens, dur)
		totalDur += d
		for k, p := range ops {
			if _, ok := raw[k]; !ok {
				raw[k] = &perOp{status: map[int]int{}}
			}
			agg := raw[k]
			agg.count += p.count
			agg.err5xx += p.err5xx
			agg.netErr += p.netErr
			agg.durs = append(agg.durs, p.durs...)
			for st, n := range p.status {
				agg.status[st] += n
			}
		}
	}

	rep := &Report{
		Host:        cli.base,
		Scenario:    scen,
		Users:       cli.users,
		DocsPerUser: cli.docs,
		Concurrency: cli.concurrency,
		DurationMs:  float64(totalDur) / float64(time.Millisecond),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		PerOp:       map[string]OpReport{},
	}
	for name, p := range raw {
		or := OpReport{
			Requests:  p.count,
			Errors5xx: p.err5xx,
			NetErrors: p.netErr,
			Status:    p.status,
			OpPerSec:  float64(p.count) / totalDur.Seconds(),
			Latency:   percentile(sortedCopy(p.durs)),
		}
		rep.PerOp[name] = or
		rep.Total.Requests += p.count
		rep.Total.Errors += p.err5xx + p.netErr
	}
	if totalDur > 0 {
		rep.Total.OpsPerSec = float64(rep.Total.Requests) / totalDur.Seconds()
	}

	body, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		log.Fatalf("encode report: %v", err)
	}
	if cli.out != "" {
		if err := os.WriteFile(cli.out, body, 0o644); err != nil {
			log.Fatalf("write %s: %v", cli.out, err)
		}
		log.Printf("report -> %s", cli.out)
	} else {
		fmt.Println(string(body))
	}
	printSummary(rep)
	return 0
}

// probe confirms the server answers an authenticated request before the run.
// ZCARD on the unscoped board needs no owner claim, so a 200 proves the
// handler + JWT + KVRocks path end to end.
func probe(base, token string) error {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/api/zcard/board?f=s", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := newHTTPClient(8).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("probe got HTTP %d", resp.StatusCode)
	}
	return nil
}

func newHTTPClient(maxIdlePerHost int) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        maxIdlePerHost * 2,
		MaxIdleConnsPerHost: maxIdlePerHost * 2,
		IdleConnTimeout:     30 * time.Second,
		ForceAttemptHTTP2:   false,
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

// drive runs `work` on `concurrency` workers until the deadline (or the
// total-request ceiling) and returns per-operation results plus wall time.
// The scenario's collection is fixed, so the URL is /api/<op>/<coll> per the
// wire protocol (AGENTS.md section 2).
func drive(coll string, dur time.Duration, totalCap int64,
	work func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string)) (map[string]*perOp, time.Duration) {

	client := newHTTPClient(cli.concurrency)
	base := strings.TrimRight(cli.base, "/")

	ops := make(map[string]*perOp)
	var mu sync.Mutex
	deadline := time.Now().Add(dur)
	start := time.Now()
	var done int64

	var wg sync.WaitGroup
	for w := 0; w < cli.concurrency; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(w)))
			for time.Now().Before(deadline) {
				if totalCap > 0 && atomic.LoadInt64(&done) >= totalCap {
					return
				}
				u := rng.Intn(len(cliTokens))
				opName, body, query := work(w, rng, client, u, cliTokens[u])
				method := http.MethodPost
				if isRead(opName) {
					method = http.MethodGet
				}
				req, err := http.NewRequest(method, base+"/api/"+opName+"/"+coll, nil)
				if err != nil {
					atomic.AddInt64(&done, 1)
					return
				}
				req.Header.Set("Authorization", "Bearer "+cliTokens[u])
				if body != nil {
					req.Body = io.NopCloser(body)
					req.Header.Set("Content-Type", "application/json")
				}
				if query != "" {
					req.URL.RawQuery = query
				}
				t0 := time.Now()
				resp, err := client.Do(req)
				d := time.Since(t0)
				var status int
				if err == nil {
					status = resp.StatusCode
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				mu.Lock()
				p := ops[opName]
				if p == nil {
					p = &perOp{status: map[int]int{}}
					ops[opName] = p
				}
				p.count++
				p.durs = append(p.durs, d)
				switch {
				case err != nil:
					p.netErr++
				case status >= 500:
					p.err5xx++
					p.status[status]++
				default:
					p.status[status]++
				}
				mu.Unlock()
				atomic.AddInt64(&done, 1)
			}
		}(w)
	}
	wg.Wait()
	return ops, time.Since(start)
}

// cliTokens is set by each scenario before drive runs.
var cliTokens []string

func isRead(op string) bool {
	switch op {
	case "hget", "hexists", "hgetall", "hkeys", "hvals", "hlen", "find", "findone",
		"count", "hrandfield", "hscan", "hscannovalues", "hmget",
		"strget", "strgetall", "lrange", "llen", "lindex", "smembers", "sismember",
		"scard", "zscore", "zcard", "zcount", "zrange", "zrevrange",
		"zrangebyscore", "zrevrangebyscore", "zrank", "zrevrank", "sql":
		return true
	}
	return false
}

func percentile(sorted []time.Duration) LatencyReport {
	if len(sorted) == 0 {
		return LatencyReport{}
	}
	get := func(p float64) float64 {
		if len(sorted) == 1 {
			return float64(sorted[0]) / float64(time.Microsecond)
		}
		idx := int(p * float64(len(sorted)-1))
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return float64(sorted[idx]) / float64(time.Microsecond)
	}
	var sum float64
	for _, x := range sorted {
		sum += float64(x)
	}
	return LatencyReport{
		MinUs:  float64(sorted[0]) / float64(time.Microsecond),
		AvgUs:  sum / float64(len(sorted)),
		P50Us:  get(0.50),
		P90Us:  get(0.90),
		P95Us:  get(0.95),
		P99Us:  get(0.99),
		P999Us: get(0.999),
		MaxUs:  float64(sorted[len(sorted)-1]) / float64(time.Microsecond),
	}
}

func sortedCopy(d []time.Duration) []time.Duration {
	c := make([]time.Duration, len(d))
	copy(c, d)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c
}

func printSummary(rep *Report) {
	fmt.Printf("\n== %s: %d req in %.1fs = %.0f ops/s, %d errors ==\n",
		rep.Scenario, rep.Total.Requests, rep.DurationMs/1000.0, rep.Total.OpsPerSec, rep.Total.Errors)
	type row struct {
		name string
		ops  float64
		p50  float64
		p99  float64
		errs int
		stat map[int]int
	}
	var rows []row
	for name, o := range rep.PerOp {
		errs := o.Errors5xx + o.NetErrors
		rows = append(rows, row{name, o.OpPerSec, o.Latency.P50Us, o.Latency.P99Us, errs, o.Status})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ops > rows[j].ops })
	fmt.Printf("%-12s %10s %10s %10s %6s  %s\n", "op", "ops/s", "p50(us)", "p99(us)", "errs", "status")
	for _, r := range rows {
		fmt.Printf("%-12s %10.0f %10.0f %10.0f %6d  %v\n", r.name, r.ops, r.p50, r.p99, r.errs, r.stat)
	}
}

// ---------- scenarios ----------
//
// Each returns (opName, jsonBody-or-nil, rawQuery). drive() picks GET for
// reads and POST for writes, and authenticates as the chosen user. Keys
// rotate over the seeded per-user rows; unscoped collections use shared
// keys (board/counter) or per-user keys. Each scenario names its own
// collection as the first drive() argument.

func runCRUD(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration) {
	cliTokens = tokens
	return drive("notes", dur, cli.requests, func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string) {
		doc := fmt.Sprintf("user%04d-%04d", u, rng.Intn(cli.docs))
		switch rng.Intn(4) {
		case 0:
			return "hget", nil, "f=" + doc
		case 1:
			b, _ := json.Marshal(map[string]any{"title": "t", "tag": "x", "seq": rng.Intn(100)})
			return "hset", bytes.NewReader(b), "f=" + doc
		case 2:
			return "hsetnx", bytes.NewReader([]byte(`{"title":"nx"}`)), "f=" + doc
		default:
			return "hexists", nil, "f=" + doc
		}
	})
}

func runFind(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration) {
	cliTokens = tokens
	return drive("notes", dur, cli.requests, func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string) {
		switch rng.Intn(4) {
		case 0:
			return "find", nil, url.Values{"q": {`{"tag":"tag0"}`}, "limit": {"20"}}.Encode()
		case 1:
			return "find", nil, url.Values{"q": {`{"seq":{"$gte":10}}`}, "limit": {"50"}}.Encode()
		case 2:
			return "findone", nil, url.Values{"q": {`{"tag":"tag3"}`}}.Encode()
		default:
			return "count", nil, url.Values{"q": {`{"tag":"tag1"}`}}.Encode()
		}
	})
}

// runFanout exercises the whole-collection reads: every one scans the
// caller's full owner slice (docs rows), so this is the O(collection)
// workload the docs warn about.
func runFanout(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration) {
	cliTokens = tokens
	return drive("notes", dur, cli.requests, func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string) {
		switch rng.Intn(4) {
		case 0:
			return "hgetall", nil, ""
		case 1:
			return "hlen", nil, ""
		case 2:
			return "hkeys", nil, ""
		default:
			return "count", nil, url.Values{"q": {`{}`}}.Encode()
		}
	})
}

func runList(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration) {
	cliTokens = tokens
	return drive("queue", dur, cli.requests, func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string) {
		key := fmt.Sprintf("q%04d", u)
		switch rng.Intn(3) {
		case 0:
			b, _ := json.Marshal(map[string]any{"items": []string{fmt.Sprintf("j%d", rng.Intn(1000))}})
			return "lpush", bytes.NewReader(b), "f=" + key
		case 1:
			return "lpop", nil, "f=" + key
		default:
			return "llen", nil, "f=" + key
		}
	})
}

func runZSet(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration) {
	cliTokens = tokens
	return drive("board", dur, cli.requests, func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string) {
		m := fmt.Sprintf("m%d", rng.Intn(200))
		switch rng.Intn(4) {
		case 0:
			b, _ := json.Marshal(map[string]float64{m: float64(rng.Intn(1000))})
			return "zadd", bytes.NewReader(b), "f=board"
		case 1:
			return "zrange", nil, "f=board&start=0&stop=49&withscores=1"
		case 2:
			return "zincrby", nil, "f=board&member=" + m + "&n=1"
		default:
			return "zscore", nil, "f=board&member=" + m
		}
	})
}

func runSet(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration) {
	cliTokens = tokens
	return drive("tags", dur, cli.requests, func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string) {
		key := fmt.Sprintf("tags%04d", u)
		m := fmt.Sprintf("t%d", rng.Intn(100))
		switch rng.Intn(4) {
		case 0:
			b, _ := json.Marshal(map[string]any{"members": []string{m}})
			return "sadd", bytes.NewReader(b), "f=" + key
		case 1:
			return "smembers", nil, "f=" + key
		case 2:
			return "sismember", nil, "f=" + key + "&member=" + m
		default:
			return "scard", nil, "f=" + key
		}
	})
}

func runStr(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration) {
	cliTokens = tokens
	return drive("kv", dur, cli.requests, func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string) {
		key := fmt.Sprintf("k%04d-%d", u, rng.Intn(50))
		switch rng.Intn(2) {
		case 0:
			b, _ := json.Marshal(map[string]any{"v": "value-abc"})
			return "strset", bytes.NewReader(b), "f=" + key
		default:
			return "strget", nil, "f=" + key
		}
	})
}

// runIncr drives HINCRBY on a dedicated, never-deleted counter row per user
// (userXXXX-0000). The CRUD scenario never deletes -0000 (it uses random
// other rows and hsetnx, not hdel), so 404s here would signal a real bug.
func runIncr(tokens []string, dur time.Duration) (map[string]*perOp, time.Duration) {
	cliTokens = tokens
	return drive("notes", dur, cli.requests, func(w int, rng *rand.Rand, c *http.Client, u int, tok string) (string, *bytes.Reader, string) {
		doc := fmt.Sprintf("user%04d-0000", u)
		return "hincrby", nil, "f=" + doc + "&field=seq&n=1"
	})
}
