package dopdb

import (
	"context"
	"sort"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ----------------------------------------------------------------------------
// ZSetCollection — Redis Sorted-Set key type over Mongo.
// doc form: {_id, members:[{m, score}], owner?}. Order is NOT stored; it is
// derived by sorting (score asc, then m asc) on every read, mirroring Redis
// ZSET semantics. All ops are read-modify-write so Go and TS stay in lock-step
// (no Mongo aggregation divergence to reconcile).
// ----------------------------------------------------------------------------

// ZSetMember is one (member, score) pair.
type ZSetMember struct {
	M     string  `json:"m" bson:"m"`
	Score float64 `json:"score" bson:"score"`
}

type zsetDoc struct {
	Members []ZSetMember `json:"members" bson:"members"`
	Owner   string       `json:"owner,omitempty" bson:"owner,omitempty"`
}

type ZSetCollection[K comparable] struct {
	c *Collection[K, *zsetDoc]
}

func NewZSet[K comparable](opts ...Option) *ZSetCollection[K] {
	return &ZSetCollection[K]{c: New[K, *zsetDoc](opts...)}
}

func (z *ZSetCollection[K]) Collection() string { return z.c.coll }

func (z *ZSetCollection[K]) HttpOn(perms ...Perm) *ZSetCollection[K] {
	p := All
	if len(perms) > 0 {
		p = 0
		for _, x := range perms {
			p |= x
		}
	}
	setHTTPPerm(z.c.coll, p)
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

func zfilter(key string, scope M) bson.M {
	f := bson.M{"_id": key}
	for k, v := range scope {
		f[k] = v
	}
	return f
}

func zsort(ms []ZSetMember, rev bool) {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].Score != ms[j].Score {
			if rev {
				return ms[i].Score > ms[j].Score
			}
			return ms[i].Score < ms[j].Score
		}
		if rev {
			return ms[i].M > ms[j].M
		}
		return ms[i].M < ms[j].M
	})
}

func (z *ZSetCollection[K]) load(ctx context.Context, ds, key string, scope M) ([]ZSetMember, error) {
	var doc zsetDoc
	err := z.c.backend(ds).c(z.c.coll).FindOne(ctx, zfilter(key, scope)).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return doc.Members, nil
}

func (z *ZSetCollection[K]) save(ctx context.Context, ds, key string, scope M, ms []ZSetMember) error {
	_, err := z.c.backend(ds).c(z.c.coll).UpdateOne(ctx, zfilter(key, scope),
		bson.M{"$set": bson.M{"members": ms}}, options.UpdateOne().SetUpsert(true))
	return err
}

func zresolveIdx(n, idx int) int {
	if idx < 0 {
		return n + idx
	}
	return idx
}

func zclip(s, e, n int) (int, int) {
	if s < 0 {
		s = 0
	}
	if e > n {
		e = n
	}
	if e < s {
		e = s
	}
	return s, e
}

func zrender(ms []ZSetMember, withScores bool) any {
	if withScores {
		out := make([]map[string]any, len(ms))
		for i, m := range ms {
			out[i] = map[string]any{"m": m.M, "score": m.Score}
		}
		return out
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.M
	}
	return out
}

func (z *ZSetCollection[K]) HttpZAdd(ctx context.Context, ds, key string, pairs map[string]float64, scope M) (int, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	idx := map[string]int{}
	for i, m := range ms {
		idx[m.M] = i
	}
	added := 0
	for m, score := range pairs {
		if i, ok := idx[m]; ok {
			ms[i].Score = score
		} else {
			ms = append(ms, ZSetMember{M: m, Score: score})
			idx[m] = len(ms) - 1
			added++
		}
	}
	zsort(ms, false)
	return added, z.save(ctx, ds, key, scope, ms)
}

func (z *ZSetCollection[K]) HttpZRem(ctx context.Context, ds, key string, members []string, scope M) (int, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	gone := map[string]bool{}
	for _, m := range members {
		gone[m] = true
	}
	kept := ms[:0]
	removed := 0
	for _, m := range ms {
		if gone[m.M] {
			removed++
			continue
		}
		kept = append(kept, m)
	}
	return removed, z.save(ctx, ds, key, scope, kept)
}

func (z *ZSetCollection[K]) HttpZScore(ctx context.Context, ds, key, member string, scope M) (float64, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	for _, m := range ms {
		if m.M == member {
			return m.Score, nil
		}
	}
	return 0, ErrNoDoc
}

func (z *ZSetCollection[K]) HttpZCard(ctx context.Context, ds, key string, scope M) (int64, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	return int64(len(ms)), nil
}

func (z *ZSetCollection[K]) HttpZCount(ctx context.Context, ds, key string, min, max float64, scope M) (int64, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, m := range ms {
		if m.Score >= min && m.Score <= max {
			n++
		}
	}
	return n, nil
}

func (z *ZSetCollection[K]) HttpZIncrBy(ctx context.Context, ds, key, member string, inc float64, scope M) (float64, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	found := false
	var ns float64
	for i := range ms {
		if ms[i].M == member {
			ms[i].Score += inc
			ns = ms[i].Score
			found = true
			break
		}
	}
	if !found {
		ms = append(ms, ZSetMember{M: member, Score: inc})
		ns = inc
	}
	zsort(ms, false)
	return ns, z.save(ctx, ds, key, scope, ms)
}

func (z *ZSetCollection[K]) HttpZRange(ctx context.Context, ds, key string, start, stop int, rev, withScores bool, scope M) (any, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return nil, err
	}
	zsort(ms, rev)
	n := len(ms)
	s, e := zclip(zresolveIdx(n, start), zresolveIdx(n, stop)+1, n)
	if s >= n {
		return zrender(nil, withScores), nil
	}
	return zrender(ms[s:e], withScores), nil
}

func (z *ZSetCollection[K]) HttpZRangeByScore(ctx context.Context, ds, key string, min, max float64, rev, withScores bool, scope M) (any, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return nil, err
	}
	var sub []ZSetMember
	for _, m := range ms {
		if m.Score >= min && m.Score <= max {
			sub = append(sub, m)
		}
	}
	zsort(sub, rev)
	return zrender(sub, withScores), nil
}

func (z *ZSetCollection[K]) HttpZRank(ctx context.Context, ds, key, member string, rev bool, scope M) (int, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return -1, err
	}
	zsort(ms, rev)
	for i, m := range ms {
		if m.M == member {
			return i, nil
		}
	}
	return -1, nil
}

func (z *ZSetCollection[K]) HttpZPop(ctx context.Context, ds, key string, count int, rev bool, scope M) (any, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return nil, err
	}
	zsort(ms, rev)
	if count <= 0 {
		count = 1
	}
	if count > len(ms) {
		count = len(ms)
	}
	popped := ms[:count]
	rest := ms[count:]
	// deep-copy popped for rendering (rest is saved)
	popCopy := make([]ZSetMember, len(popped))
	copy(popCopy, popped)
	if err := z.save(ctx, ds, key, scope, rest); err != nil {
		return nil, err
	}
	return zrender(popCopy, true), nil
}

func (z *ZSetCollection[K]) HttpZRemRangeByRank(ctx context.Context, ds, key string, start, stop int, scope M) (int, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	zsort(ms, false)
	n := len(ms)
	s, e := zclip(zresolveIdx(n, start), zresolveIdx(n, stop)+1, n)
	removed := 0
	if s < n {
		removed = e - s
	}
	var kept []ZSetMember
	kept = append(kept, ms[:s]...)
	kept = append(kept, ms[e:]...)
	return removed, z.save(ctx, ds, key, scope, kept)
}

func (z *ZSetCollection[K]) HttpZRemRangeByScore(ctx context.Context, ds, key string, min, max float64, scope M) (int, error) {
	ms, err := z.load(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	kept := ms[:0]
	removed := 0
	for _, m := range ms {
		if m.Score >= min && m.Score <= max {
			removed++
			continue
		}
		kept = append(kept, m)
	}
	return removed, z.save(ctx, ds, key, scope, kept)
}
