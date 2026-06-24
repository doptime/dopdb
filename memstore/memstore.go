// Package memstore provides an in-memory implementation of dopdb.Store and a
// JSON codec, for unit-testing application code without a running MongoDB. It is
// NOT for production: no durability, no indexes, a simplified filter evaluator.
package memstore

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/doptime/dopdb"
)

// JSONCodec encodes documents as JSON (the production codec is BSON).
type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (JSONCodec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// Store is an in-memory dopdb.Store.
type Store struct {
	mu   sync.RWMutex
	data map[string]map[string][]byte // coll -> id -> doc(JSON, includes _id)
}

// New returns an empty in-memory Store.
func New() *Store { return &Store{data: map[string]map[string][]byte{}} }

var _ dopdb.Store = (*Store)(nil)

// injectID mirrors the Mongo adapter: the stored document always carries _id, so
// Find({"_id": ...}) behaves identically to production.
func injectID(id string, doc []byte) []byte {
	m := map[string]any{}
	if len(doc) > 0 {
		_ = json.Unmarshal(doc, &m)
	}
	m["_id"] = id
	b, _ := json.Marshal(m)
	return b
}

func (s *Store) col(coll string) map[string][]byte {
	c, ok := s.data[coll]
	if !ok {
		c = map[string][]byte{}
		s.data[coll] = c
	}
	return c
}

func (s *Store) EnsureIndex(_ context.Context, _ string, _ dopdb.IndexSpec) error { return nil }

func (s *Store) Put(_ context.Context, coll, id string, doc []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.col(coll)[id] = injectID(id, doc)
	return nil
}

func (s *Store) PutIfAbsent(_ context.Context, coll, id string, doc []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.col(coll)
	if _, ok := c[id]; ok {
		return false, nil
	}
	c[id] = injectID(id, doc)
	return true, nil
}

func (s *Store) PutMany(_ context.Context, coll string, ids []string, docs [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.col(coll)
	for i, id := range ids {
		c[id] = injectID(id, docs[i])
	}
	return nil
}

func (s *Store) PutScoped(_ context.Context, coll, id string, doc []byte, ownerField, ownerVal string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.col(coll)
	if existing, ok := c[id]; ok {
		var em map[string]any
		_ = json.Unmarshal(existing, &em)
		if ov, _ := em[ownerField].(string); ov != ownerVal {
			return dopdb.ErrForbidden // exists, owned by someone else
		}
	}
	m := map[string]any{}
	if len(doc) > 0 {
		_ = json.Unmarshal(doc, &m)
	}
	m["_id"] = id
	m[ownerField] = ownerVal // force owner (non-forgeable), mirrors mongostore
	b, _ := json.Marshal(m)
	c[id] = b
	return nil
}

func (s *Store) Get(_ context.Context, coll, id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if b, ok := s.col(coll)[id]; ok {
		return b, nil
	}
	return nil, dopdb.ErrNoDoc
}

func (s *Store) GetMany(_ context.Context, coll string, ids []string) ([][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.col(coll)
	out := make([][]byte, len(ids))
	for i, id := range ids {
		if b, ok := c[id]; ok {
			out[i] = b
		}
	}
	return out, nil
}

func (s *Store) Delete(_ context.Context, coll string, ids []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.col(coll)
	var n int64
	for _, id := range ids {
		if _, ok := c[id]; ok {
			delete(c, id)
			n++
		}
	}
	return n, nil
}

func (s *Store) Exists(_ context.Context, coll, id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.col(coll)[id]
	return ok, nil
}

func (s *Store) IDs(_ context.Context, coll string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.col(coll)
	out := make([]string, 0, len(c))
	for id := range c {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) All(_ context.Context, coll string) ([]string, [][]byte, error) {
	ids, _ := s.IDs(context.Background(), coll)
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.col(coll)
	docs := make([][]byte, len(ids))
	for i, id := range ids {
		docs[i] = c[id]
	}
	return ids, docs, nil
}

func (s *Store) Count(_ context.Context, coll string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.col(coll))), nil
}

func (s *Store) Incr(_ context.Context, coll, id, fieldPath string, delta float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.col(coll)
	doc := map[string]any{}
	if b, ok := c[id]; ok {
		_ = json.Unmarshal(b, &doc)
	}
	cur, _ := doc[fieldPath].(float64)
	doc[fieldPath] = cur + delta
	doc["_id"] = id
	b, _ := json.Marshal(doc)
	c[id] = b
	return nil
}

func (s *Store) Find(_ context.Context, coll string, filter dopdb.M, opt dopdb.FindOpt) ([]string, [][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.col(coll)
	var ids []string
	for id := range c {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var outIDs []string
	var outDocs [][]byte
	for _, id := range ids {
		var doc map[string]any
		if err := json.Unmarshal(c[id], &doc); err != nil {
			continue
		}
		if matchFilter(doc, filter) {
			outIDs = append(outIDs, id)
			outDocs = append(outDocs, c[id])
		}
	}
	if opt.Skip > 0 {
		if int64(len(outIDs)) <= opt.Skip {
			return nil, nil, nil
		}
		outIDs, outDocs = outIDs[opt.Skip:], outDocs[opt.Skip:]
	}
	if opt.Limit > 0 && int64(len(outIDs)) > opt.Limit {
		outIDs, outDocs = outIDs[:opt.Limit], outDocs[:opt.Limit]
	}
	return outIDs, outDocs, nil
}

// matchFilter is a minimal evaluator: enough operators to exercise sanitized
// queries in tests ($and/$or plus the common field operators).
func matchFilter(doc map[string]any, filter dopdb.M) bool {
	for k, cond := range filter {
		switch k {
		case "$and":
			for _, sub := range cond.([]any) {
				if !matchFilter(doc, toM(sub)) {
					return false
				}
			}
		case "$or":
			matched := false
			for _, sub := range cond.([]any) {
				if matchFilter(doc, toM(sub)) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		default:
			if !matchField(doc[k], cond) {
				return false
			}
		}
	}
	return true
}

func toM(v any) dopdb.M {
	if m, ok := v.(dopdb.M); ok {
		return m
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return dopdb.M{}
}

func matchField(actual, cond any) bool {
	condM, ok := cond.(map[string]any)
	if !ok {
		return equalJSON(actual, cond)
	}
	for op, want := range condM {
		switch op {
		case "$eq":
			if !equalJSON(actual, want) {
				return false
			}
		case "$ne":
			if equalJSON(actual, want) {
				return false
			}
		case "$gt", "$gte", "$lt", "$lte":
			af, aok := toFloat(actual)
			wf, wok := toFloat(want)
			if !aok || !wok {
				return false
			}
			switch op {
			case "$gt":
				if !(af > wf) {
					return false
				}
			case "$gte":
				if !(af >= wf) {
					return false
				}
			case "$lt":
				if !(af < wf) {
					return false
				}
			case "$lte":
				if !(af <= wf) {
					return false
				}
			}
		case "$in":
			found := false
			for _, w := range want.([]any) {
				if equalJSON(actual, w) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case "$exists":
			wantExists, _ := want.(bool)
			if (actual != nil) != wantExists {
				return false
			}
		case "$regex":
			s, _ := actual.(string)
			sub, _ := want.(string)
			if !strings.Contains(s, sub) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func equalJSON(a, b any) bool {
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	return a == b
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}
