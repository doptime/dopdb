import { test } from "node:test";
import assert from "node:assert/strict";
import { serve } from "../src/server.js";
import { collection, f } from "../src/schema.js";

// Hardening regressions from the 2026-06-26 ablation. The owner-scope / filter /
// limit / body behaviours (F1/F3/F4/F5) are verified end-to-end against a real
// Mongo in milestone M2; this file locks the one guard that needs no database.

// F2 — a row-scoped collection whose owner field is NOT bound to a claim is a
// silent-scope-failure trap: prepareWrite would never set the owner, so the
// {owner:@uid} predicate would match nothing. buildRuntime must reject it at
// startup, BEFORE dialing Mongo, so this test needs no database and is fast.
test("serve() rejects a scoped collection whose owner field is unbound (fail-closed)", async () => {
  const schema = {
    notes: collection({ owner: f.string(), text: f.string() }).ownerScope("owner"),
  };
  await assert.rejects(
    serve({ schema, mongo: { uri: "mongodb://127.0.0.1:1/x", db: "x" }, jwtSecret: "s" }),
    /not bound to a claim/,
  );
});
