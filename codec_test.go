package dopdb

import (
	"bytes"
	"testing"
	"time"
)

// The storage format moved from BSON to CBOR. These tests pin the two properties
// the rest of the framework relies on: json tags name the stored fields, and the
// encoding is deterministic (set members and unique-index claims are compared as
// bytes, so two equal values must encode identically).

type codecUser struct {
	Name      string    `json:"name"`
	Age       int       `json:"age"`
	Nick      string    `json:"nick,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Skip      string    `json:"-"`
}

func TestCBORRoundTrip(t *testing.T) {
	in := codecUser{Name: "Ada", Age: 30, CreatedAt: time.Now().UTC().Truncate(time.Nanosecond), Skip: "dropped"}
	b, err := encodeCBOR(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out codecUser
	if err := decodeCBOR(b, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != in.Name || out.Age != in.Age {
		t.Errorf("round trip = %+v want %+v", out, in)
	}
	if !out.CreatedAt.Equal(in.CreatedAt) {
		t.Errorf("time round trip: %v != %v", out.CreatedAt, in.CreatedAt)
	}
	if out.Skip != "" {
		t.Errorf(`json:"-" field was stored: %q`, out.Skip)
	}
}

// Field names come from the json tags, so what is stored and what crosses HTTP
// carry identical names. This is why the bson tags could simply be deleted.
func TestCBORUsesJSONTagNames(t *testing.T) {
	b, err := encodeCBOR(codecUser{Name: "Ada", Age: 1})
	if err != nil {
		t.Fatal(err)
	}
	m, err := decodeDoc(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["name"]; !ok {
		t.Errorf("expected lower-case json field name, got keys %v", keysOf(m))
	}
	if _, ok := m["Name"]; ok {
		t.Error("Go field name leaked into storage")
	}
	if _, ok := m["nick"]; ok {
		t.Error("omitempty field should be absent when zero")
	}
}

// decodeDoc must yield map[string]any (not map[any]any) or the query engine
// cannot walk a document.
func TestDecodeDocGivesStringKeyedMaps(t *testing.T) {
	b, err := encodeCBOR(map[string]any{"a": map[string]any{"b": 1}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := decodeDoc(b)
	if err != nil {
		t.Fatal(err)
	}
	nested, ok := m["a"].(map[string]any)
	if !ok {
		t.Fatalf("nested map is %T, want map[string]any", m["a"])
	}
	if _, ok := asFloat(nested["b"]); !ok {
		t.Errorf("nested number is %T", nested["b"])
	}
}

// Canonical encoding: equal values must produce identical bytes, in any map
// insertion order. Set deduplication and unique-index claims both depend on it.
func TestCBOREncodingIsDeterministic(t *testing.T) {
	a, err := encodeCBOR(map[string]any{"x": 1, "y": "two", "z": []any{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodeCBOR(map[string]any{"z": []any{1, 2}, "y": "two", "x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("canonical encoding is not order-independent:\n%x\n%x", a, b)
	}
}

func TestUniqueValueKeyMatchesEqualValues(t *testing.T) {
	k1, ok1 := uniqueValueKey("ada@x.io")
	k2, ok2 := uniqueValueKey("ada@x.io")
	if !ok1 || !ok2 || k1 != k2 {
		t.Errorf("equal values produced different unique slots: %q vs %q", k1, k2)
	}
	if _, ok := uniqueValueKey(nil); ok {
		t.Error("nil must not claim a unique slot (sparse behaviour)")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
