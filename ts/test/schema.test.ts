import { test } from "node:test";
import assert from "node:assert/strict";

import {
  f,
  collection,
  validate,
  prepareWrite,
  specOf,
  buildSpec,
  nanoid,
  HGet,
  HSet,
  ReadOnly,
  Writes,
  All,
} from "../src/schema.js";
import { ValidationError } from "../src/errors.js";

const User = collection({
  _id: f.string(),
  name: f.string().trim().lowercase().required().min(2),
  age: f.number().min(0).max(150).optional(),
  role: f.string().enum("user", "admin").default("user"),
  id: f.string().default("@nanoid"),
}).named("users");

const Order = collection({
  _id: f.string(),
  total: f.number().min(0),
  owner: f.string().bind("@uid"),
}).named("orders").ownerScope("owner");

test("validate: required + length + enum + numeric bounds", () => {
  assert.throws(() => validate(User, { name: "" }), ValidationError);
  assert.throws(() => validate(User, { name: "a" }), ValidationError); // < min 2
  assert.throws(() => validate(User, { name: "ok", age: 200 }), ValidationError);
  assert.throws(() => validate(User, { name: "ok", role: "root" }), ValidationError);
  assert.doesNotThrow(() => validate(User, { name: "ok", age: 30, role: "admin" }));
});

test("validate: partial skips absent required fields", () => {
  assert.doesNotThrow(() => validate(User, { age: 22 }, { partial: true }));
  assert.throws(() => validate(User, { age: 22 }), ValidationError); // name required in full mode
});

test("prepareWrite client: strips bound fields, applies string mods, partial-validates", () => {
  const out = prepareWrite(Order, { total: 5, owner: "spoofed" }); // client (no claims)
  assert.equal("owner" in out, false, "client may not set a bound field");
  assert.equal(out.total, 5);

  const u = prepareWrite(User, { name: "  ADA  " }); // client
  assert.equal(u.name, "ada", "trim + lowercase applied");
  assert.equal("role" in u, false, "server-only default not filled on client");
});

test("prepareWrite http: fills @-bound owner from JWT claims, fills defaults", () => {
  const out = prepareWrite(Order, { total: 5, owner: "spoofed" }, { claims: { uid: "u-123" } });
  assert.equal(out.owner, "u-123", "owner comes from the verified claim, not the client");

  const u = prepareWrite(User, { name: "ada" }, { claims: { uid: "u-1" } });
  assert.equal(u.role, "user", "literal default filled server-side");
  assert.equal(typeof u.id, "string");
  assert.equal((u.id as string).length, 21, "@nanoid token resolved to a 21-char id");
});

test("prepareWrite http: missing required claim for a bound field is rejected", () => {
  assert.throws(() => prepareWrite(Order, { total: 1 }, { claims: {} }), ValidationError);
});

test("prepareWrite trusted: keeps caller-supplied bound field, validates fully", () => {
  const out = prepareWrite(Order, { total: 9, owner: "u-self" }, { trusted: true });
  assert.equal(out.owner, "u-self");
  assert.throws(() => prepareWrite(Order, { owner: "u-self" }, { trusted: true }), ValidationError); // total required
});

test("specOf: emits schema-as-data with indexes and owner field", () => {
  const spec = specOf("Order", Order);
  assert.equal(spec.name, "orders");
  assert.equal(spec.ownerField, "owner");
  const ownerField = spec.fields.find((x) => x.name === "owner");
  assert.equal(ownerField?.bind, "uid");

  const us = specOf("User", User);
  assert.equal(us.name, "users");
  // role has an enum, name has min — carried into the data spec
  assert.equal(us.fields.find((x) => x.name === "name")?.min, 2);
});

test("nanoid: correct length and alphabet", () => {
  const id = nanoid(16);
  assert.equal(id.length, 16);
  assert.match(id, /^[A-Za-z0-9_-]+$/);
});

test("buildSpec: emits the whole schema as one data document", () => {
  const spec = buildSpec({ User, Order });
  assert.equal(spec.version, 1);
  assert.equal(spec.collections.length, 2);
  const names = spec.collections.map((c) => c.name).sort();
  assert.deepEqual(names, ["orders", "users"]);
  // round-trips through JSON (it is the artifact a Go engine would read)
  const json = JSON.parse(JSON.stringify(spec));
  assert.equal(json.collections.find((c: any) => c.name === "orders").ownerField, "owner");
});

test("httpOn declares the command bitmask; no args = All", () => {
  const c1 = collection({ _id: f.string() }).named("a").httpOn(HGet, HSet);
  assert.equal(c1.opts.httpPerm, HGet | HSet);
  const c2 = collection({ _id: f.string() }).named("b").httpOn();
  assert.equal(c2.opts.httpPerm, All);
  const c3 = collection({ _id: f.string() }).named("c").httpOn(ReadOnly);
  assert.equal(c3.opts.httpPerm, ReadOnly);
  // Writes is disjoint from ReadOnly; together they make All.
  assert.equal(ReadOnly | Writes, All);
});
