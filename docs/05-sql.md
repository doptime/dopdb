# 05 · SQL (SELECT over Hash collections)

## Why it exists

Once the query moved in-process (`01-data`), the filter document stopped being a database's native language and became dopdb's own. At that point *"write the filter as JSON"* is a choice, not a constraint — and for a human writing an ad-hoc query, or a tool generating one, SQL is the better surface.

So SQL is a **front end**, not an engine. It parses to exactly the same `(filter, sort, skip, limit, projection)` shape `FIND` already produces and hands it to the same evaluator. There is no second execution path, no second set of semantics, and nothing here that `FIND` cannot already do. `sql.go` and `ts/src/sql.ts` are twins, and the cross-engine conformance suite drives both.

```
SQL text ──parse──▶ (filter, sort, limit, offset, projection) ──▶ query engine ──▶ rows
                          ▲
                    the same shape FIND builds from ?q= / ?s= / ?p= / ?limit=
```

## Why Hash collections only

**A Hash collection is a table.** The Redis hash field is the primary key; the CBOR value is a row with named columns. `SELECT ... WHERE ... ORDER BY ... LIMIT` maps onto that exactly.

The other four collection types are not tabular:

| Type | Why SQL does not fit |
|---|---|
| **String** | one value per key — there are no columns to select |
| **List** | ordered elements with no names; `LRANGE` already expresses the only sensible query |
| **Set** | unordered members with no schema |
| **ZSet** | a fixed `(member, score)` pair; `ZRANGEBYSCORE` is strictly more expressive and O(log n), where SQL would be a scan |

Putting SQL on them would mean inventing a fake schema to query it with, and the result would be *worse* than the native command. So `SQL` on a non-Hash collection returns **404**, the same shape as `STRGET` against a Hash collection.

## Why read-only

Every write invariant lives in the write commands: owner-scope forcing, `@`-binding, the write plan (modifiers, timestamps, validation), unique-index claims, change publication. A SQL `INSERT`/`UPDATE`/`DELETE` would either bypass all of that or duplicate it — two code paths that must agree forever. `SELECT` is the whole language; writes stay on `HSET`/`HDEL`/`HINCRBY`.

## Grammar

```sql
SELECT * | COUNT(*) | col [, col ...]
FROM <collection>
[WHERE <expr>]
[ORDER BY col [ASC|DESC] [, ...]]
[LIMIT <n>] [OFFSET <n>]
```

`<expr>`:

| Form | Compiles to |
|---|---|
| `col = v` · `!=` · `<>` · `<` · `<=` · `>` · `>=` | `$eq` `$ne` `$lt` `$lte` `$gt` `$gte` |
| `col [NOT] IN (v, ...)` | `$in` / `$nin` |
| `col [NOT] BETWEEN a AND b` | `{$gte, $lte}` |
| `col [NOT] LIKE 'p'` · `ILIKE` | `$regex` (`%`→any run, `_`→one char); `ILIKE` adds `$options:"i"` |
| `col IS [NOT] NULL` | `null` / `{$ne: null}` |
| `AND` · `OR` · `NOT` · `( ... )` | `$and` · `$or` · `$nor` |

- **Columns** may be dotted paths into a nested document (`addr.city`), and `_id` refers to the document key.
- **Identifiers** may be quoted with `"` or backticks; they must still be `[A-Za-z_][A-Za-z0-9_]*` (so `$` can never appear in one).
- **String literals** use single quotes, with `''` for a literal quote. Double quotes are *identifiers*, not strings — as in standard SQL.
- `AND` binds tighter than `OR`; parentheses override.

Two predicates on the same column both apply: `WHERE a > 1 AND a < 5` compiles to `$and`, not a merged key, because merging would silently drop one and widen the result.

## Not supported — and why

Each of these is **rejected with a message**, never silently ignored. A quietly dropped `JOIN` or `GROUP BY` would return a plausible-looking wrong answer, which is worse than an error.

| Rejected | Reason |
|---|---|
| `JOIN`, `UNION`, subqueries, `FROM a, b` | each collection is authorized separately by its own `HttpOn` bitmask — a join would read a collection the caller was never granted |
| `GROUP BY`, `HAVING`, `SUM`, `AVG`, `MIN`, `MAX` | these do not fit the `(filter, sort, page)` shape, so they would need a second execution path. `COUNT(*)` does fit and *is* supported |
| `AS` aliases | the Go engine decodes rows into the declared type `V`; a renamed column has nowhere to land. Supporting it in TypeScript only would split the two engines |
| `INSERT` / `UPDATE` / `DELETE` / DDL | see "why read-only" |
| `WHERE col = NULL` | never true in SQL; the error names `IS NULL`, which is what was meant |
| more than one statement | `;` is tolerated once, at the end |

## Security

Three checks, all on the HTTP path:

1. **`FROM` must name the collection in the URL.** This is the load-bearing one: without it, `POST /api/sql/public_notes` with `SELECT * FROM secrets` would read a collection the caller has no grant for. A mismatch is a `400`.
2. **The owner scope is AND-ed in** and cannot be widened by the statement. `WHERE owner = 'bob'` sent by alice intersects with `{owner: alice}` and matches nothing; so does `WHERE owner = 'bob' OR owner = 'alice'`.
3. **The compiled filter still passes `SanitizeFilter`.** SQL is not a way around the operator allowlist — it can only produce operators that were already on it.

Plus the usual: `SQL` is its own `Perm` bit, so a collection exposes it explicitly (`HttpOn(dopdb.ReadOnly)` includes it; `HttpOn(dopdb.HGet|dopdb.HSet)` does not), and `LIMIT` is defaulted and clamped exactly as `?limit=` is on `FIND`.

## Cost

Identical to `FIND`, because it *is* `FIND`: **O(collection)**. `LIMIT 10` does not make the query cheap — every row is still read and filtered; the limit only bounds what comes back. `SELECT` on a large collection is a scan, and no `WHERE` clause changes that. See `01-data`.

## HTTP

```
POST /api/sql/<coll>          body: the statement, or {"sql": "..."}
GET  /api/sql/<coll>?q=<url-encoded statement>
```

Returns an array of rows, or `{"count": n}` for `SELECT COUNT(*)`. Errors are `400 validation`.

```bash
TOKEN="Bearer <jwt>"
curl -XPOST localhost:8080/api/sql/users -H "Authorization: $TOKEN" \
     -d "SELECT name, age FROM users WHERE age >= 18 ORDER BY age DESC LIMIT 20"

curl -XPOST localhost:8080/api/sql/users -H "Authorization: $TOKEN" \
     -d '{"sql":"SELECT COUNT(*) FROM users WHERE email LIKE '"'"'%@example.com'"'"'"}'
```

## Go

```go
users := dopdb.New[string, *User](dopdb.WithCollection("users")).HttpOn(dopdb.ReadOnly)

rows, err := users.Query("SELECT * FROM users WHERE age >= 18 ORDER BY name LIMIT 20")
n, err := users.QueryCount("SELECT COUNT(*) FROM users WHERE age >= 18")
```

`Query` is the trusted path: no owner scope and no limit clamp, exactly like `Find`. It still checks the `FROM` clause against the collection, because a mismatched table is a bug either way. `ParseSQL` is exported if you want the compiled `(filter, opt)` without running it.

## TypeScript

```ts
const rows = await db.Users.sql("SELECT * FROM users WHERE age >= 18 ORDER BY name LIMIT 20");
const n = await db.Users.sqlCount("SELECT COUNT(*) FROM users WHERE age >= 18");
```

Available on both the browser client (`clientDb`) and the trusted server db (`serverDb`). `parseSql` is exported from `dopdb/server` for the compiled form.

## Tests

- `sql_test.go` / `ts/test/sql.test.ts` — the compiler, case for case in both engines, plus proof that every compiled filter agrees with the query engine and passes the sanitizer. **No server needed.**
- `httpserve/integration_test.go` — the FROM check, owner scope (including the OR-widening attempt), Hash-only rejection, and the limit clamp.
- `httpserve/conformance_test.go` — `TestConformanceSQL` drives both engines with identical statements and diffs the results, including every rejection's status *and* error code.
