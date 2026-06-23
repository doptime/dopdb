// Package config loads dopdb runtime configuration from a TOML file, resolving
// secrets from environment variables (never from the file — RL4).
//
// It is dependency-free (a small TOML reader covering exactly this schema), so
// it compiles and is testable without external modules. Schema:
//
//	[http]
//	addr           = ":8080"
//	jwt_secret_env = "DOPTIME_JWT_SECRET"   # env var holding the HS256 key / RS256 PEM
//	auto_auth      = false                  # dev-only grant-on-first-use; MUST be false in prod
//	cors_origins   = ["https://app.example.com"]
//
//	[[mongo]]
//	name    = "default"                     # a "default" source is required
//	uri_env = "DOPTIME_MONGO_URI"           # env var holding the connection string (may carry creds)
//	uri     = "mongodb://localhost:27017"   # literal fallback (dev only); env wins if set
//	db      = "appdb"
//
//	[[mongo]]
//	name = "analytics"
//	uri  = "mongodb://localhost:27017"
//	db   = "analytics"
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// HTTPConfig is the [http] section.
type HTTPConfig struct {
	Addr         string
	JWTSecretEnv string
	JWTSecret    string // resolved from JWTSecretEnv at Load time; never read from the file
	AutoAuth     bool
	CORSOrigins  []string
}

// MongoSource is one [[mongo]] datasource.
type MongoSource struct {
	Name   string
	URIEnv string
	URI    string // resolved: env (URIEnv) wins over the literal
	DB     string
}

// Config is the whole document.
type Config struct {
	HTTP  HTTPConfig
	Mongo []MongoSource
}

// Load reads and resolves a config file. Secrets and connection strings are
// pulled from the named environment variables, overriding any literal in the
// file. The result is validated.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg, err := parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.resolveEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolveEnv applies environment overrides for secrets / connection strings.
func (c *Config) resolveEnv() {
	if c.HTTP.JWTSecretEnv != "" {
		if v := os.Getenv(c.HTTP.JWTSecretEnv); v != "" {
			c.HTTP.JWTSecret = v
		}
	}
	for i := range c.Mongo {
		if c.Mongo[i].URIEnv != "" {
			if v := os.Getenv(c.Mongo[i].URIEnv); v != "" {
				c.Mongo[i].URI = v
			}
		}
	}
}

// Source returns the datasource with the given name.
func (c *Config) Source(name string) (MongoSource, bool) {
	for _, m := range c.Mongo {
		if m.Name == name {
			return m, true
		}
	}
	return MongoSource{}, false
}

// Default returns the "default" datasource (required to exist).
func (c *Config) Default() MongoSource {
	m, _ := c.Source("default")
	return m
}

// Validate enforces the invariants the framework relies on.
func (c *Config) Validate() error {
	if len(c.Mongo) == 0 {
		return fmt.Errorf("config: at least one [[mongo]] datasource is required")
	}
	if _, ok := c.Source("default"); !ok {
		return fmt.Errorf("config: a [[mongo]] datasource named \"default\" is required")
	}
	seen := map[string]bool{}
	for _, m := range c.Mongo {
		if m.Name == "" {
			return fmt.Errorf("config: a [[mongo]] datasource is missing name")
		}
		if seen[m.Name] {
			return fmt.Errorf("config: duplicate datasource name %q", m.Name)
		}
		seen[m.Name] = true
		if m.URI == "" {
			return fmt.Errorf("config: datasource %q has no uri (set uri or %s)", m.Name, m.URIEnv)
		}
		if m.DB == "" {
			return fmt.Errorf("config: datasource %q has no db", m.Name)
		}
	}
	if c.HTTP.JWTSecret == "" {
		return fmt.Errorf("config: http jwt secret is empty (set env %q)", c.HTTP.JWTSecretEnv)
	}
	return nil
}

// Warnings surfaces non-fatal risks (e.g. dev settings left on). The caller may
// log these at startup.
func (c *Config) Warnings() []string {
	var w []string
	if c.HTTP.AutoAuth {
		w = append(w, "http.auto_auth is ON — grant-on-first-use; never run this in production")
	}
	for _, m := range c.Mongo {
		if m.URIEnv == "" && strings.Contains(m.URI, "@") {
			w = append(w, fmt.Sprintf("datasource %q has credentials in a literal uri — move it to %s via uri_env", m.Name, "an env var"))
		}
	}
	return w
}

// ----------------------------------------------------------------------------
// Minimal TOML reader (covers exactly this schema: [section], [[array]], and
// key = string | int | float | bool | [string,...]). Not a general TOML parser.
// ----------------------------------------------------------------------------

func parse(text string) (*Config, error) {
	cfg := &Config{}
	var (
		section   string       // current [section]
		mongoOpen bool         // inside an [[mongo]] element
		cur       *MongoSource // current [[mongo]] element
	)
	lines := strings.Split(text, "\n")
	for n, raw := range lines {
		line := stripComment(strings.TrimSpace(raw))
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]"):
			name := strings.TrimSpace(line[2 : len(line)-2])
			if name != "mongo" {
				return nil, fmt.Errorf("line %d: unknown array-of-tables [[%s]]", n+1, name)
			}
			cfg.Mongo = append(cfg.Mongo, MongoSource{})
			cur = &cfg.Mongo[len(cfg.Mongo)-1]
			mongoOpen = true
			section = ""
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.TrimSpace(line[1 : len(line)-1])
			mongoOpen = false
			cur = nil
		default:
			key, val, err := splitKV(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			if mongoOpen {
				if err := assignMongo(cur, key, val, n+1); err != nil {
					return nil, err
				}
			} else if section == "http" {
				if err := assignHTTP(&cfg.HTTP, key, val, n+1); err != nil {
					return nil, err
				}
			} else {
				return nil, fmt.Errorf("line %d: key %q outside a known section", n+1, key)
			}
		}
	}
	return cfg, nil
}

func assignHTTP(h *HTTPConfig, key, val string, line int) error {
	switch key {
	case "addr":
		h.Addr = mustString(val)
	case "jwt_secret_env":
		h.JWTSecretEnv = mustString(val)
	case "jwt_secret":
		h.JWTSecret = mustString(val) // dev only; prefer jwt_secret_env
	case "auto_auth":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("line %d: auto_auth must be true/false", line)
		}
		h.AutoAuth = b
	case "cors_origins":
		h.CORSOrigins = parseStringArray(val)
	default:
		return fmt.Errorf("line %d: unknown http key %q", line, key)
	}
	return nil
}

func assignMongo(m *MongoSource, key, val string, line int) error {
	switch key {
	case "name":
		m.Name = mustString(val)
	case "uri_env":
		m.URIEnv = mustString(val)
	case "uri":
		m.URI = mustString(val)
	case "db":
		m.DB = mustString(val)
	default:
		return fmt.Errorf("line %d: unknown mongo key %q", line, key)
	}
	return nil
}

func splitKV(line string) (key, val string, err error) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", "", fmt.Errorf("expected key = value, got %q", line)
	}
	return strings.TrimSpace(line[:eq]), strings.TrimSpace(line[eq+1:]), nil
}

// stripComment removes a trailing # comment that is not inside quotes.
func stripComment(line string) string {
	inS, inD := false, false
	for i, r := range line {
		switch r {
		case '\'':
			if !inD {
				inS = !inS
			}
		case '"':
			if !inS {
				inD = !inD
			}
		case '#':
			if !inS && !inD {
				return strings.TrimSpace(line[:i])
			}
		}
	}
	return line
}

func mustString(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v // lenient: accept bareword
}

func parseStringArray(v string) []string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil
	}
	inner := strings.TrimSpace(v[1 : len(v)-1])
	if inner == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, mustString(part))
	}
	return out
}
