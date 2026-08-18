package dopdb

import (
	"context"
	"strings"

	"github.com/redis/go-redis/v9"
)

// ----------------------------------------------------------------------------
// ListCollection — the Redis List key type, natively.
//
// Mongo held {_id, items:[E], owner?} and emulated every L*/R* command with
// $push/$pop/$set array surgery — several of them as read-modify-write, which
// is why LREM and LINSERT could lose a concurrent update. On KVRocks the list
// IS a Redis list at <ns>:<coll>:<key>, so LPUSH/RPUSH/LPOP/RPOP/LRANGE/LLEN/
// LINDEX/LSET/LREM/LTRIM/LINSERT are the server's own atomic commands. Elements
// are stored CBOR-encoded so any JSON value survives the round trip.
//
// One deliberate carry-over: LPUSH with several items inserts them at the head
// in the order given ([a,b] onto [c] yields [a,b,c]), which is what the Mongo
// build did. Raw Redis LPUSH would yield [b,a,c]; dopdb reverses before pushing
// so existing callers and the TypeScript engine keep agreeing.
// ----------------------------------------------------------------------------

// ListCollection is the typed handle to a Redis-List collection. E documents the
// element type for callers; values cross the HTTP boundary as JSON.
type ListCollection[K comparable, E any] struct {
	k keyspace
}

// NewList constructs a List collection. WithCollection names it.
func NewList[K comparable, E any](opts ...Option) *ListCollection[K, E] {
	return &ListCollection[K, E]{k: newKeyspace("NewList", opts...)}
}

// Collection returns the collection name.
func (l *ListCollection[K, E]) Collection() string { return l.k.coll }

// HttpOn exposes this List collection over HTTP and declares its command set.
func (l *ListCollection[K, E]) HttpOn(perms ...Perm) *ListCollection[K, E] {
	setHTTPPerm(l.k.coll, permsFrom(perms))
	RegisterHttp(l)
	return l
}

// ListAccessor is the runtime surface for L*/R* commands.
type ListAccessor interface {
	HttpLPush(ctx context.Context, ds, key string, items []any, scope M) error
	HttpRPush(ctx context.Context, ds, key string, items []any, scope M) error
	HttpLPop(ctx context.Context, ds, key string, scope M) (any, error)
	HttpRPop(ctx context.Context, ds, key string, scope M) (any, error)
	HttpLRange(ctx context.Context, ds, key string, start, stop int, scope M) (any, error)
	HttpLLen(ctx context.Context, ds, key string, scope M) (int64, error)
	HttpLIndex(ctx context.Context, ds, key string, index int, scope M) (any, error)
	HttpLSet(ctx context.Context, ds, key string, index int, item any, scope M) error
	HttpLRem(ctx context.Context, ds, key string, count int, item any, scope M) error
	HttpLTrim(ctx context.Context, ds, key string, start, stop int, scope M) error
	HttpLInsert(ctx context.Context, ds, key string, before bool, pivot, item any, scope M) error
}

// encodeItems CBOR-encodes a batch of wire values into Redis arguments.
func encodeItems(items []any) ([]any, error) {
	out := make([]any, len(items))
	for i, it := range items {
		b, err := encodeCBOR(it)
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}

func decodeItem(raw string) (any, error) {
	var v any
	if err := decodeCBOR([]byte(raw), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func decodeItems(raws []string) ([]any, error) {
	out := make([]any, len(raws))
	for i, r := range raws {
		v, err := decodeItem(r)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// rw resolves the backend and enforces the write-side ownership claim.
func (l *ListCollection[K, E]) rw(ctx context.Context, ds, key string, scope M) (*kvBackend, string, error) {
	b := l.k.backend(ds)
	rk, err := b.entryKey(l.k.coll, key)
	if err != nil {
		return nil, "", err
	}
	if err := b.claimOwner(ctx, l.k.coll, key, scope); err != nil {
		return nil, "", err
	}
	return b, rk, nil
}

// ro resolves the backend and enforces the read-side ownership check.
func (l *ListCollection[K, E]) ro(ctx context.Context, ds, key string, scope M) (*kvBackend, string, error) {
	b := l.k.backend(ds)
	rk, err := b.entryKey(l.k.coll, key)
	if err != nil {
		return nil, "", err
	}
	if err := b.checkOwner(ctx, l.k.coll, key, scope); err != nil {
		return nil, "", err
	}
	return b, rk, nil
}

func (l *ListCollection[K, E]) HttpLPush(ctx context.Context, ds, key string, items []any, scope M) error {
	if len(items) == 0 {
		return nil
	}
	b, rk, err := l.rw(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	// reverse so the batch lands head-first in the order the caller gave
	rev := make([]any, len(items))
	for i, it := range items {
		rev[len(items)-1-i] = it
	}
	args, err := encodeItems(rev)
	if err != nil {
		return err
	}
	return b.rdb.LPush(ctx, rk, args...).Err()
}

func (l *ListCollection[K, E]) HttpRPush(ctx context.Context, ds, key string, items []any, scope M) error {
	if len(items) == 0 {
		return nil
	}
	b, rk, err := l.rw(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	args, err := encodeItems(items)
	if err != nil {
		return err
	}
	return b.rdb.RPush(ctx, rk, args...).Err()
}

func (l *ListCollection[K, E]) HttpLPop(ctx context.Context, ds, key string, scope M) (any, error) {
	b, rk, err := l.rw(ctx, ds, key, scope)
	if err != nil {
		return nil, err
	}
	raw, err := b.rdb.LPop(ctx, rk).Result()
	if isRedisNil(err) {
		return nil, ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	// Redis drops the key when the last element goes; the claim must go too.
	b.releaseIfEmpty(ctx, l.k.coll, key, scope)
	return decodeItem(raw)
}

func (l *ListCollection[K, E]) HttpRPop(ctx context.Context, ds, key string, scope M) (any, error) {
	b, rk, err := l.rw(ctx, ds, key, scope)
	if err != nil {
		return nil, err
	}
	raw, err := b.rdb.RPop(ctx, rk).Result()
	if isRedisNil(err) {
		return nil, ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	// Redis drops the key when the last element goes; the claim must go too.
	b.releaseIfEmpty(ctx, l.k.coll, key, scope)
	return decodeItem(raw)
}

func (l *ListCollection[K, E]) HttpLRange(ctx context.Context, ds, key string, start, stop int, scope M) (any, error) {
	b, rk, err := l.ro(ctx, ds, key, scope)
	if err != nil {
		return []any{}, nil
	}
	raws, err := b.rdb.LRange(ctx, rk, int64(start), int64(stop)).Result()
	if err != nil {
		return nil, err
	}
	items, err := decodeItems(raws)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []any{}
	}
	return items, nil
}

func (l *ListCollection[K, E]) HttpLLen(ctx context.Context, ds, key string, scope M) (int64, error) {
	b, rk, err := l.ro(ctx, ds, key, scope)
	if err != nil {
		return 0, nil
	}
	return b.rdb.LLen(ctx, rk).Result()
}

func (l *ListCollection[K, E]) HttpLIndex(ctx context.Context, ds, key string, index int, scope M) (any, error) {
	b, rk, err := l.ro(ctx, ds, key, scope)
	if err != nil {
		return nil, nil
	}
	raw, err := b.rdb.LIndex(ctx, rk, int64(index)).Result()
	if isRedisNil(err) {
		return nil, nil // out of range: null, as before
	}
	if err != nil {
		return nil, err
	}
	return decodeItem(raw)
}

func (l *ListCollection[K, E]) HttpLSet(ctx context.Context, ds, key string, index int, item any, scope M) error {
	b, rk, err := l.rw(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	raw, err := encodeCBOR(item)
	if err != nil {
		return err
	}
	err = b.rdb.LSet(ctx, rk, int64(index), raw).Err()
	if err != nil && isOutOfRange(err) {
		return ErrNoDoc // index outside the list, or no such key
	}
	return err
}

func (l *ListCollection[K, E]) HttpLRem(ctx context.Context, ds, key string, count int, item any, scope M) error {
	b, rk, err := l.rw(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	raw, err := encodeCBOR(item)
	if err != nil {
		return err
	}
	if err := b.rdb.LRem(ctx, rk, int64(count), raw).Err(); err != nil {
		return err
	}
	b.releaseIfEmpty(ctx, l.k.coll, key, scope)
	return nil
}

func (l *ListCollection[K, E]) HttpLTrim(ctx context.Context, ds, key string, start, stop int, scope M) error {
	b, rk, err := l.rw(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	if err := b.rdb.LTrim(ctx, rk, int64(start), int64(stop)).Err(); err != nil {
		return err
	}
	b.releaseIfEmpty(ctx, l.k.coll, key, scope)
	return nil
}

func (l *ListCollection[K, E]) HttpLInsert(ctx context.Context, ds, key string, before bool, pivot, item any, scope M) error {
	b, rk, err := l.rw(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	praw, err := encodeCBOR(pivot)
	if err != nil {
		return err
	}
	iraw, err := encodeCBOR(item)
	if err != nil {
		return err
	}
	op := "AFTER"
	if before {
		op = "BEFORE"
	}
	// LINSERT answers -1 when the pivot is absent; that is not an error here,
	// matching the previous "pivot not found -> no change" behaviour.
	return b.rdb.LInsert(ctx, rk, op, praw, iraw).Err()
}

// isOutOfRange recognises the LSET index error without string-matching every
// server dialect more than necessary (KVRocks and Redis both say "index out of
// range"; a missing key answers "no such key").
func isOutOfRange(err error) bool {
	if err == nil || err == redis.Nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "out of range") || strings.Contains(msg, "no such key")
}
