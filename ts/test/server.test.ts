import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import type { AddressInfo } from "node:net";

import { f, collection } from "../src/schema.js";
import { defineApi, _clearApiRegistry } from "../src/api.js";
import { serve, type DopdbServer } from "../src/server.js";
import { clientDb } from "../src/client.js";

// ---- a minimal in-memory Mongo Db (only what exec() touches) -----------------

function matchField(val: unknown, cond: unknown): boolean {
  if (cond !== null && typeof cond === "object" && !Array.isArray(cond) && !(cond instanceof Date)) {
    for (const [op, arg] of Object.entries(cond as Record<string, unknown>)) {
      switch (op) {
        case "$eq": if (val !== arg) return false; break;
        case "$ne": if (val === arg) return false; break;
        case "$gt": if (!((val as number) > (arg as number))) return false; break;
        case "$gte": if (!((val as number) >= (arg as number))) return false; break;
        case "$lt": if (!((val as number) < (arg as number))) return false; break;
        case "$lte": if (!((val as number) <= (arg as number))) return false; break;
        case "$in": if (!(arg as unknown[]).includes(val)) return false; break;
        case "$nin": if ((arg as unknown[]).includes(val)) return false; break;
        default: return false;
      }
    }
    return true;
  }
  return val === cond;
}

function matchDoc(doc: Record<string, unknown>, filter: Record<string, unknown>): boolean {
  for (const [k, cond] of Object.entries(filter)) {
    if (k === "$and") {
      if (!(cond as Record<string, unknown>[]).every((c) => matchDoc(doc, c))) return false;
    } else if (k === "$or") {
      if (!(cond as Record<string, unknown>[]).some((c) => matchDoc(doc, c))) return false;
    } else if (!matchField(doc[k], cond)) {
      return false;
    }
  }
  return true;
}

class DupKeyError extends Error {
  code = 11000;
}

type ChangeEvent = { operationType: string; documentKey: { _id: string }; fullDocument: Record<string, unknown> | null; _id: { seq: number } };

function fakeCollection() {
  const store = new Map<string, Record<string, unknown>>();
  const watchers = new Set<(ev: ChangeEvent) => void>();
  let seq = 0;
  const log: ChangeEvent[] = [];
  const all = () => [...store.values()];
  const sel = (filter: Record<string, unknown>) => all().filter((d) => matchDoc(d, filter));
  const emit = (operationType: string, id: string, fullDocument: Record<string, unknown> | null) => {
    const ev: ChangeEvent = { operationType, documentKey: { _id: id }, fullDocument, _id: { seq: ++seq } };
    log.push(ev);
    for (const w of watchers) w(ev);
  };
  const cursor = (rows: Record<string, unknown>[]) => {
    let data = rows.slice();
    let proj: Record<string, 0 | 1> | null = null;
    const applyProj = (d: Record<string, unknown>) => {
      if (!proj) return d;
      const inc = Object.values(proj).some((v) => v === 1);
      const out: Record<string, unknown> = {};
      if (inc) {
        for (const k of Object.keys(d)) if (proj[k] === 1) out[k] = d[k];
        if (proj._id !== 0 && "_id" in d) out._id = d._id;
      } else {
        for (const k of Object.keys(d)) if (proj[k] !== 0) out[k] = d[k];
      }
      return out;
    };
    const c: any = {
      project: (p: Record<string, 0 | 1>) => { proj = p; return c; },
      sort: () => c,
      skip: (n: number) => { data = data.slice(n); return c; },
      limit: (n: number) => { data = data.slice(0, n); return c; },
      toArray: async () => data.map(applyProj),
      async *[Symbol.asyncIterator]() { for (const d of data) yield applyProj(d); },
    };
    return c;
  };
  const put = (filter: Record<string, unknown>, doc: Record<string, unknown>, upsert?: boolean) => {
    const hit = sel(filter)[0];
    if (hit) { store.set(String(hit._id), { ...doc }); emit("replace", String(doc._id), { ...doc }); return { matchedCount: 1, upsertedCount: 0 }; }
    if (upsert) {
      const id = String(doc._id);
      if (store.has(id)) throw new DupKeyError(); // filter failed on a non-_id field (e.g. owner)
      store.set(id, { ...doc });
      emit("insert", id, { ...doc });
      return { matchedCount: 0, upsertedCount: 1 };
    }
    return { matchedCount: 0, upsertedCount: 0 };
  };
  return {
    async findOne(filter: Record<string, unknown>) { return sel(filter)[0] ?? null; },
    async insertOne(doc: Record<string, unknown>) {
      const id = String(doc._id);
      if (store.has(id)) throw new DupKeyError();
      store.set(id, { ...doc });
      emit("insert", id, { ...doc });
      return { insertedId: id };
    },
    async replaceOne(filter: Record<string, unknown>, doc: Record<string, unknown>, opts?: { upsert?: boolean }) {
      return put(filter, doc, opts?.upsert);
    },
    async bulkWrite(ops: { replaceOne: { filter: Record<string, unknown>; replacement: Record<string, unknown>; upsert?: boolean } }[]) {
      for (const op of ops) put(op.replaceOne.filter, op.replaceOne.replacement, op.replaceOne.upsert);
      return { ok: 1 };
    },
    async updateOne(filter: Record<string, unknown>, update: Record<string, unknown>, opts?: { upsert?: boolean }) {
      const hit = sel(filter)[0];
      const inc = (update.$inc ?? {}) as Record<string, number>;
      if (hit) {
        for (const [k, n] of Object.entries(inc)) hit[k] = ((hit[k] as number) ?? 0) + n;
        emit("update", String(hit._id), { ...hit });
        return { matchedCount: 1, upsertedCount: 0 };
      }
      if (opts?.upsert) {
        const doc: Record<string, unknown> = { _id: filter._id };
        for (const [k, n] of Object.entries(inc)) doc[k] = n;
        store.set(String(doc._id), doc);
        emit("insert", String(doc._id), { ...doc });
        return { matchedCount: 0, upsertedCount: 1 };
      }
      return { matchedCount: 0, upsertedCount: 0 };
    },
    async deleteMany(filter: Record<string, unknown>) {
      const hits = sel(filter);
      for (const d of hits) { store.delete(String(d._id)); emit("delete", String(d._id), null); }
      return { deletedCount: hits.length };
    },
    async countDocuments(filter: Record<string, unknown>) { return sel(filter).length; },
    find(filter: Record<string, unknown>) { return cursor(sel(filter)); },
    async createIndex() { return "ok"; },
    watch(pipeline: Record<string, any>[], opts?: { resumeAfter?: { seq?: number } }) {
      // parse an optional { $match: { "fullDocument.<field>": value } } stage
      let mf: string | undefined;
      let mv: unknown;
      for (const stage of pipeline ?? []) {
        const match = stage?.$match as Record<string, unknown> | undefined;
        if (match) for (const [k, v] of Object.entries(match)) if (k.startsWith("fullDocument.")) { mf = k.slice("fullDocument.".length); mv = v; }
      }
      const queue: ChangeEvent[] = [];
      let resolveNext: ((r: IteratorResult<ChangeEvent>) => void) | null = null;
      let closed = false;
      const push = (ev: ChangeEvent) => {
        if (mf !== undefined && ev.fullDocument?.[mf] !== mv) return; // owner scope
        if (resolveNext) { resolveNext({ value: ev, done: false }); resolveNext = null; }
        else queue.push(ev);
      };
      watchers.add(push);
      // resume: replay log entries after the given token (respecting scope)
      const resumeSeq = typeof opts?.resumeAfter?.seq === "number" ? opts.resumeAfter.seq : null;
      if (resumeSeq != null) {
        for (const ev of log) {
          if (ev._id.seq <= resumeSeq) continue;
          if (mf !== undefined && ev.fullDocument?.[mf] !== mv) continue;
          queue.push(ev);
        }
      }
      const stream: any = {
        [Symbol.asyncIterator]() { return this; },
        next(): Promise<IteratorResult<ChangeEvent>> {
          if (closed) return Promise.resolve({ value: undefined as any, done: true });
          if (queue.length) return Promise.resolve({ value: queue.shift()!, done: false });
          return new Promise((r) => { resolveNext = r; });
        },
        async close() {
          closed = true;
          watchers.delete(push);
          if (resolveNext) { resolveNext({ value: undefined as any, done: true }); resolveNext = null; }
        },
        return() { return this.close().then(() => ({ value: undefined as any, done: true })); },
      };
      return stream;
    },
  };
}

function fakeMongo() {
  const colls = new Map<string, ReturnType<typeof fakeCollection>>();
  return {
    collection(name: string) {
      if (!colls.has(name)) colls.set(name, fakeCollection());
      return colls.get(name)!;
    },
  };
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
  srv = await serve({
    schema,
    mongo: fakeMongo() as any,
    jwtSecret: SECRET,
    permit: () => true, // behavioral suite: exercise data/@-binding/scope, not the gate
    port: 0, // ephemeral
  });
  const addr = srv.http!.address() as AddressInfo;
  base = `http://127.0.0.1:${addr.port}`;
});

after(async () => {
  await srv.close();
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

test("hset fills @-bound owner from the JWT, not the client body", async () => {
  const r = await call("POST", "/api/hset/notes?f=n1", tokA, { text: "hello", owner: "spoofed" });
  assert.equal(r.status, 200);
  const got = await call("GET", "/api/hget/notes?f=n1", tokA);
  assert.equal(got.status, 200);
  assert.equal(got.body.owner, "alice", "owner is the verified uid, not the spoofed value");
  assert.equal(got.body.text, "hello");
});

test("owner-scope: bob cannot read alice's note (404 under his scope)", async () => {
  const got = await call("GET", "/api/hget/notes?f=n1", tokB);
  assert.equal(got.status, 404);
});

test("owner-scope: bob cannot overwrite alice's note (403 via dup key)", async () => {
  const r = await call("POST", "/api/hset/notes?f=n1", tokB, { text: "hijack" });
  assert.equal(r.status, 403);
  const still = await call("GET", "/api/hget/notes?f=n1", tokA);
  assert.equal(still.body.text, "hello", "alice's data is intact");
});

test("hsetnx returns inserted=false on an existing key", async () => {
  const r = await call("POST", "/api/hsetnx/notes?f=n1", tokA, { text: "again" });
  assert.equal(r.status, 200);
  assert.equal(r.body.inserted, false);
});

test("validation: empty required text is rejected with 400", async () => {
  const r = await call("POST", "/api/hset/notes?f=n2", tokA, { text: "" });
  assert.equal(r.status, 400);
  assert.equal(r.body.code, "validation");
});

test("find is owner-scoped: alice sees her note, bob sees none", async () => {
  await call("POST", "/api/hset/notes?f=n3", tokA, { text: "second" });
  const a = await call("POST", "/api/find/notes", tokA, {});
  assert.ok(Array.isArray(a.body) && a.body.length >= 2);
  const b = await call("POST", "/api/find/notes", tokB, {});
  assert.deepEqual(b.body, []);
});

test("unauthenticated access to an owner-scoped collection is 401", async () => {
  const r = await call("GET", "/api/hget/notes?f=n1");
  assert.equal(r.status, 401);
});

test("/api/<name> runs the endpoint with claims in ctx", async () => {
  const r = await call("POST", "/api/greet", tokA, { name: "Ada" });
  assert.equal(r.status, 200);
  assert.deepEqual(r.body, { msg: "hi Ada", caller: "alice" });
});

test("unknown collection is 404", async () => {
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

test("hmset writes many keys, owner-filled; hmget reads them aligned to input", async () => {
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

test("hmget is owner-scoped: bob cannot read alice's keys", async () => {
  const got = await call("GET", "/api/hmget/notes?f=m1&f=m2", tokB);
  assert.deepEqual(got.body, [null, null]);
});

test("find projection limits returned fields", async () => {
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

test("watch streams live, owner-scoped changes to the client (SSE)", async () => {
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

test("count is owner-scoped and filterable", async () => {
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

test("watch resumes from Last-Event-ID, replaying changes missed while disconnected", async () => {
  const c1 = await openSSE("/api/watch/notes", tokA);
  await sleep(60);
  await call("POST", "/api/hset/notes?f=r1", tokA, { text: "r1" });
  await waitFor(() => c1.events.some((e) => e.data.key === "r1"));
  const token = c1.lastId!;
  assert.ok(token, "server emitted a resume token (SSE id)");
  c1.close();
  await sleep(40);

  // change happens while the client is disconnected
  await call("POST", "/api/hset/notes?f=r2", tokA, { text: "r2" });

  const c2 = await openSSE("/api/watch/notes", tokA, token);
  await waitFor(() => c2.events.some((e) => e.data.key === "r2"));
  assert.ok(c2.events.some((e) => e.data.key === "r2"), "resume replayed the missed change");
  assert.equal(c2.events.some((e) => e.data.key === "r1"), false, "did not replay before the token");
  c2.close();
});

// ---- @-binding (key tokens, @field, anti-forgery) + new commands ------------

test("@uuid key: server generates the record key; @field + counter + title fire", async () => {
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

test("@uid key: read/write your own record by the JWT claim", async () => {
  await call("POST", "/api/hset/profiles?f=@uid", tokA, { name: "alice prime" });
  const self = await call("GET", "/api/hget/profiles?f=@uid", tokA);
  assert.equal(self.status, 200);
  assert.equal(self.body._id, "alice");
  assert.equal(self.body.name, "Alice Prime");
  const bob = await call("GET", "/api/hget/profiles?f=@uid", tokB);
  assert.equal(bob.status, 404, "@uid resolves to bob → no such record");
});

test("anti-forgery: client @-keys and bound fields cannot override server context", async () => {
  await call("POST", "/api/hset/profiles?f=af1", tokA, { name: "x", owner: "root", "@uid": "root" });
  const got = await call("GET", "/api/hget/profiles?f=af1", tokA);
  assert.equal(got.body.owner, "alice", "bound field overwritten by the JWT");
  assert.equal(got.body["@uid"], undefined, "forged @-key stripped");
  assert.equal(got.body.slug, "af1", "@field default since slug was absent");
});

test("del removes a record", async () => {
  await call("POST", "/api/hset/profiles?f=del1", tokA, { name: "bye" });
  assert.equal((await call("GET", "/api/hget/profiles?f=del1", tokA)).status, 200);
  const d = await call("POST", "/api/del/profiles?f=del1", tokA, {});
  assert.equal(d.status, 200);
  assert.equal((await call("GET", "/api/hget/profiles?f=del1", tokA)).status, 404);
});

test("hincrbyfloat adds a float to a numeric field", async () => {
  await call("POST", "/api/hset/profiles?f=hf1", tokA, { name: "n" }); // hits=1 (counter)
  const r = await call("POST", "/api/hincrbyfloat/profiles?f=hf1&field=hits&n=1.5", tokA, {});
  assert.equal(r.status, 200);
  const got = await call("GET", "/api/hget/profiles?f=hf1", tokA);
  assert.equal(got.body.hits, 2.5);
});

test("client.save derives the key from _id", async () => {
  const db = clientDb(schema, { baseUrl: base, getToken: () => tokA });
  await db.Profile.save({ _id: "save1", name: "saved" });
  const got = await call("GET", "/api/hget/profiles?f=save1", tokA);
  assert.equal(got.status, 200);
  assert.equal(got.body._id, "save1");
  assert.equal(got.body.name, "Saved");
});
