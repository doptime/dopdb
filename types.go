package dopdb

import "errors"

// ----------------------------------------------------------------------------
// Core types and sentinels.
//
// The Store / Codec abstraction was removed: dopdb is now bound directly to
// MongoDB (no swappable backend, no in-memory store). What remains here are the
// engine-neutral data types the rest of the framework speaks — filters, query
// shaping, index declarations — plus the sentinel errors callers match with
// errors.Is. Keeping these out of the Mongo-specific file means the HTTP layer,
// the modifiers, and the sanitizer do not import the driver.
// ----------------------------------------------------------------------------

// ErrNoDoc is the canonical "document/field not found" error. Callers match it
// with errors.Is so the storage engine can change without rewriting handling.
var ErrNoDoc = errors.New("dopdb: document not found")

// ErrForbidden is returned when a row-level ownership check fails (the caller
// tried to read or overwrite a document owned by someone else).
var ErrForbidden = errors.New("dopdb: forbidden")

// M is a free-form document/filter/update map, intentionally identical in shape
// to a JSON object / bson.M so the Mongo layer is a trivial conversion.
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
// ensures it on construction.
type IndexSpec struct {
	// Keys: field -> +1 / -1. Ordered (supports compound indexes).
	Keys []SortKey
	// Text: text-index fields (mongo "text").
	Text []string
	// Geo: 2dsphere index fields.
	Geo    []string
	Unique bool
	Name   string
}
