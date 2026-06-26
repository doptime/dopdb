import { test } from "node:test";
import assert from "node:assert/strict";

import { f, collection } from "../src/schema.js";
import { ensureIndexes } from "../src/server.js";

test("ensureIndexes creates unique and TTL indexes from the schema", async () => {
  const created: { key: Record<string, unknown>; opts: any }[] = [];
  const fakeDb = {
    collection() {
      return {
        async createIndex(key: Record<string, unknown>, opts: any) {
          created.push({ key, opts });
          return "ok";
        },
      };
    },
  };

  const schema = {
    Session: collection({
      _id: f.string(),
      email: f.string().unique(),
      createdAt: f.date().ttl(3600),
    }).named("sessions"),
  };

  await ensureIndexes(schema, fakeDb as any);

  const ttl = created.find((c) => c.opts.expireAfterSeconds != null);
  assert.ok(ttl, "a TTL index was created");
  assert.equal(ttl!.opts.expireAfterSeconds, 3600);
  assert.deepEqual(ttl!.key, { createdAt: 1 });
  assert.equal(ttl!.opts.unique, undefined, "TTL index is not unique");

  const uniq = created.find((c) => c.opts.unique === true);
  assert.ok(uniq, "a unique index was created");
  assert.deepEqual(uniq!.key, { email: 1 });
});
