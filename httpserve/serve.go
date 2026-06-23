package httpserve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, status, err := h.parse(r)
	if err != nil {
		writeErr(w, status, err)
		return
	}

	if c.Cmd == "API" {
		if !h.Perms.Allowed("API", c.Coll) {
			writeErr(w, http.StatusForbidden, errors.New("not permitted: API::"+c.Coll))
			return
		}
		h.serveAPI(w, r, c)
		return
	}

	if !dataCommands[c.Cmd] {
		writeErr(w, http.StatusBadRequest, errors.New("unknown command: "+c.Cmd))
		return
	}

	// Permission gate: command :: collection.
	if !h.Perms.Allowed(c.Cmd, c.Coll) {
		writeErr(w, http.StatusForbidden, errors.New("not permitted: "+c.Cmd+"::"+c.Coll))
		return
	}

	acc, ok := dopdb.LookupHttp(c.Coll)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("collection not registered: "+c.Coll))
		return
	}

	// Resolve the row-level owner scope (deny if scoped but unauthenticated).
	scope, authed := dopdb.OwnerScope(c.Coll, c.Claims)
	if !authed {
		writeErr(w, http.StatusUnauthorized, errors.New("authentication required for "+c.Coll))
		return
	}
	scoped := dopdb.IsOwnerScoped(c.Coll)

	h.dispatch(r.Context(), w, c, acc, scope, scoped)
}

func (h *Handler) dispatch(ctx context.Context, w http.ResponseWriter, c *ReqCtx, acc dopdb.HttpAccessor, scope dopdb.M, scoped bool) {
	key := c.Field() // ?f= is the document key for per-key commands

	switch c.Cmd {
	case "HGET":
		if key == "" {
			writeErr(w, http.StatusBadRequest, errors.New("HGET requires ?f="))
			return
		}
		var (
			v   any
			err error
		)
		if scoped {
			v, err = acc.HttpGetScoped(ctx, key, scope)
		} else {
			v, err = acc.HttpGet(ctx, key)
		}
		writeResult(w, v, err)

	case "HSET":
		if key == "" {
			writeErr(w, http.StatusBadRequest, errors.New("HSET requires ?f="))
			return
		}
		c.mergeBody() // fold body value fields into params (@-context already set)
		var err error
		if scoped {
			err = acc.HttpSetScoped(ctx, key, c.Params, scope)
		} else {
			err = acc.HttpSet(ctx, key, c.Params)
		}
		writeOK(w, err)

	case "HSETNX":
		if key == "" {
			writeErr(w, http.StatusBadRequest, errors.New("HSETNX requires ?f="))
			return
		}
		c.mergeBody()
		inserted, err := acc.HttpSetNX(ctx, key, c.Params)
		writeResult(w, map[string]any{"inserted": inserted}, err)

	case "HDEL", "DEL":
		if len(c.Fields) == 0 {
			writeErr(w, http.StatusBadRequest, errors.New(c.Cmd+" requires ?f="))
			return
		}
		var err error
		if scoped {
			// delete each key only if owned by caller
			for _, k := range c.Fields {
				if err = acc.HttpDelScoped(ctx, k, scope); err != nil {
					break
				}
			}
		} else {
			err = acc.HttpDel(ctx, c.Fields...)
		}
		writeOK(w, err)

	case "HEXISTS":
		if key == "" {
			writeErr(w, http.StatusBadRequest, errors.New("HEXISTS requires ?f="))
			return
		}
		var (
			ex  bool
			err error
		)
		if scoped {
			ex, err = acc.HttpExistsScoped(ctx, key, scope)
		} else {
			ex, err = acc.HttpExists(ctx, key)
		}
		writeResult(w, map[string]any{"exists": ex}, err)

	case "HGETALL", "HVALS":
		v, err := acc.HttpGetAll(ctx, scope) // scope nil for unscoped collections
		writeResult(w, v, err)

	case "HKEYS":
		if scoped {
			writeErr(w, http.StatusForbidden, errors.New("HKEYS not allowed on a row-scoped collection"))
			return
		}
		v, err := acc.HttpKeys(ctx)
		writeResult(w, v, err)

	case "HLEN":
		if scoped {
			writeErr(w, http.StatusForbidden, errors.New("HLEN not allowed on a row-scoped collection"))
			return
		}
		n, err := acc.HttpLen(ctx)
		writeResult(w, map[string]any{"len": n}, err)

	case "HINCRBY", "HINCRBYFLOAT":
		if key == "" {
			writeErr(w, http.StatusBadRequest, errors.New(c.Cmd+" requires ?f="))
			return
		}
		field := c.Queries.Get("field")
		if field == "" {
			writeErr(w, http.StatusBadRequest, errors.New(c.Cmd+" requires ?field="))
			return
		}
		delta, perr := strconv.ParseFloat(c.Queries.Get("n"), 64)
		if perr != nil {
			writeErr(w, http.StatusBadRequest, errors.New("invalid ?n="))
			return
		}
		if scoped {
			// only increment if the document is owned by the caller
			ok, err := acc.HttpExistsScoped(ctx, key, scope)
			if err != nil || !ok {
				writeErr(w, http.StatusForbidden, dopdb.ErrForbidden)
				return
			}
		}
		writeOK(w, acc.HttpIncrBy(ctx, key, field, delta))

	case "FIND":
		filter, ferr := c.parseFilter()
		if ferr != nil {
			writeErr(w, http.StatusBadRequest, ferr)
			return
		}
		opt := dopdb.FindOpt{}
		if l, e := strconv.ParseInt(c.Queries.Get("limit"), 10, 64); e == nil {
			opt.Limit = l
		}
		if s, e := strconv.ParseInt(c.Queries.Get("skip"), 10, 64); e == nil {
			opt.Skip = s
		}
		v, err := acc.HttpFind(ctx, filter, scope, opt)
		writeResult(w, v, err)
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
		writeErr(w, http.StatusNotFound, err)
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
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, dopdb.ErrForbidden):
		writeErr(w, http.StatusForbidden, err)
	default:
		writeErr(w, http.StatusBadRequest, err)
	}
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
