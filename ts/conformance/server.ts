#!/usr/bin/env node
// dopdb conformance server — a minimal TS server spawned by the Go
// conformance test (httpserve/conformance_test.go) to verify Go≡TS wire
// behavior. The schema mirrors the Go test's collections exactly.
//
//   node --import tsx conformance/server.ts
//
// Env: PORT, MONGO_URI, MONGO_DB, JWT_SECRET. Prints "DOPDB_TS_READY <port>"
// once listening, then blocks until killed.

import { serve } from "../src/server.js";
import { collection, f } from "../src/schema.js";

// Schema mirrors httpserve/conformance_test.go:
//   notes  — owner-scoped, owner field bound to @uid
//   items  — non-scoped, for basic wire parity
const schema = {
  Notes: collection({
    _id: f.string(),
    text: f.string().required(),
    owner: f.string().bind("@uid"),
  })
    .named("notes")
    .ownerScope("owner"),
  Items: collection({
    _id: f.string(),
    label: f.string(),
  }).named("items"),
};

async function main(): Promise<void> {
  const port = Number(process.env.PORT);
  const uri = process.env.MONGO_URI;
  const db = process.env.MONGO_DB;
  const jwtSecret = process.env.JWT_SECRET;
  if (!port || !uri || !db || !jwtSecret) {
    process.stderr.write("conformance/server.ts: PORT, MONGO_URI, MONGO_DB, JWT_SECRET all required\n");
    process.exit(2);
  }
  const srv = await serve({
    schema,
    mongo: { uri, db },
    jwtSecret,
    port,
    permit: () => true, // behavioral conformance, not the permission gate
  });
  process.stdout.write(`DOPDB_TS_READY ${port}\n`);
  // Block until signaled.
  process.on("SIGTERM", () => { void srv.close().then(() => process.exit(0)); });
  process.on("SIGINT", () => { void srv.close().then(() => process.exit(0)); });
}

void main();
