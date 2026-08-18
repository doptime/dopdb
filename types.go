package dopdb

import "errors"

// ----------------------------------------------------------------------------
// Core types and sentinels.
//
// The Store / Codec abstraction was removed: dopdb is now bound directly to
// KVRocks (no swappable backend, no in-memory store). What remains here are the
// engine-neutral data types the rest of the framework speaks — filters, query
// shaping, index declarations — plus the sentinel errors callers match with
// errors.Is. Keeping these out of the KVRocks-specific file means the HTTP
// layer, the modifiers, and the sanitizer do not import the redis client.
// ----------------------------------------------------------------------------

// ErrNoDoc is the canonical "document/field not found" error. Callers match it
// with errors.Is so the storage engine can change without rewriting handling.
var ErrNoDoc = errors.New("dopdb: document not found")

// ErrForbidden is returned when a row-level ownership check fails (the caller
// tried to read or overwrite a document owned by someone else).
var ErrForbidden = errors.New("dopdb: forbidden")

// ErrDuplicate is returned when a write would break an `index:"unique"`
// declaration. Mongo surfaced this as driver error E11000; on KVRocks dopdb
// enforces it itself (see index.go), so it needs a sentinel of its own.
var ErrDuplicate = errors.New("dopdb: duplicate key")

// M is a free-form document/filter/update map, intentionally identical in shape
// to a JSON object so the wire protocol is a trivial conversion.
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

// IndexSpec is an index declaration derived from struct tags.
//
// KVRocks is a key/value store: it has no secondary indexes and no query
// planner, so Find scans the collection hash and filters in-process (query.go).
// That changes what an index declaration can mean:
//
//   - Unique — ENFORCED by dopdb itself, via a side hash that claims each value
//     (index.go). This is the one index kind with observable semantics, and it
//     is preserved so an `index:"unique"` tag still rejects duplicates.
//   - Keys / Text / Geo — RECORDED but inert. There is nothing to create on the
//     server and Find does not consult them. They are kept so the struct tags,
//     the docs, and the wire vocabulary stay in one language, and so a future
//     indexing backend needs no tag-format change.
type IndexSpec struct {
	// Keys: field -> ascending/descending. Ordered (compound declarations).
	// Inert on KVRocks — kept for declaration fidelity.
	Keys []SortKey
	// Text: full-text fields. Inert on KVRocks.
	Text []string
	// Geo: geospatial fields. Inert on KVRocks.
	Geo []string
	// Unique is enforced by dopdb on every write path.
	Unique bool
	Name   string
}
