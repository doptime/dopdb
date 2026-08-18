import { test } from "node:test";
import assert from "node:assert/strict";

import { f, collection } from "../src/schema.js";
import { ensureIndexes } from "../src/server.js";

// ensureIndexes no longer creates anything on the server: KVRocks has no
// secondary indexes. Only `unique` has runtime meaning — dopdb enforces it
// itself — so the test asserts what is REGISTERED rather than what was created.
//
// A TTL declaration on a Hash field has no equivalent at all: a Hash collection
// is ONE Redis key, so a per-document expiry cannot be expressed. That is
// asserted here so the gap cannot be reintroduced silently.

test("ensureIndexes registers unique fields and ignores the rest", async () => {
  const registered: { coll: string; fields: string[] }[] = [];
  const fakeBackend = {
    registerUnique(coll: string, fields: string[]) {
      registered.push({ coll, fields });
    },
  };

  const schema = {
    Session: collection({
      _id: f.string(),
      email: f.string().unique(),
      createdAt: f.date().ttl(3600),
      label: f.string(),
    }).named("sessions"),
  };

  await ensureIndexes(schema, fakeBackend as never);

  assert.equal(registered.length, 1);
  assert.equal(registered[0].coll, "sessions");
  assert.deepEqual(registered[0].fields, ["email"], "only the unique field is enforceable");
});

test("a schema with no unique fields registers an empty set", async () => {
  const registered: string[][] = [];
  const fakeBackend = { registerUnique: (_c: string, fields: string[]) => void registered.push(fields) };
  const schema = { Plain: collection({ _id: f.string(), x: f.number() }).named("plain") };
  await ensureIndexes(schema, fakeBackend as never);
  assert.deepEqual(registered, [[]]);
});
