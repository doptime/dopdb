package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `
# dopdb config
[http]
addr           = ":9000"
jwt_secret_env = "TEST_JWT_SECRET"
cors_origins   = ["https://a.example.com", "https://b.example.com"]

[[mongo]]
name    = "default"
uri_env = "TEST_MONGO_URI"
uri     = "mongodb://localhost:27017"   # dev fallback
db      = "appdb"

[[mongo]]
name = "analytics"
uri  = "mongodb://localhost:27017"
db   = "analytics"
`

func writeTmp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAndEnvOverride(t *testing.T) {
	t.Setenv("TEST_JWT_SECRET", "s3cr3t")
	t.Setenv("TEST_MONGO_URI", "mongodb://user:pw@prod:27017/?authSource=admin")

	cfg, err := Load(writeTmp(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != ":9000" {
		t.Errorf("addr=%q", cfg.HTTP.Addr)
	}
	if cfg.HTTP.JWTSecret != "s3cr3t" {
		t.Errorf("jwt secret not resolved from env: %q", cfg.HTTP.JWTSecret)
	}
	if len(cfg.HTTP.CORSOrigins) != 2 || cfg.HTTP.CORSOrigins[0] != "https://a.example.com" {
		t.Errorf("cors=%v", cfg.HTTP.CORSOrigins)
	}
	def := cfg.Default()
	if def.URI != "mongodb://user:pw@prod:27017/?authSource=admin" {
		t.Errorf("default uri not overridden by env: %q", def.URI)
	}
	if def.DB != "appdb" {
		t.Errorf("default db=%q", def.DB)
	}
	if _, ok := cfg.Source("analytics"); !ok {
		t.Error("analytics source missing")
	}
}

func TestEnvFallbackToLiteral(t *testing.T) {
	// env not set -> literal uri is used
	t.Setenv("TEST_JWT_SECRET", "x")
	cfg, err := Load(writeTmp(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Default().URI != "mongodb://localhost:27017" {
		t.Errorf("expected literal uri fallback, got %q", cfg.Default().URI)
	}
}

func TestValidateRequiresDefault(t *testing.T) {
	t.Setenv("TEST_JWT_SECRET", "x")
	body := `
[http]
jwt_secret_env = "TEST_JWT_SECRET"
[[mongo]]
name = "analytics"
uri  = "mongodb://localhost:27017"
db   = "analytics"
`
	if _, err := Load(writeTmp(t, body)); err == nil {
		t.Fatal("expected error: missing default datasource")
	}
}

func TestValidateRequiresSecret(t *testing.T) {
	body := `
[http]
jwt_secret_env = "DEFINITELY_UNSET_VAR_XYZ"
[[mongo]]
name = "default"
uri  = "mongodb://localhost:27017"
db   = "appdb"
`
	if _, err := Load(writeTmp(t, body)); err == nil {
		t.Fatal("expected error: empty jwt secret")
	}
}

func TestWarnings(t *testing.T) {
	cfg := &Config{
		HTTP: HTTPConfig{JWTSecret: "x"},
		Mongo: []MongoSource{
			{Name: "default", URI: "mongodb://user:pw@h:27017", DB: "d"}, // creds in literal
		},
	}
	w := cfg.Warnings()
	if len(w) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(w), w)
	}
}

func TestStripCommentRespectsQuotes(t *testing.T) {
	// a '#' inside a quoted value must survive
	body := `
[http]
jwt_secret_env = "TEST_JWT_SECRET"
addr = "host#notacomment:80"   # real comment
[[mongo]]
name = "default"
uri  = "mongodb://localhost:27017"
db   = "appdb"
`
	t.Setenv("TEST_JWT_SECRET", "x")
	cfg, err := Load(writeTmp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != "host#notacomment:80" {
		t.Errorf("addr=%q (comment stripping ate a quoted #)", cfg.HTTP.Addr)
	}
}
