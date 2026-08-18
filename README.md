# dopdb

**Write one schema; get the types, validation, typed client, and HTTP server for both Go and TypeScript — no codegen, no writing it twice.**

dopdb is a merge-rewrite of `doptime` + `redisdb` + `doptime-client`, with the data backend on **KVRocks** (Apache Kvrocks — a RocksDB-backed store speaking the Redis protocol). Its single goal: **eliminate the glue code you write five times just to store and fetch a piece of data.**

---

## The pain it removes

For an ordinary "users can CRUD their own data" feature you traditionally write:

1. the database collection shape;
2. the backend types;
3. the backend validation;
4. a REST/RPC layer (controller → service → DAO);
5. the same types again on the frontend, plus fetch wrappers, auth, and tenant isolation.

Five places to keep in sync by hand; change one field and five places must follow — and it's easy to forget "don't let user A read user B's rows."

## What dopdb gives you

| You write | You get for free |
|---|---|
| **one schema** (fields + validation + who owns the row) | backend types, frontend types, runtime validation, a typed client, an HTTP server |
| `collection(...).httpOn()` (one line) | that collection is safely callable from the frontend — no endpoints to write |
| `.ownerScope("owner")` (one line) | automatic multi-tenant isolation — each user sees only their own rows, and the client can't widen it |

- **Zero glue code**: no hand-written API endpoints, no fetch wrappers. The frontend "calls the database" (`db.users.hget(...)`); the framework handles auth, isolation, and routing.
- **No codegen**: not a code generator — there's no "generated, then hand-edited, then regenerated and conflicts" cycle. One schema drives both engines at runtime.
- **One set of types across front and back**: change a field and both engines' types move together — a compile error, not a production surprise.
- **Multi-tenancy by default**: `@`-binding + owner-scope bake "this is my own data" into the framework, instead of relying on you to remember `WHERE owner = me` on every query.
- **Redis data structures used as Redis data structures**: bound directly to the protocol — a Hash collection is one Redis hash, and List/Set/ZSet are the real types, so `LPUSH`/`ZINCRBY`/`ZPOPMIN`/`HSCAN` are single atomic commands rather than array surgery. Values are stored as CBOR.

## A Redis-compatible data layer

dopdb covers the **redisdb-compatible data structures**, and on KVRocks each one is backed by the native Redis type:

| Type | Commands | Backing key |
|---|---|---|
| **Hash** (the core type) | HGet/HSet/HSetNX/HDel/HExists/HGetAll/HKeys/HVals/HLen/HIncrBy/HIncrByFloat/HMSet/HMGet/HScan/HScanNoValues/HRandField | one Redis HASH: `<ns>:<coll>`, field = document id, value = the CBOR document |
| **String** | STRGET/STRSET/STRSETALL/STRGETALL/STRDEL (+ native TTL) | a Redis STRING per key |
| **List** | LPUSH/RPUSH/LPOP/RPOP/LRANGE/LLEN/LINDEX/LSET/LREM/LTRIM/LINSERTBEFORE/LINSERTAFTER | a Redis LIST per key |
| **Set** | SADD/SREM/SMEMBERS/SISMEMBER/SCARD | a Redis SET per key |
| **ZSet** | ZADD/ZREM/ZSCORE/ZCARD/ZCOUNT/ZINCRBY/ZRANGE/ZREVRANGE/ZRANGEBYSCORE/ZREVRANGEBYSCORE/ZRANK/ZREVRANK/ZPOPMIN/ZPOPMAX/ZREMRANGEBYRANK/ZREMRANGEBYSCORE | a Redis ZSET per key |

Every command is verified to behave identically across the Go and TypeScript engines (a cross-implementation conformance harness runs both and diffs them). Blocking ops (`BLPop`/`BRPop`/`BRPopLPush`) are intentionally not implemented; the subscription need is covered by `watch`.

## What KVRocks does not give you

Worth knowing before you design a collection, because these are real and not papered over:

- **`FIND` scans.** There is no server-side query language. `FIND`/`COUNT`/`FINDONE` walk the collection hash and evaluate the filter in-process. The filter dialect is unchanged (`$gt`, `$in`, `$regex`, `$elemMatch`, …) — only the cost model is: **O(collection), not O(result)**. Because the query is dopdb's own now, it is also reachable as SQL:

  ```sql
  SELECT name, age FROM users WHERE age >= 18 ORDER BY age DESC LIMIT 20
  ```

  Read-only, Hash collections only, no `JOIN` — a front end over the same engine, costing exactly what `FIND` costs. See [`docs/05-sql.md`](./docs/05-sql.md).
- **Indexes are gone except `unique`.** `unique` is enforced by dopdb itself (a side hash claiming each value; violation → HTTP 409). Ascending/descending/text/geo declarations are accepted and inert.
- **No per-document TTL.** A Hash collection is one Redis key, so a single document inside it cannot expire on its own. Per-*key* TTL is native for the String family.
- **`watch` cannot replay.** Change events are dopdb's own pub/sub publication, so no replica set and no server configuration are needed — but a reconnect starts fresh, and only writes made through dopdb are seen.

## Mental model (one sentence)

> **One schema, two equivalent engines (Go and TypeScript), one wire protocol. The frontend isn't "calling a backend API" — it's safely operating on the database directly.**

```
            one schema
          /            \
        Go              TypeScript
   (server, direct      (same server, or a
    to KVRocks)          typed browser client)
          \            /
       the same URL wire protocol
       (mix freely: Go server + TS client, or vice versa)
```

TypeScript is not a "client SDK" — it's an **equivalent re-implementation** of Go: same URL scheme, same command vocabulary, same `@`-binding, isolation, and permission model. The two engines behave identically command-for-command (a conformance test guards this).

## A taste

Declare once on the backend (Go):

```go
type Note struct {
    Text string `json:"text"`
}

notes := dopdb.New[string, *Note](dopdb.WithCollection("notes")).
    HttpOn() // debug: everything on; tighten with an agent once it works
```

Use it directly on the frontend (TypeScript, no endpoint code):

```ts
await db.notes.hset("@uuid", { text: "buy milk" }); // create (server generates the id)
const mine = await db.notes.hgetall();              // only ever returns my own notes
```

The "API layer" in the middle — gone.

## Good fit / not a fit

**Good fit**: data-driven apps (SaaS, tools, dashboards, CRUD with per-user isolation) that want unified front/back types, no API layer, and key-addressed access to a KVRocks/Redis-protocol store.

**Not a fit**: workloads whose read pattern is ad-hoc queries over large collections (every `FIND` is a scan); systems centered on multi-document transactions or joins; cases that can't accept the "push access to the edge" security model; non-Redis-protocol backends.

## Next

- **How to use it** → see [`AGENTS.md`](./AGENTS.md): a terse, full-coverage usage manual (for a human or an AI coding agent to follow).
- **Per-topic detail** → see [`docs/`](./docs/): data model, HTTP wire protocol, configuration, TypeScript, runbook.
- **Run the tests** → see [`docs/TESTING.md`](./docs/TESTING.md).

## Publishing the TypeScript package to npm

The `ts/` subdirectory publishes as the npm package **`@kequnyang/dopdb`** (the unscoped name `dopdb` is blocked by npm's typo-squatting guard — too similar to `depd`/`dpdm`/`gopd`/`lowdb`). Publishing runs through [`publish-npm.yml`](./.github/workflows/publish-npm.yml).

**One-time setup** (already done): an npm **Automation** access token (publish rights for `@kequnyang/dopdb`) is stored as the secret `NPM_TOKEN` under a GitHub **environment** named `NPM_TOKEN`. The workflow declares `environment: NPM_TOKEN`, so the secret resolves at publish time.

**Each release**:
1. Bump the version in [`ts/package.json`](./ts/package.json) — npm rejects re-publishing an existing version.
2. Either publish a **GitHub Release** (triggers the workflow), or run the workflow manually: **Actions → publish-npm → Run workflow** → pick `npm_tag` (`alpha` during pre-1.0 so `latest` isn't grabbed early) and optionally `dry_run` first.
3. The workflow runs `npm publish --access public` (scoped packages are private by default; `--access public` makes them free to install). Provenance is auto-attempted (public repo) and skipped (private repo).

Consumers then install with `npm install @kequnyang/dopdb@latest` (or `@alpha`).
