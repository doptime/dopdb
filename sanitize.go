package dopdb

import (
	"fmt"
	"strings"
)

// ----------------------------------------------------------------------------
// Filter sanitization.
//
// In Redis/doptime the frontend could only invoke a CLOSED set of verbs, so a
// per-key on/off whitelist was a sufficient safety model. FIND reopened that
// surface: a filter is an arbitrary document supplied by the caller.
//
// On KVRocks the filter is no longer handed to a database — query.go evaluates
// it in this process (see the dialect note below). That removes the server-side
// injection class outright, but NOT the need for this function: an unbounded
// filter is still a denial-of-service surface (pathological nesting, operators
// with no bounded cost) and the allowlist is what keeps the accepted dialect
// identical to the one the TypeScript engine implements. Anything outside the
// list is rejected here rather than silently ignored downstream.
//
// The operator names are unchanged from the Mongo build on purpose: the HTTP
// wire protocol, the typed clients and the docs all speak this dialect, and the
// storage swap was not a reason to break them. The evaluator in query.go accepts
// exactly this set.
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

// forbiddenOps are operators that executed code, performed writes, or traversed
// collections in the Mongo dialect. query.go implements none of them, so they
// could simply fall through the allowlist — they are named explicitly anyway,
// because a filter that contains one is a caller expecting behaviour dopdb will
// not provide, and a clear error beats a silent empty result set.
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
