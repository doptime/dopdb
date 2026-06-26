# dopdb (TypeScript α)

One TypeScript **source of truth** for DB + API. Declare a collection once; get
native types, runtime validation, indexes, `@`-binding, a browser client (fetch),
and a Node server (MongoDB) — **no codegen, no double-write, no WASM on the
client**.

This is the α track: TypeScript is the framework of record. Go stays in the repo
in a **non-overlapping role** (client SDK and/or an optional schema-driven
engine), never re-implementing the schema — see "One repo, two languages" below.

Status: increments 1–2 done. `tsc --noEmit` clean, 36/36 tests green (including
the Node+Mongo server **and live `watch`/SSE**, exercised end-to-end against an
in-memory Mongo so it all runs without a database).

- Increment 1 — the single-source spine (schema, types, validation, client, server).
- Increment 2 — `watch` (change streams → SSE), `hmset`/`hmget`, find sort/projection,
  and `dopdb-spec` (emit `dopdb.schema.json` for the optional Go engine).

---

## The whole idea in one screen

```ts
// shared/schema.ts — the ONE definition, imported by both sides
import { collection, f, type Infer } from "dopdb";

export const schema = {
  Note: collection({
    _id:   f.string(),
    text:  f.string().required().min(1),
    tags:  f.string().optional(),
    owner: f.string().bind("@uid"),   // server fills from JWT; read-scoped too
  })
    .named("notes")
    .ownerScope("owner"),
};

export type Note = Infer<typeof schema.Note>;
//   { _id: string; text: string; owner: string; tags?: string }
```

```ts
// web (browser) — no WASM, just fetch
import { clientDb } from "dopdb/client";
import { schema } from "./shared/schema";

const db = clientDb(schema, { baseUrl: "/", getToken: () => token });

const n  = await db.Note.hget("n1");          // Note | null   (typed)
await db.Note.hset("n1", { text: "hi" });     // owner NOT required — it's @-bound
const mine = await db.Note.find({ tags: { $gt: "" } });

await db.Note.hmset({ n1: { text: "a" }, n2: { text: "b" } });   // one round-trip
const some = await db.Note.hmget(["n1", "n2"]);                   // aligned, null = miss

// live updates (owner-scoped on the server); returns an unsubscribe fn
const stop = await db.Note.watch((ev) => {
  // ev: { type: "insert"|"update"|"replace"|"delete"; key: string; doc: Note|null }
  console.log(ev.type, ev.key, ev.doc);
});
// later: stop();
```

```ts
// server (Node) — binds MongoDB directly (no Store abstraction)
import { serve, defineApi } from "dopdb/server";
import { schema } from "./shared/schema";

defineApi(function greet(in: { name: string }) {     // tRPC-style typed endpoint
  return { msg: "hi " + in.name };
});

await serve({
  schema,
  mongo: { uri: process.env.MONGO_URI!, db: "app" },
  jwtSecret: process.env.JWT_SECRET!,
  port: 8080,                                          // one-line serve
});
```

```ts
// the function API is typed across the wire, no codegen:
import type { } from "../server";          // (import the api object's TYPE)
import { apiClient } from "dopdb/client";
const call = apiClient<typeof api>({ baseUrl: "/" });
const r = await call("greet", { name: "Ada" });        // typed in & out
```

The schema object is **imported**, never generated; the API handle's **type** is
imported with `import type`, so the handler never ships to the browser.

---

## One repo, two languages — the rule

The project can be **simultaneously a Go module and an npm package** — provided
the two languages have **non-overlapping roles around the single TS source**:

```
dopdb/                       ← one git repo, one identity
├── go.mod                   ← module github.com/doptime/dopdb   → `go get` still works
├── *.go, api/, mongostore/  ← Go: now a CLIENT SDK and/or an optional
│                              schema-as-data ENGINE — NOT a second schema
├── package.json             ← npm package "dopdb"              → `npm install` works
├── tsconfig.json
└── src/                     ← TS: the framework of record (this package)
    ├── schema.ts            ← THE single source (types + validation + spec)
    ├── client.ts  server.ts ← fetch client / node+mongo server
    └── api.ts  errors.ts  sanitize.ts
```

- `go get github.com/doptime/dopdb` → resolves the Go package at the repo root.
- `npm install dopdb` → resolves by name from the registry; its location in the
  tree is irrelevant to npm.
- `go.mod` and `package.json` coexist in one directory without conflict.

The line that keeps this from being the "split" we rejected: **exactly one
source of truth for schema/types — TS.** Go either (a) just calls a dopdb server
(a client), or (b) reads the emitted schema-as-data (`specOf()` → JSON) as an
optional engine. The moment Go redeclares fields/validation, it becomes a second
framework. It must not.

Tooling note: keep `node_modules` confined and scope Go builds (`go build ./api/...`)
or run a Makefile target; the TS runtime deps are near-zero (only the Mongo
driver, server-side), so this stays painless.

---

## What's realized from the 15 decisions

| # | Decision | Where |
|---|----------|-------|
| 1 | No TS-from-Go codegen; types authored in TS | `schema.ts` (`Infer`/`InferInput`) |
| 2–3 | Typed `db.User` object; redisdb-compatible method names | `client.ts` / `server.ts` `DbApi` |
| 4 | Drop public callApi/removeApi; one `defineApi` handle | `api.ts` `defineApi().remove()` |
| 5 | `watch` via Mongo change streams (SSE), owner-scoped | `server.ts` `handleWatch` / `client.ts` `watch` |
| 6 | One-line serve | `server.ts` `serve({ port })` |
| 7 | URL `/api/<cmd>/<coll>` (+ `/<db>/`), discriminated by closed verb set | `client.ts` `cmdPath` / `server.ts` `route` |
| 8 | Keep HGET/HSET vocabulary (+ hmset/hmget, point 13) | `DATA_COMMANDS` |
| 9 | Shared validation/sanitizer in TS | `schema.ts` `validate`, `sanitize.ts` |
| 10 | No OpenAPI — types are the doc | (types) |
| 11 | Typed errors | `errors.ts` (`instanceof` both sides) |
| 12 | `@`-binding into structured body/JWT; never in the URL | `prepareWrite({claims})`, `ownerScope` |
| 13 | No batch — `hmset`/`hmget` cover it | `client.ts`/`server.ts` `hmset`/`hmget` |
| 14 | No Store abstraction — bind Mongo directly | `server.ts` `exec()` |
| 15 | No dev panel | — |

Deferred to increment 3: client-side `watch` **auto-reconnect** (with last-seen
resume token), fuller Mongo coverage (aggregation-backed reads, TTL indexes), and
the optional **Go schema-as-data engine** that consumes `dopdb.schema.json` (the
TS side already emits it — see below).

## Schema-as-data (for the optional Go engine / tooling)

```bash
node --import tsx bin/spec.ts ./src/shared/schema.ts > dopdb.schema.json
# or, after build:  npx dopdb-spec ./dist/shared/schema.js > dopdb.schema.json
```

`buildSpec(schema)` (and the CLI) emit `{ version, collections[] }` — fields,
indexes, owner field, `@`-bindings. This is the **one** artifact a non-TS engine
reads; it is derived from the single TS source, so the Go side consumes data, it
never redeclares the schema.

---

## Model notes (read once)

- **The key is separate from the value.** `hget(key)` / `hset(key, value)` — the
  key is stored as Mongo `_id`. Declaring `_id: f.string()` in the shape is
  optional and only types it on reads; it's never required in a write body.
- **Three write modes**, one function (`prepareWrite`): client (strips `@`-bound
  fields, validates provided fields early), HTTP server (fills `@`-bound fields
  from the verified JWT), trusted (raw server calls keep caller-supplied values).
  This is the Go `Collection` (trusted) vs `httpserve` (scoped) split, unified.
- **`.named()` renames both** the public API name and the storage collection.
- **Owner-scope is one declaration, two effects:** `.bind("@uid")` on the owner
  field fills it on write *and* filters every read by it. No `@` ever appears in
  a URL.

## Develop

```bash
npm install
npm run typecheck      # tsc --noEmit (also checks the type-level assertions)
npm run test           # node --test via tsx (includes the server e2e)
npm run build          # emit dist/
```

The server e2e test (`test/server.test.ts`) runs against an in-memory Mongo
fake. To test against a real database, swap in `mongodb-memory-server` or a live
`MONGO_URI` in your environment.
