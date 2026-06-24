// Package mongostore implements dopdb.Store on top of MongoDB.
//
// This is the ONLY file in dopdb that imports the Mongo driver. Everything
// typed (Collection[K,V], modifiers, sanitizer) depends on the narrow
// dopdb.Store interface, so the database is swappable and the core is testable
// without a server.
//
// Targets go.mongodb.org/mongo-driver/v2. To use it, add to your go.mod:
//
//	require go.mongodb.org/mongo-driver/v2 v2.x.y
//
// then wire it once at startup:
//
//	st, _ := mongostore.New(ctx, "mongodb://localhost:27017", "appdb")
//	dopdb.SetDefaultStore(st)
//	dopdb.SetDefaultCodec(mongostore.BSONCodec{})
//
// NOTE: this file could not be compiled in the authoring sandbox (the Mongo
// driver's vanity-import domains are unreachable there). Compile it in your
// environment; if a v2 point-release shifted an options signature, the fix is
// mechanical. The logic and the Store contract are settled.
package mongostore

import (
	"context"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/config"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// BSONCodec is the production document codec: documents are stored as BSON,
// which is queryable, inspectable (Compass/mongosh), and language-neutral —
// the "format as interop contract" fix from the review. Field names come from
// `bson:"..."` tags.
type BSONCodec struct{}

func (BSONCodec) Marshal(v any) ([]byte, error)   { return bson.Marshal(v) }
func (BSONCodec) Unmarshal(b []byte, v any) error { return bson.Unmarshal(b, v) }

// Store implements dopdb.Store against a single Mongo database.
type Store struct {
	db *mongo.Database
}

// New connects to MongoDB and returns a Store bound to dbName.
func New(ctx context.Context, uri, dbName string) (*Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}
	return &Store{db: client.Database(dbName)}, nil
}

// NewFromSource wires a Store from a resolved config.MongoSource. Typical
// startup:
//
//	cfg, _ := config.Load("config.toml")
//	st, _ := mongostore.NewFromSource(ctx, cfg.Default())
//	dopdb.SetDefaultStore(st)
//	dopdb.SetDefaultCodec(mongostore.BSONCodec{})
func NewFromSource(ctx context.Context, src config.MongoSource) (*Store, error) {
	return New(ctx, src.URI, src.DB)
}

func (s *Store) c(coll string) *mongo.Collection { return s.db.Collection(coll) }

func (s *Store) EnsureIndex(ctx context.Context, coll string, idx dopdb.IndexSpec) error {
	keys := bson.D{}
	for _, k := range idx.Keys {
		dir := int32(1)
		if !k.Asc {
			dir = -1
		}
		keys = append(keys, bson.E{Key: k.Field, Value: dir})
	}
	for _, tf := range idx.Text {
		keys = append(keys, bson.E{Key: tf, Value: "text"})
	}
	opt := options.Index()
	if idx.Unique {
		opt = opt.SetUnique(true)
	}
	if idx.Name != "" {
		opt = opt.SetName(idx.Name)
	}
	_, err := s.c(coll).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: keys, Options: opt})
	return err
}

// withID decodes a value-bytes document and forces _id = id so the upsert is
// keyed deterministically regardless of whether V carries an _id field.
func withID(id string, doc []byte) (bson.M, error) {
	m := bson.M{}
	if len(doc) > 0 {
		if err := bson.Unmarshal(doc, &m); err != nil {
			return nil, err
		}
	}
	m["_id"] = id
	return m, nil
}

func (s *Store) Put(ctx context.Context, coll, id string, doc []byte) error {
	m, err := withID(id, doc)
	if err != nil {
		return err
	}
	_, err = s.c(coll).ReplaceOne(ctx, bson.M{"_id": id}, m, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) PutIfAbsent(ctx context.Context, coll, id string, doc []byte) (bool, error) {
	m, err := withID(id, doc)
	if err != nil {
		return false, err
	}
	delete(m, "_id") // _id supplied via filter on insert
	res, err := s.c(coll).UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$setOnInsert": m}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return false, err
	}
	return res.UpsertedCount > 0, nil
}

func (s *Store) PutMany(ctx context.Context, coll string, ids []string, docs [][]byte) error {
	if len(ids) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(ids))
	for i, id := range ids {
		m, err := withID(id, docs[i])
		if err != nil {
			return err
		}
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(bson.M{"_id": id}).SetReplacement(m).SetUpsert(true))
	}
	_, err := s.c(coll).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

func (s *Store) Get(ctx context.Context, coll, id string) ([]byte, error) {
	raw, err := s.c(coll).FindOne(ctx, bson.M{"_id": id}).Raw()
	if err == mongo.ErrNoDocuments {
		return nil, dopdb.ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func (s *Store) GetMany(ctx context.Context, coll string, ids []string) ([][]byte, error) {
	cur, err := s.c(coll).Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	byID := make(map[string][]byte, len(ids))
	for cur.Next(ctx) {
		raw := append([]byte(nil), cur.Current...)
		id, _ := cur.Current.Lookup("_id").StringValueOK()
		byID[id] = raw
	}
	out := make([][]byte, len(ids))
	for i, id := range ids {
		out[i] = byID[id] // nil if absent
	}
	return out, cur.Err()
}

func (s *Store) Delete(ctx context.Context, coll string, ids []string) (int64, error) {
	res, err := s.c(coll).DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

func (s *Store) Exists(ctx context.Context, coll, id string) (bool, error) {
	n, err := s.c(coll).CountDocuments(ctx, bson.M{"_id": id}, options.Count().SetLimit(1))
	return n > 0, err
}

func (s *Store) IDs(ctx context.Context, coll string) ([]string, error) {
	cur, err := s.c(coll).Find(ctx, bson.M{},
		options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []string
	for cur.Next(ctx) {
		if id, ok := cur.Current.Lookup("_id").StringValueOK(); ok {
			out = append(out, id)
		}
	}
	return out, cur.Err()
}

func (s *Store) All(ctx context.Context, coll string) ([]string, [][]byte, error) {
	cur, err := s.c(coll).Find(ctx, bson.M{})
	if err != nil {
		return nil, nil, err
	}
	defer cur.Close(ctx)
	var ids []string
	var docs [][]byte
	for cur.Next(ctx) {
		id, _ := cur.Current.Lookup("_id").StringValueOK()
		ids = append(ids, id)
		docs = append(docs, append([]byte(nil), cur.Current...))
	}
	return ids, docs, cur.Err()
}

func (s *Store) Count(ctx context.Context, coll string) (int64, error) {
	return s.c(coll).CountDocuments(ctx, bson.M{})
}

func (s *Store) Incr(ctx context.Context, coll, id, fieldPath string, delta float64) error {
	_, err := s.c(coll).UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$inc": bson.M{fieldPath: delta}}, options.UpdateOne().SetUpsert(true))
	return err
}

func (s *Store) Find(ctx context.Context, coll string, filter dopdb.M, opt dopdb.FindOpt) ([]string, [][]byte, error) {
	fo := options.Find()
	if opt.Limit > 0 {
		fo = fo.SetLimit(opt.Limit)
	}
	if opt.Skip > 0 {
		fo = fo.SetSkip(opt.Skip)
	}
	if len(opt.SortKeys) > 0 {
		sort := bson.D{}
		for _, sk := range opt.SortKeys {
			dir := int32(1)
			if !sk.Asc {
				dir = -1
			}
			sort = append(sort, bson.E{Key: sk.Field, Value: dir})
		}
		fo = fo.SetSort(sort)
	}
	if len(opt.Projection) > 0 {
		fo = fo.SetProjection(bson.M(opt.Projection))
	}

	cur, err := s.c(coll).Find(ctx, bson.M(filter), fo)
	if err != nil {
		return nil, nil, err
	}
	defer cur.Close(ctx)
	var ids []string
	var docs [][]byte
	for cur.Next(ctx) {
		id, _ := cur.Current.Lookup("_id").StringValueOK()
		ids = append(ids, id)
		docs = append(docs, append([]byte(nil), cur.Current...))
	}
	return ids, docs, cur.Err()
}

// compile-time assertion that Store satisfies the interface.
var _ dopdb.Store = (*Store)(nil)
