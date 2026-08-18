import { test } from "node:test";
import assert from "node:assert/strict";

import {
  matchFilter,
  sortDocs,
  sortKeysFromMap,
  applyProjection,
  compareValues,
  type Doc,
} from "../src/query.js";

// The query engine replaced MongoDB's query planner, so it needs tests of its
// own — these are the only tests that pin the FILTER DIALECT itself, and they
// need no server. They deliberately mirror Go's query_test.go case for case, so
// a divergence between the two engines shows up here first.

const doc = (o: Doc): Doc => o;

test("equality, arrays and absent fields", () => {
  const d = doc({ name: "Ada", age: 30, tags: ["a", "b"] });
  const cases: [string, Record<string, unknown>, boolean][] = [
    ["implicit eq", { name: "Ada" }, true],
    ["implicit eq miss", { name: "Bob" }, false],
    ["$eq", { name: { $eq: "Ada" } }, true],
    ["$ne", { name: { $ne: "Bob" } }, true],
    ["array contains element", { tags: "b" }, true],
    ["array whole match", { tags: ["a", "b"] }, true],
    ["array miss", { tags: "z" }, false],
    ["absent field eq null", { nope: null }, true],
    ["absent field eq value", { nope: "x" }, false],
    ["two clauses AND implicitly", { name: "Ada", age: 30 }, true],
    ["one clause fails", { name: "Ada", age: 31 }, false],
  ];
  for (const [label, filter, want] of cases) {
    assert.equal(matchFilter(d, filter), want, label);
  }
});

test("comparison operators", () => {
  const d = doc({ age: 30, score: 4.5 });
  const cases: [Record<string, unknown>, boolean][] = [
    [{ age: { $gt: 29 } }, true],
    [{ age: { $gt: 30 } }, false],
    [{ age: { $gte: 30 } }, true],
    [{ age: { $lt: 31 } }, true],
    [{ age: { $lte: 29 } }, false],
    [{ age: { $gt: 20, $lt: 40 } }, true],
    [{ age: { $gt: 20, $lt: 25 } }, false],
    [{ score: { $gte: 4.5 } }, true],
    [{ missing: { $gt: 1 } }, false],
  ];
  for (const [filter, want] of cases) {
    assert.equal(matchFilter(d, filter), want, JSON.stringify(filter));
  }
});

test("logical operators", () => {
  const d = doc({ a: 1, b: "x" });
  const cases: [string, Record<string, unknown>, boolean][] = [
    ["$and both", { $and: [{ a: 1 }, { b: "x" }] }, true],
    ["$and one fails", { $and: [{ a: 1 }, { b: "y" }] }, false],
    ["$or one hits", { $or: [{ a: 9 }, { b: "x" }] }, true],
    ["$or none", { $or: [{ a: 9 }, { b: "y" }] }, false],
    ["$nor none hit", { $nor: [{ a: 9 }] }, true],
    ["$nor one hit", { $nor: [{ a: 1 }] }, false],
    ["$not", { a: { $not: { $gt: 5 } } }, true],
    ["$not negates", { a: { $not: { $lt: 5 } } }, false],
  ];
  for (const [label, filter, want] of cases) {
    assert.equal(matchFilter(d, filter), want, label);
  }
});

test("element and array operators", () => {
  const d = doc({
    tags: ["red", "blue"],
    items: [{ n: "a", q: 2 }, { n: "b", q: 5 }],
    n: 10,
    name: "Hello World",
  });
  const cases: [string, Record<string, unknown>, boolean][] = [
    ["$exists true", { tags: { $exists: true } }, true],
    ["$exists false", { nope: { $exists: false } }, true],
    ["$exists false on present", { tags: { $exists: false } }, false],
    ["$size", { tags: { $size: 2 } }, true],
    ["$size wrong", { tags: { $size: 3 } }, false],
    ["$all", { tags: { $all: ["blue", "red"] } }, true],
    ["$all missing one", { tags: { $all: ["blue", "green"] } }, false],
    ["$in", { n: { $in: [1, 10] } }, true],
    ["$nin", { n: { $nin: [1, 2] } }, true],
    ["$elemMatch", { items: { $elemMatch: { q: { $gt: 4 } } } }, true],
    ["$elemMatch miss", { items: { $elemMatch: { q: { $gt: 9 } } } }, false],
    ["$mod", { n: { $mod: [3, 1] } }, true],
    ["$mod miss", { n: { $mod: [3, 2] } }, false],
    ["$type string", { name: { $type: "string" } }, true],
    ["$type number", { name: { $type: "number" } }, false],
    ["$regex", { name: { $regex: "^Hello" } }, true],
    ["$regex case-sensitive", { name: { $regex: "^hello" } }, false],
    ["$regex with i option", { name: { $regex: "^hello", $options: "i" } }, true],
    ["dot path into array", { "items.n": "b" }, true],
    ["dot path miss", { "items.n": "z" }, false],
  ];
  for (const [label, filter, want] of cases) {
    assert.equal(matchFilter(d, filter), want, label);
  }
});

// An operator outside the allowlist must never widen a result set: a filter the
// engine does not understand matches nothing rather than everything.
test("unknown operators match nothing", () => {
  const d = doc({ a: 1 });
  assert.equal(matchFilter(d, { a: { $where: "true" } }), false);
  assert.equal(matchFilter(d, { $expr: { $eq: [1, 1] } }), false);
});

test("sort is multi-key and direction-aware", () => {
  const rows: Doc[] = [
    { _id: "c", age: 30, name: "carol" },
    { _id: "a", age: 40, name: "alice" },
    { _id: "b", age: 30, name: "bob" },
  ];
  sortDocs(rows, [{ field: "age", asc: true }, { field: "name", asc: true }]);
  assert.deepEqual(rows.map((r) => r._id), ["b", "c", "a"]);

  sortDocs(rows, [{ field: "age", asc: false }]);
  assert.equal(rows[0]._id, "a");
});

test("sort across mixed types is total and stable (null < number < string)", () => {
  const rows: Doc[] = [{ _id: "1", v: "text" }, { _id: "2", v: 5 }, { _id: "3", v: null }];
  sortDocs(rows, [{ field: "v", asc: true }]);
  assert.deepEqual(rows.map((r) => r._id), ["3", "2", "1"]);
});

test("sortKeysFromMap orders field names so an unordered map is deterministic", () => {
  const keys = sortKeysFromMap({ b: -1, a: 1 });
  assert.deepEqual(keys, [{ field: "a", asc: true }, { field: "b", asc: false }]);
});

test("projection: include mode, exclude mode and the _id flag", () => {
  const d = doc({ _id: "k1", a: 1, b: 2, c: 3 });

  const inc = applyProjection(d, { a: 1 });
  assert.equal(inc.a, 1);
  assert.equal("b" in inc, false);
  assert.equal(inc._id, "k1", "_id kept by default in include mode");

  const incNoId = applyProjection(d, { a: 1, _id: 0 });
  assert.equal("_id" in incNoId, false);

  const exc = applyProjection(d, { b: 0 });
  assert.equal("b" in exc, false);
  assert.equal(exc.a, 1);
  assert.equal(exc.c, 3);
});

test("compareValues reports incomparable rather than guessing", () => {
  assert.equal(compareValues(1, 2), -1);
  assert.equal(compareValues("b", "a"), 1);
  assert.equal(compareValues(null, null), 0);
  assert.equal(compareValues([1], [2]), null, "arrays are not ordered");
});

test("dates compare and match", () => {
  const early = new Date("2020-01-01T00:00:00Z");
  const late = new Date("2026-01-01T00:00:00Z");
  const d = doc({ at: early });
  assert.equal(matchFilter(d, { at: { $lt: late } }), true);
  assert.equal(matchFilter(d, { at: { $gt: late } }), false);
  assert.equal(matchFilter(d, { at: early }), true);
  assert.equal(matchFilter(d, { at: "2020-01-01T00:00:00.000Z" }), true, "ISO string coerces");
});
