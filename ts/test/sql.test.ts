import { test } from "node:test";
import assert from "node:assert/strict";

import { parseSql, checkTable, likeToRegex, SqlError } from "../src/sql.js";
import { matchFilter, type Doc } from "../src/query.js";
import { sanitizeFilter } from "../src/sanitize.js";

// The SQL layer is a front end that compiles to the same (filter, sort, page)
// shape `find` uses, so these tests check the COMPILATION — no server needed.
// They mirror Go's sql_test.go case for case; a divergence between the two
// engines shows up here first.

test("comparisons and predicates compile to the expected filters", () => {
  const cases: [string, unknown][] = [
    ["SELECT * FROM t WHERE a = 1", { a: 1 }],
    ["SELECT * FROM t WHERE a = 'x'", { a: "x" }],
    ["SELECT * FROM t WHERE a != 1", { a: { $ne: 1 } }],
    ["SELECT * FROM t WHERE a <> 1", { a: { $ne: 1 } }],
    ["SELECT * FROM t WHERE a > 1", { a: { $gt: 1 } }],
    ["SELECT * FROM t WHERE a >= 1", { a: { $gte: 1 } }],
    ["SELECT * FROM t WHERE a < 1", { a: { $lt: 1 } }],
    ["SELECT * FROM t WHERE a <= 1", { a: { $lte: 1 } }],
    ["SELECT * FROM t WHERE a = TRUE", { a: true }],
    ["SELECT * FROM t WHERE a = -3", { a: -3 }],
    ["SELECT * FROM t WHERE a IN (1, 2)", { a: { $in: [1, 2] } }],
    ["SELECT * FROM t WHERE a NOT IN ('x')", { a: { $nin: ["x"] } }],
    ["SELECT * FROM t WHERE a BETWEEN 1 AND 5", { a: { $gte: 1, $lte: 5 } }],
    ["SELECT * FROM t WHERE a IS NULL", { a: null }],
    ["SELECT * FROM t WHERE a IS NOT NULL", { a: { $ne: null } }],
    ["SELECT * FROM t WHERE a LIKE 'ab%'", { a: { $regex: "^ab.*$" } }],
    ["SELECT * FROM t WHERE a ILIKE 'ab%'", { a: { $regex: "^ab.*$", $options: "i" } }],
    ["SELECT * FROM t WHERE addr.city = 'NY'", { "addr.city": "NY" }],
    ["SELECT * FROM t WHERE _id = 'k1'", { _id: "k1" }],
  ];
  for (const [sql, want] of cases) {
    assert.deepEqual(parseSql(sql).filter, want, sql);
  }
});

test("AND / OR / NOT / parentheses", () => {
  assert.equal((parseSql("SELECT * FROM t WHERE a = 1 AND b = 2").filter as any).$and.length, 2);
  assert.equal((parseSql("SELECT * FROM t WHERE a = 1 OR b = 2").filter as any).$or.length, 2);
  assert.equal((parseSql("SELECT * FROM t WHERE NOT a = 1").filter as any).$nor.length, 1);
  assert.ok((parseSql("SELECT * FROM t WHERE (a = 1 OR a = 2) AND b = 3").filter as any).$and);
});

// Two predicates on the SAME column must both survive — key-merging would
// silently drop one and widen the result.
test("two predicates on one column both apply", () => {
  const q = parseSql("SELECT * FROM t WHERE a > 1 AND a < 5");
  assert.equal(matchFilter({ a: 3 }, q.filter), true);
  assert.equal(matchFilter({ a: 9 }, q.filter), false);
});

test("projection, ORDER BY, LIMIT, OFFSET and COUNT(*)", () => {
  const q = parseSql("SELECT name, age FROM t ORDER BY age DESC, name ASC LIMIT 10 OFFSET 5");
  assert.equal(q.table, "t");
  assert.deepEqual(q.projection, { name: 1, age: 1 });
  assert.deepEqual(q.sortKeys, [{ field: "age", asc: false }, { field: "name", asc: true }]);
  assert.equal(q.limit, 10);
  assert.equal(q.offset, 5);

  assert.equal(parseSql("SELECT COUNT(*) FROM t WHERE a = 1").count, true);
});

// SQL is a front end, not a second dialect: the compiled filter must behave
// exactly like the equivalent hand-written one.
test("the compiled filter agrees with the query engine", () => {
  const rows: Doc[] = [
    { _id: "1", name: "ada", age: 30, tags: ["x"] },
    { _id: "2", name: "bob", age: 40 },
    { _id: "3", name: "cid", age: 50, tags: ["y"] },
  ];
  const ids = (sql: string) => rows.filter((r) => matchFilter(r, parseSql(sql).filter)).map((r) => r._id);

  assert.deepEqual(ids("SELECT * FROM t WHERE age >= 40"), ["2", "3"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE age > 30 AND age < 50"), ["2"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE name IN ('ada','cid')"), ["1", "3"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE name LIKE 'a%'"), ["1"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE name ILIKE 'A%'"), ["1"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE tags IS NULL"), ["2"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE tags IS NOT NULL"), ["1", "3"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE NOT age = 40"), ["1", "3"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE _id = '2'"), ["2"]);
  assert.deepEqual(ids("SELECT * FROM t WHERE name = 'nobody'"), []);
});

// Everything dopdb deliberately does not implement must be REJECTED with a clear
// message, never silently ignored — a quietly dropped JOIN or GROUP BY would
// return a plausible-looking wrong answer.
test("unsupported SQL is rejected, not ignored", () => {
  const cases: [string, RegExp][] = [
    ["INSERT INTO t VALUES (1)", /read-only/],
    ["UPDATE t SET a = 1", /read-only/],
    ["DELETE FROM t WHERE a = 1", /read-only/],
    ["DROP TABLE t", /DDL/],
    ["SELECT * FROM t JOIN u ON t.a = u.a", /JOIN/],
    ["SELECT * FROM t, u", /multiple tables/],
    ["SELECT * FROM t GROUP BY a", /GROUP BY/],
    ["SELECT SUM(a) FROM t", /COUNT\(\*\)/],
    ["SELECT a AS b FROM t", /aliases/],
    ["SELECT * FROM t WHERE a = NULL", /IS NULL/],
    ["SELECT * FROM t; SELECT * FROM t", /one statement/],
    ["SELECT * FROM t WHERE a = 1 extra", /unexpected/],
    ["SELECT * FROM t LIMIT -1", /non-negative/],
    ["SELECT * FROM `t$evil`", /illegal identifier/],
    ['SELECT * FROM t WHERE a = "x"', /literal value/], // double quotes are identifiers
  ];
  for (const [sql, want] of cases) {
    assert.throws(() => parseSql(sql), want, sql);
  }
});

// A rejected statement is the caller's mistake: it must surface as 400, not 500.
test("SqlError carries 400 / validation", () => {
  try {
    parseSql("NOPE");
    assert.fail("should have thrown");
  } catch (e) {
    assert.ok(e instanceof SqlError);
    assert.equal((e as SqlError).status, 400);
    assert.equal((e as SqlError).code, "validation");
  }
});

// The FROM clause is a security boundary: each collection is authorized
// separately, so a statement must not name a different one.
test("FROM must match the collection being served", () => {
  const q = parseSql("SELECT * FROM secrets WHERE a = 1");
  assert.throws(() => checkTable(q, "notes"), /does not match/);
  assert.doesNotThrow(() => checkTable(q, "secrets"));
});

// SQL is not a way around the operator allowlist.
test("compiled filters pass the sanitizer", () => {
  for (const sql of [
    "SELECT * FROM t WHERE a = 1 AND (b > 2 OR c LIKE 'x%')",
    "SELECT * FROM t WHERE a IN (1,2,3) AND d IS NOT NULL",
    "SELECT * FROM t WHERE NOT a BETWEEN 1 AND 2",
  ]) {
    assert.doesNotThrow(() => sanitizeFilter(parseSql(sql).filter));
  }
});

test("'' escapes a quote inside a string literal", () => {
  assert.equal((parseSql("SELECT * FROM t WHERE name = 'O''Brien'").filter as any).name, "O'Brien");
});

test("LIKE metacharacters are escaped, wildcards are not", () => {
  assert.equal(likeToRegex("a.c"), "^a\\.c$");
  assert.equal(likeToRegex("a%c_d"), "^a.*c.d$");
  const q = parseSql("SELECT * FROM t WHERE a LIKE 'a.c'");
  assert.equal(matchFilter({ a: "axc" }, q.filter), false);
  assert.equal(matchFilter({ a: "a.c" }, q.filter), true);
});
