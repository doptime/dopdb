import { test } from "node:test";
import assert from "node:assert/strict";

import * as pkg from "../src/index.js";

// The package's own docs tell users to write `.httpOn(StrSet | StrGet)`. Those
// bits existed in the schema module but were never re-exported from the entry
// point, so the documented call compiled to `undefined | undefined` and the only
// working option was `All` — the opposite of least privilege. This pins the
// export list against the command table so the two cannot drift again.
test("every command bit in CMD_BIT is exported from the package entry", () => {
  const missing: string[] = [];
  for (const cmd of Object.keys(pkg.CMD_BIT)) {
    // CMD_BIT keys are upper-case command names; the exported constants are
    // PascalCase, so match case-insensitively on the identifier set.
    const found = Object.entries(pkg).some(
      ([name, v]) => typeof v === "bigint" && name.toUpperCase() === cmd,
    );
    if (!found) missing.push(cmd);
  }
  assert.deepEqual(missing, [], `command bits missing from the entry point: ${missing.join(", ")}`);
});

test("the exported bits are distinct and non-zero", () => {
  const seen = new Map<bigint, string>();
  for (const [name, v] of Object.entries(pkg)) {
    if (typeof v !== "bigint") continue;
    if (name === "ReadOnly" || name === "Writes" || name === "All" || name === "HashAll") continue;
    assert.notEqual(v, 0n, `${name} is zero`);
    const prev = seen.get(v);
    assert.equal(prev, undefined, `${name} collides with ${prev}`);
    seen.set(v, name);
  }
});

// ReadOnly must not contain a mutating command: it is what a caller reaches for
// when granting "reads only".
test("ReadOnly contains no write command", () => {
  const writes = [
    pkg.HSet, pkg.HSetNX, pkg.HDel, pkg.Del, pkg.HMSet, pkg.HIncrBy, pkg.HIncrByFloat,
    pkg.StrSet, pkg.StrSetAll, pkg.StrDel, pkg.SAdd, pkg.SRem,
    pkg.LPush, pkg.RPush, pkg.LPop, pkg.RPop, pkg.LSet, pkg.LRem, pkg.LTrim,
    pkg.ZAdd, pkg.ZRem, pkg.ZIncrBy, pkg.ZPopMin, pkg.ZPopMax,
  ];
  for (const w of writes) assert.equal(pkg.ReadOnly & w, 0n, "ReadOnly grants a write command");
  // and the read side is actually in there
  assert.notEqual(pkg.ReadOnly & pkg.HGet, 0n);
  assert.notEqual(pkg.ReadOnly & pkg.SQL, 0n);
});
