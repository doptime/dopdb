# AUDIT + SQL · round 2

Two things in this round: a side-effect audit of the KVRocks migration, and the
SQL query layer.

Verification, both engines, against a live Redis-protocol server:

```
go vet ./...                                  ok
gofmt -l .                                    clean
go test ./...   (no server)                   ok — integration cases skip
go test ./...   (live server)                 ok — 4/4 packages, conformance included
tsc --noEmit                                  ok
npm test        (no server)                   81 pass, 27 skip
npm test        (live server)                 108 pass, 0 fail
```

---

# Part 1 · Side-effect audit

I went through every changed file looking for behaviour that moved without being
asked to. Three things had. Two of them were bugs I introduced; one was a
regression against the Mongo build. All three are fixed and pinned by tests —
**none of them had a test before, which is exactly why they survived.**

## Finding 1 — TypeScript lost concurrent increments, silently 🔴

**Severity: high. Silent data loss.** Reproduced before the fix:

```
20 concurrent hincrby on one key
  non-200 responses: 0; final n = 11   (want 20)   ← run 1
  non-200 responses: 0; final n = 15   (want 20)   ← run 2
```

Every request returned **200**. Nine increments simply vanished. Go was correct
(20/20) throughout, so this was a one-engine bug.

**Cause.** `hincrby` and the scoped write are `WATCH`/`MULTI` read-modify-writes.
`WATCH` is **per-connection** state, and ioredis multiplexes the entire process
over **one socket**. Two concurrent transactions therefore shared a watch set:
the first `EXEC` cleared it, and every later transaction committed unprotected.
`UNWATCH` from one call cancelled the other's protection too.

Go never had this: go-redis's `Watch` checks out a dedicated connection from the
pool, so the isolation is structural.

**Fix.** `KvBackend` now keeps a dedicated transaction connection plus an
in-process queue that admits one transaction at a time (`kvrocks.ts`, `tx()`).
Within a process that is fully correct; across processes `WATCH` still does its
job. After the fix: **20/20, repeatedly.**

Pinned by `ts/test/server.test.ts` → *"concurrent hincrby loses no increments"*.

## Finding 2 — a unique-index violation returned 500 in TS, 409 in Go 🟠

**Severity: medium. Engine divergence on a documented error class.**

```
Go : 409  {"error":"...duplicate key...","code":"conflict"}
TS : 500  {"error":"dopdb: duplicate key: ...","code":"error"}
```

`enforceUnique` threw a bare `Error`, which the HTTP error mapper (correctly)
treats as an internal failure. A client branching on `code === "conflict"` would
have worked against Go and not against TS.

**Fix.** It now throws `ConflictError` (400-family, `409`/`conflict`), matching
Go's `ErrDuplicate`. Pinned by *"a unique-index violation is 409 conflict, not
500"*.

## Finding 3 — a `json:"_id"` field stopped being filled 🟠

**Severity: medium. Regression against the Mongo build.**

The Mongo build forced `_id` into every stored document (`withID`), so a struct
declaring that field got the key back on read. On KVRocks the id lives in the
hash **field**, not inside the value, so nothing populated it:

```go
type Doc struct{ ID string `json:"_id"`; Text string `json:"text"` }
c.HSet("k1", &Doc{Text: "hello"})
c.HGet("k1")   // before: ID == ""   ← the Mongo build said "k1"
```

**Fix.** The collection precomputes the index of that field at `New()` time and
fills it on decode (`decodeAt`), across **every** read path that knows the key —
`HGet`, `HGetAll`, `HVals`, `HMGet`, `Find`, `HScan`, and the HTTP equivalents.
Zero storage cost; the id is still stored once. Pinned by
`TestIntegrationIDFieldIsFilledOnRead`, which checks all six paths.

## Also cleaned up

- **Dead code removed**: `kvBackend.expire` (String TTL is applied inline by
  `SET ... EX`) and `globToRegex` in both engines (HSCAN and SCAN match globs
  server-side now, so nothing called it).
- **`del` no longer invents change events**: deleting an absent key published a
  `delete` event to watchers. Now nothing is published when `HDEL` removes
  nothing. Both engines.

## Checked and found correct — no change needed

Recorded so the next reader does not re-derive it:

- `mergeScope` → `$and` is handled by the evaluator; a scope cannot be widened by
  a caller filter (verified for `FIND`, `HMGET`, scoped scan, and now SQL).
- `putScoped` claims unique values once, outside the retry loop — no double claim
  on contention.
- Go's `Watch` uses a pooled dedicated connection; the `minPoolSize = 64` floor
  from the previous round is what keeps it from starving.
- `HRANDFIELD` with a positive count returns distinct fields, matching `$sample`.
- Sorting `ids()`/`all()`/scan pages is new but not a behaviour change: Mongo
  never guaranteed order either, and determinism is what makes the two engines
  comparable.
- `_id`-in-response differs between engines (Go returns the typed `V`, TS returns
  the raw document). This is **pre-existing**, not from the migration — the Mongo
  build behaved the same way. Left alone rather than silently changed.

---

# Part 2 · SQL

## The design question you asked: Hash-only?

**Yes — and it is rejected outright on the other four, not half-supported.**

A Hash collection *is* a table: the Redis hash field is the primary key, the CBOR
value is a row with named columns. `SELECT/WHERE/ORDER BY/LIMIT` maps onto it
exactly. The others are not tabular:

| Type | Why not |
|---|---|
| String | one value per key — no columns to select |
| List | ordered elements with no names; `LRANGE` is already the only sensible query |
| Set | unordered members, no schema |
| ZSet | a fixed `(member, score)` pair; `ZRANGEBYSCORE` is more expressive **and** O(log n), where SQL would be a scan |

Forcing SQL onto them would mean inventing a fake schema to query it with, and
the result would be *worse* than the native command. `SQL` on a non-Hash
collection returns **404**.

## What it is

A **front end**, not an engine. It parses into exactly the
`(filter, sort, skip, limit, projection)` shape `FIND` already produces, and runs
on the same evaluator:

```
SQL text ──parse──▶ (filter, sort, limit, offset, projection) ──▶ query engine ──▶ rows
                          ▲ the same shape FIND builds from ?q= / ?s= / ?p= / ?limit=
```

One dialect, one execution path, one cost model. `sql.go` and `ts/src/sql.ts` are
twins, and `TestConformanceSQL` drives both with identical statements.

## Supported

```sql
SELECT * | COUNT(*) | col [, col ...]
FROM <collection>
[WHERE <expr>]
[ORDER BY col [ASC|DESC] [, ...]]
[LIMIT n] [OFFSET n]
```

`=` `!=` `<>` `<` `<=` `>` `>=` · `[NOT] IN` · `[NOT] BETWEEN` · `[NOT] LIKE` ·
`ILIKE` · `IS [NOT] NULL` · `AND` `OR` `NOT` · parentheses. Dotted column paths
(`addr.city`), `_id` for the key, `''` escapes a quote.

`WHERE a > 1 AND a < 5` compiles to `$and`, not a merged key — merging would
silently drop one predicate and widen the result.

## Rejected, each with a reason

Never silently ignored: a quietly dropped `JOIN` returns a plausible-looking
**wrong** answer, which is worse than an error.

| Rejected | Reason |
|---|---|
| `JOIN` / `UNION` / subquery / `FROM a, b` | each collection is authorized by its own `HttpOn` bitmask — a join reads one the caller was never granted |
| `GROUP BY` / `HAVING` / `SUM` / `AVG` | they do not fit the filter+sort+page shape, so they would need a second execution path. `COUNT(*)` fits and **is** supported |
| `AS` aliases | Go decodes rows into the typed `V`; a renamed column has nowhere to land. TS-only support would split the engines |
| `INSERT` / `UPDATE` / `DELETE` / DDL | every write invariant (owner-scope forcing, `@`-binding, write plan, unique claims, change publication) lives in the write commands. SQL would bypass or duplicate all of it |
| `WHERE col = NULL` | never true in SQL; the error names `IS NULL` |

## Security — three checks

1. **`FROM` must name the collection in the URL.** The load-bearing one: without
   it, `POST /api/sql/public_notes` carrying `SELECT * FROM secrets` would read a
   collection with no grant. Mismatch → 400.
2. **The owner scope is AND-ed in and cannot be widened.** Tested with the
   obvious attack: `WHERE owner = 'bob'` and
   `WHERE owner = 'bob' OR owner = 'alice'`, both sent by alice, both return only
   alice's rows.
3. **The compiled filter still passes `SanitizeFilter`.** SQL can only emit
   operators already on the allowlist.

Plus: `SQL` is its own `Perm` bit (**appended** at 59, never inserted — bit values
are persisted by `SaveJSON`, so renumbering would silently re-grant commands),
and `LIMIT` is defaulted/clamped exactly as `?limit=` is.

## Cost — read this before using it

Identical to `FIND`, because it **is** `FIND`: **O(collection)**. `LIMIT 10` does
not make the query cheap — every row is still read and filtered; the limit only
bounds what comes back. No `WHERE` clause changes that. SQL makes the query
easier to *write*, not faster to *run*.

## Surfaces

```bash
POST /api/sql/<coll>   # body: the statement, or {"sql":"..."}
GET  /api/sql/<coll>?q=<url-encoded statement>
```

```go
rows, err := users.Query("SELECT * FROM users WHERE age >= 18 ORDER BY name LIMIT 20")
n, err    := users.QueryCount("SELECT COUNT(*) FROM users WHERE age >= 18")
```

```ts
const rows = await db.Users.sql("SELECT * FROM users WHERE age >= 18 ORDER BY name LIMIT 20");
const n    = await db.Users.sqlCount("SELECT COUNT(*) FROM users WHERE age >= 18");
```

Go's `Query` is the trusted path (no owner scope, no clamp — like `Find`); the
`FROM` check still applies. `ParseSQL` / `parseSql` are exported if you want the
compiled form without running it.

## Files added this round

**Go**: `sql.go`, `sql_test.go`
**TS**: `ts/src/sql.ts`, `ts/test/sql.test.ts`
**Docs**: `docs/05-sql.md`
**Touched**: `perms.go`, `http_accessor.go`, `httpserve/serve.go`,
`httpserve/integration_test.go`, `httpserve/conformance_test.go`,
`ts/src/{schema,server,client}.ts`, plus README / AGENTS / docs cross-links.
