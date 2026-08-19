#!/usr/bin/env node
// dopdb TypeScript stress server — the TS engine's half of the load harness.
// It mirrors cmd/stress/serve.go exactly: the same owner-scoped Hash collection
// (notes) plus String/List/Set/ZSet collections, the same seed (users x docs),
// the same JWT secret, all collections .httpOn(All) (debug: every command on),
// so the engine-neutral load client (cmd/stress/load.go) can drive it the same
// way it drives the Go server. A real comparison requires both engines to serve
// the identical workload — this file is the TS side of that contract.
//
//   ts/node_modules/.bin/tsx stress/server.ts   (from ts/; node --import tsx breaks on Node < 20)
//
// Env:
//   PORT             HTTP listen port (default 8092; the Go side uses 8091)
//   KVROCKS_URI      redis://host:port (default redis://127.0.0.1:6666)
//   KVROCKS_NAMESPACE dopdb key prefix (default "stress")
//   JWT_SECRET       HS256 secret (must match the load client)
//   USERS            users to seed (default 50)
//   DOCS             docs per user (default 50)
//
// Prints "DOPDB_TS_READY <port>" once listening AND seeded, then blocks until
// killed.

import { serve } from "../src/server.js";
import { collection, f, All } from "../src/schema.js";

// Mirror of cmd/stress/serve.go's note struct + collections. notes is
// owner-scoped with owner bound to the uid claim; the four native-Redis-type
// collections are non-scoped buckets the S*/L*/Z*/STR* commands drive by name.
// notes fields are all optional (except the key) so the load client's partial
// hset/hsetnx bodies validate — exactly as the Go server (no validate tags) does.
const schema = {
  Notes: collection({
    _id: f.string(),
    owner: f.string().bind("@uid"),
    // Optional so the load client's partial hset/hsetnx bodies ({"title":"nx"},
    // {"title":...,"tag":...,"seq":...}) validate — exactly as the Go server's
    // note struct does (no validate tags, no missing-field rejection).
    title: f.string().optional(),
    tag: f.string().optional(),
    seq: f.number().optional(),
    at: f.number().optional(),
  })
    .named("notes")
    .ownerScope("owner")
    .httpOn(All),
  Kv: collection({
    _id: f.string(),
    v: f.string().optional(),
  }).named("kv").httpOn(All),
  Queue: collection({
    _id: f.string(),
    items: f.string().optional(),
  }).named("queue").httpOn(All),
  Tags: collection({
    _id: f.string(),
    members: f.string().optional(),
  }).named("tags").httpOn(All),
  Board: collection({
    _id: f.string(),
    members: f.string().optional(),
  }).named("board").httpOn(All),
};

function envInt(key: string, def: number): number {
  const v = process.env[key];
  if (v === undefined || v === "") return def;
  const n = Number(v);
  if (!Number.isInteger(n) || n < 1) {
    process.stderr.write(`stress/server.ts: ${key} must be a positive integer\n`);
    process.exit(2);
  }
  return n;
}

async function main(): Promise<void> {
  const port = envInt("PORT", 8092);
  const uri = process.env.KVROCKS_URI ?? "redis://127.0.0.1:6666";
  const namespace = process.env.KVROCKS_NAMESPACE ?? "stress";
  const jwtSecret = process.env.JWT_SECRET ?? "stress-secret-do-not-use-in-prod";
  const users = envInt("USERS", 50);
  const docs = envInt("DOCS", 50);

  const srv = await serve({
    schema,
    kvrocks: { uri, namespace },
    jwtSecret,
    port,
  });
  console.log(`dopdb ts stress server on :${port} (kvrocks=${uri} ns=${namespace} users=${users} docs/user=${docs})`);

  // Seed the owner-scoped collection so the FIND/COUNT/fanout scenarios read a
  // real workload. serverDb().hset is the raw trusted path (empty scope => a
  // plain put, no owner check) — the TS analogue of the Go native HSet seed,
  // so both engines populate identical bytes before the load starts.
  const now = Date.now();
  let n = 0;
  for (let u = 0; u < users; u++) {
    const owner = `user${String(u).padStart(4, "0")}`;
    for (let d = 0; d < docs; d++) {
      const id = `${owner}-${String(d).padStart(4, "0")}`;
      await srv.db.Notes.hset(id, {
        _id: id,
        owner,
        title: `note ${owner} ${String(d).padStart(4, "0")}`,
        tag: `tag${d % 10}`,
        seq: d,
        at: now,
      });
      n++;
    }
  }
  console.log(`seeded ${n} notes (${users} users x ${docs} docs)`);

  process.stdout.write(`DOPDB_TS_READY ${port}\n`);

  const shutdown = () => {
    void srv.close().then(() => process.exit(0));
  };
  process.on("SIGTERM", shutdown);
  process.on("SIGINT", shutdown);
}

void main();
