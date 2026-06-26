package httpserve

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ReqCtx is the parsed, authenticated context of one data/API request. It is the
// dopdb analogue of doptime's DoptimeReqCtx.
type ReqCtx struct {
	Cmd    string   // upper-cased command, e.g. "HGET"; "API" for api calls
	Coll   string   // collection name (the URL key part, @-resolved)
	Fields []string // ?f= values, @-resolved; Field() is the first
	DB     string   // ?ds= datasource/database selector ("default" if absent)

	Queries url.Values
	Params  map[string]any // merged query+header+@-context, fed to writes
	Claims  Claims
	Body    []byte // raw request body (interpreted per-command)
}

// Field returns the first ?f= value (the document key for per-key commands).
func (c *ReqCtx) Field() string {
	if len(c.Fields) > 0 {
		return c.Fields[0]
	}
	return ""
}

// Server holds configuration for parsing and authenticating requests.
type Server struct {
	JWTSecret string
	cache     *tokenCache
}

// NewServer constructs a Server with the given JWT secret (HS256 key or RS256
// PEM public key).
func NewServer(jwtSecret string) *Server {
	return &Server{JWTSecret: jwtSecret, cache: newTokenCache(10000)}
}

// parse builds a ReqCtx from an HTTP request: it splits CMD-KEY, reads fields
// and datasource, verifies the JWT, performs @-substitution, and assembles the
// param map with forged @-params stripped and server @-context injected.
func (s *Server) parse(r *http.Request) (*ReqCtx, int, error) {
	c := &ReqCtx{Claims: Claims{}, Params: map[string]any{}}

	pathStr := r.URL.Path
	if r.URL.RawPath != "" {
		pathStr = r.URL.RawPath
	}
	pathStr = strings.TrimRight(pathStr, "/")

	// Everything is routed under /api/:
	//   data command:  /api/<cmd>/<coll>   (two segments; cmd ∈ the closed verb set)
	//   api call:      /api/<name>         (one segment)
	// The datasource is a query parameter (?ds=), never a path segment.
	idx := strings.Index(pathStr, "/api/")
	if idx < 0 {
		return nil, http.StatusBadRequest, errors.New("url must be /api/<cmd>/<coll> (data) or /api/<name> (api)")
	}
	rest := strings.Trim(pathStr[idx+len("/api/"):], "/")
	if rest == "" {
		return nil, http.StatusBadRequest, errors.New("url missing command/collection or api name")
	}
	parts := strings.Split(rest, "/")
	for i := range parts {
		if dec, derr := url.QueryUnescape(parts[i]); derr == nil {
			parts[i] = dec
		}
	}
	if len(parts) >= 2 {
		c.Cmd = strings.ToUpper(parts[0]) // data command
		c.Coll = parts[1]
	} else {
		c.Cmd = "API" // function endpoint
		c.Coll = parts[0]
	}
	if c.Cmd == "" || c.Coll == "" {
		return nil, http.StatusBadRequest, errors.New("url missing command or collection")
	}

	var err error

	c.Queries = r.URL.Query()
	c.Fields = c.Queries["f"]
	c.DB = c.Queries.Get("ds")
	if c.DB == "" {
		c.DB = "default"
	}
	c.Body, _ = io.ReadAll(r.Body)

	// Verify JWT (if present) before any @-substitution.
	if status, err := s.parseJWT(r, c); err != nil {
		return nil, status, err
	}

	// @-substitution in collection name and fields (from JWT claims / uuid / nanoid).
	if c.Coll, err = c.replaceTags(c.Coll); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	for i, f := range c.Fields {
		if c.Fields[i], err = c.replaceTags(f); err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}

	s.buildParams(r, c)
	return c, http.StatusOK, nil
}

func (s *Server) parseJWT(r *http.Request, c *ReqCtx) (int, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		return http.StatusOK, nil // anonymous; downstream policy decides
	}
	if claims, ok := s.cache.get(tok); ok {
		if err := checkExp(claims); err != nil {
			return http.StatusUnauthorized, err
		}
		c.Claims = claims
		return http.StatusOK, nil
	}
	claims, err := VerifyJWT(tok, s.JWTSecret)
	if err != nil {
		return http.StatusUnauthorized, err
	}
	s.cache.put(tok, claims)
	c.Claims = claims
	return http.StatusOK, nil
}

// replaceTags resolves @-tags inside an identifier. @uuid and @nanoid[N] generate
// values; any other @name is looked up in the verified JWT claims. A missing
// claim is an error (fail closed). Numeric claims are rendered as integers to
// avoid scientific notation (the doptime float64-id hazard).
func (c *ReqCtx) replaceTags(input string) (string, error) {
	if !strings.Contains(input, "@") {
		return input, nil
	}
	parts := strings.Split(input, "@")
	var sb strings.Builder
	sb.WriteString(parts[0])
	for _, tag := range parts[1:] {
		switch {
		case tag == "uuid":
			sb.WriteString(newUUID())
		case strings.HasPrefix(tag, "nanoid"):
			n := 21
			if rest := strings.TrimPrefix(tag, "nanoid"); rest != "" {
				if m, err := strconv.Atoi(rest); err == nil && m > 0 {
					n = m
				}
			}
			sb.WriteString(nanoID(n))
		default:
			v, ok := c.Claims[tag]
			if !ok {
				return "", fmt.Errorf("jwt missing claim %q for @-binding", tag)
			}
			sb.WriteString(claimToString(v))
		}
	}
	return sb.String(), nil
}

func claimToString(v any) string {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return strconv.FormatInt(i, 10)
		}
		return n.String()
	case float64:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'f', -1, 64)
	case string:
		return n
	default:
		return fmt.Sprint(v)
	}
}

// buildParams assembles the param map: query + headers + (already-parsed) body,
// then strips any client-supplied @-key (anti-forgery), then injects the
// server-controlled @-context and all JWT claims as @<claim>.
func (s *Server) buildParams(r *http.Request, c *ReqCtx) {
	if c.Params == nil {
		c.Params = map[string]any{}
	}
	for k, v := range c.Queries {
		if len(v) == 1 {
			c.Params[k] = v[0]
		} else {
			c.Params[k] = v
		}
	}
	for k, v := range r.Header {
		if len(v) == 1 {
			c.Params[k] = v[0]
		} else {
			c.Params[k] = v
		}
	}

	// Strip forged @-params: clients may never supply @-prefixed keys.
	for k := range c.Params {
		if strings.HasPrefix(k, "@") {
			delete(c.Params, k)
		}
	}

	// Inject server-controlled context.
	c.Params["@key"] = c.Coll
	c.Params["@field"] = c.Field()
	c.Params["@remoteAddr"] = r.RemoteAddr
	c.Params["@host"] = r.Host
	c.Params["@method"] = r.Method
	c.Params["@path"] = r.URL.Path
	c.Params["@rawQuery"] = r.URL.RawQuery
	for k, v := range c.Claims {
		c.Params["@"+k] = v
	}
}

// mergeBody folds a decoded JSON body object into the params (its non-@ fields),
// for write commands. @-context injected earlier wins and the body may never
// carry @-keys. Returns false if the body was not a JSON object.
func (c *ReqCtx) mergeBody() bool {
	if len(c.Body) == 0 {
		return false
	}
	m := map[string]any{}
	dec := json.NewDecoder(strings.NewReader(string(c.Body)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return false
	}
	for k, v := range m {
		if strings.HasPrefix(k, "@") {
			continue // never let the body carry @-context
		}
		c.Params[k] = v
	}
	return true
}

// ---- local uuid / nanoid (no external deps) ----

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

const nanoAlphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict"

func nanoID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = nanoAlphabet[int(b[i])&63]
	}
	return string(b)
}
