package dopdb

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ----------------------------------------------------------------------------
// StringCollection — Redis String key type over Mongo.
//
// doc form: {_id, v, owner?, expireAt?}. The whole value lives in the single
// "v" field (doptime StringKey model). Embeds *Collection so it satisfies
// HttpAccessor (and may be registered), and adds its own StringAccessor surface
// for the STR* command family. Owner-scope reuses the same global maps as Hash
// (SetOwnerScope), with the owner field stored at the doc top level.
// ----------------------------------------------------------------------------

// strDoc is the stored form of a String value.
type strDoc struct {
	V        any        `json:"v" bson:"v"`
	Owner    string     `json:"owner,omitempty" bson:"owner,omitempty"`
	ExpireAt *time.Time `json:"expireAt,omitempty" bson:"expireAt,omitempty"`
}

// StringCollection is the typed handle to a Redis-String collection.
type StringCollection[K comparable] struct {
	c *Collection[K, *strDoc]
}

// NewString constructs a String collection. As with Hash, the collection name
// defaults to V's type unless overridden by WithCollection.
func NewString[K comparable](opts ...Option) *StringCollection[K] {
	return &StringCollection[K]{c: New[K, *strDoc](opts...)}
}

// EnsureTTL creates a TTL index on expireAt so documents with an expiration
// are auto-deleted by Mongo. Call once after registration when TTL is used.
// Collection returns the collection name. The embedded *Collection field is
// named "Collection", which shadows the promoted Collection() method — so we
// redeclare it here to satisfy the HttpAccessor interface.
func (s *StringCollection[K]) Collection() string { return s.c.coll }

// HttpOn exposes this String collection over HTTP and declares its command
// set (doptime HttpOn model). Overrides the embedded Collection.HttpOn so the
// registered accessor is the StringCollection (carrying StringAccessor), not
// the bare embedded Collection — otherwise dispatch's StringAccessor assertion
// would fail.
func (s *StringCollection[K]) HttpOn(perms ...Perm) *StringCollection[K] {
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

func (s *StringCollection[K]) EnsureTTL(ctx context.Context, ds string) error {
	_, err := s.c.backend(ds).c(s.c.coll).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expireAt", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	return err
}

// StringAccessor is the runtime surface the HTTP dispatcher calls for STR*
// commands. scope, when non-nil, is the owner-scope predicate {_id, owner}.
type StringAccessor interface {
	HttpStrGet(ctx context.Context, ds, key string, scope M) (any, error)
	HttpStrSet(ctx context.Context, ds, key string, value any, exp time.Duration, scope M) error
	HttpStrSetAll(ctx context.Context, ds string, items map[string]any, scope M) error
	HttpStrGetAll(ctx context.Context, ds, match string, scope M) (map[string]any, error)
	HttpStrDel(ctx context.Context, ds string, scope M, keys ...string) error
}

// HttpStrGet returns the bare value at key (Redis GET).
func (s *StringCollection[K]) HttpStrGet(ctx context.Context, ds, key string, scope M) (any, error) {
	filter := bson.M{"_id": key}
	for k, v := range scope {
		filter[k] = v
	}
	var doc strDoc
	err := s.c.backend(ds).c(s.c.coll).FindOne(ctx, filter).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	return doc.V, nil
}

// HttpStrSet sets the value at key (Redis SET). exp>0 sets an expireAt for TTL.
// Upsert: the filter carries the owner (scope) so a new doc is owned by caller.
func (s *StringCollection[K]) HttpStrSet(ctx context.Context, ds, key string, value any, exp time.Duration, scope M) error {
	setDoc := bson.M{"v": value}
	if exp > 0 {
		t := time.Now().Add(exp)
		setDoc["expireAt"] = t
	}
	filter := bson.M{"_id": key}
	for k, v := range scope {
		filter[k] = v
	}
	_, err := s.c.backend(ds).c(s.c.coll).UpdateOne(ctx, filter, bson.M{"$set": setDoc}, options.UpdateOne().SetUpsert(true))
	return err
}

// HttpStrSetAll sets many key→value pairs (Redis MSET).
func (s *StringCollection[K]) HttpStrSetAll(ctx context.Context, ds string, items map[string]any, scope M) error {
	if len(items) == 0 {
		return nil
	}
	var ops []mongo.WriteModel
	for k, v := range items {
		filter := bson.M{"_id": k}
		for sk, sv := range scope {
			filter[sk] = sv
		}
		ops = append(ops, mongo.NewUpdateOneModel().
			SetFilter(filter).
			SetUpdate(bson.M{"$set": bson.M{"v": v}}).
			SetUpsert(true))
	}
	_, err := s.c.backend(ds).c(s.c.coll).BulkWrite(ctx, ops)
	return err
}

// HttpStrGetAll returns {key: value} for all (or glob-matched) keys.
func (s *StringCollection[K]) HttpStrGetAll(ctx context.Context, ds, match string, scope M) (map[string]any, error) {
	filter := bson.M{}
	for k, v := range scope {
		filter[k] = v
	}
	if match != "" && match != "*" {
		filter["_id"] = bson.M{"$regex": globToRegex(match)}
	}
	cur, err := s.c.backend(ds).c(s.c.coll).Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := map[string]any{}
	for cur.Next(ctx) {
		var doc strDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		id, _ := cur.Current.Lookup("_id").StringValueOK()
		out[id] = doc.V
	}
	return out, cur.Err()
}

// HttpStrDel deletes one or more keys (Redis DEL).
func (s *StringCollection[K]) HttpStrDel(ctx context.Context, ds string, scope M, keys ...string) error {
	filter := bson.M{"_id": bson.M{"$in": keys}}
	for k, v := range scope {
		filter[k] = v
	}
	_, err := s.c.backend(ds).c(s.c.coll).DeleteMany(ctx, filter)
	return err
}
