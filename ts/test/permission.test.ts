import { test } from "node:test";
import assert from "node:assert/strict";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Permissions } from "../src/permission.js";

test("default-deny: ungranted pairs are refused", () => {
  const p = new Permissions();
  assert.equal(p.allowed("HGET", "User"), false);
});

test("grant / deny (command case-insensitive)", () => {
  const p = new Permissions();
  p.grant("HGET", "User");
  assert.equal(p.allowed("HGET", "User"), true);
  assert.equal(p.allowed("hget", "User"), true);
  assert.equal(p.allowed("HSET", "User"), false);
  p.deny("HGET", "User");
  assert.equal(p.allowed("HGET", "User"), false);
});

test("save / load round-trip", async () => {
  const p = new Permissions();
  p.grant("HGET", "User").grant("FIND", "Order").deny("DEL", "User");
  const path = join(tmpdir(), `dopdb-perm-${Date.now()}.json`);
  await p.saveJSON(path);
  const q = await Permissions.loadJSON(path);
  assert.equal(q.allowed("HGET", "User"), true);
  assert.equal(q.allowed("FIND", "Order"), true);
  assert.equal(q.allowed("DEL", "User"), false);
  assert.equal(q.allowed("UNKNOWN", "X"), false);
});
