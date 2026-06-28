# dopdb

**One schema, two equivalent engines (Go + TypeScript), one wire protocol.** Declare a collection once and get native types, runtime validation, a typed browser client (`fetch`), and a Node + MongoDB server — **no codegen, no writing it twice.**

This package is the **TypeScript engine**: a complete, standalone implementation (not a client SDK for a Go backend). It runs the server on Node and provides a typed browser client; it speaks the same URL wire protocol, command vocabulary, `@`-binding, isolation, and permission model as the Go engine, so the two are interchangeable.

```bash
npm install dopdb
# server only: also install the peer
npm install mongodb
```

> Requires Node ≥ 20 (ESM). `mongodb` is an **optional** peer dependency — only `dopdb/server` needs it; `dopdb` and `dopdb/client` are browser-safe.

## Entry points

| Import | For | Pulls in |
|---|---|---|
| `dopdb` | the shared schema (`collection`, `f`, permission constants) | nothing node/mongodb — browser-safe |
| `dopdb/client` | the browser `fetch` client | nothing node/mongodb |
| `dopdb/server` | the Node + MongoDB server (`serve`, `createNextHandler`, `serverDb`, `defineApi`) | `mongodb` |

## One schema, everywhere

```ts
// schema.ts — imported by client, server, and Next.js alike
import { collection, f, HGet, HGetAll, HSet, HDel } from "dopdb";

export const schema = {
  notes: collection({
    _id:   f.string(),
    owner: f.string().bind("@uid"),          // owner comes from the JWT uid; the client can't change it
    text:  f.string().required(),
  })
    .named("notes")
    .ownerScope("owner")                      // row-level isolation
    .httpOn(HGet | HGetAll | HSet | HDel),    // expose + authorize (no args = All, debug only)
};
```

## Browser client — no fetch code, no API layer

```ts
import { clientDb } from "dopdb/client";
import { schema } from "./schema";

const db = clientDb(schema, {
  baseUrl: "https://api.example.com",
  getToken: () => localStorage.token,
});

await db.notes.hSet("@uuid", { text: "buy milk" }); // create — "@uuid" => the server generates the id
const mine = await db.notes.hGetAll();               // only ever returns my own notes
await db.notes.hDel(id);
```

`db.notes.*` is fully typed from the schema. There is no controller/service/DAO and no hand-written endpoint — the client safely operates on the database, and the framework enforces auth, isolation, and routing.

## Server

### In Next.js (App Router) — zero config

```ts
// app/api/[...slug]/route.ts
import { createNextHandler } from "dopdb/server";
import { schema } from "@/schema";

export const { GET, POST, OPTIONS } = createNextHandler({
  schema,
  mongo: { uri: process.env.MONGO_URI!, db: "appdb" },
  jwtSecret: process.env.JWT_SECRET!,         // HS256 secret or RS256 PEM public key
});
export const runtime = "nodejs";              // the MongoDB driver is not Edge-compatible
```

This takes over `/api/hget/notes`, `/api/find/...`, `/api/<fn>`, watch (SSE), etc. The prefix follows the folder you place it in (rename to `app/db/[...slug]` for `/db/*`, no code change).

### Standalone Node

```ts
import { serve, serverDb } from "dopdb/server";
const srv = await serve({ schema, mongo: { uri, db: "appdb" }, jwtSecret, port: 8080 });

// trusted, in-process reads/writes (no scope/JWT):
const db = serverDb(schema, srv.mongo);
await db.notes.hSet("u1", { text: "hi" });
```

## What you get

- **Zero glue code**: no endpoints, no fetch wrappers — the frontend calls database methods.
- **One set of types front-and-back**: change a field and both sides move together (a compile error, not a runtime surprise).
- **Multi-tenancy by default**: `@`-binding + `.ownerScope()` mean each user only ever sees their own rows, and the client can't widen it.
- **Permissions in one line**: `.httpOn(flags)` exposes + authorizes a collection (off by default); `Perm` constants are exported (as `BigInt`) and bit-compatible with the Go engine.
- **Redis-compatible data structures on MongoDB**: Hash, plus String / List / Set / ZSet — every command verified to behave identically across the Go and TypeScript engines.

| Type | Commands |
|---|---|
| Hash | HGet/HSet/HSetNX/HDel/HExists/HGetAll/HKeys/HVals/HLen/HIncrBy/HIncrByFloat/HMSet/HMGet/HScan/HScanNoValues/HRandField |
| String | STRGET/STRSET/STRSETALL/STRGETALL/STRDEL (+ TTL) |
| List | LPUSH/RPUSH/LPOP/RPOP/LRANGE/LLEN/LINDEX/LSET/LREM/LTRIM/LINSERTBEFORE/LINSERTAFTER |
| Set | SADD/SREM/SMEMBERS/SISMEMBER/SCARD |
| ZSet | ZADD/ZREM/ZSCORE/ZCARD/ZCOUNT/ZINCRBY/ZRANGE/ZREVRANGE/ZRANGEBYSCORE/ZREVRANGEBYSCORE/ZRANK/ZREVRANK/ZPOPMIN/ZPOPMAX/ZREMRANGEBYRANK/ZREMRANGEBYSCORE |

Blocking ops (`BLPOP`/`BRPOP`/`BRPOPLPUSH`) are intentionally not implemented — MongoDB has no native blocking; use `watch` (change streams) for subscriptions. `watch` needs MongoDB running as a replica set.

## Security model (brief)

Keys are always strings (JS loses precision on large integers). The framework strips any `@`-prefixed key the client sends and injects the verified context, so identity/ownership can't be forged. `find` filters and sort/projection reject `$`-operator injection. JWT is HS256 or RS256 (`none` rejected). A collection that hasn't called `.httpOn()` returns 403 for its data commands.

## License

MIT. See [`LICENSE`](./LICENSE). Source and issues: https://github.com/doptime/dopdb
