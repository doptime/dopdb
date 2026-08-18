package dopdb

import (
	"context"
	"sort"
)

// ----------------------------------------------------------------------------
// SetCollection — the Redis Set key type, natively.
//
// Mongo held {_id, members:[M], owner?} and leaned on $addToSet/$pull to fake
// set semantics. KVRocks stores a real Redis set at <ns>:<coll>:<key>, so SADD/
// SREM/SMEMBERS/SISMEMBER/SCARD are the actual commands and deduplication is the
// server's job. Members are CBOR-encoded in canonical form, which is what makes
// deduplication correct: two equal values always produce identical bytes.
//
// SMEMBERS order is unspecified in Redis, so dopdb sorts the encoded members
// before decoding. A set has no order to preserve; a stable answer is worth more
// than the accident of the server's iteration.
// ----------------------------------------------------------------------------

// SetCollection is the typed handle to a Redis-Set collection.
type SetCollection[K comparable] struct {
	k keyspace
}

// NewSet constructs a Set collection. WithCollection names it.
func NewSet[K comparable](opts ...Option) *SetCollection[K] {
	return &SetCollection[K]{k: newKeyspace("NewSet", opts...)}
}

// Collection returns the collection name.
func (s *SetCollection[K]) Collection() string { return s.k.coll }

// HttpOn exposes this Set collection over HTTP and declares its command set.
func (s *SetCollection[K]) HttpOn(perms ...Perm) *SetCollection[K] {
	setHTTPPerm(s.k.coll, permsFrom(perms))
	RegisterHttp(s)
	return s
}

// SetAccessor is the runtime surface for S* commands.
type SetAccessor interface {
	HttpSAdd(ctx context.Context, ds, key string, members []any, scope M) error
	HttpSRem(ctx context.Context, ds, key string, members []any, scope M) error
	HttpSMembers(ctx context.Context, ds, key string, scope M) (any, error)
	HttpSIsMember(ctx context.Context, ds, key string, member any, scope M) (bool, error)
	HttpSCard(ctx context.Context, ds, key string, scope M) (int64, error)
}

// HttpSAdd adds members (Redis SADD). The key is created on first add.
func (s *SetCollection[K]) HttpSAdd(ctx context.Context, ds, key string, members []any, scope M) error {
	if len(members) == 0 {
		return nil
	}
	b := s.k.backend(ds)
	if err := b.claimOwner(ctx, s.k.coll, key, scope); err != nil {
		return err
	}
	args, err := encodeItems(members)
	if err != nil {
		return err
	}
	return b.rdb.SAdd(ctx, b.memberKey(s.k.coll, key), args...).Err()
}

// HttpSRem removes members (Redis SREM).
func (s *SetCollection[K]) HttpSRem(ctx context.Context, ds, key string, members []any, scope M) error {
	if len(members) == 0 {
		return nil
	}
	b := s.k.backend(ds)
	if err := b.claimOwner(ctx, s.k.coll, key, scope); err != nil {
		return err
	}
	args, err := encodeItems(members)
	if err != nil {
		return err
	}
	return b.rdb.SRem(ctx, b.memberKey(s.k.coll, key), args...).Err()
}

// HttpSMembers returns the members (empty if the key is absent).
func (s *SetCollection[K]) HttpSMembers(ctx context.Context, ds, key string, scope M) (any, error) {
	b := s.k.backend(ds)
	if err := b.checkOwner(ctx, s.k.coll, key, scope); err != nil {
		return []any{}, nil
	}
	raws, err := b.rdb.SMembers(ctx, b.memberKey(s.k.coll, key)).Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(raws) // Redis set iteration order is unspecified
	out, err := decodeItems(raws)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []any{}
	}
	return out, nil
}

// HttpSIsMember reports membership (Redis SISMEMBER).
func (s *SetCollection[K]) HttpSIsMember(ctx context.Context, ds, key string, member any, scope M) (bool, error) {
	b := s.k.backend(ds)
	if err := b.checkOwner(ctx, s.k.coll, key, scope); err != nil {
		return false, nil
	}
	raw, err := encodeCBOR(member)
	if err != nil {
		return false, err
	}
	return b.rdb.SIsMember(ctx, b.memberKey(s.k.coll, key), raw).Result()
}

// HttpSCard returns the member count (Redis SCARD).
func (s *SetCollection[K]) HttpSCard(ctx context.Context, ds, key string, scope M) (int64, error) {
	b := s.k.backend(ds)
	if err := b.checkOwner(ctx, s.k.coll, key, scope); err != nil {
		return 0, nil
	}
	return b.rdb.SCard(ctx, b.memberKey(s.k.coll, key)).Result()
}
