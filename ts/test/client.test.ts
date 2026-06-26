import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";

import { f, collection } from "../src/schema.js";
import { clientDb, apiClient } from "../src/client.js";
import { ForbiddenError, ValidationError } from "../src/errors.js";
import type { Endpoint } from "../src/api.js";

const schema = {
  User: collection({ _id: f.string(), name: f.string() }).named("users"),
  Order: collection({ _id: f.string(), total: f.number(), owner: f.string().bind("@uid") })
    .named("orders")
    .inDb("analytics"),
};

interface Captured {
  url: string;
  method: string;
  headers: Record<string, string>;
  body?: string;
}

let captured: Captured;
let responder: (c: Captured) => { status: number; body: unknown };

function install(): void {
  globalThis.fetch = (async (input: any, init: any) => {
    const headers: Record<string, string> = {};
    const h = init?.headers ?? {};
    for (const k of Object.keys(h)) headers[k.toLowerCase()] = h[k];
    captured = { url: String(input), method: init?.method ?? "GET", headers, body: init?.body };
    const { status, body } = responder(captured);
    return new Response(JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json" },
    });
  }) as typeof fetch;
}

beforeEach(() => {
  install();
  responder = () => ({ status: 200, body: { ok: true } });
});

test("hget: GET /api/<cmd>/<coll>?f=key, Bearer header, no @ in URL", async () => {
  responder = () => ({ status: 200, body: { _id: "u1", name: "Ada" } });
  const db = clientDb(schema, { baseUrl: "https://api.x.com/", getToken: () => "tok123" });
  const u = await db.User.hget("u1");
  assert.deepEqual(u, { _id: "u1", name: "Ada" });
  assert.equal(captured.method, "GET");
  assert.equal(captured.url, "https://api.x.com/api/hget/users?f=u1");
  assert.equal(captured.headers["authorization"], "Bearer tok123");
  assert.ok(!captured.url.includes("@"), "identity must never travel in the URL");
});

test("non-default datasource is selected with ?ds=", async () => {
  responder = () => ({ status: 200, body: { len: 3 } });
  const db = clientDb(schema, { baseUrl: "https://api.x.com" });
  const n = await db.Order.hlen();
  assert.equal(n, 3);
  assert.equal(captured.url, "https://api.x.com/api/hlen/orders?ds=analytics");
});

test("hget: 404 maps to null, not an error", async () => {
  responder = () => ({ status: 404, body: { error: "not found", code: "not_found" } });
  const db = clientDb(schema, {});
  assert.equal(await db.User.hget("missing"), null);
});

test("hset: POST body carries the value; bound owner is not sent by the client", async () => {
  responder = () => ({ status: 200, body: { ok: true } });
  const db = clientDb(schema, { baseUrl: "" });
  await db.Order.hset("o1", { total: 42 });
  assert.equal(captured.method, "POST");
  assert.equal(captured.url, "/api/hset/orders?ds=analytics&f=o1");
  assert.deepEqual(JSON.parse(captured.body!), { total: 42 });
});

test("hsetnx: returns the inserted flag", async () => {
  responder = () => ({ status: 200, body: { inserted: false } });
  const db = clientDb(schema, {});
  assert.equal(await db.Order.hsetnx("o1", { total: 1 }), false);
});

test("find: sends the filter as the POST body, limit/skip as query", async () => {
  responder = () => ({ status: 200, body: [{ _id: "o1", total: 5 }] });
  const db = clientDb(schema, {});
  const rows = await db.Order.find({ total: { $gt: 3 } }, { limit: 10 });
  assert.equal(rows.length, 1);
  assert.equal(captured.method, "POST");
  assert.match(captured.url, /\/api\/find\/orders\?ds=analytics&limit=10$/);
  assert.deepEqual(JSON.parse(captured.body!), { total: { $gt: 3 } });
});

test("find: an illegal operator fails before the network", async () => {
  let called = false;
  responder = () => {
    called = true;
    return { status: 200, body: [] };
  };
  const db = clientDb(schema, {});
  await assert.rejects(() => db.Order.find({ $where: "1" } as any), ValidationError);
  assert.equal(called, false, "no request should be sent for a rejected filter");
});

test("403 becomes a typed ForbiddenError", async () => {
  responder = () => ({ status: 403, body: { error: "forbidden", code: "forbidden" } });
  const db = clientDb(schema, {});
  await assert.rejects(() => db.Order.hset("o1", { total: 1 }), ForbiddenError);
});

test("apiClient: POSTs typed input to /api/<name>", async () => {
  type Api = { greet: Endpoint<{ name: string }, { msg: string }> };
  responder = () => ({ status: 200, body: { msg: "hi Ada" } });
  const call = apiClient<Api>({ baseUrl: "https://api.x.com" });
  const out = await call("greet", { name: "Ada" });
  assert.deepEqual(out, { msg: "hi Ada" });
  assert.equal(captured.url, "https://api.x.com/api/greet");
  assert.deepEqual(JSON.parse(captured.body!), { name: "Ada" });
});
