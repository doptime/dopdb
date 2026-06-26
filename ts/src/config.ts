// Runtime configuration loader — the TypeScript counterpart of the Go `config`
// package. Reads a TOML (or JSON) file and resolves secrets / connection strings
// from environment variables, NEVER from the file itself. Same schema as Go:
//
//   [http]
//   addr           = ":8080"
//   jwt_secret_env = "DOPTIME_JWT_SECRET"   # env var holding the HS256 key
//   cors_origins   = ["https://app.example.com"]
//
//   [[mongo]]
//   name    = "default"                     # a "default" source is required
//   uri_env = "DOPTIME_MONGO_URI"           # env var holding the connection string
//   uri     = "mongodb://localhost:27017"   # literal fallback (dev only)
//   db      = "appdb"
//
// Dependency-free: a tiny reader covering exactly this schema, so it loads with no
// external packages. Use a `.json` extension to provide the same shape as JSON.

import { readFileSync } from "node:fs";

export interface HttpConfig {
  addr: string;
  jwtSecretEnv?: string;
  /** Resolved from jwtSecretEnv at load time; never read from the file. */
  jwtSecret?: string;
  corsOrigins?: string[];
}

export interface MongoSource {
  name: string;
  uriEnv?: string;
  /** Resolved: env (uriEnv) wins over the literal. */
  uri: string;
  db: string;
}

export interface Config {
  http: HttpConfig;
  mongo: MongoSource[];
}

/** The "default" datasource (required). */
export function defaultSource(cfg: Config): MongoSource {
  const d = cfg.mongo.find((m) => m.name === "default");
  if (!d) throw new Error('config: a [[mongo]] source named "default" is required');
  return d;
}

/** Look up a datasource by name. */
export function source(cfg: Config, name: string): MongoSource | undefined {
  return cfg.mongo.find((m) => m.name === name);
}

/** Load and resolve a config file (TOML, or JSON if the path ends in `.json`). */
export function loadConfig(path: string): Config {
  const text = readFileSync(path, "utf8");
  const raw = path.endsWith(".json") ? (JSON.parse(text) as RawConfig) : parseToml(text);
  const cfg = normalize(raw);
  resolveEnv(cfg);
  validate(cfg);
  return cfg;
}

// ---- shape coming out of the parser (snake_case, like the file) -------------

interface RawConfig {
  http?: {
    addr?: string;
    jwt_secret_env?: string;
    cors_origins?: string[];
  };
  mongo?: { name?: string; uri_env?: string; uri?: string; db?: string }[];
}

function normalize(raw: RawConfig): Config {
  const http = raw.http ?? {};
  return {
    http: {
      addr: http.addr ?? ":8080",
      jwtSecretEnv: http.jwt_secret_env,
      corsOrigins: http.cors_origins,
    },
    mongo: (raw.mongo ?? []).map((m) => ({
      name: m.name ?? "",
      uriEnv: m.uri_env,
      uri: m.uri ?? "",
      db: m.db ?? "",
    })),
  };
}

function resolveEnv(cfg: Config): void {
  if (cfg.http.jwtSecretEnv) {
    const v = process.env[cfg.http.jwtSecretEnv];
    if (v) cfg.http.jwtSecret = v;
  }
  for (const m of cfg.mongo) {
    if (m.uriEnv) {
      const v = process.env[m.uriEnv];
      if (v) m.uri = v; // env wins over the literal
    }
  }
}

function validate(cfg: Config): void {
  if (!cfg.mongo.some((m) => m.name === "default")) {
    throw new Error('config: at least one [[mongo]] source must be name = "default"');
  }
  for (const m of cfg.mongo) {
    if (!m.name) throw new Error("config: every [[mongo]] needs a name");
    if (!m.uri) throw new Error(`config: mongo source "${m.name}" has no uri (set uri or its uri_env)`);
    if (!m.db) throw new Error(`config: mongo source "${m.name}" has no db`);
  }
}

// ---- minimal TOML reader (exactly this schema) ------------------------------

function parseToml(text: string): RawConfig {
  const out: RawConfig = {};
  let cursor: Record<string, unknown> | null = null; // current [table]
  let mongoArr: Record<string, unknown>[] | undefined;

  for (let line of text.split("\n")) {
    line = stripComment(line).trim();
    if (!line) continue;

    if (line === "[http]") {
      out.http = out.http ?? {};
      cursor = out.http as Record<string, unknown>;
      continue;
    }
    if (line === "[[mongo]]") {
      mongoArr = (out.mongo as Record<string, unknown>[] | undefined) ?? [];
      out.mongo = mongoArr as RawConfig["mongo"];
      const entry: Record<string, unknown> = {};
      mongoArr.push(entry);
      cursor = entry;
      continue;
    }
    if (line.startsWith("[")) {
      cursor = null; // an unknown table — ignore its keys
      continue;
    }
    const eq = line.indexOf("=");
    if (eq < 0 || !cursor) continue;
    const key = line.slice(0, eq).trim();
    cursor[key] = parseValue(line.slice(eq + 1).trim());
  }
  return out;
}

function stripComment(line: string): string {
  let inStr = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (c === '"') inStr = !inStr;
    else if (c === "#" && !inStr) return line.slice(0, i);
  }
  return line;
}

function parseValue(v: string): unknown {
  if (v === "true") return true;
  if (v === "false") return false;
  if (v.startsWith("[") && v.endsWith("]")) {
    const inner = v.slice(1, -1).trim();
    if (!inner) return [];
    return inner.split(",").map((s) => unquote(s.trim())).filter((s) => s.length > 0);
  }
  if (/^-?\d+$/.test(v)) return parseInt(v, 10);
  return unquote(v);
}

function unquote(s: string): string {
  if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'"))) {
    return s.slice(1, -1);
  }
  return s;
}

/** Parse `:8080` / `0.0.0.0:8080` / `8080` into a port number. */
export function portFromAddr(addr: string): number | undefined {
  const m = /:(\d+)$/.exec(addr) ?? /^(\d+)$/.exec(addr);
  return m ? parseInt(m[1], 10) : undefined;
}
