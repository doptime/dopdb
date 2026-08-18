package dopdb

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ----------------------------------------------------------------------------
// The query engine.
//
// Mongo evaluated filters, sorts and projections server-side. KVRocks has no
// query language at all, so the same work happens here, in-process, over the
// decoded documents of one collection hash. The FILTER DIALECT IS UNCHANGED —
// the operators below are exactly the allowlist in sanitize.go, so the HTTP wire
// protocol, the client types and the docs did not have to move. What changed is
// only where the evaluation happens.
//
// Consequences worth stating plainly, because they are real:
//   - Find/Count/FindOne read every field of the collection hash (HSCAN in
//     pages) and decode each document. Cost is O(collection), not O(result).
//     Filter on a small collection, or keep the working set addressable by key.
//   - There is no index-backed ordering. Sort happens after the scan, on the
//     matched slice.
//   - Type ordering is dopdb's own (typeRank below), not BSON's. Comparisons
//     between different types are stable and documented rather than undefined.
// ----------------------------------------------------------------------------

// matchFilter reports whether doc satisfies filter. filter must already have
// passed SanitizeFilter; unknown operators are treated as non-matching rather
// than as errors, so a hostile filter can never widen a result set.
func matchFilter(doc map[string]any, filter M) bool {
	for k, cond := range filter {
		switch k {
		case "$and":
			subs, ok := asSlice(cond)
			if !ok {
				return false
			}
			for _, s := range subs {
				sm, ok := asFilter(s)
				if !ok || !matchFilter(doc, sm) {
					return false
				}
			}
		case "$or":
			subs, ok := asSlice(cond)
			if !ok {
				return false
			}
			any := false
			for _, s := range subs {
				if sm, ok := asFilter(s); ok && matchFilter(doc, sm) {
					any = true
					break
				}
			}
			if !any {
				return false
			}
		case "$nor":
			subs, ok := asSlice(cond)
			if !ok {
				return false
			}
			for _, s := range subs {
				if sm, ok := asFilter(s); ok && matchFilter(doc, sm) {
					return false
				}
			}
		default:
			if strings.HasPrefix(k, "$") {
				return false // top-level operator outside the logical set
			}
			if !matchField(doc, k, cond) {
				return false
			}
		}
	}
	return true
}

// matchField evaluates one `field: condition` pair. condition is either an
// operator document ({$gt: 3}) or a literal to compare for equality.
func matchField(doc map[string]any, path string, cond any) bool {
	val, present := lookupPath(doc, path)
	if om, ok := operatorDoc(cond); ok {
		return matchOperators(val, present, om)
	}
	return valueMatches(val, present, cond)
}

// operatorDoc reports whether cond is an operator document — a map whose keys
// all start with '$'. A map with plain keys is an embedded-document equality
// comparison, exactly as in the previous engine.
func operatorDoc(cond any) (map[string]any, bool) {
	m, ok := cond.(map[string]any)
	if !ok || len(m) == 0 {
		return nil, false
	}
	for k := range m {
		if !strings.HasPrefix(k, "$") {
			return nil, false
		}
	}
	return m, true
}

func matchOperators(val any, present bool, ops map[string]any) bool {
	// $regex and $options are one operator split across two keys.
	if rx, hasRx := ops["$regex"]; hasRx {
		opts, _ := ops["$options"].(string)
		if !matchRegex(val, present, rx, opts) {
			return false
		}
	}
	for op, arg := range ops {
		switch op {
		case "$regex", "$options":
			// handled above
		case "$eq":
			if !valueMatches(val, present, arg) {
				return false
			}
		case "$ne":
			if valueMatches(val, present, arg) {
				return false
			}
		case "$gt", "$gte", "$lt", "$lte":
			if !matchCompare(val, present, op, arg) {
				return false
			}
		case "$in":
			if !matchIn(val, present, arg) {
				return false
			}
		case "$nin":
			if matchIn(val, present, arg) {
				return false
			}
		case "$exists":
			want, ok := asBool(arg)
			if !ok || present != want {
				return false
			}
		case "$type":
			if !present || !matchType(val, arg) {
				return false
			}
		case "$size":
			arr, ok := val.([]any)
			if !present || !ok {
				return false
			}
			n, ok := asFloat(arg)
			if !ok || float64(len(arr)) != n {
				return false
			}
		case "$all":
			want, ok := asSlice(arg)
			if !present || !ok {
				return false
			}
			for _, w := range want {
				if !valueMatches(val, true, w) {
					return false
				}
			}
		case "$elemMatch":
			sub, ok := asFilter(arg)
			if !present || !ok {
				return false
			}
			arr, ok := val.([]any)
			if !ok {
				return false
			}
			hit := false
			for _, e := range arr {
				em, ok := e.(map[string]any)
				if !ok {
					// scalar element: apply the condition to the element itself
					if matchOperators(e, true, mustOperatorDoc(sub)) {
						hit = true
						break
					}
					continue
				}
				if matchFilter(em, sub) {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		case "$mod":
			pair, ok := asSlice(arg)
			if !present || !ok || len(pair) != 2 {
				return false
			}
			div, ok1 := asFloat(pair[0])
			rem, ok2 := asFloat(pair[1])
			num, ok3 := asFloat(val)
			if !ok1 || !ok2 || !ok3 || div == 0 {
				return false
			}
			if float64(int64(num)%int64(div)) != rem {
				return false
			}
		case "$not":
			sub, ok := operatorDoc(arg)
			if !ok {
				return false
			}
			if matchOperators(val, present, sub) {
				return false
			}
		default:
			return false // outside the allowlist: never matches
		}
	}
	return true
}

// mustOperatorDoc adapts a plain filter map to the operator form used when
// $elemMatch is applied to an array of scalars.
func mustOperatorDoc(m M) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if strings.HasPrefix(k, "$") {
			out[k] = v
		}
	}
	return out
}

// valueMatches implements equality with the array semantics of the previous
// engine: a field holding an array matches if the array itself equals the
// operand OR any element does.
func valueMatches(val any, present bool, want any) bool {
	if !present {
		return want == nil
	}
	if equalValues(val, want) {
		return true
	}
	if arr, ok := val.([]any); ok {
		for _, e := range arr {
			if equalValues(e, want) {
				return true
			}
		}
	}
	return false
}

func matchCompare(val any, present bool, op string, arg any) bool {
	if !present {
		return false
	}
	test := func(v any) bool {
		c, ok := compareValues(v, arg)
		if !ok {
			return false
		}
		switch op {
		case "$gt":
			return c > 0
		case "$gte":
			return c >= 0
		case "$lt":
			return c < 0
		case "$lte":
			return c <= 0
		}
		return false
	}
	if test(val) {
		return true
	}
	if arr, ok := val.([]any); ok {
		for _, e := range arr {
			if test(e) {
				return true
			}
		}
	}
	return false
}

func matchIn(val any, present bool, arg any) bool {
	want, ok := asSlice(arg)
	if !ok {
		return false
	}
	for _, w := range want {
		if valueMatches(val, present, w) {
			return true
		}
	}
	return false
}

func matchRegex(val any, present bool, pattern any, opts string) bool {
	if !present {
		return false
	}
	pat, ok := pattern.(string)
	if !ok {
		return false
	}
	var flags string
	for _, o := range opts {
		switch o {
		case 'i', 'm', 's':
			flags += string(o)
		}
	}
	if flags != "" {
		pat = "(?" + flags + ")" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return false
	}
	test := func(v any) bool {
		s, ok := v.(string)
		return ok && re.MatchString(s)
	}
	if test(val) {
		return true
	}
	if arr, ok := val.([]any); ok {
		for _, e := range arr {
			if test(e) {
				return true
			}
		}
	}
	return false
}

// matchType accepts the JSON-flavoured type names dopdb's wire protocol uses.
// Mongo's numeric BSON type codes have no meaning here and are rejected.
func matchType(val any, arg any) bool {
	want, ok := arg.(string)
	if !ok {
		return false
	}
	got := typeName(val)
	switch strings.ToLower(want) {
	case "number", "double", "int", "long", "decimal":
		return got == "number"
	case "bool", "boolean":
		return got == "bool"
	case "string":
		return got == "string"
	case "array":
		return got == "array"
	case "object":
		return got == "object"
	case "null":
		return got == "null"
	case "date", "timestamp":
		return got == "date"
	}
	return false
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case time.Time:
		return "date"
	}
	if _, ok := asFloat(v); ok {
		return "number"
	}
	return "unknown"
}

// ---- field access ----------------------------------------------------------

// lookupPath resolves a dot path against a decoded document. Intermediate
// arrays are traversed the way the previous engine did: "tags.name" against
// {tags:[{name:"a"}]} yields ["a"].
func lookupPath(doc map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = doc
	for i, p := range parts {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[p]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			// collect the sub-path from every element that has it
			var out []any
			for _, e := range node {
				if em, ok := e.(map[string]any); ok {
					if v, ok := em[p]; ok {
						out = append(out, v)
					}
				}
			}
			if len(out) == 0 {
				return nil, false
			}
			if i == len(parts)-1 {
				return out, true
			}
			cur = out
		default:
			return nil, false
		}
	}
	return cur, true
}

// ---- value comparison ------------------------------------------------------

// typeRank gives a total order across types so sorting a heterogeneous field is
// deterministic. This is dopdb's own ordering, not BSON's.
func typeRank(v any) int {
	switch v.(type) {
	case nil:
		return 0
	case bool:
		return 3
	case string:
		return 2
	case time.Time:
		return 4
	case []any:
		return 5
	case map[string]any:
		return 6
	}
	if _, ok := asFloat(v); ok {
		return 1
	}
	return 7
}

// compareValues returns -1/0/+1 and whether the two values are comparable at
// all. Values of different types compare by typeRank, so a sort never panics
// and never depends on map iteration order.
func compareValues(a, b any) (int, bool) {
	ra, rb := typeRank(a), typeRank(b)
	if ra != rb {
		if ra < rb {
			return -1, true
		}
		return 1, true
	}
	switch ra {
	case 0:
		return 0, true
	case 1:
		fa, _ := asFloat(a)
		fb, _ := asFloat(b)
		switch {
		case fa < fb:
			return -1, true
		case fa > fb:
			return 1, true
		}
		return 0, true
	case 2:
		return strings.Compare(a.(string), b.(string)), true
	case 3:
		ba, bb := a.(bool), b.(bool)
		switch {
		case !ba && bb:
			return -1, true
		case ba && !bb:
			return 1, true
		}
		return 0, true
	case 4:
		ta, tb := a.(time.Time), b.(time.Time)
		switch {
		case ta.Before(tb):
			return -1, true
		case ta.After(tb):
			return 1, true
		}
		return 0, true
	}
	// arrays / objects / unknown: equal or incomparable
	if equalValues(a, b) {
		return 0, true
	}
	return 0, false
}

// equalValues is deep equality with numeric coercion (a document decoded from
// CBOR carries int64 where the JSON filter carries float64).
func equalValues(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if fa, ok := asFloat(a); ok {
		if fb, ok := asFloat(b); ok {
			return fa == fb
		}
		return false
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case time.Time:
		switch bv := b.(type) {
		case time.Time:
			return av.Equal(bv)
		case string:
			t, err := time.Parse(time.RFC3339Nano, bv)
			return err == nil && av.Equal(t)
		}
		return false
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equalValues(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, x := range av {
			y, ok := bv[k]
			if !ok || !equalValues(x, y) {
				return false
			}
		}
		return true
	}
	return false
}

// asFloat coerces every numeric representation CBOR or JSON can produce.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

func asBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func asSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}

func asFilter(v any) (M, bool) {
	m, ok := v.(map[string]any)
	return M(m), ok
}

// ---- result shaping --------------------------------------------------------

// row is one candidate document during a scan: its id, its decoded form, and
// the raw bytes to hand back when it survives filtering.
type row struct {
	id  string
	doc map[string]any
	raw []byte
}

// applyFindOpt sorts, skips, limits and projects a matched result set. It is the
// in-process stand-in for the cursor options the driver used to send.
func applyFindOpt(rows []row, opt FindOpt) ([]row, error) {
	keys := opt.SortKeys
	if len(keys) == 0 && len(opt.Sort) > 0 {
		keys = sortKeysFromMap(opt.Sort)
	}
	if len(keys) > 0 {
		sortRows(rows, keys)
	}
	if opt.Skip > 0 {
		if int(opt.Skip) >= len(rows) {
			rows = nil
		} else {
			rows = rows[opt.Skip:]
		}
	}
	if opt.Limit > 0 && int64(len(rows)) > opt.Limit {
		rows = rows[:opt.Limit]
	}
	if len(opt.Projection) > 0 {
		for i := range rows {
			projected := applyProjection(rows[i].doc, opt.Projection)
			b, err := encodeCBOR(projected)
			if err != nil {
				return nil, fmt.Errorf("dopdb: project: %w", err)
			}
			rows[i].doc = projected
			rows[i].raw = b
		}
	}
	return rows, nil
}

// sortKeysFromMap turns the unordered Sort map into ordered directives. Map
// iteration is randomised in Go, so the field names are sorted first to keep a
// multi-key Sort map at least deterministic; SortKeys remains the way to state
// an intended order.
func sortKeysFromMap(m M) []SortKey {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]SortKey, 0, len(names))
	for _, k := range names {
		asc := true
		if f, ok := asFloat(m[k]); ok && f < 0 {
			asc = false
		}
		if b, ok := m[k].(bool); ok && !b {
			asc = false
		}
		out = append(out, SortKey{Field: k, Asc: asc})
	}
	return out
}

func sortRows(rows []row, keys []SortKey) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, k := range keys {
			var a, b any
			if k.Field == "_id" {
				a, b = rows[i].id, rows[j].id
			} else {
				a, _ = lookupPath(rows[i].doc, k.Field)
				b, _ = lookupPath(rows[j].doc, k.Field)
			}
			c, ok := compareValues(a, b)
			if !ok || c == 0 {
				continue
			}
			if k.Asc {
				return c < 0
			}
			return c > 0
		}
		return false
	})
}

// applyProjection returns a copy of doc restricted (or reduced) by proj.
// Include mode and exclude mode are mutually exclusive, as before; _id follows
// its own flag and is included by default in include mode.
func applyProjection(doc map[string]any, proj M) map[string]any {
	include := false
	for k, v := range proj {
		if k == "_id" {
			continue
		}
		if truthyProj(v) {
			include = true
			break
		}
	}
	out := map[string]any{}
	if include {
		for k, v := range proj {
			if !truthyProj(v) || k == "_id" {
				continue
			}
			if val, ok := lookupPath(doc, k); ok {
				setPath(out, k, val)
			}
		}
		if idv, ok := proj["_id"]; !ok || truthyProj(idv) {
			if v, ok := doc["_id"]; ok {
				out["_id"] = v
			}
		}
		return out
	}
	for k, v := range doc {
		out[k] = v
	}
	for k, v := range proj {
		if !truthyProj(v) {
			delete(out, k)
		}
	}
	return out
}

func truthyProj(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	if f, ok := asFloat(v); ok {
		return f != 0
	}
	return false
}

func setPath(dst map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	cur := dst
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = val
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}
