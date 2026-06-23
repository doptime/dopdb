package dopdb

import (
	"fmt"
	"strings"
)

// ----------------------------------------------------------------------------
// Filter sanitization.
//
// This is the heart of the migration's hardest problem. In Redis/doptime the
// frontend could only invoke a CLOSED set of verbs, so a per-key on/off
// whitelist was a sufficient safety model. Mongo's query surface is an open,
// arbitrary document — exposing it raw is NoSQL injection by construction
// ({$where:"while(true){}"}, server-side JS, cross-collection reads, etc.).
//
// dopdb keeps the "closed surface" guarantee by never accepting a raw Mongo
// filter from an untrusted caller: SanitizeFilter walks the filter and admits
// only a vetted operator allowlist, rejecting everything that can execute code,
// reach other collections, or write. Field-level scoping (which fields a given
// collection exposes, and the mandatory owner==@uid predicate) is layered on top
// by the HTTP/permission layer; this function enforces the operator-level floor
// that every query — Go-native or HTTP — passes through.
// ----------------------------------------------------------------------------

// allowedQueryOps are filter operators considered safe to accept from callers.
var allowedQueryOps = map[string]bool{
	// comparison
	"$eq": true, "$ne": true, "$gt": true, "$gte": true,
	"$lt": true, "$lte": true, "$in": true, "$nin": true,
	// logical
	"$and": true, "$or": true, "$nor": true, "$not": true,
	// element
	"$exists": true, "$type": true,
	// array
	"$all": true, "$elemMatch": true, "$size": true,
	// evaluation (safe subset)
	"$regex": true, "$options": true, "$mod": true,
}

// forbiddenOps are operators that execute code, perform writes, traverse
// collections, or otherwise escape the read sandbox. Rejected with a clear error
// even though they would also be caught by the allowlist — naming them makes
// audits and error messages legible.
var forbiddenOps = map[string]bool{
	"$where": true, "$function": true, "$accumulator": true,
	"$expr":   true, // $expr can embed $function/$let; disallow wholesale
	"$lookup": true, "$graphLookup": true, "$unionWith": true,
	"$merge": true, "$out": true, "$facet": true,
}

// SanitizeFilter validates a query filter, returning a safe copy or an error.
// The original is not mutated.
func SanitizeFilter(filter M) (M, error) {
	if filter == nil {
		return M{}, nil
	}
	out, err := sanitizeDoc(filter, 0)
	if err != nil {
		return nil, err
	}
	return out.(M), nil
}

const maxFilterDepth = 12

func sanitizeDoc(v any, depth int) (any, error) {
	if depth > maxFilterDepth {
		return nil, fmt.Errorf("dopdb: filter nested too deeply (>%d)", maxFilterDepth)
	}
	switch t := v.(type) {
	case M: // note: M is an alias of map[string]any, so this also covers plain maps
		out := make(M, len(t))
		for k, val := range t {
			if err := checkKey(k); err != nil {
				return nil, err
			}
			sv, err := sanitizeDoc(val, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = sv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			sv, err := sanitizeDoc(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = sv
		}
		return out, nil
	default:
		// scalar leaf (string/number/bool/nil/time/etc.) — safe as-is
		return v, nil
	}
}

func checkKey(k string) error {
	if !strings.HasPrefix(k, "$") {
		// A normal field path. Disallow operator dollar signs hidden mid-path
		// and the field-name injection vectors.
		if strings.Contains(k, "$") {
			return fmt.Errorf("dopdb: illegal field path %q", k)
		}
		return nil
	}
	if forbiddenOps[k] {
		return fmt.Errorf("dopdb: operator %q is not permitted", k)
	}
	if !allowedQueryOps[k] {
		return fmt.Errorf("dopdb: operator %q is not in the query allowlist", k)
	}
	return nil
}
