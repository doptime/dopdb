#!/usr/bin/env node
// dopdb conformance server — a minimal TS server spawned by the Go
// conformance test (httpserve/conformance_test.go) to verify Go≡TS wire
// behavior. The schema mirrors the Go test's collections exactly.
//
//   node --import tsx conformance/server.ts
//
// Env: PORT, KVROCKS_URI, KVROCKS_NAMESPACE, JWT_SECRET. Prints
// "DOPDB_TS_READY <port>" once listening, then blocks until killed.

import { serve } from "../src/server.js";
import { collection, f, All } from "../src/schema.js";

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
    .httpOn(All)
    .ownerScope("owner"),
  Items: collection({
    _id: f.string(),
    label: f.string(),
  }).named("items").httpOn(All),
  // String family (STR*): a native Redis string per key, non-scoped.
  Strs: collection({
    _id: f.string(),
    v: f.string(),
  }).named("strvals").httpOn(All),
  // Set family (S*): a native Redis set per key, non-scoped.
  Setvals: collection({
    _id: f.string(),
    members: f.string(), // array via wire; not schema-validated for S*
  }).named("setvals").httpOn(All),
  // List family (L*/R*): a native Redis list per key, non-scoped.
  Listvals: collection({
    _id: f.string(),
    items: f.string(), // array via wire
  }).named("listvals").httpOn(All),
  // ZSet family (Z*): a native Redis sorted set per key, non-scoped.
  Zsetvals: collection({
    _id: f.string(),
    members: f.string(), // array via wire
  }).named("zsetvals").httpOn(All),
};

async function main(): Promise<void> {
  const port = Number(process.env.PORT);
  const uri = process.env.KVROCKS_URI;
  const namespace = process.env.KVROCKS_NAMESPACE;
  const jwtSecret = process.env.JWT_SECRET;
  if (!port || !uri || !namespace || !jwtSecret) {
    process.stderr.write("conformance/server.ts: PORT, KVROCKS_URI, KVROCKS_NAMESPACE, JWT_SECRET all required\n");
    process.exit(2);
  }
  const srv = await serve({
    schema,
    kvrocks: { uri, namespace },
    jwtSecret,
    port,
    // No blanket permit. The harness used to pass `permit: () => true`, which
    // made the TypeScript engine allow everything and so made every 403 in the
    // Go engine an unreachable difference — the gate was the one thing the
    // "conformance" suite could never compare. Each collection declares its own
    // bitmask instead, exactly as the Go side grants them.
  });
  process.stdout.write(`DOPDB_TS_READY ${port}\n`);
  // Block until signaled.
  process.on("SIGTERM", () => { void srv.close().then(() => process.exit(0)); });
  process.on("SIGINT", () => { void srv.close().then(() => process.exit(0)); });
}

void main();
