package dopdb

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ----------------------------------------------------------------------------
// Process-wide store + codec.
//
// In production:
//	dopdb.SetDefaultStore(mongostore.New(ctx, "mongodb://...", "mydb"))
// Tests install an in-memory store + JSON codec.
// ----------------------------------------------------------------------------

var (
	defaultStore Store
	defaultCodec Codec
)

// SetDefaultStore installs the storage backend used by New when no per-collection
// store is given.
func SetDefaultStore(s Store) { defaultStore = s }

// SetDefaultCodec installs the document codec (BSON in production).
func SetDefaultCodec(c Codec) { defaultCodec = c }

// ----------------------------------------------------------------------------
// Options
// ----------------------------------------------------------------------------

type config struct {
	db         string
	collection string
	store      Store
	codec      Codec
}

// Option configures a Collection at construction.
type Option func(*config)

// WithDB selects a logical database/namespace (advisory: the mongostore adapter
// may map this to a Mongo database). Defaults to the store's default DB.
func WithDB(name string) Option { return func(c *config) { c.db = name } }

// WithCollection overrides the collection name (otherwise derived from V's type
// name). Required when V is a scalar type, whose name is not a legal collection.
func WithCollection(name string) Option { return func(c *config) { c.collection = name } }

// WithKey is an alias of WithCollection kept for familiarity with redisdb/doptime.
func WithKey(name string) Option { return WithCollection(name) }

// WithStore overrides the backend for this collection (e.g. a second cluster for
// migration), instead of the process default.
func WithStore(s Store) Option { return func(c *config) { c.store = s } }

// ----------------------------------------------------------------------------
// Collection
// ----------------------------------------------------------------------------

// Collection is the typed handle to one document collection: documents of type V
// keyed by K. It is the dopdb analogue of redisdb's HashKey — a hash of structs
// IS a keyed document collection, which is exactly what Mongo stores natively.
//
// V may be a struct or *struct. K may be any comparable type; it is serialized
// to a single canonical string _id (see serializeKey).
type Collection[K comparable, V any] struct {
	coll  string
	store Store
	codec Codec
	plan  *writePlan

	vIsPtr     bool
	vElemType  reflect.Type // underlying struct type when V is struct/*struct
	pkFieldIdx int          // index of the field whose type is assignable to K, or -1
}

// New constructs a typed Collection. On failure it logs and returns nil
// (matching redisdb/doptime ergonomics); callers should nil-check. Index
// declarations found in struct tags are ensured here, idempotently.
func New[K comparable, V any](opts ...Option) *Collection[K, V] {
	cfg := &config{store: defaultStore, codec: defaultCodec}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.store == nil {
		panic("dopdb: no Store configured; call dopdb.SetDefaultStore(...) first")
	}
	if cfg.codec == nil {
		panic("dopdb: no Codec configured; call dopdb.SetDefaultCodec(...) first")
	}

	vType := reflect.TypeOf((*V)(nil)).Elem()
	coll := cfg.collection
	if coll == "" {
		coll = collectionNameByType(vType)
	}
	if coll == "" {
		panic("dopdb: cannot derive collection name from scalar V; pass WithCollection(...)")
	}

	c := &Collection[K, V]{
		coll:       coll,
		store:      cfg.store,
		codec:      cfg.codec,
		plan:       buildWritePlan(vType),
		pkFieldIdx: -1,
	}

	t := vType
	for t.Kind() == reflect.Ptr {
		c.vIsPtr = true
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		c.vElemType = t
		c.pkFieldIdx = primaryKeyFieldIndex(t, reflect.TypeOf((*K)(nil)).Elem())
		for _, idx := range indexSpecsFromTags(t) {
			_ = c.store.EnsureIndex(context.Background(), coll, idx)
		}
	}
	return c
}

// Collection returns the underlying collection name.
func (c *Collection[K, V]) Collection() string { return c.coll }

// ---- key serialization (one canonical codec; fixes redisdb's 3-way split) ----

// serializeKey produces the string _id for a key. Strings pass through;
// integer kinds use base-10; everything else is JSON-encoded. Using a single
// codec for both write and read eliminates the redisdb bug where struct keys
// were written via msgpack but read back via JSON (and the singular/plural
// decoders disagreed). String/integer ids also avoid the float64 _id hazard the
// doptime docs warn about.
func (c *Collection[K, V]) serializeKey(k K) (string, error) {
	rv := reflect.ValueOf(k)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	default:
		b, err := json.Marshal(k)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

func (c *Collection[K, V]) deserializeKey(id string) (K, error) {
	var k K
	rt := reflect.TypeOf((*K)(nil)).Elem()
	switch rt.Kind() {
	case reflect.String:
		reflect.ValueOf(&k).Elem().SetString(id)
		return k, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return k, err
		}
		reflect.ValueOf(&k).Elem().SetInt(n)
		return k, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return k, err
		}
		reflect.ValueOf(&k).Elem().SetUint(n)
		return k, nil
	default:
		err := json.Unmarshal([]byte(id), &k)
		return k, err
	}
}

// ---- value codec ----

func (c *Collection[K, V]) encode(v V) ([]byte, error) {
	out, err := c.plan.apply(v) // modifiers + timestamps + validation, on WRITE
	if err != nil {
		return nil, err
	}
	return c.codec.Marshal(out)
}

func (c *Collection[K, V]) decode(b []byte) (V, error) {
	var v V
	if c.vIsPtr {
		// allocate a fresh element so each decoded value is independent
		pv := reflect.New(c.vElemType)
		if err := c.codec.Unmarshal(b, pv.Interface()); err != nil {
			return v, err
		}
		return pv.Interface().(V), nil
	}
	err := c.codec.Unmarshal(b, &v)
	return v, err
}

// ---- core methods (redisdb-compatible names) ----

// HSet upserts value under key.
func (c *Collection[K, V]) HSet(key K, value V) error {
	id, err := c.serializeKey(key)
	if err != nil {
		return err
	}
	doc, err := c.encode(value)
	if err != nil {
		return err
	}
	return c.store.Put(context.Background(), c.coll, id, doc)
}

// HSetScoped atomically writes value under key only if the stored document is
// owned by ownerVal (its ownerField == ownerVal) or absent; a document owned by
// someone else yields ErrForbidden. This is the race-free scoped write.
func (c *Collection[K, V]) HSetScoped(key K, value V, ownerField, ownerVal string) error {
	id, err := c.serializeKey(key)
	if err != nil {
		return err
	}
	doc, err := c.encode(value)
	if err != nil {
		return err
	}
	return c.store.PutScoped(context.Background(), c.coll, id, doc, ownerField, ownerVal)
}

// HSetNX inserts only if the key is absent. Returns true if it was inserted.
func (c *Collection[K, V]) HSetNX(key K, value V) (bool, error) {
	id, err := c.serializeKey(key)
	if err != nil {
		return false, err
	}
	doc, err := c.encode(value)
	if err != nil {
		return false, err
	}
	return c.store.PutIfAbsent(context.Background(), c.coll, id, doc)
}

// Save derives the key from V's primary-key field (the first field whose type is
// assignable to K) and upserts. Falls back to requiring an explicit key if no
// such field exists.
func (c *Collection[K, V]) Save(value V) error {
	if c.pkFieldIdx < 0 {
		return fmt.Errorf("dopdb: Save needs a V field assignable to K; none found in %s", c.coll)
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	key, ok := rv.Field(c.pkFieldIdx).Interface().(K)
	if !ok {
		return fmt.Errorf("dopdb: primary key field is not of key type in %s", c.coll)
	}
	return c.HSet(key, value)
}

// HGet returns the value for key, or ErrNoDoc.
func (c *Collection[K, V]) HGet(key K) (V, error) {
	var zero V
	id, err := c.serializeKey(key)
	if err != nil {
		return zero, err
	}
	b, err := c.store.Get(context.Background(), c.coll, id)
	if err != nil {
		return zero, err // ErrNoDoc propagates
	}
	return c.decode(b)
}

// HMGet returns values aligned with keys. A missing key yields a zero value.
// Unlike redisdb, a decode failure is returned rather than silently dropped.
func (c *Collection[K, V]) HMGet(keys ...K) ([]V, error) {
	ids := make([]string, len(keys))
	for i, k := range keys {
		id, err := c.serializeKey(k)
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	docs, err := c.store.GetMany(context.Background(), c.coll, ids)
	if err != nil {
		return nil, err
	}
	out := make([]V, len(docs))
	for i, b := range docs {
		if b == nil {
			continue // missing -> zero value
		}
		if out[i], err = c.decode(b); err != nil {
			return nil, fmt.Errorf("dopdb: decode %s[%s]: %w", c.coll, ids[i], err)
		}
	}
	return out, nil
}

// HMSet upserts a batch.
func (c *Collection[K, V]) HMSet(kv map[K]V) error {
	ids := make([]string, 0, len(kv))
	docs := make([][]byte, 0, len(kv))
	for k, v := range kv {
		id, err := c.serializeKey(k)
		if err != nil {
			return err
		}
		doc, err := c.encode(v)
		if err != nil {
			return err
		}
		ids = append(ids, id)
		docs = append(docs, doc)
	}
	return c.store.PutMany(context.Background(), c.coll, ids, docs)
}

// HGetAll returns every key/value pair.
func (c *Collection[K, V]) HGetAll() (map[K]V, error) {
	ids, docs, err := c.store.All(context.Background(), c.coll)
	if err != nil {
		return nil, err
	}
	out := make(map[K]V, len(ids))
	for i, id := range ids {
		k, err := c.deserializeKey(id)
		if err != nil {
			return nil, fmt.Errorf("dopdb: bad key %q in %s: %w", id, c.coll, err)
		}
		v, err := c.decode(docs[i])
		if err != nil {
			return nil, fmt.Errorf("dopdb: decode %s[%s]: %w", c.coll, id, err)
		}
		out[k] = v
	}
	return out, nil
}

// HDel removes the given keys.
func (c *Collection[K, V]) HDel(keys ...K) error {
	ids := make([]string, len(keys))
	for i, k := range keys {
		id, err := c.serializeKey(k)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	_, err := c.store.Delete(context.Background(), c.coll, ids)
	return err
}

// Del is an alias of HDel for a single key.
func (c *Collection[K, V]) Del(key K) error { return c.HDel(key) }

// HExists reports whether key is present.
func (c *Collection[K, V]) HExists(key K) (bool, error) {
	id, err := c.serializeKey(key)
	if err != nil {
		return false, err
	}
	return c.store.Exists(context.Background(), c.coll, id)
}

// HKeys returns all keys.
func (c *Collection[K, V]) HKeys() ([]K, error) {
	ids, err := c.store.IDs(context.Background(), c.coll)
	if err != nil {
		return nil, err
	}
	out := make([]K, len(ids))
	for i, id := range ids {
		if out[i], err = c.deserializeKey(id); err != nil {
			return nil, fmt.Errorf("dopdb: bad key %q in %s: %w", id, c.coll, err)
		}
	}
	return out, nil
}

// HVals returns all values.
func (c *Collection[K, V]) HVals() ([]V, error) {
	_, docs, err := c.store.All(context.Background(), c.coll)
	if err != nil {
		return nil, err
	}
	out := make([]V, len(docs))
	for i, b := range docs {
		if out[i], err = c.decode(b); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// HLen returns the document count.
func (c *Collection[K, V]) HLen() (int64, error) {
	return c.store.Count(context.Background(), c.coll)
}

// HIncrBy atomically increments a numeric field (dot-path) on key's document,
// upserting if absent. This is a true atomic $inc — the redisdb `counter`
// modifier could only do a racy read-modify-write in Go.
func (c *Collection[K, V]) HIncrBy(key K, fieldPath string, delta int64) error {
	id, err := c.serializeKey(key)
	if err != nil {
		return err
	}
	return c.store.Incr(context.Background(), c.coll, id, fieldPath, float64(delta))
}

// HIncrByFloat is the float form of HIncrBy.
func (c *Collection[K, V]) HIncrByFloat(key K, fieldPath string, delta float64) error {
	id, err := c.serializeKey(key)
	if err != nil {
		return err
	}
	return c.store.Incr(context.Background(), c.coll, id, fieldPath, delta)
}

// ---- the capability the KV model never had ----

// Find runs a field-content query and returns matching values. The filter is
// passed through SanitizeFilter first; when this path is reached from the HTTP
// layer the same sanitizer guarantees the frontend cannot smuggle $where /
// $function / cross-collection $lookup, etc.
func (c *Collection[K, V]) Find(filter M, opt FindOpt) ([]V, error) {
	safe, err := SanitizeFilter(filter)
	if err != nil {
		return nil, err
	}
	_, docs, err := c.store.Find(context.Background(), c.coll, safe, opt)
	if err != nil {
		return nil, err
	}
	out := make([]V, len(docs))
	for i, b := range docs {
		if out[i], err = c.decode(b); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// FindOne returns the first match, or ErrNoDoc.
func (c *Collection[K, V]) FindOne(filter M) (V, error) {
	var zero V
	vs, err := c.Find(filter, FindOpt{Limit: 1})
	if err != nil {
		return zero, err
	}
	if len(vs) == 0 {
		return zero, ErrNoDoc
	}
	return vs[0], nil
}

// ----------------------------------------------------------------------------
// reflection helpers
// ----------------------------------------------------------------------------

// disallowedNames are scalar/builtin type names that cannot serve as a
// collection name (ported from redisdb/doptime).
var disallowedNames = map[string]bool{
	"": true, "string": true, "int": true, "int8": true, "int16": true,
	"int32": true, "int64": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "float32": true, "float64": true,
	"float": true, "bool": true, "byte": true, "rune": true, "complex64": true,
	"complex128": true, "map": true, "interface": true,
}

func collectionNameByType(t reflect.Type) string {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	name := t.Name()
	if disallowedNames[strings.ToLower(name)] {
		return ""
	}
	return name
}

func primaryKeyFieldIndex(structT, keyT reflect.Type) int {
	if keyT.Kind() == reflect.Ptr {
		keyT = keyT.Elem()
	}
	for i := 0; i < structT.NumField(); i++ {
		if structT.Field(i).Type.AssignableTo(keyT) {
			return i
		}
	}
	return -1
}

// indexSpecsFromTags reads `index:"..."` struct tags into IndexSpecs.
// Supported values: "1"/"-1" (asc/desc), "text", "2dsphere", "unique"
// (single-field unique), or combinations like "1,unique".
func indexSpecsFromTags(t reflect.Type) []IndexSpec {
	var specs []IndexSpec
	bsonName := func(f reflect.StructField) string {
		if tag := f.Tag.Get("bson"); tag != "" {
			if name := strings.Split(tag, ",")[0]; name != "" && name != "-" {
				return name
			}
		}
		return f.Name
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("index")
		if tag == "" {
			continue
		}
		name := bsonName(f)
		spec := IndexSpec{Name: name + "_idx"}
		for _, part := range strings.Split(tag, ",") {
			switch strings.TrimSpace(part) {
			case "1", "asc", "":
				spec.Keys = append(spec.Keys, SortKey{Field: name, Asc: true})
			case "-1", "desc":
				spec.Keys = append(spec.Keys, SortKey{Field: name, Asc: false})
			case "text":
				spec.Text = append(spec.Text, name)
			case "2dsphere":
				spec.Keys = append(spec.Keys, SortKey{Field: name, Asc: true}) // adapter maps 2dsphere
			case "unique":
				spec.Unique = true
			}
		}
		if len(spec.Keys) == 0 && len(spec.Text) == 0 {
			spec.Keys = append(spec.Keys, SortKey{Field: name, Asc: true})
		}
		specs = append(specs, spec)
	}
	return specs
}
