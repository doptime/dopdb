import { test } from "node:test";
import assert from "node:assert/strict";

import { TopN, rowLess, type Doc, type SortKey } from "../src/query.js";

// Bounded retention must return EXACTLY what a full sort-then-slice would. It is
// an optimisation, so any difference is a correctness bug, not a trade-off.
// (An inverted heap comparison here kept the WORST n instead of the best, and
// only the end-to-end tests caught it — hence this direct one.)
test("TopN matches a full sort followed by slice", () => {
  const orders: SortKey[][] = [
    [],
    [{ field: "age", asc: true }],
    [{ field: "age", asc: false }],
    [{ field: "age", asc: true }, { field: "name", asc: false }],
    [{ field: "_id", asc: false }],
  ];
  for (const keys of orders) {
    for (const n of [1, 3, 10, 50, 500]) {
      const rows: Doc[] = [];
      for (let i = 0; i < 300; i++) {
        rows.push({
          _id: `k${String(i).padStart(3, "0")}`,
          age: Math.floor(Math.random() * 20),
          name: `n${String(Math.floor(Math.random() * 50)).padStart(3, "0")}`,
        });
      }
      const want = [...rows]
        .sort((a, b) => (rowLess(a, b, keys) ? -1 : rowLess(b, a, keys) ? 1 : 0))
        .slice(0, n)
        .map((r) => r._id);

      const heap = new TopN(n, keys);
      for (const r of rows) heap.push(r);
      const got = heap.sorted().map((r) => r._id);

      assert.deepEqual(got, want, `keys=${JSON.stringify(keys)} n=${n}`);
    }
  }
});
