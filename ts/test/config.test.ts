import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { loadConfig, defaultSource, source, portFromAddr } from "../src/config.js";

function writeTmp(name: string, text: string): string {
  const dir = mkdtempSync(join(tmpdir(), "dopdb-cfg-"));
  const p = join(dir, name);
  writeFileSync(p, text, "utf8");
  return p;
}

const TOML = `
# dopdb config
[http]
addr           = ":9090"
jwt_secret_env = "CFG_JWT"
cors_origins   = ["https://a.example.com", "https://b.example.com"]

[[mongo]]
name    = "default"
uri_env = "CFG_MONGO"
uri     = "mongodb://localhost:27017"
db      = "appdb"

[[mongo]]
name = "analytics"
uri  = "mongodb://localhost:27018"  # inline comment
db   = "stats"
`;

test("TOML: parses http + [[mongo]] tables and arrays", () => {
  delete process.env.CFG_JWT;
  delete process.env.CFG_MONGO;
  const cfg = loadConfig(writeTmp("config.toml", TOML));
  assert.equal(cfg.http.addr, ":9090");
  assert.equal(cfg.http.jwtSecretEnv, "CFG_JWT");
  assert.deepEqual(cfg.http.corsOrigins, ["https://a.example.com", "https://b.example.com"]);
  assert.equal(cfg.mongo.length, 2);
  assert.equal(defaultSource(cfg).db, "appdb");
  assert.equal(source(cfg, "analytics")?.uri, "mongodb://localhost:27018"); // comment stripped
});

test("env resolution: jwt secret + mongo uri come from env, not the file", () => {
  process.env.CFG_JWT = "s3cr3t-from-env";
  process.env.CFG_MONGO = "mongodb://prod-host:27017";
  const cfg = loadConfig(writeTmp("config.toml", TOML));
  assert.equal(cfg.http.jwtSecret, "s3cr3t-from-env");
  assert.equal(defaultSource(cfg).uri, "mongodb://prod-host:27017"); // env wins over literal
  delete process.env.CFG_JWT;
  delete process.env.CFG_MONGO;
});

test("validation: a default source is required", () => {
  const bad = `
[[mongo]]
name = "other"
uri  = "mongodb://x:27017"
db   = "d"
`;
  assert.throws(() => loadConfig(writeTmp("config.toml", bad)), /default/);
});

test("JSON config is accepted via a .json extension", () => {
  const json = JSON.stringify({
    http: { addr: "0.0.0.0:7000", cors_origins: ["*"] },
    mongo: [{ name: "default", uri: "mongodb://localhost:27017", db: "j" }],
  });
  const cfg = loadConfig(writeTmp("config.json", json));
  assert.equal(cfg.http.addr, "0.0.0.0:7000");
  assert.deepEqual(cfg.http.corsOrigins, ["*"]);
  assert.equal(defaultSource(cfg).db, "j");
});

test("portFromAddr parses :port, host:port, and bare port", () => {
  assert.equal(portFromAddr(":8080"), 8080);
  assert.equal(portFromAddr("0.0.0.0:7000"), 7000);
  assert.equal(portFromAddr("9090"), 9090);
  assert.equal(portFromAddr("unix:/tmp/sock"), undefined);
});
