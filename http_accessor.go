package dopdb

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
)

// ----------------------------------------------------------------------------
// The type-erasure bridge.
//
// The HTTP layer dispatches by a collection NAME and a command STRING known
// only at runtime, but Collection[K,V] is generic. HttpAccessor is the
// non-generic surface the dispatcher calls; *Collection[K,V] implements it by
// deserializing the string key to K and boxing V as any. This mirrors how
// doptime erased types behind httpapi.ApiInterface / redisdb.IHttpKey.
//
// Keys arrive as strings (already @-resolved from the JWT by the HTTP layer);
// request bodies arrive as a merged param map and are decoded into V via a JSON
// round-trip, which honours the same json tags we keep equal to the bson tags.
// ----------------------------------------------------------------------------

// HttpAccessor is the runtime, non-generic view of a collection used by the
// HTTP dispatcher. Every method takes/returns engine-neutral types.
type HttpAccessor interface {
	Collection() string

	HttpGet(ctx context.Context, key string) (any, error)
	HttpSet(ctx context.Context, key string, params M) error
	HttpSetNX(ctx context.Context, key string, params M) (bool, error)
	HttpDel(ctx context.Context, keys ...string) error
	HttpExists(ctx context.Context, key string) (bool, error)

	// HttpGetAll returns all values; if scope is non-nil it is applied as a
	// mandatory filter (the row-level isolation predicate).
	HttpGetAll(ctx context.Context, scope M) (any, error)
	HttpKeys(ctx context.Context) (any, error)
	HttpLen(ctx context.Context) (int64, error)
	HttpIncrBy(ctx context.Context, key, field string, delta float64) error

	// HttpFind ANDs scope (if any) with the caller filter, then sanitizes.
	HttpFind(ctx context.Context, filter M, scope M, opt FindOpt) (any, error)

	// Scoped per-key operations enforce row-level ownership for collections that
	// declared an owner scope. Reads return only the caller's document; writes
	// refuse to overwrite a document owned by someone else.
	HttpGetScoped(ctx context.Context, key string, scope M) (any, error)
	HttpExistsScoped(ctx context.Context, key string, scope M) (bool, error)
	HttpSetScoped(ctx context.Context, key string, params M, scope M) error
	HttpDelScoped(ctx context.Context, key string, scope M) error
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

func (c *Collection[K, V]) HttpGet(_ context.Context, key string) (any, error) {
	k, err := c.deserializeKey(key)
	if err != nil {
		return nil, err
	}
	return c.HGet(k)
}

func (c *Collection[K, V]) HttpSet(_ context.Context, key string, params M) error {
	v, err := c.valueFromParams(params)
	if err != nil {
		return err
	}
	k, err := c.deserializeKey(key)
	if err != nil {
		return err
	}
	return c.HSet(k, v)
}

func (c *Collection[K, V]) HttpSetNX(_ context.Context, key string, params M) (bool, error) {
	v, err := c.valueFromParams(params)
	if err != nil {
		return false, err
	}
	k, err := c.deserializeKey(key)
	if err != nil {
		return false, err
	}
	return c.HSetNX(k, v)
}

func (c *Collection[K, V]) HttpDel(_ context.Context, keys ...string) error {
	ks := make([]K, len(keys))
	for i, s := range keys {
		k, err := c.deserializeKey(s)
		if err != nil {
			return err
		}
		ks[i] = k
	}
	return c.HDel(ks...)
}

func (c *Collection[K, V]) HttpExists(_ context.Context, key string) (bool, error) {
	k, err := c.deserializeKey(key)
	if err != nil {
		return false, err
	}
	return c.HExists(k)
}

func (c *Collection[K, V]) HttpGetAll(_ context.Context, scope M) (any, error) {
	if len(scope) > 0 {
		return c.Find(scope, FindOpt{})
	}
	return c.HVals()
}

func (c *Collection[K, V]) HttpKeys(_ context.Context) (any, error) {
	return c.HKeys()
}

func (c *Collection[K, V]) HttpLen(_ context.Context) (int64, error) {
	return c.HLen()
}

func (c *Collection[K, V]) HttpIncrBy(_ context.Context, key, field string, delta float64) error {
	k, err := c.deserializeKey(key)
	if err != nil {
		return err
	}
	id, err := c.serializeKey(k)
	if err != nil {
		return err
	}
	return c.store.Incr(context.Background(), c.coll, id, field, delta)
}

func (c *Collection[K, V]) HttpFind(_ context.Context, filter M, scope M, opt FindOpt) (any, error) {
	merged := mergeScope(filter, scope)
	return c.Find(merged, opt)
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

// ---- scoped per-key operations ----
//
// For an owner-scoped collection, a per-key request must not trust the key
// alone: knowing another user's document id must not grant access. These
// methods intersect {_id: key} with the owner predicate. The stored document
// always carries _id (both stores guarantee this), so the filter is uniform.
//
// NOTE: the write guard is check-then-act and therefore not atomic against a
// concurrent owner change. The atomic form needs a filtered upsert primitive on
// Store (updateOne({_id,owner:uid}, {$set,$setOnInsert})); that is a planned
// Store extension. For v1 the check closes the common hijack vector.

func (c *Collection[K, V]) HttpGetScoped(_ context.Context, key string, scope M) (any, error) {
	vs, err := c.Find(mergeScope(M{"_id": key}, scope), FindOpt{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, ErrNoDoc
	}
	return vs[0], nil
}

func (c *Collection[K, V]) HttpExistsScoped(_ context.Context, key string, scope M) (bool, error) {
	vs, err := c.Find(mergeScope(M{"_id": key}, scope), FindOpt{Limit: 1})
	if err != nil {
		return false, err
	}
	return len(vs) > 0, nil
}

func (c *Collection[K, V]) HttpSetScoped(ctx context.Context, key string, params M, scope M) error {
	// Does a document with this id exist that the caller does NOT own?
	all, err := c.Find(M{"_id": key}, FindOpt{Limit: 1})
	if err != nil {
		return err
	}
	if len(all) > 0 {
		mine, err := c.Find(mergeScope(M{"_id": key}, scope), FindOpt{Limit: 1})
		if err != nil {
			return err
		}
		if len(mine) == 0 {
			return ErrForbidden // exists, owned by someone else
		}
	}
	// Force the owner field(s) from the scope so the stored document is always
	// owned by the caller. This is non-forgeable: scope is derived from the
	// verified JWT, and these keys overwrite anything the client supplied.
	for k, v := range scope {
		params[k] = v
	}
	return c.HttpSet(ctx, key, params)
}

func (c *Collection[K, V]) HttpDelScoped(ctx context.Context, key string, scope M) error {
	mine, err := c.Find(mergeScope(M{"_id": key}, scope), FindOpt{Limit: 1})
	if err != nil {
		return err
	}
	if len(mine) == 0 {
		return ErrForbidden // not owned by caller (or absent)
	}
	return c.HttpDel(ctx, key)
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

// OwnerScope returns the mandatory filter for coll given the caller's claims,
// or nil if the collection is not scoped. Returns (nil, false) if scoped but the
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
