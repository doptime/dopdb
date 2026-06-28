package httpserve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/api"
)

// Handler is the dopdb HTTP entry point. It turns a closed command vocabulary
// over HTTP into MongoDB operations, with JWT @-binding, per-(command,
// collection) permissions, and row-level owner scoping. CRUD endpoints
// disappear: the frontend speaks HGET/HSET/... directly and safely.
type Handler struct {
	*Server
	Perms *Permissions
	// APIDispatch handles CMD == "API" (the api.Api function pipeline). It is
	// engine-independent and ported separately; when nil, API calls return 501.
	APIDispatch func(w http.ResponseWriter, r *http.Request, c *ReqCtx)
}

// NewHandler wires a Server (JWT) and a permission set into an http.Handler.
func NewHandler(s *Server, perms *Permissions) *Handler {
	return &Handler{Server: s, Perms: perms}
}

// dataCommands is the closed set of verbs the frontend may invoke. Anything
// outside it (and outside the API path) is rejected — this closedness is the
// safety property carried over from Redis's fixed command vocabulary.
var dataCommands = map[string]bool{
	"HGET": true, "HSET": true, "HSETNX": true, "HDEL": true, "DEL": true,
	"HEXISTS": true, "HGETALL": true, "HKEYS": true, "HVALS": true,
	"HLEN": true, "HINCRBY": true, "HINCRBYFLOAT": true, "FIND": true,
	"HMSET": true, "HMGET": true, "COUNT": true, "FINDONE": true, "WATCH": true,
	"HSCAN": true, "HSCANNOVALUES": true, "HRANDFIELD": true,
	"STRGET": true, "STRSET": true, "STRSETALL": true, "STRGETALL": true, "STRDEL": true,
	"SADD": true, "SREM": true, "SMEMBERS": true, "SISMEMBER": true, "SCARD": true,
	"LPUSH": true, "RPUSH": true, "LPOP": true, "RPOP": true, "LRANGE": true, "LLEN": true,
	"LINDEX": true, "LSET": true, "LREM": true, "LTRIM": true, "LINSERTBEFORE": true, "LINSERTAFTER": true,
	"ZADD": true, "ZREM": true, "ZSCORE": true, "ZCARD": true, "ZCOUNT": true, "ZINCRBY": true,
	"ZRANGE": true, "ZREVRANGE": true, "ZRANGEBYSCORE": true, "ZREVRANGEBYSCORE": true,
	"ZRANK": true, "ZREVRANK": true, "ZPOPMIN": true, "ZPOPMAX": true,
	"ZREMRANGEBYRANK": true, "ZREMRANGEBYSCORE": true,
}

// Guardrails (mirrored on the TypeScript side: server.ts MAX_BODY/DEFAULT_LIMIT/
// MAX_LIMIT). They cap two DoS surfaces — unbounded request bodies and
// unbounded FIND result sets.
const (
	maxBodyBytes = 1 << 20 // 1 MiB request-body ceiling (413 above it)
	defaultLimit = 100     // FIND with no/invalid limit is capped here
	maxLimit     = 1000    // an explicit FIND limit is clamped to this
)

// checkSortProj rejects sort/projection objects whose keys contain '$' (e.g.
// '$where') or whose values are neither number nor boolean. Mirrors
// ts/src/server.ts checkSortProj.
func checkSortProj(v any, what string) error {
	om, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid ?%s=: expected JSON object", what)
	}
	for k, val := range om {
		if strings.Contains(k, "$") {
			return fmt.Errorf("invalid ?%s= field %q (contains $)", what, k)
		}
		switch val.(type) {
		case float64, bool: // JSON numbers decode to float64
			continue
		default:
			return fmt.Errorf("invalid ?%s= value for %q (expected number or boolean)", what, k)
		}
	}
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// F4: cap the request body. Reads beyond the ceiling fail in parse() (which
	// surfaces 413), instead of being accumulated unbounded.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	c, status, err := h.parse(r)
	if err != nil {
		writeErr(w, status, "validation", err)
		return
	}

	if c.Cmd == "API" {
		if !h.Perms.Allowed("API", c.Coll) {
			writeErr(w, http.StatusForbidden, "forbidden", errors.New("not permitted: API::"+c.Coll))
			return
		}
		h.serveAPI(w, r, c)
		return
	}

	if !dataCommands[c.Cmd] {
		writeErr(w, http.StatusBadRequest, "validation", errors.New("unknown command: "+c.Cmd))
		return
	}

	// Permission gate: command :: collection. A collection's HttpOn(...) bitmask
	// is the primary source of truth (debug default = all on); the legacy
	// Perms grant/deny map still works for back-compat and runtime overrides.
	if !dopdb.HttpAllowed(c.Cmd, c.Coll) && !h.Perms.Allowed(c.Cmd, c.Coll) {
		writeErr(w, http.StatusForbidden, "forbidden", errors.New("not permitted: "+c.Cmd+"::"+c.Coll))
		return
	}

	acc, ok := dopdb.LookupHttp(c.Coll)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", errors.New("collection not registered: "+c.Coll))
		return
	}

	// Resolve the row-level owner scope (deny if scoped but unauthenticated).
	scope, authed := dopdb.OwnerScope(c.Coll, c.Claims)
	if !authed {
		writeErr(w, http.StatusUnauthorized, "unauthorized", errors.New("authentication required for "+c.Coll))
		return
	}
	scoped := dopdb.IsOwnerScoped(c.Coll)

	h.dispatch(r.Context(), w, c, acc, scope, scoped)
}

func (h *Handler) dispatch(ctx context.Context, w http.ResponseWriter, c *ReqCtx, acc dopdb.HttpKey, scope dopdb.M, scoped bool) {
	ha, _ := acc.(dopdb.HttpAccessor) // nil for non-Hash collections; Hash cases use ha
	key := c.Field()                  // ?f= is the document key for per-key commands

	switch c.Cmd {
	case "HGET":
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("HGET requires ?f="))
			return
		}
		var (
			v   any
			err error
		)
		if scoped {
			v, err = ha.HttpGetScoped(ctx, c.DB, key, scope)
		} else {
			v, err = ha.HttpGet(ctx, c.DB, key)
		}
		writeResult(w, v, err)

	case "HSET":
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("HSET requires ?f="))
			return
		}
		c.mergeBody() // fold body value fields into params (@-context already set)
		var err error
		if scoped {
			err = ha.HttpSetScoped(ctx, c.DB, key, c.Params, scope)
		} else {
			err = ha.HttpSet(ctx, c.DB, key, c.Params)
		}
		writeOK(w, err)

	case "HSETNX":
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("HSETNX requires ?f="))
			return
		}
		c.mergeBody()
		var inserted bool
		var err error
		if scoped {
			// F10: scoped hsetnx checks ownership first — prevents cross-tenant existence leakage
			inserted, err = ha.HttpSetNXScoped(ctx, c.DB, key, c.Params, scope)
		} else {
			inserted, err = ha.HttpSetNX(ctx, c.DB, key, c.Params)
		}
		writeResult(w, map[string]any{"inserted": inserted}, err)

	case "HDEL", "DEL":
		if len(c.Fields) == 0 {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?f="))
			return
		}
		var err error
		if scoped {
			// delete each key only if owned by caller
			for _, k := range c.Fields {
				if err = ha.HttpDelScoped(ctx, c.DB, k, scope); err != nil {
					break
				}
			}
		} else {
			err = ha.HttpDel(ctx, c.DB, c.Fields...)
		}
		writeOK(w, err)

	case "HEXISTS":
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("HEXISTS requires ?f="))
			return
		}
		var (
			ex  bool
			err error
		)
		if scoped {
			ex, err = ha.HttpExistsScoped(ctx, c.DB, key, scope)
		} else {
			ex, err = ha.HttpExists(ctx, c.DB, key)
		}
		writeResult(w, map[string]any{"exists": ex}, err)

	case "HGETALL", "HVALS":
		v, err := ha.HttpGetAll(ctx, c.DB, scope) // scope nil for unscoped collections
		writeResult(w, v, err)

	case "HKEYS":
		if scoped {
			v, err := ha.HttpKeysScoped(ctx, c.DB, scope)
			writeResult(w, v, err)
			return
		}
		v, err := ha.HttpKeys(ctx, c.DB)
		writeResult(w, v, err)

	case "HLEN":
		if scoped {
			n, err := ha.HttpLenScoped(ctx, c.DB, scope)
			writeResult(w, map[string]any{"len": n}, err)
			return
		}
		n, err := ha.HttpLen(ctx, c.DB)
		writeResult(w, map[string]any{"len": n}, err)

	case "HRANDFIELD":
		count := 1
		if n, e := strconv.Atoi(c.Queries.Get("count")); e == nil {
			count = n
		}
		v, err := ha.HttpRandField(ctx, c.DB, count, scope)
		writeResult(w, v, err)

	case "HSCAN", "HSCANNOVALUES":
		var cursor uint64
		if cu, e := strconv.ParseUint(c.Queries.Get("cursor"), 10, 64); e == nil {
			cursor = cu
		}
		count := int64(10)
		if cn, e := strconv.ParseInt(c.Queries.Get("count"), 10, 64); e == nil && cn > 0 {
			count = cn
		}
		match := c.Queries.Get("match")
		if c.Cmd == "HSCANNOVALUES" {
			v, err := ha.HttpScanNoValues(ctx, c.DB, match, cursor, count, scope)
			writeResult(w, v, err)
			return
		}
		v, err := ha.HttpScan(ctx, c.DB, match, cursor, count, scope)
		writeResult(w, v, err)

	case "HINCRBY", "HINCRBYFLOAT":
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?f="))
			return
		}
		field := c.Queries.Get("field")
		if field == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?field="))
			return
		}
		delta, perr := strconv.ParseFloat(c.Queries.Get("n"), 64)
		if perr != nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("invalid ?n="))
			return
		}
		if scoped {
			// only increment if the document is owned by the caller
			ok, err := ha.HttpExistsScoped(ctx, c.DB, key, scope)
			if err != nil || !ok {
				writeErr(w, http.StatusForbidden, "forbidden", dopdb.ErrForbidden)
				return
			}
		}
		writeOK(w, ha.HttpIncrBy(ctx, c.DB, key, field, delta))

	case "FIND":
		filter, ferr := c.parseFilter()
		if ferr != nil {
			writeErr(w, http.StatusBadRequest, "validation", ferr)
			return
		}
		opt := dopdb.FindOpt{}
		if l, e := strconv.ParseInt(c.Queries.Get("limit"), 10, 64); e == nil {
			opt.Limit = l
		}
		// F3: cap unbounded/oversized result sets (mirrors TS). An unset/invalid
		// limit falls back to defaultLimit; an over-large one is clamped.
		if opt.Limit <= 0 {
			opt.Limit = defaultLimit
		} else if opt.Limit > maxLimit {
			opt.Limit = maxLimit
		}
		if s, e := strconv.ParseInt(c.Queries.Get("skip"), 10, 64); e == nil {
			opt.Skip = s
		}
		// F13: parse sort (?s=<json>) and projection (?p=<json>) — mirrors TS.
		// F5/F13: validate sort/projection to reject $-operator injection.
		if sv := c.Queries.Get("s"); sv != "" {
			if err := json.Unmarshal([]byte(sv), &opt.Sort); err != nil {
				writeErr(w, http.StatusBadRequest, "validation", fmt.Errorf("invalid ?s= JSON: %w", err))
				return
			}
			if err := checkSortProj(opt.Sort, "s"); err != nil {
				writeErr(w, http.StatusBadRequest, "validation", err)
				return
			}
		}
		if pv := c.Queries.Get("p"); pv != "" {
			if err := json.Unmarshal([]byte(pv), &opt.Projection); err != nil {
				writeErr(w, http.StatusBadRequest, "validation", fmt.Errorf("invalid ?p= JSON: %w", err))
				return
			}
			if err := checkSortProj(opt.Projection, "p"); err != nil {
				writeErr(w, http.StatusBadRequest, "validation", err)
				return
			}
		}
		v, err := ha.HttpFind(ctx, c.DB, filter, scope, opt)
		writeResult(w, v, err)

	case "HMSET":
		var items map[string]dopdb.M
		if err := json.Unmarshal(c.Body, &items); err != nil || len(items) == 0 {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("HMSET requires a JSON object body {id:{...}}"))
			return
		}
		writeOK(w, ha.HttpMSet(ctx, c.DB, items, scope))

	case "HMGET":
		if len(c.Fields) == 0 {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("HMGET requires ?f="))
			return
		}
		v, err := ha.HttpMGet(ctx, c.DB, scope, c.Fields...)
		writeResult(w, v, err)

	case "COUNT":
		filter, ferr := c.parseFilter()
		if ferr != nil {
			writeErr(w, http.StatusBadRequest, "validation", ferr)
			return
		}
		n, err := ha.HttpCount(ctx, c.DB, filter, scope)
		writeResult(w, map[string]any{"count": n}, err)

	case "FINDONE":
		filter, ferr := c.parseFilter()
		if ferr != nil {
			writeErr(w, http.StatusBadRequest, "validation", ferr)
			return
		}
		v, err := ha.HttpFindOne(ctx, c.DB, filter, scope)
		writeResult(w, v, err)

	case "STRGET":
		sa, ok := acc.(dopdb.StringAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a string collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("STRGET requires ?f="))
			return
		}
		v, err := sa.HttpStrGet(ctx, c.DB, key, scope)
		writeResult(w, v, err)

	case "STRSET":
		sa, ok := acc.(dopdb.StringAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a string collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("STRSET requires ?f="))
			return
		}
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil || body["v"] == nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(`STRSET body needs {"v":<value>}`))
			return
		}
		var exp time.Duration
		if es, e := strconv.Atoi(c.Queries.Get("expiration")); e == nil && es > 0 {
			exp = time.Duration(es) * time.Second
		}
		writeOK(w, sa.HttpStrSet(ctx, c.DB, key, body["v"], exp, scope))

	case "STRDEL":
		sa, ok := acc.(dopdb.StringAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a string collection: "+c.Coll))
			return
		}
		if len(c.Fields) == 0 {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("STRDEL requires ?f="))
			return
		}
		writeOK(w, sa.HttpStrDel(ctx, c.DB, scope, c.Fields...))

	case "STRGETALL":
		sa, ok := acc.(dopdb.StringAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a string collection: "+c.Coll))
			return
		}
		v, err := sa.HttpStrGetAll(ctx, c.DB, c.Queries.Get("match"), scope)
		writeResult(w, v, err)

	case "STRSETALL":
		sa, ok := acc.(dopdb.StringAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a string collection: "+c.Coll))
			return
		}
		var items map[string]any
		if err := json.Unmarshal(c.Body, &items); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("STRSETALL body needs {key:value,...}"))
			return
		}
		writeOK(w, sa.HttpStrSetAll(ctx, c.DB, items, scope))

	case "LPUSH", "RPUSH":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?f="))
			return
		}
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+` body needs {"items":[...]}`))
			return
		}
		its, _ := body["items"].([]any)
		if c.Cmd == "LPUSH" {
			writeOK(w, la.HttpLPush(ctx, c.DB, key, its, scope))
		} else {
			writeOK(w, la.HttpRPush(ctx, c.DB, key, its, scope))
		}

	case "LPOP", "RPOP":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?f="))
			return
		}
		if c.Cmd == "LPOP" {
			v, err := la.HttpLPop(ctx, c.DB, key, scope)
			writeResult(w, v, err)
		} else {
			v, err := la.HttpRPop(ctx, c.DB, key, scope)
			writeResult(w, v, err)
		}

	case "LRANGE":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		start, stop := parseRange(c)
		v, err := la.HttpLRange(ctx, c.DB, key, start, stop, scope)
		writeResult(w, v, err)

	case "LLEN":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		n, err := la.HttpLLen(ctx, c.DB, key, scope)
		writeResult(w, map[string]any{"len": n}, err)

	case "LINDEX":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		idx, _ := strconv.Atoi(c.Queries.Get("index"))
		v, err := la.HttpLIndex(ctx, c.DB, key, idx, scope)
		writeResult(w, v, err)

	case "LSET":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		idx, _ := strconv.Atoi(c.Queries.Get("index"))
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil || body["item"] == nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(`LSET body needs {"item":<value>}`))
			return
		}
		writeOK(w, la.HttpLSet(ctx, c.DB, key, idx, body["item"], scope))

	case "LREM":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		count, _ := strconv.Atoi(c.Queries.Get("count"))
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil || body["item"] == nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(`LREM body needs {"item":<value>}`))
			return
		}
		writeOK(w, la.HttpLRem(ctx, c.DB, key, count, body["item"], scope))

	case "LTRIM":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		start, stop := parseRange(c)
		writeOK(w, la.HttpLTrim(ctx, c.DB, key, start, stop, scope))

	case "LINSERTBEFORE", "LINSERTAFTER":
		la, ok := acc.(dopdb.ListAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a list collection: "+c.Coll))
			return
		}
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil || body["pivot"] == nil || body["item"] == nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(`LINSERT body needs {"pivot":..,"item":..}`))
			return
		}
		writeOK(w, la.HttpLInsert(ctx, c.DB, key, c.Cmd == "LINSERTBEFORE", body["pivot"], body["item"], scope))

	case "SADD":
		sa, ok := acc.(dopdb.SetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a set collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("SADD requires ?f="))
			return
		}
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("SADD body needs {\"members\":[...]}"))
			return
		}
		ms, _ := body["members"].([]any)
		writeOK(w, sa.HttpSAdd(ctx, c.DB, key, ms, scope))

	case "SREM":
		sa, ok := acc.(dopdb.SetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a set collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("SREM requires ?f="))
			return
		}
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("SREM body needs {\"members\":[...]}"))
			return
		}
		ms, _ := body["members"].([]any)
		writeOK(w, sa.HttpSRem(ctx, c.DB, key, ms, scope))

	case "SMEMBERS":
		sa, ok := acc.(dopdb.SetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a set collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("SMEMBERS requires ?f="))
			return
		}
		v, err := sa.HttpSMembers(ctx, c.DB, key, scope)
		writeResult(w, v, err)

	case "SISMEMBER":
		sa, ok := acc.(dopdb.SetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a set collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("SISMEMBER requires ?f="))
			return
		}
		member := c.Queries.Get("member")
		ex, err := sa.HttpSIsMember(ctx, c.DB, key, member, scope)
		writeResult(w, map[string]any{"member": ex}, err)

	case "SCARD":
		sa, ok := acc.(dopdb.SetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a set collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("SCARD requires ?f="))
			return
		}
		n, err := sa.HttpSCard(ctx, c.DB, key, scope)
		writeResult(w, map[string]any{"card": n}, err)

	// ---- ZSet (Z*) family ----
	case "ZADD":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZADD requires ?f="))
			return
		}
		var pairs map[string]float64
		if err := json.Unmarshal(c.Body, &pairs); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(`ZADD body needs {"member":score,...}`))
			return
		}
		added, err := za.HttpZAdd(ctx, c.DB, key, pairs, scope)
		writeResult(w, added, err)

	case "ZREM":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZREM requires ?f="))
			return
		}
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(`ZREM body needs {"members":[...]}`))
			return
		}
		raw, _ := body["members"].([]any)
		members := make([]string, 0, len(raw))
		for _, x := range raw {
			if s, ok := x.(string); ok {
				members = append(members, s)
			}
		}
		removed, err := za.HttpZRem(ctx, c.DB, key, members, scope)
		writeResult(w, removed, err)

	case "ZSCORE":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZSCORE requires ?f="))
			return
		}
		sc, err := za.HttpZScore(ctx, c.DB, key, c.Queries.Get("member"), scope)
		writeResult(w, sc, err)

	case "ZCARD":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZCARD requires ?f="))
			return
		}
		card, err := za.HttpZCard(ctx, c.DB, key, scope)
		writeResult(w, map[string]any{"card": card}, err)

	case "ZCOUNT":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZCOUNT requires ?f="))
			return
		}
		min, max := parseMinMax(c)
		cnt, err := za.HttpZCount(ctx, c.DB, key, min, max, scope)
		writeResult(w, map[string]any{"count": cnt}, err)

	case "ZINCRBY":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZINCRBY requires ?f="))
			return
		}
		inc, perr := strconv.ParseFloat(c.Queries.Get("n"), 64)
		if perr != nil {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZINCRBY requires ?n=<number>"))
			return
		}
		ns, err := za.HttpZIncrBy(ctx, c.DB, key, c.Queries.Get("member"), inc, scope)
		writeResult(w, ns, err)

	case "ZRANGE", "ZREVRANGE":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?f="))
			return
		}
		start, stop := parseRange(c)
		ws := c.Queries.Get("withscores")
		v, err := za.HttpZRange(ctx, c.DB, key, start, stop, c.Cmd == "ZREVRANGE", ws == "true" || ws == "1", scope)
		writeResult(w, v, err)

	case "ZRANGEBYSCORE", "ZREVRANGEBYSCORE":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?f="))
			return
		}
		min, max := parseMinMax(c)
		ws := c.Queries.Get("withscores")
		v, err := za.HttpZRangeByScore(ctx, c.DB, key, min, max, c.Cmd == "ZREVRANGEBYSCORE", ws == "true" || ws == "1", scope)
		writeResult(w, v, err)

	case "ZRANK", "ZREVRANK":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?f="))
			return
		}
		rk, err := za.HttpZRank(ctx, c.DB, key, c.Queries.Get("member"), c.Cmd == "ZREVRANK", scope)
		writeResult(w, map[string]any{"rank": rk}, err)

	case "ZPOPMIN", "ZPOPMAX":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New(c.Cmd+" requires ?f="))
			return
		}
		count, _ := strconv.Atoi(c.Queries.Get("count"))
		v, err := za.HttpZPop(ctx, c.DB, key, count, c.Cmd == "ZPOPMAX", scope)
		writeResult(w, v, err)

	case "ZREMRANGEBYRANK":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZREMRANGEBYRANK requires ?f="))
			return
		}
		start, stop := parseRange(c)
		removed, err := za.HttpZRemRangeByRank(ctx, c.DB, key, start, stop, scope)
		writeResult(w, removed, err)

	case "ZREMRANGEBYSCORE":
		za, ok := acc.(dopdb.ZSetAccessor)
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", errors.New("not a zset collection: "+c.Coll))
			return
		}
		if key == "" {
			writeErr(w, http.StatusBadRequest, "validation", errors.New("ZREMRANGEBYSCORE requires ?f="))
			return
		}
		min, max := parseMinMax(c)
		removed, err := za.HttpZRemRangeByScore(ctx, c.DB, key, min, max, scope)
		writeResult(w, removed, err)

	case "WATCH":
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeErr(w, http.StatusInternalServerError, "error", errors.New("streaming unsupported"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		emit := func(op, id string, doc any) error {
			payload, _ := json.Marshal(map[string]any{"type": op, "id": id, "doc": doc})
			if _, err := w.Write([]byte("data: " + string(payload) + "\n\n")); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}
		_ = ha.HttpWatch(ctx, c.DB, scope, emit)
	}
}

// parseFilter reads the FIND filter from ?q=<json> or the request body.
func (c *ReqCtx) parseFilter() (dopdb.M, error) {
	raw := c.Queries.Get("q")
	var src []byte
	if raw != "" {
		src = []byte(raw)
	} else {
		src = c.Body
	}
	if len(src) == 0 {
		return dopdb.M{}, nil
	}
	var m dopdb.M
	if err := json.Unmarshal(src, &m); err != nil {
		return nil, errors.New("invalid filter json")
	}
	return m, nil
}

// serveAPI dispatches an api.Api endpoint. The APIDispatch override wins if set
// (e.g. for streaming); otherwise the built-in pipeline runs by name.
func (h *Handler) serveAPI(w http.ResponseWriter, r *http.Request, c *ReqCtx) {
	if h.APIDispatch != nil {
		h.APIDispatch(w, r, c)
		return
	}
	c.mergeBody() // fold body args into params; @-context already injected
	ret, err := api.CallByName(r.Context(), c.Coll, c.Params, c.Body)
	if errors.Is(err, api.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", err)
		return
	}
	writeResult(w, ret, err)
}

// ---- response helpers ----

func writeResult(w http.ResponseWriter, v any, err error) {
	if err != nil {
		statusForError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeOK(w http.ResponseWriter, err error) {
	if err != nil {
		statusForError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func statusForError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dopdb.ErrNoDoc):
		writeErr(w, http.StatusNotFound, "not_found", err)
	case errors.Is(err, dopdb.ErrForbidden):
		writeErr(w, http.StatusForbidden, "forbidden", err)
	default:
		writeErr(w, http.StatusInternalServerError, "error", err)
	}
}

func writeErr(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error(), "code": code})
}

// parseRange reads ?start=/?stop= (default 0/-1, Redis semantics).
// toFloat coerces a JSON number (float64) or numeric string to float64.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func parseRange(c *ReqCtx) (int, int) {
	start, _ := strconv.Atoi(c.Queries.Get("start"))
	stop := -1
	if s, e := strconv.Atoi(c.Queries.Get("stop")); e == nil {
		stop = s
	}
	return start, stop
}

// parseMinMax reads ?min=/?max= (default -Inf/+Inf, Redis Z* score-range semantics).
func parseMinMax(c *ReqCtx) (float64, float64) {
	min, max := math.Inf(-1), math.Inf(1)
	if s := c.Queries.Get("min"); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			min = f
		}
	}
	if s := c.Queries.Get("max"); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			max = f
		}
	}
	return min, max
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
