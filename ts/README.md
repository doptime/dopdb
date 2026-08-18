# dopdb

**One schema, two equivalent engines (Go + TypeScript), one wire protocol.** Declare a collection once and get native types, runtime validation, a typed browser client (`fetch`), and a Node + KVRocks server — **no codegen, no writing it twice.**

This package is the **TypeScript engine**: a complete, standalone implementation (not a client SDK for a Go backend). It runs the server on Node and provides a typed browser client; it speaks the same URL wire protocol, command vocabulary, `@`-binding, isolation, and permission model as the Go engine, so the two are interchangeable.

```bash
npm install @kequnyang/dopdb
# server only: also install the peer
npm install ioredis
```

> Requires Node ≥ 20 (ESM). `ioredis` is an **optional** peer dependency — only `dopdb/server` needs it; `dopdb` and `dopdb/client` are browser-safe. (`cbor-x`, the storage codec, ships as a normal dependency and is imported only from the server entry.)

## Entry points

| Import | For | Pulls in |
|---|---|---|
| `dopdb` | the shared schema (`collection`, `f`, permission constants) | no node builtins, no redis client — browser-safe |
| `dopdb/client` | the browser `fetch` client | no node builtins, no redis client |
| `dopdb/server` | the Node + KVRocks server (`serve`, `createNextHandler`, `serverDb`, `defineApi`) | `ioredis` |

## One schema, everywhere

```ts
// schema.ts — imported by client, server, and Next.js alike
import { collection, f, HGet, HGetAll, HSet, HDel } from "@kequnyang/dopdb";

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
import { clientDb } from "@kequnyang/dopdb/client";
import { schema } from "./schema";

const db = clientDb(schema, {
  baseUrl: "https://api.example.com",
  getToken: () => localStorage.token,
});

await db.notes.hset("@uuid", { text: "buy milk" }); // create — "@uuid" => the server generates the id
const mine = await db.notes.hgetall();               // only ever returns my own notes
await db.notes.hdel(id);
```

`db.notes.*` is fully typed from the schema. There is no controller/service/DAO and no hand-written endpoint — the client safely operates on the database, and the framework enforces auth, isolation, and routing.

## Server

### In Next.js (App Router) — zero config

```ts
// app/api/[...slug]/route.ts
import { createNextHandler } from "@kequnyang/dopdb/server";
import { schema } from "@/schema";

export const { GET, POST, OPTIONS } = createNextHandler({
  schema,
  kvrocks: { uri: process.env.KVROCKS_URI!, namespace: "appdb" },
  jwtSecret: process.env.JWT_SECRET!,         // HS256 secret or RS256 PEM public key
});
export const runtime = "nodejs";              // the redis client is not Edge-compatible
```

This takes over `/api/hget/notes`, `/api/find/...`, `/api/<fn>`, watch (SSE), etc. The prefix follows the folder you place it in (rename to `app/db/[...slug]` for `/db/*`, no code change).

### Standalone Node

```ts
import { serve, serverDb } from "@kequnyang/dopdb/server";
const srv = await serve({ schema, kvrocks: { uri, namespace: "appdb" }, jwtSecret, port: 8080 });

// trusted, in-process reads/writes (no scope/JWT):
const db = srv.db;   // the trusted, typed server-side db
await db.notes.hset("u1", { text: "hi" });
```

## What you get

- **Zero glue code**: no endpoints, no fetch wrappers — the frontend calls database methods.
- **One set of types front-and-back**: change a field and both sides move together (a compile error, not a runtime surprise).
- **Multi-tenancy by default**: `@`-binding + `.ownerScope()` mean each user only ever sees their own rows, and the client can't widen it.
- **Permissions in one line**: `.httpOn(flags)` exposes + authorizes a collection (off by default); `Perm` constants are exported (as `BigInt`) and bit-compatible with the Go engine.
- **Redis data structures, natively**: Hash is one Redis hash per collection; String / List / Set / ZSet are the real Redis types, so their commands are single atomic commands. Values are stored as CBOR. Every command is verified to behave identically across the Go and TypeScript engines.

| Type | Commands |
|---|---|
| Hash | HGet/HSet/HSetNX/HDel/HExists/HGetAll/HKeys/HVals/HLen/HIncrBy/HIncrByFloat/HMSet/HMGet/HScan/HScanNoValues/HRandField |
| String | STRGET/STRSET/STRSETALL/STRGETALL/STRDEL (+ TTL) |
| List | LPUSH/RPUSH/LPOP/RPOP/LRANGE/LLEN/LINDEX/LSET/LREM/LTRIM/LINSERTBEFORE/LINSERTAFTER |
| Set | SADD/SREM/SMEMBERS/SISMEMBER/SCARD |
| ZSet | ZADD/ZREM/ZSCORE/ZCARD/ZCOUNT/ZINCRBY/ZRANGE/ZREVRANGE/ZRANGEBYSCORE/ZREVRANGEBYSCORE/ZRANK/ZREVRANK/ZPOPMIN/ZPOPMAX/ZREMRANGEBYRANK/ZREMRANGEBYSCORE |

Blocking ops (`BLPOP`/`BRPOP`/`BRPOPLPUSH`) are intentionally not implemented; use `watch` for subscriptions.

### What KVRocks does not give you

- **`find` scans.** No server-side query language: `find`/`count`/`findone` walk the collection and filter in-process. The filter dialect is unchanged; the cost is O(collection), not O(result). The same query is also reachable as SQL — `db.Users.sql("SELECT * FROM users WHERE age >= 18 ORDER BY name LIMIT 20")` — read-only and Hash-only.
- **Only `unique` indexes.** Enforced by dopdb itself (violation → 409). `.asc()`/`.desc()`/`.text()` declarations are inert, and `.ttl(...)` on a Hash field cannot be honoured at all — a Hash collection is one Redis key. Per-key TTL is native for the String family.
- **`watch` cannot replay.** Events come from dopdb's own pub/sub channel — no replica set or server configuration needed — but there is no resume token, a reconnect starts fresh, and only writes made through dopdb are seen.

## Security model (brief)

Keys are always strings (JS loses precision on large integers). The framework strips any `@`-prefixed key the client sends and injects the verified context, so identity/ownership can't be forged. `find` filters and sort/projection reject `$`-operator injection. JWT is HS256 or RS256 (`none` rejected). A collection that hasn't called `.httpOn()` returns 403 for its data commands.

## License

MIT. See [`LICENSE`](./LICENSE). Source and issues: https://github.com/doptime/dopdb
