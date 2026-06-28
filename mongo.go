package dopdb

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ----------------------------------------------------------------------------
// Mongo-direct backend + multi-datasource registry.
//
// The Store/Codec abstraction is gone: dopdb is bound directly to MongoDB. The
// operations below are the same proven driver-v2 logic the former mongostore
// adapter used, now a concrete type in this package. BSON is the on-disk format
// (queryable, inspectable); values cross this boundary as BSON bytes that the
// generic Collection produced with bson.Marshal.
//
// Multiple databases are supported at runtime: the config file may declare
// several [[mongo]] sources; a request selects one with ?ds=<name>, defaulting
// to the source named "default". A typed Go Collection uses its bound datasource
// (WithDB) for native calls, or the request-selected one over HTTP.
// ----------------------------------------------------------------------------

// mongoBackend is one MongoDB database dopdb talks to directly.
type mongoBackend struct {
	db *mongo.Database
}

func (b *mongoBackend) c(coll string) *mongo.Collection { return b.db.Collection(coll) }

// Datasources is the runtime registry of named databases. The name "default" is
// required; ?ds=<name> (or WithDB) selects another. Safe for concurrent use.
type Datasources struct {
	mu  sync.RWMutex
	m   map[string]*mongoBackend
	def string
}

// NewDatasources returns an empty registry whose default source name is "default".
func NewDatasources() *Datasources {
	return &Datasources{m: map[string]*mongoBackend{}, def: "default"}
}

// Add registers db under name. Call Add("default", ...) for the default source.
func (d *Datasources) Add(name string, db *mongo.Database) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[name] = &mongoBackend{db: db}
}

// get resolves a backend by name; "" or an unknown name falls back to default.
func (d *Datasources) get(name string) (*mongoBackend, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if name == "" {
		name = d.def
	}
	b, ok := d.m[name]
	if !ok {
		b, ok = d.m[d.def]
	}
	return b, ok
}

// Names returns the registered datasource names (unsorted).
func (d *Datasources) Names() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.m))
	for k := range d.m {
		out = append(out, k)
	}
	return out
}

// ConnectDatasources opens a client per (name, uri, db) triple and returns a
// registry. Typical startup wires this from config:
//
//	cfg, _ := config.Load("config.toml")
//	ds, _ := dopdb.ConnectDatasources(ctx, cfg)   // see config bridge in serve.go
//	dopdb.SetDatasources(ds)
func ConnectDatasources(ctx context.Context, sources []DatasourceConfig) (*Datasources, error) {
	ds := NewDatasources()
	for _, s := range sources {
		client, err := mongo.Connect(options.Client().ApplyURI(s.URI))
		if err != nil {
			return nil, err
		}
		if err := client.Ping(ctx, nil); err != nil {
			return nil, err
		}
		ds.Add(s.Name, client.Database(s.DB))
	}
	return ds, nil
}

// DatasourceConfig is the minimal (name, uri, db) a datasource needs. The config
// package resolves uri from the environment; this is the already-resolved form.
type DatasourceConfig struct {
	Name string
	URI  string
	DB   string
}

// process-wide registry, installed by Serve / SetDatasources.
var defaultDatasources *Datasources

// SetDatasources installs the process-wide datasource registry.
func SetDatasources(d *Datasources) { defaultDatasources = d }

// backend resolves the backend for a datasource name (""=default), panicking if
// no registry is configured — the deliberate "fail loud at startup" contract
// (the same shape as the former SetDefaultStore requirement).
func backendFor(ds string) *mongoBackend {
	if defaultDatasources == nil {
		panic("dopdb: no datasources configured; call dopdb.SetDatasources(...) or dopdb.Serve(...) first")
	}
	b, ok := defaultDatasources.get(ds)
	if !ok {
		panic("dopdb: datasource not found and no default registered: " + ds)
	}
	return b
}

// ---- index ----

func mongoIndexModel(idx IndexSpec) mongo.IndexModel {
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
	for _, gf := range idx.Geo {
		keys = append(keys, bson.E{Key: gf, Value: "2dsphere"})
	}
	opt := options.Index()
	if idx.Unique {
		opt = opt.SetUnique(true)
	}
	if idx.Name != "" {
		opt = opt.SetName(idx.Name)
	}
	return mongo.IndexModel{Keys: keys, Options: opt}
}

func (b *mongoBackend) ensureIndex(ctx context.Context, coll string, idx IndexSpec) error {
	_, err := b.c(coll).Indexes().CreateOne(ctx, mongoIndexModel(idx))
	return err
}

// withID decodes value bytes and forces _id = id so the upsert is keyed
// deterministically regardless of whether V carries an _id field.
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

// ---- writes ----

func (b *mongoBackend) put(ctx context.Context, coll, id string, doc []byte) error {
	m, err := withID(id, doc)
	if err != nil {
		return err
	}
	_, err = b.c(coll).ReplaceOne(ctx, bson.M{"_id": id}, m, options.Replace().SetUpsert(true))
	return err
}

// putScoped is the atomic filtered upsert: it matches only the caller's own
// document; a foreign-owned _id misses the filter, the upsert attempts a
// duplicate-_id insert → E11000 → ErrForbidden. No TOCTOU window.
func (b *mongoBackend) putScoped(ctx context.Context, coll, id string, doc []byte, ownerField, ownerVal string) error {
	m := bson.M{}
	if len(doc) > 0 {
		if err := bson.Unmarshal(doc, &m); err != nil {
			return err
		}
	}
	delete(m, "_id")         // _id is immutable; the filter supplies it on insert
	m[ownerField] = ownerVal // force owner (non-forgeable); matches the filter
	filter := bson.M{"_id": id, ownerField: ownerVal}
	_, err := b.c(coll).UpdateOne(ctx, filter, bson.M{"$set": m}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrForbidden
		}
		return err
	}
	return nil
}

func (b *mongoBackend) putIfAbsent(ctx context.Context, coll, id string, doc []byte) (bool, error) {
	m, err := withID(id, doc)
	if err != nil {
		return false, err
	}
	delete(m, "_id") // _id supplied via filter on insert
	res, err := b.c(coll).UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$setOnInsert": m}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return false, err
	}
	return res.UpsertedCount > 0, nil
}

func (b *mongoBackend) putMany(ctx context.Context, coll string, ids []string, docs [][]byte) error {
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
	_, err := b.c(coll).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

func (b *mongoBackend) incr(ctx context.Context, coll, id, fieldPath string, delta float64) error {
	_, err := b.c(coll).UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$inc": bson.M{fieldPath: delta}}, options.UpdateOne().SetUpsert(true))
	return err
}

// ---- reads ----

func (b *mongoBackend) get(ctx context.Context, coll, id string) ([]byte, error) {
	raw, err := b.c(coll).FindOne(ctx, bson.M{"_id": id}).Raw()
	if err == mongo.ErrNoDocuments {
		return nil, ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func (b *mongoBackend) getMany(ctx context.Context, coll string, ids []string) ([][]byte, error) {
	cur, err := b.c(coll).Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
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
		out[i] = byID[id]
	}
	return out, cur.Err()
}

func (b *mongoBackend) del(ctx context.Context, coll string, ids []string) (int64, error) {
	res, err := b.c(coll).DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

func (b *mongoBackend) exists(ctx context.Context, coll, id string) (bool, error) {
	n, err := b.c(coll).CountDocuments(ctx, bson.M{"_id": id}, options.Count().SetLimit(1))
	return n > 0, err
}

func (b *mongoBackend) ids(ctx context.Context, coll string) ([]string, error) {
	cur, err := b.c(coll).Find(ctx, bson.M{}, options.Find().SetProjection(bson.M{"_id": 1}))
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

func (b *mongoBackend) all(ctx context.Context, coll string) ([]string, [][]byte, error) {
	cur, err := b.c(coll).Find(ctx, bson.M{})
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

func (b *mongoBackend) count(ctx context.Context, coll string) (int64, error) {
	return b.c(coll).CountDocuments(ctx, bson.M{})
}

func (b *mongoBackend) find(ctx context.Context, coll string, filter M, opt FindOpt) ([]string, [][]byte, error) {
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
	} else if len(opt.Sort) > 0 {
		fo = fo.SetSort(bson.M(opt.Sort))
	}
	if len(opt.Projection) > 0 {
		fo = fo.SetProjection(bson.M(opt.Projection))
	}
	cur, err := b.c(coll).Find(ctx, bson.M(filter), fo)
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

// countFilter counts documents matching an (already sanitized) filter.
func (b *mongoBackend) countFilter(ctx context.Context, coll string, filter M) (int64, error) {
	return b.c(coll).CountDocuments(ctx, bson.M(filter))
}

// watch opens a change stream on coll and invokes emit for each change. For a
// scoped collection (scope non-nil) the pipeline matches fullDocument by the
// owner predicate, so a caller only sees its own inserts/updates/replaces;
// deletes carry no fullDocument and are therefore not delivered to scoped
// watchers. It resumes automatically across transient errors using the resume
// token, and returns when ctx is cancelled or the stream ends cleanly.
//
// NOTE: change streams require MongoDB to run as a replica set.
func (b *mongoBackend) watch(ctx context.Context, coll string, scope M, emit func(op, id string, raw []byte) error) error {
	pipeline := mongo.Pipeline{}
	if len(scope) > 0 {
		match := bson.D{}
		for k, v := range scope {
			match = append(match, bson.E{Key: "fullDocument." + k, Value: v})
		}
		pipeline = mongo.Pipeline{bson.D{{Key: "$match", Value: match}}}
	}
	var resume bson.Raw
	for {
		opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
		if resume != nil {
			opts = opts.SetResumeAfter(resume)
		}
		cs, err := b.c(coll).Watch(ctx, pipeline, opts)
		if err != nil {
			return err
		}
		for cs.Next(ctx) {
			resume = cs.ResumeToken()
			var ev struct {
				OperationType string `bson:"operationType"`
				DocumentKey   struct {
					ID string `bson:"_id"`
				} `bson:"documentKey"`
				FullDocument bson.Raw `bson:"fullDocument"`
			}
			if err := cs.Decode(&ev); err != nil {
				continue
			}
			var raw []byte
			if len(ev.FullDocument) > 0 {
				raw = []byte(ev.FullDocument)
			}
			if err := emit(ev.OperationType, ev.DocumentKey.ID, raw); err != nil {
				_ = cs.Close(ctx)
				return err // consumer gone (e.g. client disconnected)
			}
		}
		cerr := cs.Err()
		_ = cs.Close(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if cerr == nil {
			return nil
		}
		time.Sleep(time.Second) // transient error: back off, then resume from token
	}
}

// ---- Hash scan/sample primitives (Redis HSCAN / HRANDFIELD on Mongo) --------

// globToRegex converts a Redis glob (used by HSCAN match) to an anchored regex.
func globToRegex(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '.', '+', '(', ')', '|', '[', ']', '{', '}', '^', '$', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("$")
	return b.String()
}

// sample returns up to count random _ids (Redis HRANDFIELD). scope, when
// non-nil, restricts the population (owner-scope) via a $match stage.
func (b *mongoBackend) sample(ctx context.Context, coll string, count int, scope M) ([]string, error) {
	if count <= 0 {
		count = 1
	}
	pipe := mongo.Pipeline{}
	if len(scope) > 0 {
		pipe = append(pipe, bson.D{{Key: "$match", Value: bson.M(scope)}})
	}
	pipe = append(pipe,
		bson.D{{Key: "$sample", Value: bson.D{{Key: "size", Value: count}}}},
		bson.D{{Key: "$project", Value: bson.D{{Key: "_id", Value: 1}}}},
	)
	cur, err := b.c(coll).Aggregate(ctx, pipe)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var ids []string
	for cur.Next(ctx) {
		if id, ok := cur.Current.Lookup("_id").StringValueOK(); ok {
			ids = append(ids, id)
		}
	}
	return ids, cur.Err()
}

// scan emulates Redis HSCAN over Mongo: glob match -> regex on _id, paginated by
// a numeric cursor (offset). Returns (ids, docs, nextCursor); nextCursor 0 = done.
// scope, when non-nil, AND-filters by owner (owner-scope).
func (b *mongoBackend) scan(ctx context.Context, coll, match string, cursor uint64, count int64, scope M) ([]string, [][]byte, uint64, error) {
	if count <= 0 {
		count = 10
	}
	filter := M{}
	for k, v := range scope {
		filter[k] = v
	}
	if match != "" && match != "*" {
		filter["_id"] = M{"$regex": globToRegex(match)}
	}
	ids, docs, err := b.find(ctx, coll, filter, FindOpt{
		SortKeys: []SortKey{{Field: "_id", Asc: true}},
		Skip:     int64(cursor),
		Limit:    count,
	})
	if err != nil {
		return nil, nil, 0, err
	}
	next := uint64(0)
	if int64(len(ids)) == count {
		next = cursor + uint64(len(ids))
	}
	return ids, docs, next, nil
}
