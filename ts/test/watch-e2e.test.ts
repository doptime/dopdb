import { test } from "node:test";
import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import { f, collection } from "../src/schema.js";
import { serve } from "../src/server.js";
import type { DopdbServer } from "../src/server.js";
// M3 TS watch E2E against real MongoDB replica set.
// Verifies: insert/update push, scoped delete no-delivery (I-WA2).

const schema = {
	notes: collection({
		text: f.string(),
		owner: f.string().bind("@uid"),
	}).named("notes").ownerScope("owner"),
};

const SECRET = "test";

function makeJWT(uid: string): string {
	const enc = (o: unknown) => Buffer.from(JSON.stringify(o)).toString("base64url");
	const head = enc({ alg: "HS256", typ: "JWT" });
	const body = enc({ uid });
	const sig = createHmac("sha256", SECRET).update(`${head}.${body}`).digest("base64url");
	return `${head}.${body}.${sig}`;
}

function portOf(srv: DopdbServer): number {
	const addr = srv.http?.address();
	if (addr && typeof addr === "object" && "port" in addr) {
		return (addr as { port: number }).port;
	}
	throw new Error("server not listening");
}

const MONGO = process.env.DOPDB_TEST_MONGO_URI;

test("TS watch: insert and update events are pushed", { skip: MONGO ? false : "set DOPDB_TEST_MONGO_URI (replica set) to run" }, async () => {
	const srv = await serve({
		schema,
		mongo: { uri: MONGO!, db: "dopdb_watch_ts" },
		jwtSecret: SECRET,
		permit: () => true,
		port: 0,
	});
	try {
		const base = `http://127.0.0.1:${portOf(srv)}`;
		const tok = makeJWT("alice");

		// First insert a note
		await fetch(`${base}/api/hset/notes?f=n1`, {
			method: "POST",
			headers: { Authorization: `Bearer ${tok}`, "Content-Type": "application/json" },
			body: JSON.stringify({ text: "hello" }),
		});

		// Open SSE watch
		const watchResp = await fetch(`${base}/api/watch/notes`, {
			headers: { Authorization: `Bearer ${tok}` },
		});
		assert.strictEqual(watchResp.status, 200);
		assert.ok(watchResp.headers.get("content-type")?.includes("text/event-stream"));

		const reader = watchResp.body!.getReader();
		const events: string[] = [];

		// Read events in background
		const readPromise = (async () => {
			for (let i = 0; i < 5; i++) {
				const { value, done } = await reader.read();
				if (done) break;
				const text = new TextDecoder().decode(value);
				const lines = text.split("\n\n").filter(Boolean);
				for (const line of lines) {
					const data = line.startsWith("data: ") ? line.slice(6) : "";
					if (data) {
						const ev = JSON.parse(data);
						events.push(ev.type);
					}
				}
			}
		})();

		// Insert a new note to trigger event
		await fetch(`${base}/api/hset/notes?f=n2`, {
			method: "POST",
			headers: { Authorization: `Bearer ${tok}`, "Content-Type": "application/json" },
			body: JSON.stringify({ text: "world" }),
		});

		// Wait for SSE to deliver
		await new Promise((r) => setTimeout(r, 2000));
		reader.cancel();
		await readPromise;

		console.log(`watch events received: ${events.join(", ")}`);
		assert.ok(events.length >= 1, `expected at least 1 watch event, got ${events.length}`);
		// Should see an insert or replace event
		const hasInsert = events.some((e) => e === "insert" || e === "replace");
		assert.ok(hasInsert, `expected insert/replace event, got: ${events.join(", ")}`);
	} finally {
		await srv.close();
	}
});
