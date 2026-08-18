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

[[kvrocks]]
name         = "default"
uri_env      = "TEST_KVROCKS_URI"
uri          = "redis://localhost:6666"   # dev fallback
password_env = "TEST_KVROCKS_PASSWORD"
namespace    = "appdb"

[[kvrocks]]
name      = "analytics"
uri       = "redis://localhost:6666"
namespace = "analytics"
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
	t.Setenv("TEST_KVROCKS_URI", "redis://prod:6666")
	t.Setenv("TEST_KVROCKS_PASSWORD", "ns-token")

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
	if def.URI != "redis://prod:6666" {
		t.Errorf("default uri not overridden by env: %q", def.URI)
	}
	if def.Password != "ns-token" {
		t.Errorf("password not resolved from env: %q", def.Password)
	}
	if def.Namespace != "appdb" {
		t.Errorf("default namespace=%q", def.Namespace)
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
	if cfg.Default().URI != "redis://localhost:6666" {
		t.Errorf("expected literal uri fallback, got %q", cfg.Default().URI)
	}
}

func TestValidateRequiresDefault(t *testing.T) {
	t.Setenv("TEST_JWT_SECRET", "x")
	body := `
[http]
jwt_secret_env = "TEST_JWT_SECRET"
[[kvrocks]]
name      = "analytics"
uri       = "redis://localhost:6666"
namespace = "analytics"
`
	if _, err := Load(writeTmp(t, body)); err == nil {
		t.Fatal("expected error: missing default datasource")
	}
}

func TestValidateRequiresSecret(t *testing.T) {
	body := `
[http]
jwt_secret_env = "DEFINITELY_UNSET_VAR_XYZ"
[[kvrocks]]
name      = "default"
uri       = "redis://localhost:6666"
namespace = "appdb"
`
	if _, err := Load(writeTmp(t, body)); err == nil {
		t.Fatal("expected error: empty jwt secret")
	}
}

// A mongodb:// URI is now a configuration error rather than a silent
// misconfiguration that only fails at dial time.
func TestValidateRejectsNonRedisURI(t *testing.T) {
	t.Setenv("TEST_JWT_SECRET", "x")
	body := `
[http]
jwt_secret_env = "TEST_JWT_SECRET"
[[kvrocks]]
name      = "default"
uri       = "mongodb://localhost:27017"
namespace = "appdb"
`
	if _, err := Load(writeTmp(t, body)); err == nil {
		t.Fatal("expected error: uri must be redis:// or rediss://")
	}
}

func TestValidateRequiresNamespace(t *testing.T) {
	t.Setenv("TEST_JWT_SECRET", "x")
	body := `
[http]
jwt_secret_env = "TEST_JWT_SECRET"
[[kvrocks]]
name = "default"
uri  = "redis://localhost:6666"
`
	if _, err := Load(writeTmp(t, body)); err == nil {
		t.Fatal("expected error: missing namespace")
	}
}

func TestWarnings(t *testing.T) {
	cfg := &Config{
		HTTP: HTTPConfig{JWTSecret: "x"},
		Kvrocks: []KvrocksSource{
			{Name: "default", URI: "redis://user:pw@h:6666", Namespace: "d"}, // creds in literal
		},
	}
	w := cfg.Warnings()
	if len(w) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(w), w)
	}
}

func TestWarnsOnLiteralPassword(t *testing.T) {
	cfg := &Config{
		HTTP: HTTPConfig{JWTSecret: "x"},
		Kvrocks: []KvrocksSource{
			{Name: "default", URI: "redis://h:6666", Password: "hunter2", Namespace: "d"},
		},
	}
	if w := cfg.Warnings(); len(w) != 1 {
		t.Fatalf("expected 1 warning for a literal password, got %d: %v", len(w), w)
	}
}

func TestStripCommentRespectsQuotes(t *testing.T) {
	// a '#' inside a quoted value must survive
	body := `
[http]
jwt_secret_env = "TEST_JWT_SECRET"
addr = "host#notacomment:80"   # real comment
[[kvrocks]]
name      = "default"
uri       = "redis://localhost:6666"
namespace = "appdb"
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
