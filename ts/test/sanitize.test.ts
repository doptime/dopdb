import { test } from "node:test";
import assert from "node:assert/strict";

import { sanitizeFilter } from "../src/sanitize.js";
import { ValidationError } from "../src/errors.js";

test("allows the vetted operator surface", () => {
  const filter = {
    name: "ada",
    age: { $gte: 18, $lte: 65 },
    tags: { $in: ["a", "b"] },
    $or: [{ active: true }, { score: { $gt: 10 } }],
  };
  assert.doesNotThrow(() => sanitizeFilter(filter));
});

test("rejects code-executing and write/traversal operators", () => {
  assert.throws(() => sanitizeFilter({ $where: "1==1" }), ValidationError);
  assert.throws(() => sanitizeFilter({ $expr: { $eq: ["$a", "$b"] } }), ValidationError);
  assert.throws(() => sanitizeFilter({ x: { $function: {} } }), ValidationError);
  assert.throws(() => sanitizeFilter({ $merge: {} }), ValidationError);
  assert.throws(() => sanitizeFilter({ pipe: { $lookup: {} } }), ValidationError);
});

test("rejects unknown operators and illegal field paths", () => {
  assert.throws(() => sanitizeFilter({ x: { $weird: 1 } }), ValidationError);
  assert.throws(() => sanitizeFilter({ "a$b": 1 }), ValidationError);
});

test("enforces a maximum nesting depth", () => {
  let node: Record<string, unknown> = { v: 1 };
  for (let i = 0; i < 20; i++) node = { $and: [node] };
  assert.throws(() => sanitizeFilter(node), ValidationError);
});

test("returns a copy and does not mutate the input", () => {
  const input = { age: { $gt: 5 }, tags: { $in: [1, 2] } };
  const out = sanitizeFilter(input);
  assert.deepEqual(out, input);
  assert.notEqual(out, input);
  assert.notEqual((out as any).age, input.age);
});
