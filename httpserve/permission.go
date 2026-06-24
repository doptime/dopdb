package httpserve

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
)

// Permissions is the black/white list gating data commands, keyed by
// "COMMAND::collection". It mirrors doptime's single-list on/off model
// (command::key::on/off) so existing operational habits carry over; here the
// "key" is a collection name.
//
// This in-memory implementation is sufficient for a single process and for dev
// (with AutoAuth). For a cluster, back the same interface with a dopdb
// collection ("_permissions", _id = "CMD::coll", value {on: bool}) so the list
// is shared — the HTTP layer only calls Allowed/Grant.
type Permissions struct {
	mu sync.RWMutex
	m  map[string]bool // "CMD::coll" -> on
	// AutoAuth: in development, grant-on-first-use so the whitelist builds
	// itself exactly to what the client exercises. NEVER enable in production.
	AutoAuth bool
}

// NewPermissions returns an empty permission set. Pass autoAuth=true only in dev.
func NewPermissions(autoAuth bool) *Permissions {
	return &Permissions{m: map[string]bool{}, AutoAuth: autoAuth}
}

func permKey(cmd, coll string) string {
	return strings.ToUpper(cmd) + "::" + coll
}

// Allowed reports whether (cmd, coll) is permitted. With AutoAuth on, an unseen
// pair is granted and reported allowed.
func (p *Permissions) Allowed(cmd, coll string) bool {
	key := permKey(cmd, coll)
	p.mu.RLock()
	on, seen := p.m[key]
	auto := p.AutoAuth
	p.mu.RUnlock()
	if seen {
		return on
	}
	if auto {
		p.mu.Lock()
		p.m[key] = true
		p.mu.Unlock()
		return true
	}
	return false
}

// Grant adds an allow entry.
func (p *Permissions) Grant(cmd, coll string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[permKey(cmd, coll)] = true
}

// Deny adds a deny entry (an explicit off, which beats AutoAuth).
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

// LoadJSON deserialises a JSON file back into a Permissions instance with
// AutoAuth disabled.
func LoadJSON(path string) (*Permissions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]bool
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &Permissions{m: m, AutoAuth: false}, nil
}
