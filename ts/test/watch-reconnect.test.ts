import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";

import { f, collection } from "../src/schema.js";
import { clientDb } from "../src/client.js";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
async function waitFor(pred: () => boolean, ms = 1500): Promise<void> {
  const t0 = Date.now();
  while (!pred()) {
    if (Date.now() - t0 > ms) throw new Error("waitFor timed out");
    await sleep(10);
  }
}

function sseResponse(frames: string[], keepOpen = false): Response {
  const enc = new TextEncoder();
  const stream = new ReadableStream({
    start(c) {
      for (const fr of frames) c.enqueue(enc.encode(fr));
      if (!keepOpen) c.close();
    },
  });
  return new Response(stream, { status: 200, headers: { "content-type": "text/event-stream" } });
}

let calls: { url: string; headers: Record<string, string> }[] = [];

beforeEach(() => {
  calls = [];
  globalThis.fetch = (async (input: any, init: any) => {
    const headers: Record<string, string> = {};
    const h = init?.headers ?? {};
    for (const k of Object.keys(h)) headers[k.toLowerCase()] = h[k];
    calls.push({ url: String(input), headers });
    if (calls.length === 1) {
      // first connection: one event then the stream closes
      return sseResponse([`id: t1\ndata: ${JSON.stringify({ type: "insert", key: "a", doc: { x: 1 } })}\n\n`]);
    }
    if (calls.length === 2) {
      // reconnection: another event then closes
      return sseResponse([`id: t2\ndata: ${JSON.stringify({ type: "update", key: "b", doc: { x: 2 } })}\n\n`]);
    }
    // third attempt: permanent → loop stops cleanly (no infinite reconnect)
    return new Response(JSON.stringify({ error: "gone", code: "not_found" }), {
      status: 404,
      headers: { "content-type": "application/json" },
    });
  }) as typeof fetch;
});

const schema = { Note: collection({ _id: f.string(), x: f.number() }).named("notes") };

test("watch reconnects on disconnect and resumes from Last-Event-ID", async () => {
  const db = clientDb(schema, { baseUrl: "https://x", getToken: () => "tok" });
  const got: any[] = [];
  const unsub = await db.Note.watch((e) => got.push(e), { backoff: { baseMs: 10, maxMs: 40 } });

  await waitFor(() => got.length >= 2);
  assert.equal(got[0].key, "a");
  assert.equal(got[1].key, "b");

  assert.ok(calls.length >= 2, "should have reconnected at least once");
  assert.equal(calls[0].url, "https://x/api/watch/notes");
  assert.equal(calls[1].headers["last-event-id"], "t1", "resumes from the last seen event id");

  // let the loop reach the permanent 404 and stop
  await waitFor(() => calls.length >= 3);
  unsub();
});

test("watch with reconnect:false does not reconnect after the stream ends", async () => {
  const db = clientDb(schema, { baseUrl: "https://x", getToken: () => "tok" });
  const got: any[] = [];
  const unsub = await db.Note.watch((e) => got.push(e), { reconnect: false, backoff: { baseMs: 10 } });
  await waitFor(() => got.length >= 1);
  await sleep(120);
  assert.equal(calls.length, 1, "no reconnect attempts");
  assert.equal(got.length, 1);
  unsub();
});
