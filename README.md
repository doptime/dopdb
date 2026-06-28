# dopdb

**Write one schema; get the types, validation, typed client, and HTTP server for both Go and TypeScript — no codegen, no writing it twice.**

dopdb is a merge-rewrite of `doptime` + `redisdb` + `doptime-client`, with the data backend swapped to **MongoDB**. Its single goal: **eliminate the glue code you write five times just to store and fetch a piece of data.**

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

- **Zero glue code**: no hand-written API endpoints, no fetch wrappers. The frontend "calls the database" (`db.users.hGet(...)`); the framework handles auth, isolation, and routing.
- **No codegen**: not a code generator — there's no "generated, then hand-edited, then regenerated and conflicts" cycle. One schema drives both engines at runtime.
- **One set of types across front and back**: change a field and both engines' types move together — a compile error, not a production surprise.
- **Multi-tenancy by default**: `@`-binding + owner-scope bake "this is my own data" into the framework, instead of relying on you to remember `WHERE owner = me` on every query.
- **Mongo used as Mongo**: bound directly to the official driver — atomic `$inc`, change streams, unique indexes, geo indexes all work, with no abstraction layer in the way.

## A Redis-compatible data layer

dopdb covers the **redisdb-compatible data structures**, each mapped onto MongoDB:

| Type | Commands | Backing doc |
|---|---|---|
| **Hash** (the core type) | HGet/HSet/HSetNX/HDel/HExists/HGetAll/HKeys/HVals/HLen/HIncrBy/HIncrByFloat/HMSet/HMGet/HScan/HScanNoValues/HRandField | a Mongo collection of documents |
| **String** | STRGET/STRSET/STRSETALL/STRGETALL/STRDEL (+ TTL) | `{_id, v, expireAt?}` |
| **List** | LPUSH/RPUSH/LPOP/RPOP/LRANGE/LLEN/LINDEX/LSET/LREM/LTRIM/LINSERTBEFORE/LINSERTAFTER | `{_id, items[]}` |
| **Set** | SADD/SREM/SMEMBERS/SISMEMBER/SCARD | `{_id, members[]}` |
| **ZSet** | ZADD/ZREM/ZSCORE/ZCARD/ZCOUNT/ZINCRBY/ZRANGE/ZREVRANGE/ZRANGEBYSCORE/ZREVRANGEBYSCORE/ZRANK/ZREVRANK/ZPOPMIN/ZPOPMAX/ZREMRANGEBYRANK/ZREMRANGEBYSCORE | `{_id, members:[{m,score}]}` |

Every command is verified to behave identically across the Go and TypeScript engines (a cross-implementation conformance harness runs both and diffs them). Blocking ops (`BLPop`/`BRPop`/`BRPopLPush`) are intentionally not implemented — MongoDB has no native blocking, and the subscription need is covered by `watch` (change streams).

## Mental model (one sentence)

> **One schema, two equivalent engines (Go and TypeScript), one wire protocol. The frontend isn't "calling a backend API" — it's safely operating on the database directly.**

```
            one schema
          /            \
        Go              TypeScript
   (server, direct      (same server, or a
    to Mongo)            typed browser client)
          \            /
       the same URL wire protocol
       (mix freely: Go server + TS client, or vice versa)
```

TypeScript is not a "client SDK" — it's an **equivalent re-implementation** of Go: same URL scheme, same command vocabulary, same `@`-binding, isolation, and permission model. The two engines behave identically command-for-command (a conformance test guards this).

## A taste

Declare once on the backend (Go):

```go
notes := dopdb.New[string, *Note](dopdb.WithCollection("notes")).
    HttpOn() // debug: everything on; tighten with an agent once it works
```

Use it directly on the frontend (TypeScript, no endpoint code):

```ts
await db.notes.hSet("@uuid", { text: "buy milk" }); // create (server generates the id)
const mine = await db.notes.hGetAll();              // only ever returns my own notes
```

The "API layer" in the middle — gone.

## Good fit / not a fit

**Good fit**: data-driven apps (SaaS, tools, dashboards, CRUD with per-user isolation) that want unified front/back types, no API layer, and MongoDB.

**Not a fit**: systems centered on complex multi-document transactions / joins; cases that can't accept the "push access to the edge" security model; non-MongoDB backends.

## Next

- **How to use it** → see [`AGENTS.md`](./AGENTS.md): a terse, full-coverage usage manual (for a human or an AI coding agent to follow).
- **Per-topic detail** → see [`docs/`](./docs/): data model, HTTP wire protocol, configuration, TypeScript, runbook.
- **Run the tests** → see [`docs/TESTING.md`](./docs/TESTING.md).
