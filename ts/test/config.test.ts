import { test } from "node:test";
import assert from "node:assert/strict";
import { writeFileSync, mkdtempSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

import { loadConfig, defaultSource, source, portFromAddr } from "../src/config.js";

const TOML = `
# dopdb config
[http]
addr           = ":9000"
jwt_secret_env = "CFG_SECRET"
cors_origins   = ["https://a.example.com", "https://b.example.com"]

[[kvrocks]]
name         = "default"
uri_env      = "CFG_KVROCKS"
uri          = "redis://localhost:6666"
password_env = "CFG_KVROCKS_PW"
namespace    = "appdb"

[[kvrocks]]
name      = "analytics"
uri       = "redis://localhost:6667"  # inline comment
namespace = "analytics"
`;

function writeTmp(body: string, name = "config.toml"): string {
  const dir = mkdtempSync(join(tmpdir(), "dopdb-cfg-"));
  const p = join(dir, name);
  writeFileSync(p, body);
  return p;
}

test("TOML: parses http + [[kvrocks]] tables and arrays", () => {
  delete process.env.CFG_KVROCKS;
  delete process.env.CFG_KVROCKS_PW;
  process.env.CFG_SECRET = "s";
  const cfg = loadConfig(writeTmp(TOML));
  assert.equal(cfg.http.addr, ":9000");
  assert.deepEqual(cfg.http.corsOrigins, ["https://a.example.com", "https://b.example.com"]);
  assert.equal(cfg.kvrocks.length, 2);
  assert.equal(defaultSource(cfg).namespace, "appdb");
  assert.equal(source(cfg, "analytics")?.uri, "redis://localhost:6667"); // comment stripped
});

test("env resolution: jwt secret, uri and password come from env, not the file", () => {
  process.env.CFG_SECRET = "from-env";
  process.env.CFG_KVROCKS = "redis://prod-host:6666";
  process.env.CFG_KVROCKS_PW = "ns-token";
  const cfg = loadConfig(writeTmp(TOML));
  assert.equal(cfg.http.jwtSecret, "from-env");
  assert.equal(defaultSource(cfg).uri, "redis://prod-host:6666"); // env wins over literal
  assert.equal(defaultSource(cfg).password, "ns-token");
  delete process.env.CFG_KVROCKS;
  delete process.env.CFG_KVROCKS_PW;
});

test("validate: a default source is required", () => {
  process.env.CFG_SECRET = "s";
  const body = `
[http]
jwt_secret_env = "CFG_SECRET"
[[kvrocks]]
name      = "analytics"
uri       = "redis://x:6666"
namespace = "a"
`;
  assert.throws(() => loadConfig(writeTmp(body)), /default/);
});

// A mongodb:// URI is now a configuration error rather than a misconfiguration
// that only surfaces at dial time.
test("validate: the uri must be redis:// or rediss://", () => {
  process.env.CFG_SECRET = "s";
  const body = `
[http]
jwt_secret_env = "CFG_SECRET"
[[kvrocks]]
name      = "default"
uri       = "mongodb://localhost:27017"
namespace = "appdb"
`;
  assert.throws(() => loadConfig(writeTmp(body)), /redis:\/\//);
});

test("validate: a namespace is required", () => {
  process.env.CFG_SECRET = "s";
  const body = `
[http]
jwt_secret_env = "CFG_SECRET"
[[kvrocks]]
name = "default"
uri  = "redis://localhost:6666"
`;
  assert.throws(() => loadConfig(writeTmp(body)), /namespace/);
});

test("JSON config files load with the same shape", () => {
  const p = writeTmp(JSON.stringify({
    http: { addr: ":1", jwt_secret_env: "CFG_SECRET" },
    kvrocks: [{ name: "default", uri: "redis://localhost:6666", namespace: "j" }],
  }), "config.json");
  process.env.CFG_SECRET = "s";
  const cfg = loadConfig(p);
  assert.equal(defaultSource(cfg).namespace, "j");
});

test("portFromAddr parses the Go-style addr forms", () => {
  assert.equal(portFromAddr(":8080"), 8080);
  assert.equal(portFromAddr("0.0.0.0:9000"), 9000);
  assert.equal(portFromAddr("8080"), 8080);
  assert.equal(portFromAddr("nope"), undefined);
});
