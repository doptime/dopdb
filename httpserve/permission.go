package httpserve

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
)

// Permissions is the black/white list gating data + API commands, keyed by
// "COMMAND::collection". It mirrors doptime's single-list on/off model
// (command::key::on/off) so existing operational habits carry over; here the
// "key" is a collection name (or an API endpoint name).
//
// Default is DENY: a pair that was never granted is refused. (The former
// dev-only AutoAuth grant-on-first-use was removed — grants are always explicit.)
//
// This in-memory implementation is sufficient for a single process; persist it
// with SaveJSON / LoadJSON, or for a cluster back the same calls with a shared
// dopdb collection — the HTTP layer only calls Allowed/Grant/Deny.
type Permissions struct {
	mu sync.RWMutex
	m  map[string]bool // "CMD::coll" -> on
}

// NewPermissions returns an empty permission set (default deny).
func NewPermissions() *Permissions {
	return &Permissions{m: map[string]bool{}}
}

func permKey(cmd, coll string) string {
	return strings.ToUpper(cmd) + "::" + coll
}

// Allowed reports whether (cmd, coll) is explicitly permitted. Unknown pairs are
// denied.
func (p *Permissions) Allowed(cmd, coll string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.m[permKey(cmd, coll)]
}

// Grant adds an allow entry.
func (p *Permissions) Grant(cmd, coll string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[permKey(cmd, coll)] = true
}

// Deny adds an explicit deny entry.
func (p *Permissions) Deny(cmd, coll string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[permKey(cmd, coll)] = false
}

// SaveJSON serialises the current permission map to a JSON file.
func (p *Permissions) SaveJSON(path string) error {
	p.mu.RLock()
	data, err := json.MarshalIndent(p.m, "", "  ")
	p.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadJSON deserialises a JSON file back into a Permissions instance.
func LoadJSON(path string) (*Permissions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]bool
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &Permissions{m: m}, nil
}
