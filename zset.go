package dopdb

import (
	"context"
	"math"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// ----------------------------------------------------------------------------
// ZSetCollection — the Redis Sorted-Set key type, natively.
//
// Mongo held {_id, members:[{m,score}], owner?} and re-derived the ordering on
// every read, because a document array has no order of its own. Every Z*
// command was therefore a read-modify-write, and two concurrent ZINCRBYs could
// lose one another.
//
// KVRocks stores a real sorted set at <ns>:<coll>:<key>: the ordering is the
// server's, ZADD/ZINCRBY/ZPOPMIN are atomic, and range queries are index reads
// rather than full-array sorts. Members are plain strings (Redis requires it),
// which the accessor signatures already assumed.
// ----------------------------------------------------------------------------

// ZSetMember is one (member, score) pair.
type ZSetMember struct {
	M     string  `json:"m"`
	Score float64 `json:"score"`
}

// ZSetCollection is the typed handle to a Redis-ZSet collection.
type ZSetCollection[K comparable] struct {
	k keyspace
}

// NewZSet constructs a ZSet collection. WithCollection names it.
func NewZSet[K comparable](opts ...Option) *ZSetCollection[K] {
	return &ZSetCollection[K]{k: newKeyspace("NewZSet", opts...)}
}

// Collection returns the collection name.
func (z *ZSetCollection[K]) Collection() string { return z.k.coll }

// HttpOn exposes this ZSet collection over HTTP and declares its command set.
func (z *ZSetCollection[K]) HttpOn(perms ...Perm) *ZSetCollection[K] {
	setHTTPPerm(z.k.coll, permsFrom(perms))
	RegisterHttp(z)
	return z
}

// ZSetAccessor is the runtime surface for Z* commands.
type ZSetAccessor interface {
	HttpZAdd(ctx context.Context, ds, key string, pairs map[string]float64, scope M) (int, error)
	HttpZRem(ctx context.Context, ds, key string, members []string, scope M) (int, error)
	HttpZScore(ctx context.Context, ds, key, member string, scope M) (float64, error)
	HttpZCard(ctx context.Context, ds, key string, scope M) (int64, error)
	HttpZCount(ctx context.Context, ds, key string, min, max float64, scope M) (int64, error)
	HttpZIncrBy(ctx context.Context, ds, key, member string, inc float64, scope M) (float64, error)
	HttpZRange(ctx context.Context, ds, key string, start, stop int, rev, withScores bool, scope M) (any, error)
	HttpZRangeByScore(ctx context.Context, ds, key string, min, max float64, rev, withScores bool, scope M) (any, error)
	HttpZRank(ctx context.Context, ds, key, member string, rev bool, scope M) (int, error)
	HttpZPop(ctx context.Context, ds, key string, count int, rev bool, scope M) (any, error)
	HttpZRemRangeByRank(ctx context.Context, ds, key string, start, stop int, scope M) (int, error)
	HttpZRemRangeByScore(ctx context.Context, ds, key string, min, max float64, scope M) (int, error)
}

// scoreArg renders a score bound the way Redis range commands expect, including
// the infinities the HTTP layer uses as "unbounded".
func scoreArg(f float64) string {
	switch {
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsInf(f, 1):
		return "+inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func zrender(ms []redis.Z, withScores bool) any {
	if withScores {
		out := make([]map[string]any, len(ms))
		for i, m := range ms {
			out[i] = map[string]any{"m": memberString(m.Member), "score": m.Score}
		}
		return out
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = memberString(m.Member)
	}
	return out
}

func memberString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (z *ZSetCollection[K]) rw(ctx context.Context, ds, key string, scope M) (*kvBackend, string, error) {
	b := z.k.backend(ds)
	if err := b.claimOwner(ctx, z.k.coll, key, scope); err != nil {
		return nil, "", err
	}
	return b, b.memberKey(z.k.coll, key), nil
}

func (z *ZSetCollection[K]) ro(ctx context.Context, ds, key string, scope M) (*kvBackend, string, error) {
	b := z.k.backend(ds)
	if err := b.checkOwner(ctx, z.k.coll, key, scope); err != nil {
		return nil, "", err
	}
	return b, b.memberKey(z.k.coll, key), nil
}

func (z *ZSetCollection[K]) HttpZAdd(ctx context.Context, ds, key string, pairs map[string]float64, scope M) (int, error) {
	b, rk, err := z.rw(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	if len(pairs) == 0 {
		return 0, nil
	}
	zs := make([]redis.Z, 0, len(pairs))
	for m, s := range pairs {
		zs = append(zs, redis.Z{Member: m, Score: s})
	}
	n, err := b.rdb.ZAdd(ctx, rk, zs...).Result()
	return int(n), err
}

func (z *ZSetCollection[K]) HttpZRem(ctx context.Context, ds, key string, members []string, scope M) (int, error) {
	b, rk, err := z.rw(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	if len(members) == 0 {
		return 0, nil
	}
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	n, err := b.rdb.ZRem(ctx, rk, args...).Result()
	return int(n), err
}

func (z *ZSetCollection[K]) HttpZScore(ctx context.Context, ds, key, member string, scope M) (float64, error) {
	b, rk, err := z.ro(ctx, ds, key, scope)
	if err != nil {
		return 0, ErrNoDoc
	}
	s, err := b.rdb.ZScore(ctx, rk, member).Result()
	if isRedisNil(err) {
		return 0, ErrNoDoc
	}
	return s, err
}

func (z *ZSetCollection[K]) HttpZCard(ctx context.Context, ds, key string, scope M) (int64, error) {
	b, rk, err := z.ro(ctx, ds, key, scope)
	if err != nil {
		return 0, nil
	}
	return b.rdb.ZCard(ctx, rk).Result()
}

func (z *ZSetCollection[K]) HttpZCount(ctx context.Context, ds, key string, min, max float64, scope M) (int64, error) {
	b, rk, err := z.ro(ctx, ds, key, scope)
	if err != nil {
		return 0, nil
	}
	return b.rdb.ZCount(ctx, rk, scoreArg(min), scoreArg(max)).Result()
}

func (z *ZSetCollection[K]) HttpZIncrBy(ctx context.Context, ds, key, member string, inc float64, scope M) (float64, error) {
	b, rk, err := z.rw(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	return b.rdb.ZIncrBy(ctx, rk, inc, member).Result()
}

func (z *ZSetCollection[K]) HttpZRange(ctx context.Context, ds, key string, start, stop int, rev, withScores bool, scope M) (any, error) {
	b, rk, err := z.ro(ctx, ds, key, scope)
	if err != nil {
		return zrender(nil, withScores), nil
	}
	var (
		ms   []redis.Z
		rerr error
	)
	if rev {
		ms, rerr = b.rdb.ZRevRangeWithScores(ctx, rk, int64(start), int64(stop)).Result()
	} else {
		ms, rerr = b.rdb.ZRangeWithScores(ctx, rk, int64(start), int64(stop)).Result()
	}
	if rerr != nil && !isRedisNil(rerr) {
		return nil, rerr
	}
	return zrender(ms, withScores), nil
}

func (z *ZSetCollection[K]) HttpZRangeByScore(ctx context.Context, ds, key string, min, max float64, rev, withScores bool, scope M) (any, error) {
	b, rk, err := z.ro(ctx, ds, key, scope)
	if err != nil {
		return zrender(nil, withScores), nil
	}
	by := &redis.ZRangeBy{Min: scoreArg(min), Max: scoreArg(max)}
	var (
		ms   []redis.Z
		rerr error
	)
	if rev {
		ms, rerr = b.rdb.ZRevRangeByScoreWithScores(ctx, rk, by).Result()
	} else {
		ms, rerr = b.rdb.ZRangeByScoreWithScores(ctx, rk, by).Result()
	}
	if rerr != nil && !isRedisNil(rerr) {
		return nil, rerr
	}
	return zrender(ms, withScores), nil
}

// HttpZRank returns the 0-based rank, or -1 when the member is absent (the
// previous contract; Redis reports a nil reply for the same case).
func (z *ZSetCollection[K]) HttpZRank(ctx context.Context, ds, key, member string, rev bool, scope M) (int, error) {
	b, rk, err := z.ro(ctx, ds, key, scope)
	if err != nil {
		return -1, nil
	}
	var (
		n    int64
		rerr error
	)
	if rev {
		n, rerr = b.rdb.ZRevRank(ctx, rk, member).Result()
	} else {
		n, rerr = b.rdb.ZRank(ctx, rk, member).Result()
	}
	if isRedisNil(rerr) {
		return -1, nil
	}
	if rerr != nil {
		return -1, rerr
	}
	return int(n), nil
}

// HttpZPop pops from the low (ZPOPMIN) or high (ZPOPMAX) end. Results always
// carry scores, as before.
func (z *ZSetCollection[K]) HttpZPop(ctx context.Context, ds, key string, count int, rev bool, scope M) (any, error) {
	b, rk, err := z.rw(ctx, ds, key, scope)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 1
	}
	var (
		ms   []redis.Z
		rerr error
	)
	if rev {
		ms, rerr = b.rdb.ZPopMax(ctx, rk, int64(count)).Result()
	} else {
		ms, rerr = b.rdb.ZPopMin(ctx, rk, int64(count)).Result()
	}
	if rerr != nil && !isRedisNil(rerr) {
		return nil, rerr
	}
	return zrender(ms, true), nil
}

func (z *ZSetCollection[K]) HttpZRemRangeByRank(ctx context.Context, ds, key string, start, stop int, scope M) (int, error) {
	b, rk, err := z.rw(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	n, err := b.rdb.ZRemRangeByRank(ctx, rk, int64(start), int64(stop)).Result()
	return int(n), err
}

func (z *ZSetCollection[K]) HttpZRemRangeByScore(ctx context.Context, ds, key string, min, max float64, scope M) (int, error) {
	b, rk, err := z.rw(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	n, err := b.rdb.ZRemRangeByScore(ctx, rk, scoreArg(min), scoreArg(max)).Result()
	return int(n), err
}
