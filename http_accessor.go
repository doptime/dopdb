package dopdb

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// ----------------------------------------------------------------------------
// The type-erasure bridge.
//
// The HTTP layer dispatches by a collection NAME and a command STRING known
// only at runtime, but Collection[K,V] is generic. HttpAccessor is the
// non-generic surface the dispatcher calls; *Collection[K,V] implements it by
// boxing V as any. This mirrors how doptime erased types behind
// httpapi.ApiInterface / redisdb.IHttpKey.
//
// The string key arriving here is already the canonical _id (the @-resolved
// ?f= value), so it is used directly as the document id. ds is the request
// datasource selector (?ds=, "default" when absent); every operation runs
// against c.backend(ds), so the same collection can live in several databases.
// Request bodies arrive as a merged param map decoded into V via a JSON
// round-trip honouring the json tags we keep equal to the bson tags.
// ----------------------------------------------------------------------------

// HttpAccessor is the runtime, non-generic view of a collection used by the
// HTTP dispatcher. Every method takes the request datasource ds and otherwise
// engine-neutral types.
type HttpAccessor interface {
	Collection() string

	HttpGet(ctx context.Context, ds, key string) (any, error)
	HttpSet(ctx context.Context, ds, key string, params M) error
	HttpSetNX(ctx context.Context, ds, key string, params M) (bool, error)
	HttpDel(ctx context.Context, ds string, keys ...string) error
	HttpExists(ctx context.Context, ds, key string) (bool, error)

	// HttpGetAll returns all values; if scope is non-nil it is applied as a
	// mandatory filter (the row-level isolation predicate).
	HttpGetAll(ctx context.Context, ds string, scope M) (any, error)
	HttpKeys(ctx context.Context, ds string) (any, error)
	HttpLen(ctx context.Context, ds string) (int64, error)
	HttpIncrBy(ctx context.Context, ds, key, field string, delta float64) error

	// HttpKeysScoped / HttpLenScoped return only the caller's own keys / count
	// for a row-scoped collection.
	HttpKeysScoped(ctx context.Context, ds string, scope M) (any, error)
	HttpLenScoped(ctx context.Context, ds string, scope M) (int64, error)

	// HttpFind ANDs scope (if any) with the caller filter, then sanitizes.
	HttpFind(ctx context.Context, ds string, filter M, scope M, opt FindOpt) (any, error)

	// Scoped per-key operations enforce row-level ownership for collections that
	// declared an owner scope.
	HttpGetScoped(ctx context.Context, ds, key string, scope M) (any, error)
	HttpExistsScoped(ctx context.Context, ds, key string, scope M) (bool, error)
	HttpSetScoped(ctx context.Context, ds, key string, params M, scope M) error
	HttpDelScoped(ctx context.Context, ds, key string, scope M) error

	// Batch + query extras.
	HttpMSet(ctx context.Context, ds string, items map[string]M, scope M) error
	HttpMGet(ctx context.Context, ds string, scope M, keys ...string) (any, error)
	HttpCount(ctx context.Context, ds string, filter M, scope M) (int64, error)
	HttpFindOne(ctx context.Context, ds string, filter M, scope M) (any, error)

	// HttpWatch streams change events; emit is called per event and returns a
	// non-nil error to stop the stream (e.g. the client disconnected).
	HttpWatch(ctx context.Context, ds string, scope M, emit func(op, id string, doc any) error) error
}

var (
	httpAccessors   = map[string]HttpAccessor{}
	httpAccessorsMu sync.RWMutex
)

// RegisterHttp exposes a collection to the HTTP data-command layer under its
// collection name. Call once per collection you want reachable from the client.
func RegisterHttp(a HttpAccessor) {
	httpAccessorsMu.Lock()
	defer httpAccessorsMu.Unlock()
	httpAccessors[a.Collection()] = a
}

// LookupHttp resolves a registered accessor by collection name.
func LookupHttp(coll string) (HttpAccessor, bool) {
	httpAccessorsMu.RLock()
	defer httpAccessorsMu.RUnlock()
	a, ok := httpAccessors[coll]
	return a, ok
}

// RegisteredCollections returns the names of all collections registered for HTTP
// access (used by Serve to auto-grant / introspect).
func RegisteredCollections() []string {
	httpAccessorsMu.RLock()
	defer httpAccessorsMu.RUnlock()
	out := make([]string, 0, len(httpAccessors))
	for k := range httpAccessors {
		out = append(out, k)
	}
	return out
}

// compile-time assertion that a Collection satisfies HttpAccessor.
var _ HttpAccessor = (*Collection[string, any])(nil)

// ---- Collection[K,V] implements HttpAccessor ----

func (c *Collection[K, V]) valueFromParams(params M) (V, error) {
	var v V
	b, err := json.Marshal(params)
	if err != nil {
		return v, err
	}
	if c.vIsPtr {
		pv := reflect.New(c.vElemType)
		if err := json.Unmarshal(b, pv.Interface()); err != nil {
			return v, err
		}
		return pv.Interface().(V), nil
	}
	err = json.Unmarshal(b, &v)
	return v, err
}

func (c *Collection[K, V]) decodeMany(docs [][]byte) (any, error) {
	out := make([]V, len(docs))
	var err error
	for i, b := range docs {
		if out[i], err = c.decode(b); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// findDS sanitizes filter, runs it against datasource ds, and decodes the rows.
func (c *Collection[K, V]) findDS(ctx context.Context, ds string, filter M, opt FindOpt) ([]V, error) {
	safe, err := SanitizeFilter(filter)
	if err != nil {
		return nil, err
	}
	_, docs, err := c.backend(ds).find(ctx, c.coll, safe, opt)
	if err != nil {
		return nil, err
	}
	out := make([]V, len(docs))
	for i, b := range docs {
		if out[i], err = c.decode(b); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Collection[K, V]) idsToKeys(ids []string) (any, error) {
	out := make([]K, len(ids))
	var err error
	for i, id := range ids {
		if out[i], err = c.deserializeKey(id); err != nil {
			return nil, fmt.Errorf("dopdb: bad key %q in %s: %w", id, c.coll, err)
		}
	}
	return out, nil
}

func (c *Collection[K, V]) HttpGet(ctx context.Context, ds, key string) (any, error) {
	b, err := c.backend(ds).get(ctx, c.coll, key)
	if err != nil {
		return nil, err
	}
	return c.decode(b)
}

func (c *Collection[K, V]) HttpSet(ctx context.Context, ds, key string, params M) error {
	v, err := c.valueFromParams(params)
	if err != nil {
		return err
	}
	doc, err := c.encode(v)
	if err != nil {
		return err
	}
	return c.backend(ds).put(ctx, c.coll, key, doc)
}

func (c *Collection[K, V]) HttpSetNX(ctx context.Context, ds, key string, params M) (bool, error) {
	v, err := c.valueFromParams(params)
	if err != nil {
		return false, err
	}
	doc, err := c.encode(v)
	if err != nil {
		return false, err
	}
	return c.backend(ds).putIfAbsent(ctx, c.coll, key, doc)
}

func (c *Collection[K, V]) HttpDel(ctx context.Context, ds string, keys ...string) error {
	_, err := c.backend(ds).del(ctx, c.coll, keys)
	return err
}

func (c *Collection[K, V]) HttpExists(ctx context.Context, ds, key string) (bool, error) {
	return c.backend(ds).exists(ctx, c.coll, key)
}

func (c *Collection[K, V]) HttpGetAll(ctx context.Context, ds string, scope M) (any, error) {
	if len(scope) > 0 {
		return c.findDS(ctx, ds, scope, FindOpt{})
	}
	_, docs, err := c.backend(ds).all(ctx, c.coll)
	if err != nil {
		return nil, err
	}
	return c.decodeMany(docs)
}

func (c *Collection[K, V]) HttpKeys(ctx context.Context, ds string) (any, error) {
	ids, err := c.backend(ds).ids(ctx, c.coll)
	if err != nil {
		return nil, err
	}
	return c.idsToKeys(ids)
}

func (c *Collection[K, V]) HttpLen(ctx context.Context, ds string) (int64, error) {
	return c.backend(ds).count(ctx, c.coll)
}

func (c *Collection[K, V]) HttpIncrBy(ctx context.Context, ds, key, field string, delta float64) error {
	return c.backend(ds).incr(ctx, c.coll, key, field, delta)
}

// keysByScope returns the keys of documents matching the (server-derived) owner
// predicate, used by the scoped HKEYS/HLEN. scope is trusted but sanitized for
// uniformity with every other Find path.
func (c *Collection[K, V]) keysByScope(ctx context.Context, ds string, scope M) ([]K, error) {
	safe, err := SanitizeFilter(scope)
	if err != nil {
		return nil, err
	}
	ids, _, err := c.backend(ds).find(ctx, c.coll, safe, FindOpt{})
	if err != nil {
		return nil, err
	}
	out := make([]K, len(ids))
	for i, id := range ids {
		if out[i], err = c.deserializeKey(id); err != nil {
			return nil, fmt.Errorf("dopdb: bad key %q in %s: %w", id, c.coll, err)
		}
	}
	return out, nil
}

func (c *Collection[K, V]) HttpKeysScoped(ctx context.Context, ds string, scope M) (any, error) {
	return c.keysByScope(ctx, ds, scope)
}

func (c *Collection[K, V]) HttpLenScoped(ctx context.Context, ds string, scope M) (int64, error) {
	ks, err := c.keysByScope(ctx, ds, scope)
	if err != nil {
		return 0, err
	}
	return int64(len(ks)), nil
}

func (c *Collection[K, V]) HttpFind(ctx context.Context, ds string, filter M, scope M, opt FindOpt) (any, error) {
	return c.findDS(ctx, ds, mergeScope(filter, scope), opt)
}

// ---- scoped per-key operations ----
//
// For an owner-scoped collection, a per-key request must not trust the key
// alone: knowing another user's document id must not grant access. These methods
// intersect {_id: key} with the owner predicate. Writes use the atomic
// putScoped (filtered upsert) so there is no check-then-act window.

func (c *Collection[K, V]) HttpGetScoped(ctx context.Context, ds, key string, scope M) (any, error) {
	vs, err := c.findDS(ctx, ds, mergeScope(M{"_id": key}, scope), FindOpt{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, ErrNoDoc
	}
	return vs[0], nil
}

func (c *Collection[K, V]) HttpExistsScoped(ctx context.Context, ds, key string, scope M) (bool, error) {
	vs, err := c.findDS(ctx, ds, mergeScope(M{"_id": key}, scope), FindOpt{Limit: 1})
	if err != nil {
		return false, err
	}
	return len(vs) > 0, nil
}

func (c *Collection[K, V]) HttpSetScoped(ctx context.Context, ds, key string, params M, scope M) error {
	// scope is the server-derived owner predicate {ownerField: ownerVal} from the
	// verified JWT. Force it onto the document (non-forgeable: it overwrites
	// anything the client supplied), then write atomically: putScoped upserts
	// only the caller's own row and rejects a foreign-owned id with ErrForbidden.
	var ownerField, ownerVal string
	for k, v := range scope {
		ownerField, ownerVal = k, fmt.Sprint(v)
		params[k] = v
	}
	v, err := c.valueFromParams(params)
	if err != nil {
		return err
	}
	doc, err := c.encode(v)
	if err != nil {
		return err
	}
	return c.backend(ds).putScoped(ctx, c.coll, key, doc, ownerField, ownerVal)
}

func (c *Collection[K, V]) HttpDelScoped(ctx context.Context, ds, key string, scope M) error {
	mine, err := c.findDS(ctx, ds, mergeScope(M{"_id": key}, scope), FindOpt{Limit: 1})
	if err != nil {
		return err
	}
	if len(mine) == 0 {
		return ErrForbidden // not owned by caller (or absent)
	}
	_, err = c.backend(ds).del(ctx, c.coll, []string{key})
	return err
}

// ---- batch + query extras ----

func (c *Collection[K, V]) HttpMSet(ctx context.Context, ds string, items map[string]M, scope M) error {
	b := c.backend(ds)
	if len(scope) > 0 {
		// scoped: each row must be the caller's own -> atomic putScoped per item.
		var ownerField string
		var ownerVal any
		for k, v := range scope {
			ownerField, ownerVal = k, v
		}
		for id, fields := range items {
			if fields == nil {
				fields = M{}
			}
			fields[ownerField] = ownerVal // force owner (non-forgeable)
			v, err := c.valueFromParams(fields)
			if err != nil {
				return err
			}
			doc, err := c.encode(v)
			if err != nil {
				return err
			}
			if err := b.putScoped(ctx, c.coll, id, doc, ownerField, fmt.Sprint(ownerVal)); err != nil {
				return err
			}
		}
		return nil
	}
	ids := make([]string, 0, len(items))
	docs := make([][]byte, 0, len(items))
	for id, fields := range items {
		v, err := c.valueFromParams(fields)
		if err != nil {
			return err
		}
		doc, err := c.encode(v)
		if err != nil {
			return err
		}
		ids = append(ids, id)
		docs = append(docs, doc)
	}
	return b.putMany(ctx, c.coll, ids, docs)
}

func (c *Collection[K, V]) HttpMGet(ctx context.Context, ds string, scope M, keys ...string) (any, error) {
	if len(scope) > 0 {
		// scoped: return only the caller's own rows among those requested.
		safe, err := SanitizeFilter(mergeScope(M{"_id": M{"$in": keys}}, scope))
		if err != nil {
			return nil, err
		}
		ids, docs, err := c.backend(ds).find(ctx, c.coll, safe, FindOpt{})
		if err != nil {
			return nil, err
		}
		byID := make(map[string]V, len(ids))
		for i, id := range ids {
			if v, derr := c.decode(docs[i]); derr == nil {
				byID[id] = v
			}
		}
		out := make([]any, len(keys))
		for i, k := range keys {
			if v, ok := byID[k]; ok {
				out[i] = v
			}
		}
		return out, nil
	}
	docs, err := c.backend(ds).getMany(ctx, c.coll, keys)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(docs))
	for i, raw := range docs {
		if raw == nil {
			continue // missing -> null
		}
		if v, derr := c.decode(raw); derr == nil {
			out[i] = v
		}
	}
	return out, nil
}

func (c *Collection[K, V]) HttpCount(ctx context.Context, ds string, filter M, scope M) (int64, error) {
	merged := mergeScope(filter, scope)
	if len(merged) == 0 {
		return c.backend(ds).count(ctx, c.coll)
	}
	safe, err := SanitizeFilter(merged)
	if err != nil {
		return 0, err
	}
	return c.backend(ds).countFilter(ctx, c.coll, safe)
}

func (c *Collection[K, V]) HttpFindOne(ctx context.Context, ds string, filter M, scope M) (any, error) {
	vs, err := c.findDS(ctx, ds, mergeScope(filter, scope), FindOpt{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, ErrNoDoc
	}
	return vs[0], nil
}

func (c *Collection[K, V]) HttpWatch(ctx context.Context, ds string, scope M, emit func(op, id string, doc any) error) error {
	return c.backend(ds).watch(ctx, c.coll, scope, func(op, id string, raw []byte) error {
		var doc any
		if raw != nil {
			if d, derr := c.decode(raw); derr == nil {
				doc = d
			}
		}
		return emit(op, id, doc)
	})
}

// mergeScope ANDs a mandatory scope predicate with a caller filter so the scope
// cannot be widened by the caller.
func mergeScope(filter, scope M) M {
	if len(scope) == 0 {
		return filter
	}
	if len(filter) == 0 {
		return scope
	}
	return M{"$and": []any{scope, filter}}
}

// ----------------------------------------------------------------------------
// Owner scope policy (row-level isolation).
//
// Redis got per-user isolation for free from key naming (userInfo<uid>). Mongo
// has no key namespace, so isolation must be an explicit predicate. A collection
// declares which document field carries the owner and which JWT claim supplies
// the value; the HTTP layer injects {field: claim} into every collection-wide
// read and forbids the client from widening it.
// ----------------------------------------------------------------------------

type scopePolicy struct {
	field string // bson/json field on the document, e.g. "owner"
	claim string // JWT claim name, e.g. "uid"
}

var (
	ownerScopes   = map[string]scopePolicy{}
	ownerScopesMu sync.RWMutex
)

// SetOwnerScope declares that collection coll is row-isolated: collection-wide
// reads are filtered to documents whose docField equals the caller's jwtClaim.
func SetOwnerScope(coll, docField, jwtClaim string) {
	ownerScopesMu.Lock()
	defer ownerScopesMu.Unlock()
	ownerScopes[coll] = scopePolicy{field: docField, claim: jwtClaim}
}

// OwnerScope returns the mandatory filter for coll given the caller's claims, or
// nil if the collection is not scoped. Returns (nil, false) if scoped but the
// required claim is absent — callers MUST treat that as "deny".
func OwnerScope(coll string, claims map[string]any) (scope M, ok bool) {
	ownerScopesMu.RLock()
	p, scoped := ownerScopes[coll]
	ownerScopesMu.RUnlock()
	if !scoped {
		return nil, true // not scoped: no predicate required
	}
	v, present := claims[p.claim]
	if !present {
		return nil, false // scoped but unauthenticated -> deny
	}
	return M{p.field: v}, true
}

// IsOwnerScoped reports whether coll has a row-isolation policy.
func IsOwnerScoped(coll string) bool {
	ownerScopesMu.RLock()
	defer ownerScopesMu.RUnlock()
	_, ok := ownerScopes[coll]
	return ok
}
