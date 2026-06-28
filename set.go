package dopdb

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ----------------------------------------------------------------------------
// SetCollection — Redis Set key type over Mongo.
// doc form: {_id, members:[M], owner?}. Members are a deduped array ($addToSet).
// ----------------------------------------------------------------------------

type setDoc struct {
	Members []any  `json:"members" bson:"members"`
	Owner   string `json:"owner,omitempty" bson:"owner,omitempty"`
}

type SetCollection[K comparable] struct {
	c *Collection[K, *setDoc]
}

func NewSet[K comparable](opts ...Option) *SetCollection[K] {
	return &SetCollection[K]{c: New[K, *setDoc](opts...)}
}

func (s *SetCollection[K]) Collection() string { return s.c.coll }

func (s *SetCollection[K]) HttpOn(perms ...Perm) *SetCollection[K] {
	p := All
	if len(perms) > 0 {
		p = 0
		for _, x := range perms {
			p |= x
		}
	}
	setHTTPPerm(s.c.coll, p)
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

func setFilter(key string, scope M) bson.M {
	f := bson.M{"_id": key}
	for k, v := range scope {
		f[k] = v
	}
	return f
}

// HttpSAdd adds members (Redis SADD). Upsert creates the doc on first add.
func (s *SetCollection[K]) HttpSAdd(ctx context.Context, ds, key string, members []any, scope M) error {
	if len(members) == 0 {
		return nil
	}
	_, err := s.c.backend(ds).c(s.c.coll).UpdateOne(ctx, setFilter(key, scope),
		bson.M{"$addToSet": bson.M{"members": bson.M{"$each": members}}},
		options.UpdateOne().SetUpsert(true))
	return err
}

// HttpSRem removes members (Redis SREM).
func (s *SetCollection[K]) HttpSRem(ctx context.Context, ds, key string, members []any, scope M) error {
	if len(members) == 0 {
		return nil
	}
	_, err := s.c.backend(ds).c(s.c.coll).UpdateOne(ctx, setFilter(key, scope),
		bson.M{"$pull": bson.M{"members": bson.M{"$in": members}}})
	return err
}

// HttpSMembers returns the member array (empty if the key is absent).
func (s *SetCollection[K]) HttpSMembers(ctx context.Context, ds, key string, scope M) (any, error) {
	var doc setDoc
	err := s.c.backend(ds).c(s.c.coll).FindOne(ctx, setFilter(key, scope)).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return []any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if doc.Members == nil {
		return []any{}, nil
	}
	return doc.Members, nil
}

// HttpSIsMember reports membership (Redis SISMEMBER).
func (s *SetCollection[K]) HttpSIsMember(ctx context.Context, ds, key string, member any, scope M) (bool, error) {
	f := setFilter(key, scope)
	f["members"] = member
	n, err := s.c.backend(ds).c(s.c.coll).CountDocuments(ctx, f, options.Count().SetLimit(1))
	return n > 0, err
}

// HttpSCard returns the member count (Redis SCARD).
func (s *SetCollection[K]) HttpSCard(ctx context.Context, ds, key string, scope M) (int64, error) {
	var doc setDoc
	err := s.c.backend(ds).c(s.c.coll).FindOne(ctx, setFilter(key, scope)).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int64(len(doc.Members)), nil
}
