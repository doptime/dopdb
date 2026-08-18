import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import type { AddressInfo } from "node:net";

import { f, collection, ReadOnly } from "../src/schema.js";
import { defineApi, _clearApiRegistry } from "../src/api.js";
import { serve, type DopdbServer } from "../src/server.js";
import { clientDb } from "../src/client.js";

// ---- harness -----------------------------------------------------------------
//
// The Mongo build could fake its database with ~180 lines of in-memory objects,
// because every command was a document operation. On KVRocks the commands ARE
// Redis commands — LPUSH, ZADD, HSCAN, WATCH/MULTI, pub/sub — and a hand-rolled
// fake of those would be testing the fake, not the engine. So this suite runs
// against a real server and skips when there is none, exactly like the Go
// integration tests.
//
// The dialect and codec are covered without a server in query.test.ts and
// codec.test.ts, so `npm test` still has real coverage on a bare checkout.

const KV = process.env.DOPDB_TEST_KVROCKS_URI;
const skip = KV ? false : "set DOPDB_TEST_KVROCKS_URI (e.g. redis://localhost:6666) to run";

/** A namespace nobody else is using, dropped after the suite. */
const NS = `dopdb_ts_test_${Date.now()}_${Math.floor(Math.random() * 1e6)}`;

async function dropNamespace(): Promise<void> {
  if (!KV) return;
  const { Redis } = await import("ioredis");
  const r = new Redis(KV);
  let cursor = "0";
  do {
    const [next, keys] = await r.scan(cursor, "MATCH", `${NS}:*`, "COUNT", 500);
    cursor = next;
    if (keys.length > 0) await r.del(...keys);
  } while (cursor !== "0");
  await r.quit();
}

function makeJWT(claims: Record<string, unknown>, secret: string): string {
  const enc = (o: unknown) => Buffer.from(JSON.stringify(o)).toString("base64url");
  const head = enc({ alg: "HS256", typ: "JWT" });
  const body = enc(claims);
  const sig = createHmac("sha256", secret).update(`${head}.${body}`).digest("base64url");
  return `${head}.${body}.${sig}`;
}

// ---- the app ----------------------------------------------------------------

const SECRET = "test-secret";

const schema = {
  Note: collection({
    _id: f.string(),
    text: f.string().required(),
    owner: f.string().bind("@uid"),
  })
    .named("notes")
    .ownerScope("owner"),
  Uniq: collection({
    _id: f.string(),
    email: f.string().unique(),
    n: f.number(),
  }).named("uniqs"),
  Profile: collection({
    _id: f.string(),
    name: f.string().title(),
    owner: f.string().bind("@uid"),
    slug: f.string().default("@field"),
    hits: f.number().counter(),
    createdAt: f.date().createdAt(),
    updatedAt: f.date().updatedAt(),
  })
    .named("profiles")
    .ownerScope("owner"),
};

const greet = defineApi(function greet(input: { name: string }, ctx) {
  return { msg: `hi ${input.name}`, caller: ctx.claims["uid"] ?? null };
});

let srv: DopdbServer;
let base: string;
const tokA = makeJWT({ uid: "alice" }, SECRET);
const tokB = makeJWT({ uid: "bob" }, SECRET);

before(async () => {
  if (!KV) return;
  srv = await serve({
    schema,
    kvrocks: { uri: KV, namespace: NS },
    jwtSecret: SECRET,
    permit: () => true, // behavioral suite: exercise data/@-binding/scope, not the gate
    port: 0, // ephemeral
  });
  const addr = srv.http!.address() as AddressInfo;
  base = `http://127.0.0.1:${addr.port}`;
});

after(async () => {
  if (srv) await srv.close();
  await dropNamespace();
  greet.remove();
  _clearApiRegistry();
});

async function call(method: string, path: string, token?: string, body?: unknown) {
  const res = await fetch(base + path, {
    method,
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  return { status: res.status, body: text ? JSON.parse(text) : null };
}

test("hset fills @-bound owner from the JWT, not the client body", { skip }, async () => {
  const r = await call("POST", "/api/hset/notes?f=n1", tokA, { text: "hello", owner: "spoofed" });
  assert.equal(r.status, 200);
  const got = await call("GET", "/api/hget/notes?f=n1", tokA);
  assert.equal(got.status, 200);
  assert.equal(got.body.owner, "alice", "owner is the verified uid, not the spoofed value");
  assert.equal(got.body.text, "hello");
});

test("owner-scope: bob cannot read alice's note (404 under his scope)", { skip }, async () => {
  const got = await call("GET", "/api/hget/notes?f=n1", tokB);
  assert.equal(got.status, 404);
});

test("owner-scope: bob cannot overwrite alice's note (403 via dup key)", { skip }, async () => {
  const r = await call("POST", "/api/hset/notes?f=n1", tokB, { text: "hijack" });
  assert.equal(r.status, 403);
  const still = await call("GET", "/api/hget/notes?f=n1", tokA);
  assert.equal(still.body.text, "hello", "alice's data is intact");
});

test("hsetnx returns inserted=false on an existing key", { skip }, async () => {
  const r = await call("POST", "/api/hsetnx/notes?f=n1", tokA, { text: "again" });
  assert.equal(r.status, 200);
  assert.equal(r.body.inserted, false);
});

test("validation: empty required text is rejected with 400", { skip }, async () => {
  const r = await call("POST", "/api/hset/notes?f=n2", tokA, { text: "" });
  assert.equal(r.status, 400);
  assert.equal(r.body.code, "validation");
});

test("find is owner-scoped: alice sees her note, bob sees none", { skip }, async () => {
  await call("POST", "/api/hset/notes?f=n3", tokA, { text: "second" });
  const a = await call("POST", "/api/find/notes", tokA, {});
  assert.ok(Array.isArray(a.body) && a.body.length >= 2);
  const b = await call("POST", "/api/find/notes", tokB, {});
  assert.deepEqual(b.body, []);
});

test("unauthenticated access to an owner-scoped collection is 401", { skip }, async () => {
  const r = await call("GET", "/api/hget/notes?f=n1");
  assert.equal(r.status, 401);
});

test("/api/<name> runs the endpoint with claims in ctx", { skip }, async () => {
  const r = await call("POST", "/api/greet", tokA, { name: "Ada" });
  assert.equal(r.status, 200);
  assert.deepEqual(r.body, { msg: "hi Ada", caller: "alice" });
});

test("unknown collection is 404", { skip }, async () => {
  const r = await call("GET", "/api/hget/ghosts?f=x", tokA);
  assert.equal(r.status, 404);
});

// ---- increment 2: hmset / hmget / projection / watch -------------------------

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
async function waitFor(pred: () => boolean, ms = 1500): Promise<void> {
  const t0 = Date.now();
  while (!pred()) {
    if (Date.now() - t0 > ms) throw new Error("waitFor timed out");
    await sleep(15);
  }
}

test("hmset writes many keys, owner-filled; hmget reads them aligned to input", { skip }, async () => {
  const r = await call("POST", "/api/hmset/notes", tokA, {
    m1: { text: "one" },
    m2: { text: "two" },
  });
  assert.equal(r.status, 200);
  const got = await call("GET", "/api/hmget/notes?f=m1&f=missing&f=m2", tokA);
  assert.equal(got.status, 200);
  assert.equal(got.body.length, 3);
  assert.equal(got.body[0].text, "one");
  assert.equal(got.body[0].owner, "alice", "owner filled from JWT on hmset");
  assert.equal(got.body[1], null, "missing key is null, position preserved");
  assert.equal(got.body[2].text, "two");
});

test("hmget is owner-scoped: bob cannot read alice's keys", { skip }, async () => {
  const got = await call("GET", "/api/hmget/notes?f=m1&f=m2", tokB);
  assert.deepEqual(got.body, [null, null]);
});

test("find projection limits returned fields", { skip }, async () => {
  const p = encodeURIComponent(JSON.stringify({ text: 1, _id: 0 }));
  const r = await call("POST", `/api/find/notes?p=${p}`, tokA, { text: "one" });
  assert.equal(r.status, 200);
  assert.ok(r.body.length >= 1);
  for (const row of r.body) {
    assert.equal("text" in row, true);
    assert.equal("owner" in row, false, "owner projected out");
    assert.equal("_id" in row, false, "_id projected out");
  }
});

test("watch streams live, owner-scoped changes to the client (SSE)", { skip }, async () => {
  const aliceEvents: any[] = [];
  const db = clientDb(schema, { baseUrl: base, getToken: () => tokA });
  const unsub = await db.Note.watch((e) => aliceEvents.push(e));

  // a write by alice → alice sees it
  await call("POST", "/api/hset/notes?f=w1", tokA, { text: "live" });
  await waitFor(() => aliceEvents.some((e) => e.key === "w1"));
  const ev = aliceEvents.find((e) => e.key === "w1");
  assert.equal(ev.doc.text, "live");
  assert.equal(ev.doc.owner, "alice");

  // a write by bob → must NOT reach alice's owner-scoped stream
  const bobBefore = aliceEvents.length;
  await call("POST", "/api/hset/notes?f=w2", tokB, { text: "bob-only" });
  await sleep(150);
  assert.equal(aliceEvents.some((e) => e.key === "w2"), false, "bob's change is not delivered to alice");
  assert.equal(aliceEvents.length, bobBefore, "no cross-owner leakage");

  // after unsubscribe, no further events
  unsub();
  await sleep(50);
  const afterUnsub = aliceEvents.length;
  await call("POST", "/api/hset/notes?f=w3", tokA, { text: "post-unsub" });
  await sleep(150);
  assert.equal(aliceEvents.length, afterUnsub, "no events after unsubscribe");
});

test("count is owner-scoped and filterable", { skip }, async () => {
  await call("POST", "/api/hset/notes?f=cnt1", tokA, { text: "countme" });
  const a = await call("POST", "/api/count/notes", tokA, { text: "countme" });
  assert.equal(a.status, 200);
  assert.equal(a.body.count, 1);
  const b = await call("POST", "/api/count/notes", tokB, { text: "countme" });
  assert.equal(b.body.count, 0, "alice's rows are not counted under bob's scope");
});

// raw SSE reader for the resume test
async function openSSE(path: string, token: string, lastEventId?: string) {
  const ctrl = new AbortController();
  const res = await fetch(base + path, {
    headers: { Authorization: `Bearer ${token}`, ...(lastEventId ? { "Last-Event-ID": lastEventId } : {}) },
    signal: ctrl.signal,
  });
  const reader = res.body!.getReader();
  const dec = new TextDecoder();
  let buf = "";
  const events: { id?: string; data: any }[] = [];
  let lastId: string | undefined;
  void (async () => {
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let i: number;
        while ((i = buf.indexOf("\n\n")) >= 0) {
          const frame = buf.slice(0, i);
          buf = buf.slice(i + 2);
          let id: string | undefined;
          let data = "";
          for (const line of frame.split("\n")) {
            if (line.startsWith("id:")) id = line.slice(3).trim();
            else if (line.startsWith("data:")) data += line.slice(5).trim();
          }
          if (id) lastId = id;
          if (data) events.push({ id, data: JSON.parse(data) });
        }
      }
    } catch {
      /* aborted */
    }
  })();
  return { events, get lastId() { return lastId; }, close: () => ctrl.abort() };
}

// Change events come from Redis pub/sub, which does not replay: a reconnect
// starts fresh and anything published during the gap is gone. That is strictly
// weaker than a change-stream resume token, so it is asserted rather than
// quietly assumed — the server emits no SSE `id:` line at all.
test("watch does not claim to resume: reconnecting starts fresh", { skip }, async () => {
  const c1 = await openSSE("/api/watch/notes", tokA);
  await sleep(60);
  await call("POST", "/api/hset/notes?f=r1", tokA, { text: "r1" });
  await waitFor(() => c1.events.some((e) => e.data.key === "r1"));
  assert.equal(c1.lastId, undefined, "no resume token is advertised (pub/sub cannot replay)");
  c1.close();
  await sleep(40);

  // change happens while the client is disconnected — it is missed, by design
  await call("POST", "/api/hset/notes?f=r2", tokA, { text: "r2" });

  const c2 = await openSSE("/api/watch/notes", tokA);
  await sleep(120);
  assert.equal(c2.events.some((e) => e.data.key === "r2"), false, "no replay of the gap");

  // but a change made after reconnecting IS delivered
  await call("POST", "/api/hset/notes?f=r3", tokA, { text: "r3" });
  await waitFor(() => c2.events.some((e) => e.data.key === "r3"));
  c2.close();
});

// ---- @-binding (key tokens, @field, anti-forgery) + new commands ------------

test("@uuid key: server generates the record key; @field + counter + title fire", { skip }, async () => {
  const r = await call("POST", "/api/hset/profiles?f=@uuid", tokA, { name: "secret agent" });
  assert.equal(r.status, 200);
  const all = await call("GET", "/api/hgetall/profiles", tokA);
  const entry = Object.entries(all.body).find(([, v]: any) => v.name === "Secret Agent") as [string, any] | undefined;
  assert.ok(entry, "created profile present in scoped hgetall");
  const [id, doc] = entry!;
  assert.equal(id.length, 36, "key is a generated uuid");
  assert.equal(doc.owner, "alice", "owner @-bound from the JWT");
  assert.equal(doc.slug, id, "@field default = the record key");
  assert.equal(doc.hits, 1, "counter initialised to 1");
  assert.equal(doc._id, id);
  assert.ok(doc.createdAt && doc.updatedAt, "timestamps filled on write");
});

test("@uid key: read/write your own record by the JWT claim", { skip }, async () => {
  await call("POST", "/api/hset/profiles?f=@uid", tokA, { name: "alice prime" });
  const self = await call("GET", "/api/hget/profiles?f=@uid", tokA);
  assert.equal(self.status, 200);
  assert.equal(self.body._id, "alice");
  assert.equal(self.body.name, "Alice Prime");
  const bob = await call("GET", "/api/hget/profiles?f=@uid", tokB);
  assert.equal(bob.status, 404, "@uid resolves to bob → no such record");
});

test("anti-forgery: client @-keys and bound fields cannot override server context", { skip }, async () => {
  await call("POST", "/api/hset/profiles?f=af1", tokA, { name: "x", owner: "root", "@uid": "root" });
  const got = await call("GET", "/api/hget/profiles?f=af1", tokA);
  assert.equal(got.body.owner, "alice", "bound field overwritten by the JWT");
  assert.equal(got.body["@uid"], undefined, "forged @-key stripped");
  assert.equal(got.body.slug, "af1", "@field default since slug was absent");
});

test("del removes a record", { skip }, async () => {
  await call("POST", "/api/hset/profiles?f=del1", tokA, { name: "bye" });
  assert.equal((await call("GET", "/api/hget/profiles?f=del1", tokA)).status, 200);
  const d = await call("POST", "/api/del/profiles?f=del1", tokA, {});
  assert.equal(d.status, 200);
  assert.equal((await call("GET", "/api/hget/profiles?f=del1", tokA)).status, 404);
});

test("hincrbyfloat adds a float to a numeric field", { skip }, async () => {
  await call("POST", "/api/hset/profiles?f=hf1", tokA, { name: "n" }); // hits=1 (counter)
  const r = await call("POST", "/api/hincrbyfloat/profiles?f=hf1&field=hits&n=1.5", tokA, {});
  assert.equal(r.status, 200);
  const got = await call("GET", "/api/hget/profiles?f=hf1", tokA);
  assert.equal(got.body.hits, 2.5);
});

test("client.save derives the key from _id", { skip }, async () => {
  const db = clientDb(schema, { baseUrl: base, getToken: () => tokA });
  await db.Profile.save({ _id: "save1", name: "saved" });
  const got = await call("GET", "/api/hget/profiles?f=save1", tokA);
  assert.equal(got.status, 200);
  assert.equal(got.body._id, "save1");
  assert.equal(got.body.name, "Saved");
});

// ---- listener property on DopdbServer ----------------------------------------

test("serve returns a DopdbServer with a callable listener", { skip }, async () => {
  assert.equal(typeof srv.listener, "function", "listener must be a function");
});

test("srv.listener can handle a fake HTTP request", { skip }, async () => {
  const req = {
    method: "GET",
    url: "/api/hget/notes?f=n1",
    headers: { authorization: `Bearer ${tokA}` },
    on: () => {},
  } as any;
  const res = {
    statusCode: 0,
    setHeader: () => {},
    writeHead: () => {},
    write: (chunk: string) => (res._body += chunk),
    end: (chunk?: string) => { if (chunk) res._body += chunk; },
    _body: "",
  } as any;
  srv.listener(req, res);
  await sleep(50);
  assert.ok(res._body.length > 0, "listener should produce a response body");
});

// ---- regressions found by the post-migration audit ---------------------------
//
// Both of these were engine divergences: Go behaved correctly, TS did not. They
// are pinned here because neither had a test, which is exactly why they survived
// the migration unnoticed.

// KVRocks has no unique index, so dopdb raises the violation itself. It must
// surface as 409/"conflict" like Go's ErrDuplicate — a plain Error would fall
// through the HTTP error mapper as an opaque 500.
test("a unique-index violation is 409 conflict, not 500", { skip }, async () => {
  const first = await call("POST", "/api/hset/uniqs?f=a", tokA, { email: "dup@x.io", n: 0 });
  assert.equal(first.status, 200);

  const dup = await call("POST", "/api/hset/uniqs?f=b", tokA, { email: "dup@x.io", n: 0 });
  assert.equal(dup.status, 409, "duplicate must be a conflict");
  assert.equal(dup.body.code, "conflict");

  // rewriting the SAME document with the same value is not a conflict
  const same = await call("POST", "/api/hset/uniqs?f=a", tokA, { email: "dup@x.io", n: 1 });
  assert.equal(same.status, 200);
});

// hincrby is a WATCH/MULTI read-modify-write. ioredis multiplexes the whole
// process over one socket, and WATCH is per-CONNECTION state — so without a
// dedicated, serialised transaction connection the first EXEC clears the watch
// set and later transactions commit unprotected, silently losing increments.
// This test failed at 11-15 of 20 before the fix, with zero error responses.
test("concurrent hincrby loses no increments", { skip }, async () => {
  await call("POST", "/api/hset/uniqs?f=ctr", tokA, { email: "ctr@x.io", n: 0 });
  const N = 20;
  const rs = await Promise.all(
    Array.from({ length: N }, () => call("POST", "/api/hincrby/uniqs?f=ctr&field=n&n=1", tokA)),
  );
  assert.equal(rs.filter((r) => r.status !== 200).length, 0, "no request may fail");

  const got = await call("GET", "/api/hget/uniqs?f=ctr", tokA);
  assert.equal(got.body.n, N, `expected ${N} increments to land, got ${got.body.n}`);
});

// ---- httpOn permission gate (Task 2) ----------------------------------------
// A collection's .httpOn(...) bitmask is the grant; no permit function needed.
test("httpOn gates data commands without a permit function", { skip }, async () => {
  const ronly = collection({ _id: f.string(), text: f.string() }).named("ronly").httpOn(ReadOnly);
  const full = collection({ _id: f.string(), text: f.string() }).named("full").httpOn(); // debug: all on
  const s = await serve({
    schema: { Ronly: ronly, Full: full },
    kvrocks: { uri: KV!, namespace: `${NS}_gate` },
    jwtSecret: SECRET,
    // NO permit / permissions — the httpOn bitmask is the only grant.
    port: 0,
  });
  try {
    const addr = s.http!.address() as AddressInfo;
    const b = `http://127.0.0.1:${addr.port}`;
    const tok = makeJWT({ uid: "alice" }, SECRET);
    const req = async (m: string, p: string, body?: unknown) => {
      const r = await fetch(b + p, {
        method: m,
        headers: {
          Authorization: `Bearer ${tok}`,
          ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
        },
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
      return r.status;
    };
    // ReadOnly collection: writes forbidden, reads allowed (404 for missing, not 403).
    assert.equal(await req("POST", "/api/hset/ronly?f=k1", { text: "x" }), 403, "hset on ReadOnly httpOn → 403");
    assert.notEqual(await req("GET", "/api/hget/ronly?f=k1"), 403, "hget on ReadOnly httpOn → not 403");
    // Debug-all collection: writes allowed.
    assert.equal(await req("POST", "/api/hset/full?f=k1", { text: "x" }), 200, "hset on httpOn() all → 200");
  } finally {
    await s.close();
  }
});
