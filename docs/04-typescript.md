# 04 · TypeScript equivalent (Next.js takeover · browser client · standalone Node · functional API)

`ts/` is a **peer, complete implementation** of Go (not a client, not a WASM bridge). Same wire protocol, same command vocabulary, same `@`-binding / row-level isolation / permission model. **Using TS is entirely independent of Go**: it forwards to no Go backend; everything Go does on KVRocks, TS does itself. `src/kvrocks.ts`, `src/codec.ts` and `src/query.ts` are deliberate twins of the identically named Go files — same key layout, same CBOR value format, same filter dialect.

Entry points (`package.json` exports):
- `dopdb` — the shared schema (`collection`, `f`, `Infer`, the `Perm` constants); pulls in no node builtins and no redis client, browser-safe.
- `dopdb/client` — the browser `fetch` client (`clientDb`, `apiClient`).
- `dopdb/server` — the Node + KVRocks server (`createNextHandler`, `serve`, `serveFromConfig`, `serverDb`, `defineApi`).

## One schema across all surfaces

```ts
// dopdb-schema.ts (imported by client / server / Next.js alike)
import { collection, f, HGet, HGetAll, HSet, HDel } from "@kequnyang/dopdb";
export const schema = {
  users: collection({
    name:  f.string(),
    email: f.string().unique(),
    age:   f.int(),
  }).named("users")
    .ownerScope("owner")                    // row-level isolation (optional)
    .httpOn(HGet | HGetAll | HSet | HDel),  // expose + authorize (no args = All, debug)
  // orders: collection({...}).named("orders").inDb("analytics"), // bind a non-default source
};
```

`.named()` sets both the public and storage name; `.inDb("analytics")` binds a data source (the client then adds `?ds=analytics`); `.ownerScope("owner")` declares isolation; `.httpOn(...)` exposes + authorizes (same meaning as Go). The `Perm` constants are exported from `@kequnyang/dopdb` as **BigInt** (the bitmask exceeds 32 bits across all types); bit values match Go.

> The TS **server** handles the full command vocabulary of `02-http` (Hash + String/List/Set/ZSet), conformance-verified against Go. The typed **client** today exposes the Hash family; for String/List/Set/ZSet, drive the wire commands directly (typed client wrappers are a follow-up).

## Taking over API routing in Next.js (primary deployment, zero config)

App Router: drop one catch-all route file to take over `/api/*` under that path:

```ts
// app/api/[...slug]/route.ts
import { createNextHandler } from "@kequnyang/dopdb/server";
import { schema } from "@/dopdb-schema";  // collections declare .httpOn(...) themselves

export const { GET, POST, OPTIONS } = createNextHandler({
  schema,
  kvrocks: { uri: process.env.KVROCKS_URI!, namespace: "appdb" },   // or datasources: [...]
  jwtSecret: process.env.JWT_SECRET!,                     // HS256 secret or RS256 PEM public key
});

export const runtime = "nodejs"; // the redis client is not Edge-compatible
```

Key points:
- **Takeover is immediate**: `GET/POST/OPTIONS` handle `/api/hget/users`, `/api/find/orders`, `/api/<fn-name>`, etc.
- **Prefix is configurable**: the handler reads Next.js's catch-all segment (the part after the mount point), so the prefix is whatever folder you place it in — rename `app/api/[...slug]` to `app/db/[...slug]` for `/db/*`, **no code change**; set the client's `apiBase: "/db"` to match.
- **Lazy connect**: KVRocks connects on the first request and is reused; the CORS preflight (OPTIONS) does not touch the DB.
- **watch (SSE)**: `GET /api/watch/<coll>` returns `text/event-stream`, pushed via `ReadableStream` (needs a replica set).
- Multiple sources: replace `kvrocks` with `datasources: [{ name, kvrocks }, ...]`; select per request with `?ds=`.
- From a config file: `nextHandlerFromConfig("config.toml", { schema })`.

Pages Router: use the standalone Node `listener` (next section): `export default (req, res) => srv.listener(req, res)`.

## Standalone Node server

```ts
import { serve, serverDb } from "@kequnyang/dopdb/server";
const srv = await serve({ schema, kvrocks: { uri, namespace: "appdb" }, jwtSecret, port: 8080 });
// multiple sources:
//   await serve({ schema, jwtSecret, port: 8080,
//     datasources: [{ name:"default", kvrocks:{uri,namespace:"appdb"} }, { name:"analytics", kvrocks:{uri,namespace:"analytics"} }] });
```

For trusted internal reads/writes (no scope/JWT): `const db = serverDb(schema, backend); await db.users.hget("u1")`. `srv.listener` also plugs into a Pages Router API route.

### Permissions

Each collection's `.httpOn(...)` bitmask authorizes its commands (default off until called) — same as Go. A legacy `Permissions` map (`grant`/`deny`, JSON-persistable) still works as a runtime override (gate order: explicit `permit` callback > `Permissions` map > the per-collection bitmask). The functional API is gated under `API::<name>`.

### JWT

HS256 (HMAC secret) and RS256 (PEM/SPKI public key, `createVerify("RSA-SHA256")`); rejects `none`; checks `exp`.

## Browser client

```ts
import { clientDb } from "@kequnyang/dopdb/client";
const db = clientDb(schema, {
  baseUrl: "https://api.example.com",   // your Next.js / Node dopdb server
  getToken: () => localStorage.token,
  // apiBase: "/db",                    // if the server is mounted under a non-/api prefix
});

await db.users.hset("u1", { name: "Ada", email: "ada@x.io", age: 30 }); // typed by the schema
const u   = await db.users.hget("u1");      // User | null
const all = await db.users.find({ age: { $gte: 18 } }, { limit: 20 });
const unsub = await db.users.watch((ev) => console.log(ev.type, ev.key, ev.doc));
```

The client assembles `/<apiBase>/<cmd>/<coll>`; a non-default source adds `?ds=`; keys use `?f=`. `watch` reads SSE via `fetch` streaming (it can send a Bearer token, which `EventSource` cannot) and resumes via `Last-Event-ID`.

## Functional API (`/api/<name>`)

```ts
import { defineApi } from "@kequnyang/dopdb/server";
const greet = defineApi(function greet(input: { name: string }, ctx) {
  return { msg: `hi ${input.name}`, caller: ctx.claims["uid"] ?? null };
});
await greet({ name: "Ada" });   // in-process, typed
```

Minimal pipeline: `decode → validate (optional) → handler` (no ParamEnhancer/ResultSaver/ResponseModifier). The client calls type-safely with `apiClient<typeof api>({ baseUrl })`; handler code never reaches the browser (only input/output **types** are shared).

## Build / test

```
make ts            # cd ts && npm install && npm run build
make ts-test       # node --import tsx --test test/*.test.ts
make ts-typecheck  # tsc -p tsconfig.json --noEmit (strict)
```
