import { test } from "node:test";
import assert from "node:assert/strict";

import { collection, f, buildSpec } from "../src/schema.js";

const users = collection({
  name: f.string(),
  email: f.string().unique(),
  age: f.number(),
}).named("users");

test("buildSpec: shape has version, collections with fields, indexes, ownerField", () => {
  const spec = buildSpec({ users });

  // Top-level shape
  assert.strictEqual(spec.version, 1);
  assert.ok(Array.isArray(spec.collections));
  assert.strictEqual(spec.collections.length, 1);

  const coll = spec.collections[0];
  assert.strictEqual(coll.name, "users");
  assert.strictEqual(typeof coll.db, "string");
  assert.ok(Array.isArray(coll.fields));
  assert.ok(Array.isArray(coll.indexes));

  // Fields: three fields, each with name and kind
  assert.strictEqual(coll.fields.length, 3);
  const fieldNames = coll.fields.map((f) => f.name);
  assert.ok(fieldNames.includes("name"));
  assert.ok(fieldNames.includes("email"));
  assert.ok(fieldNames.includes("age"));

  // email has unique index
  const email = coll.fields.find((f) => f.name === "email");
  assert.ok(email);
  assert.strictEqual(email!.unique, true);
  assert.strictEqual(email!.kind, "string");

  // name is a plain string field
  const name = coll.fields.find((f) => f.name === "name");
  assert.ok(name);
  assert.strictEqual(name!.kind, "string");
  assert.strictEqual(name!.unique, false);

  // age is number kind
  const age = coll.fields.find((f) => f.name === "age");
  assert.ok(age);
  assert.strictEqual(age!.kind, "number");

  // Indexes: email unique index present
  assert.ok(
    coll.indexes.some(
      (idx) => idx.field === "email" && idx.unique === true,
    ),
    "expected email unique index",
  );

  // ownerField is undefined when no ownerScope declared
  assert.strictEqual(coll.ownerField, undefined);
});

test("buildSpec: ownerScope populates ownerField", () => {
  const orders = collection({
    total: f.number(),
    owner: f.string(),
  }).named("orders").ownerScope("owner");

  const spec = buildSpec({ orders });
  const coll = spec.collections[0];

  assert.strictEqual(coll.ownerField, "owner");
});

test("buildSpec: multiple collections in schema", () => {
  const posts = collection({
    title: f.string(),
  }).named("posts");

  const spec = buildSpec({ users, posts });
  assert.strictEqual(spec.collections.length, 2);
  const names = spec.collections.map((c) => c.name);
  assert.ok(names.includes("users"));
  assert.ok(names.includes("posts"));
});
