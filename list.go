package dopdb

import (
	"context"
	"strconv"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ----------------------------------------------------------------------------
// ListCollection — Redis List key type over Mongo.
// doc form: {_id, items:[E], owner?}. Head = items[0] (Redis L* head semantics).
// ----------------------------------------------------------------------------

type listDoc[E any] struct {
	Items []E    `json:"items" bson:"items"`
	Owner string `json:"owner,omitempty" bson:"owner,omitempty"`
}

type ListCollection[K comparable, E any] struct {
	c *Collection[K, *listDoc[E]]
}

func NewList[K comparable, E any](opts ...Option) *ListCollection[K, E] {
	return &ListCollection[K, E]{c: New[K, *listDoc[E]](opts...)}
}

func (l *ListCollection[K, E]) Collection() string { return l.c.coll }

func (l *ListCollection[K, E]) HttpOn(perms ...Perm) *ListCollection[K, E] {
	p := All
	if len(perms) > 0 {
		p = 0
		for _, x := range perms {
			p |= x
		}
	}
	setHTTPPerm(l.c.coll, p)
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

func listFilter(key string, scope M) bson.M {
	f := bson.M{"_id": key}
	for k, v := range scope {
		f[k] = v
	}
	return f
}

func resolveIdx(n, idx int) int {
	if idx < 0 {
		return n + idx
	}
	return idx
}

func (l *ListCollection[K, E]) HttpLPush(ctx context.Context, ds, key string, items []any, scope M) error {
	if len(items) == 0 {
		return nil
	}
	_, err := l.c.backend(ds).c(l.c.coll).UpdateOne(ctx, listFilter(key, scope),
		bson.M{"$push": bson.M{"items": bson.M{"$each": items, "$position": 0}}},
		options.UpdateOne().SetUpsert(true))
	return err
}

func (l *ListCollection[K, E]) HttpRPush(ctx context.Context, ds, key string, items []any, scope M) error {
	if len(items) == 0 {
		return nil
	}
	_, err := l.c.backend(ds).c(l.c.coll).UpdateOne(ctx, listFilter(key, scope),
		bson.M{"$push": bson.M{"items": bson.M{"$each": items}}},
		options.UpdateOne().SetUpsert(true))
	return err
}

func (l *ListCollection[K, E]) HttpLPop(ctx context.Context, ds, key string, scope M) (any, error) {
	var doc listDoc[E]
	err := l.c.backend(ds).c(l.c.coll).FindOneAndUpdate(ctx, listFilter(key, scope),
		bson.M{"$pop": bson.M{"items": -1}}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	if len(doc.Items) == 0 {
		return nil, nil
	}
	return doc.Items[0], nil
}

func (l *ListCollection[K, E]) HttpRPop(ctx context.Context, ds, key string, scope M) (any, error) {
	var doc listDoc[E]
	err := l.c.backend(ds).c(l.c.coll).FindOneAndUpdate(ctx, listFilter(key, scope),
		bson.M{"$pop": bson.M{"items": 1}}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	if len(doc.Items) == 0 {
		return nil, nil
	}
	return doc.Items[len(doc.Items)-1], nil
}

func (l *ListCollection[K, E]) loadItems(ctx context.Context, ds, key string, scope M) ([]any, error) {
	var doc listDoc[E]
	err := l.c.backend(ds).c(l.c.coll).FindOne(ctx, listFilter(key, scope)).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]any, len(doc.Items))
	for i, v := range doc.Items {
		out[i] = v
	}
	return out, nil
}

func (l *ListCollection[K, E]) HttpLRange(ctx context.Context, ds, key string, start, stop int, scope M) (any, error) {
	items, err := l.loadItems(ctx, ds, key, scope)
	if err != nil {
		return nil, err
	}
	n := len(items)
	s := resolveIdx(n, start)
	if s < 0 {
		s = 0
	}
	e := resolveIdx(n, stop) + 1
	if e > n {
		e = n
	}
	if e < s {
		e = s
	}
	if s >= n {
		return []any{}, nil
	}
	return items[s:e], nil
}

func (l *ListCollection[K, E]) HttpLLen(ctx context.Context, ds, key string, scope M) (int64, error) {
	items, err := l.loadItems(ctx, ds, key, scope)
	if err != nil {
		return 0, err
	}
	return int64(len(items)), nil
}

func (l *ListCollection[K, E]) HttpLIndex(ctx context.Context, ds, key string, index int, scope M) (any, error) {
	items, err := l.loadItems(ctx, ds, key, scope)
	if err != nil {
		return nil, err
	}
	n := len(items)
	i := resolveIdx(n, index)
	if i < 0 || i >= n {
		return nil, nil
	}
	return items[i], nil
}

func (l *ListCollection[K, E]) HttpLSet(ctx context.Context, ds, key string, index int, item any, scope M) error {
	items, err := l.loadItems(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	n := len(items)
	i := resolveIdx(n, index)
	if i < 0 || i >= n {
		return ErrNoDoc
	}
	_, err = l.c.backend(ds).c(l.c.coll).UpdateOne(ctx, listFilter(key, scope),
		bson.M{"$set": bson.M{"items." + strconv.Itoa(i): item}})
	return err
}

func (l *ListCollection[K, E]) HttpLRem(ctx context.Context, ds, key string, count int, item any, scope M) error {
	items, err := l.loadItems(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	var kept []any
	removed := 0
	limit := count
	if count < 0 {
		// remove from tail
		for i := len(items) - 1; i >= 0; i-- {
			if items[i] == item && (count == 0 || removed < -count) {
				removed++
			} else {
				kept = append([]any{items[i]}, kept...)
			}
		}
	} else {
		for _, v := range items {
			if v == item && (count == 0 || removed < limit) {
				removed++
			} else {
				kept = append(kept, v)
			}
		}
	}
	_, err = l.c.backend(ds).c(l.c.coll).UpdateOne(ctx, listFilter(key, scope),
		bson.M{"$set": bson.M{"items": kept}})
	return err
}

func (l *ListCollection[K, E]) HttpLTrim(ctx context.Context, ds, key string, start, stop int, scope M) error {
	items, err := l.loadItems(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	n := len(items)
	s := resolveIdx(n, start)
	if s < 0 {
		s = 0
	}
	e := resolveIdx(n, stop) + 1
	if e > n {
		e = n
	}
	if e < s {
		e = s
	}
	var trimmed []any
	if s < n {
		trimmed = items[s:e]
	}
	_, err = l.c.backend(ds).c(l.c.coll).UpdateOne(ctx, listFilter(key, scope),
		bson.M{"$set": bson.M{"items": trimmed}})
	return err
}

func (l *ListCollection[K, E]) HttpLInsert(ctx context.Context, ds, key string, before bool, pivot, item any, scope M) error {
	items, err := l.loadItems(ctx, ds, key, scope)
	if err != nil {
		return err
	}
	out := make([]any, 0, len(items)+1)
	inserted := false
	for _, v := range items {
		if v == pivot && !inserted {
			if before {
				out = append(out, item, v)
			} else {
				out = append(out, v, item)
			}
			inserted = true
			continue
		}
		out = append(out, v)
	}
	if !inserted {
		return nil // pivot not found, redis returns 0 / -1; no change
	}
	_, err = l.c.backend(ds).c(l.c.coll).UpdateOne(ctx, listFilter(key, scope),
		bson.M{"$set": bson.M{"items": out}})
	return err
}
