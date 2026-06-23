package dopdb

import (
	"context"
	"errors"
)

// ErrNoDoc is the canonical "document/field not found" error. It replaces
// redisdb's reliance on redis.Nil. Callers distinguish it with errors.Is.
//
// The redisdb contract leaked the underlying engine (redis.Nil) into every
// call site. dopdb defines its own sentinel so the engine can change without
// rewriting application error handling.
var ErrNoDoc = errors.New("dopdb: document not found")

// ErrForbidden is returned when a row-level ownership check fails (the caller
// tried to read or overwrite a document owned by someone else).
var ErrForbidden = errors.New("dopdb: forbidden")

// M is a free-form document/filter/update map. It is intentionally identical
// in shape to bson.M / a JSON object so the Mongo adapter is a trivial cast and
// the in-memory test store can use it directly. Keeping bson out of the core is
// deliberate: the storage format is an implementation detail of the adapter,
// not a contract baked through the whole framework (the mistake redisdb made by
// threading msgpack everywhere).
type M = map[string]any

// FindOpt carries the optional shaping of a Find query. All fields are optional.
type FindOpt struct {
	// Sort: field -> +1 ascending / -1 descending. Order is not guaranteed
	// across map iteration; for multi-key sorts use SortKeys.
	Sort     M
	SortKeys []SortKey
	Limit    int64
	Skip     int64
	// Projection: field -> 1 (include) / 0 (exclude). Optional.
	Projection M
}

// SortKey is an ordered sort directive (use when sort order matters).
type SortKey struct {
	Field string
	Asc   bool
}

// IndexSpec is an idempotent index declaration derived from struct tags.
// Frontend-driven queries on a real database need indexes or they collapse to
// collection scans; dopdb makes the index part of the type definition and
// ensures it on construction (the same idempotent pattern redisdb used for
// RediSearch EnsureIndex).
type IndexSpec struct {
	// Keys: field -> +1 / -1 / "text" / "2dsphere". Ordered.
	Keys   []SortKey
	Text   []string // text-index fields (mongo "text")
	Unique bool
	Name   string
}

// Codec marshals a value to the storage byte representation and back.
// In production this is BSON (so documents are queryable and inspectable);
// in tests it is JSON. The core never imports either — it is handed a Codec.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// Store is the entire storage surface dopdb requires. It is deliberately small
// and engine-neutral: a document collection keyed by a string _id, plus the few
// operations the fixed command vocabulary maps onto. The MongoDB adapter
// implements it in ~100 lines; an in-memory implementation backs the tests.
//
// All documents crossing this boundary are already Codec-encoded bytes. The _id
// is always a string (dopdb serializes keys to a single canonical string form —
// see serializeKey — which also sidesteps the float64/scientific-notation _id
// hazard the doptime docs warn about with numeric JWT ids).
type Store interface {
	// EnsureIndex creates idx on coll if absent. Must be idempotent.
	EnsureIndex(ctx context.Context, coll string, idx IndexSpec) error

	// Put upserts doc under (coll, id). doc is Codec bytes WITHOUT _id; the
	// adapter associates it with id.
	Put(ctx context.Context, coll, id string, doc []byte) error

	// PutIfAbsent inserts only if (coll, id) does not yet exist.
	// Returns true if inserted, false if it already existed.
	PutIfAbsent(ctx context.Context, coll, id string, doc []byte) (inserted bool, err error)

	// PutMany upserts a batch. ids[i] pairs with docs[i].
	PutMany(ctx context.Context, coll string, ids []string, docs [][]byte) error

	// Get returns the Codec bytes for (coll, id), or ErrNoDoc.
	Get(ctx context.Context, coll, id string) (doc []byte, err error)

	// GetMany returns docs aligned with ids; a missing id yields a nil entry.
	GetMany(ctx context.Context, coll string, ids []string) (docs [][]byte, err error)

	// Delete removes the given ids, returning the count removed.
	Delete(ctx context.Context, coll string, ids []string) (removed int64, err error)

	// Exists reports whether (coll, id) is present.
	Exists(ctx context.Context, coll, id string) (bool, error)

	// IDs returns all _id values in coll.
	IDs(ctx context.Context, coll string) ([]string, error)

	// All returns every (id, doc) pair in coll.
	All(ctx context.Context, coll string) (ids []string, docs [][]byte, err error)

	// Count returns the number of documents in coll.
	Count(ctx context.Context, coll string) (int64, error)

	// Incr atomically applies {$inc: {fieldPath: delta}} to (coll, id),
	// upserting if absent. This is the atomic counter redisdb's `counter`
	// modifier could not provide (it did a non-atomic read-modify-write in Go).
	Incr(ctx context.Context, coll, id, fieldPath string, delta float64) error

	// Find runs a (already sanitized) filter and returns matching docs.
	// This is the capability the KV model never had: query by field content.
	Find(ctx context.Context, coll string, filter M, opt FindOpt) (ids []string, docs [][]byte, err error)
}
