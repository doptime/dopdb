import { test } from "node:test";
import assert from "node:assert/strict";

import { f, collection, prepareWrite } from "../src/schema.js";

const C = collection({
  _id: f.string(),
  name: f.string().title(),
  owner: f.string().bind("@uid"),
  slug: f.string().default("@field"),
  hits: f.number().counter(),
  createdAt: f.date().createdAt(),
  updatedAt: f.date().updatedAt(),
}).named("posts");

test("server pass: bind from ctx, @field default, title, counter, timestamps", () => {
  const ctx = { uid: "alice", field: "p1" };
  const out = prepareWrite(C, { _id: "p1", name: "hELLO wORLD" }, { ctx });
  assert.equal(out.owner, "alice"); // @-bound from ctx
  assert.equal(out.slug, "p1"); // @field → ctx.field
  assert.equal(out.name, "Hello World"); // Title Case
  assert.equal(out.hits, 1); // counter from absent → 1
  assert.ok(out.createdAt instanceof Date);
  assert.ok(out.updatedAt instanceof Date);
});

test("counter increments a supplied value", () => {
  const out = prepareWrite(C, { _id: "p1", name: "x", hits: 5 }, { ctx: { uid: "a", field: "p1" } });
  assert.equal(out.hits, 6);
});

test("createdAt preserved if supplied; updatedAt always refreshed", () => {
  const made = new Date("2020-01-01T00:00:00Z");
  const out = prepareWrite(C, { _id: "p1", name: "x", createdAt: made }, { ctx: { uid: "a", field: "p1" } });
  assert.equal((out.createdAt as Date).getTime(), made.getTime());
  assert.ok((out.updatedAt as Date).getTime() >= Date.now() - 5000);
});

test("missing bind context is fail-closed", () => {
  assert.throws(() => prepareWrite(C, { _id: "p1", name: "x" }, { ctx: { field: "p1" } }));
});

test("client pass strips bound fields and skips server-managed fills", () => {
  const out = prepareWrite(C, { _id: "p1", name: "x", owner: "forged" }, {});
  assert.equal(out.owner, undefined); // bound field stripped client-side
  assert.equal(out.hits, undefined); // counter only on the server pass
  assert.equal(out.slug, undefined); // @field default only on the server pass
});
