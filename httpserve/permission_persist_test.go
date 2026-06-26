package httpserve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadJSON(t *testing.T) {
	p := NewPermissions()

	// Grant 5 entries
	grants := [][2]string{
		{"HGET", "User"},
		{"HSET", "User"},
		{"HDEL", "Order"},
		{"FIND", "Order"},
		{"HKEYS", "User"},
	}
	for _, g := range grants {
		p.Grant(g[0], g[1])
	}

	// Deny 2 entries
	denies := [][2]string{
		{"DEL", "User"},
		{"HSET", "Order"},
	}
	for _, d := range denies {
		p.Deny(d[0], d[1])
	}

	tmp := filepath.Join(t.TempDir(), "perm.json")

	// Save
	if err := p.SaveJSON(tmp); err != nil {
		t.Fatalf("SaveJSON failed: %v", err)
	}

	// Load
	q, err := LoadJSON(tmp)
	if err != nil {
		t.Fatalf("LoadJSON failed: %v", err)
	}

	// Verify all 7 granted/denied keys match
	allKeys := append(grants, denies[:]...)
	for _, k := range allKeys {
		if got := q.Allowed(k[0], k[1]); got != p.Allowed(k[0], k[1]) {
			t.Errorf("Allowed(%q, %q): got %v, want %v", k[0], k[1], got, p.Allowed(k[0], k[1]))
		}
	}

	// Unknown key must return false (default deny)
	if q.Allowed("UNKNOWN", "Collection") {
		t.Error("expected false for unknown key (default deny)")
	}

	// LoadJSON on non-existent file must error
	_, err = LoadJSON(tmp + ".nope")
	if err == nil {
		t.Error("expected error loading non-existent file")
	} else if !os.IsNotExist(err) {
		t.Errorf("expected not-exist error, got: %v", err)
	}
}
