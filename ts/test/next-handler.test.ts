import { test } from "node:test";
import assert from "node:assert/strict";
import { createNextHandler } from "../src/server.js";
import { clientDb } from "../src/client.js";
import { collection, f } from "../src/schema.js";

const schema = { users: collection({ name: f.string() }).named("users") };

test("createNextHandler exposes GET/POST/OPTIONS", () => {
  const h = createNextHandler({ schema, mongo: { uri: "mongodb://127.0.0.1:1/x", db: "x" }, jwtSecret: "s" });
  assert.equal(typeof h.GET, "function");
  assert.equal(typeof h.POST, "function");
  assert.equal(typeof h.OPTIONS, "function");
});

test("OPTIONS preflight returns 204 without dialing Mongo", async () => {
  const h = createNextHandler({
    schema,
    mongo: { uri: "mongodb://127.0.0.1:1/x", db: "x" }, // unreachable; must NOT be connected
    jwtSecret: "s",
    cors: ["*"],
  });
  const res = await h.OPTIONS(
    new Request("http://localhost/api/hget/users", {
      method: "OPTIONS",
      headers: { origin: "https://app.example.com" },
    }),
  );
  assert.equal(res.status, 204);
  assert.equal(res.headers.get("access-control-allow-origin"), "*");
});

test("client apiBase changes the route prefix", async () => {
  let url = "";
  const orig = globalThis.fetch;
  globalThis.fetch = (async (u: unknown) => {
    url = String(u);
    return new Response("null", { status: 200, headers: { "content-type": "application/json" } });
  }) as typeof fetch;
  try {
    const db = clientDb(schema, { baseUrl: "https://api.x.com", apiBase: "/db" });
    await db.users.hget("u1");
  } finally {
    globalThis.fetch = orig;
  }
  assert.equal(url, "https://api.x.com/db/hget/users?f=u1");
});
