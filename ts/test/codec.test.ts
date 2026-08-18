import { test } from "node:test";
import assert from "node:assert/strict";

import { encodeValue, decodeValue, decodeDoc, canonical, uniqueSlot } from "../src/codec.js";

// The storage format moved from BSON to CBOR. These tests pin the property the
// rest of the engine relies on: the encoding is deterministic, because Set
// members are deduplicated by their encoded bytes and unique-index claims are
// keyed by an encoded value.

test("round trip preserves scalars, arrays, objects and dates", () => {
  const at = new Date("2026-08-18T10:00:00.000Z");
  const v = { s: "héllo", n: 42, f: 1.5, b: true, z: null, arr: [1, "two", false], nested: { a: { b: 1 } }, at };
  const out = decodeValue(encodeValue(v)) as typeof v;
  assert.equal(out.s, "héllo");
  assert.equal(out.n, 42);
  assert.equal(out.f, 1.5);
  assert.equal(out.b, true);
  assert.equal(out.z, null);
  assert.deepEqual(out.arr, [1, "two", false]);
  assert.equal(out.nested.a.b, 1);
  assert.equal(new Date(out.at as unknown as string).getTime(), at.getTime());
});

test("binary-unsafe bytes survive (this is why the driver must use Buffer replies)", () => {
  const v = { s: "\u0000\u00ff\u{1F600}" };
  assert.deepEqual(decodeValue(encodeValue(v)), v);
});

// Canonical encoding: equal values must produce identical bytes in any key
// insertion order. SADD deduplication and unique-index claims depend on it.
test("encoding is key-order independent", () => {
  const a = encodeValue({ x: 1, y: "two", z: [1, 2] });
  const b = encodeValue({ z: [1, 2], y: "two", x: 1 });
  assert.equal(Buffer.compare(a, b), 0);
});

test("canonical sorts nested object keys and leaves arrays alone", () => {
  const c = canonical({ b: 1, a: { d: 2, c: 3 }, arr: [{ y: 1, x: 2 }] }) as Record<string, unknown>;
  assert.deepEqual(Object.keys(c), ["a", "arr", "b"]);
  assert.deepEqual(Object.keys(c.a as object), ["c", "d"]);
  assert.deepEqual(Object.keys((c.arr as object[])[0]), ["x", "y"]);
});

test("decodeDoc always yields a plain object", () => {
  assert.deepEqual(decodeDoc(encodeValue({ a: 1 })), { a: 1 });
  assert.deepEqual(decodeDoc(encodeValue([1, 2])), {}, "a non-object decodes to an empty doc");
});

test("uniqueSlot is stable for equal values and absent for nullish ones", () => {
  assert.equal(uniqueSlot("ada@x.io"), uniqueSlot("ada@x.io"));
  assert.notEqual(uniqueSlot("a@x.io"), uniqueSlot("b@x.io"));
  assert.equal(uniqueSlot(null), null, "nil claims no slot (sparse behaviour)");
  assert.equal(uniqueSlot(undefined), null);
});
