package dopdb

import (
	"context"
	"time"
)

// ----------------------------------------------------------------------------
// StringCollection — the Redis String key type, natively.
//
// Mongo stored {_id, v, owner?, expireAt?} and needed a TTL index to expire it.
// KVRocks stores the CBOR-encoded value under <ns>:<coll>:<key> and expires it
// with EXPIRE, so STRSET's expiration is the server's own TTL rather than a
// background sweep of an index. Row isolation lives in the collection's owner
// index (kvrocks.go) because a bare string has no field to put an owner in.
// ----------------------------------------------------------------------------

// StringCollection is the typed handle to a Redis-String collection.
type StringCollection[K comparable] struct {
	k keyspace
}

// NewString constructs a String collection. WithCollection names it.
func NewString[K comparable](opts ...Option) *StringCollection[K] {
	return &StringCollection[K]{k: newKeyspace("NewString", opts...)}
}

// Collection returns the collection name.
func (s *StringCollection[K]) Collection() string { return s.k.coll }

// HttpOn exposes this String collection over HTTP and declares its command set
// (doptime HttpOn model). It registers the StringCollection itself, so the
// dispatcher's StringAccessor assertion succeeds.
func (s *StringCollection[K]) HttpOn(perms ...Perm) *StringCollection[K] {
	setHTTPPerm(s.k.coll, permsFrom(perms))
	RegisterHttp(s)
	return s
}

// EnsureTTL is retained for source compatibility and does nothing: KVRocks
// expires keys itself, so there is no TTL index to create. STRSET's
// ?expiration= is applied per key with EXPIRE at write time.
func (s *StringCollection[K]) EnsureTTL(ctx context.Context, ds string) error {
	_, _ = ctx, ds
	return nil
}

// StringAccessor is the runtime surface the HTTP dispatcher calls for STR*
// commands. scope, when non-nil, is the owner-scope predicate.
type StringAccessor interface {
	HttpStrGet(ctx context.Context, ds, key string, scope M) (any, error)
	HttpStrSet(ctx context.Context, ds, key string, value any, exp time.Duration, scope M) error
	HttpStrSetAll(ctx context.Context, ds string, items map[string]any, scope M) error
	HttpStrGetAll(ctx context.Context, ds, match string, scope M) (map[string]any, error)
	HttpStrDel(ctx context.Context, ds string, scope M, keys ...string) error
}

// HttpStrGet returns the bare value at key (Redis GET).
func (s *StringCollection[K]) HttpStrGet(ctx context.Context, ds, key string, scope M) (any, error) {
	b := s.k.backend(ds)
	if err := b.checkOwner(ctx, s.k.coll, key, scope); err != nil {
		// a foreign-owned key must look absent, not forbidden — same
		// non-leakage the Hash family gets from its scoped filter
		return nil, ErrNoDoc
	}
	raw, err := b.rdb.Get(ctx, b.memberKey(s.k.coll, key)).Bytes()
	if isRedisNil(err) {
		return nil, ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	var v any
	if err := decodeCBOR(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// HttpStrSet sets the value at key (Redis SET). exp>0 sets a native TTL.
func (s *StringCollection[K]) HttpStrSet(ctx context.Context, ds, key string, value any, exp time.Duration, scope M) error {
	b := s.k.backend(ds)
	if err := b.claimOwner(ctx, s.k.coll, key, scope); err != nil {
		return err
	}
	raw, err := encodeCBOR(value)
	if err != nil {
		return err
	}
	return b.rdb.Set(ctx, b.memberKey(s.k.coll, key), raw, exp).Err()
}

// HttpStrSetAll sets many key→value pairs (Redis MSET semantics, one pipeline).
func (s *StringCollection[K]) HttpStrSetAll(ctx context.Context, ds string, items map[string]any, scope M) error {
	if len(items) == 0 {
		return nil
	}
	b := s.k.backend(ds)
	pipe := b.rdb.Pipeline()
	for k, v := range items {
		if err := b.claimOwner(ctx, s.k.coll, k, scope); err != nil {
			return err
		}
		raw, err := encodeCBOR(v)
		if err != nil {
			return err
		}
		pipe.Set(ctx, b.memberKey(s.k.coll, k), raw, 0)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// HttpStrGetAll returns {key: value} for all (or glob-matched) keys.
func (s *StringCollection[K]) HttpStrGetAll(ctx context.Context, ds, match string, scope M) (map[string]any, error) {
	b := s.k.backend(ds)
	keys, err := b.ownedKeys(ctx, s.k.coll, match, scope)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(keys) == 0 {
		return out, nil
	}
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = b.memberKey(s.k.coll, k)
	}
	vals, err := b.rdb.MGet(ctx, full...).Result()
	if err != nil {
		return nil, err
	}
	for i, raw := range vals {
		str, ok := raw.(string)
		if !ok {
			continue // expired between SCAN and MGET
		}
		var v any
		if err := decodeCBOR([]byte(str), &v); err != nil {
			return nil, err
		}
		out[keys[i]] = v
	}
	return out, nil
}

// HttpStrDel deletes one or more keys (Redis DEL).
func (s *StringCollection[K]) HttpStrDel(ctx context.Context, ds string, scope M, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	b := s.k.backend(ds)
	del := make([]string, 0, len(keys))
	owned := make([]string, 0, len(keys))
	for _, k := range keys {
		if err := b.checkOwner(ctx, s.k.coll, k, scope); err != nil {
			continue // not the caller's key: silently skipped, as before
		}
		del = append(del, b.memberKey(s.k.coll, k))
		owned = append(owned, k)
	}
	if len(del) == 0 {
		return nil
	}
	if err := b.rdb.Del(ctx, del...).Err(); err != nil {
		return err
	}
	b.releaseOwner(ctx, s.k.coll, owned...)
	return nil
}
