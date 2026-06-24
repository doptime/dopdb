// data-client.test.mjs — mock-fetch tests for the DataClient
// Pure Node, no jest.  Run: node data-client.test.mjs
import { collection, configure } from "./dist/index.js";

let fetchCalls = [];
const origFetch = globalThis.fetch;
globalThis.fetch = async (url, init) => {
  fetchCalls.push({ url, init });
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
};

function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

function sub(name, fn) {
  try {
    fn();
    console.log(`  ✓ ${name}`);
  } catch (e) {
    console.error(`  ✗ ${name}: ${e.message}`);
    process.exitCode = 1;
  }
}

configure({ baseUrl: "https://api.test", token: "t" });

async function run() {
  const c = collection("Note");

  // 1) hget
  await c.hget("k1");
  sub("hget URL", () => {
    assert(fetchCalls.length >= 1, "no calls");
    assert(fetchCalls[0].url === "https://api.test/HGET-Note?f=k1", `url=${fetchCalls[0].url}`);
    assert(fetchCalls[0].init.method === "GET", `method=${fetchCalls[0].init.method}`);
  });
  sub("hget auth", () => {
    assert(fetchCalls[0].init.headers.Authorization === "Bearer t", `auth=${fetchCalls[0].init.headers.Authorization}`);
  });

  // 2) hset
  await c.hset("k1", { item: "book" });
  const hsetIdx = fetchCalls.length - 1;
  sub("hset URL", () => {
    assert(fetchCalls[hsetIdx].url === "https://api.test/HSET-Note?f=k1", `url=${fetchCalls[hsetIdx].url}`);
    assert(fetchCalls[hsetIdx].init.method === "POST", `method=${fetchCalls[hsetIdx].init.method}`);
  });
  sub("hset body", () => {
    assert(fetchCalls[hsetIdx].init.body === '{"item":"book"}', `body=${fetchCalls[hsetIdx].init.body}`);
  });
  sub("hset content-type", () => {
    assert(fetchCalls[hsetIdx].init.headers["Content-Type"] === "application/json", "missing Content-Type");
  });

  // 3) hdel
  await c.hdel("k1");
  const hdelIdx = fetchCalls.length - 1;
  sub("hdel URL", () => {
    assert(fetchCalls[hdelIdx].url === "https://api.test/HDEL-Note?f=k1", `url=${fetchCalls[hdelIdx].url}`);
    assert(fetchCalls[hdelIdx].init.method === "POST", `method=${fetchCalls[hdelIdx].init.method}`);
  });

  // 4) hkeys
  await c.hkeys();
  const hkeysIdx = fetchCalls.length - 1;
  sub("hkeys URL", () => {
    assert(fetchCalls[hkeysIdx].url === "https://api.test/HKEYS-Note", `url=${fetchCalls[hkeysIdx].url}`);
    assert(fetchCalls[hkeysIdx].init.method === "GET", `method=${fetchCalls[hkeysIdx].init.method}`);
  });

  // 5) hlen
  await c.hlen();
  const hlenIdx = fetchCalls.length - 1;
  sub("hlen URL", () => {
    assert(fetchCalls[hlenIdx].url === "https://api.test/HLEN-Note", `url=${fetchCalls[hlenIdx].url}`);
    assert(fetchCalls[hlenIdx].init.method === "GET", `method=${fetchCalls[hlenIdx].init.method}`);
  });

  // 6) find
  await c.find({ owner: "u1" });
  const findIdx = fetchCalls.length - 1;
  sub("find URL", () => {
    assert(fetchCalls[findIdx].url === "https://api.test/FIND-Note", `url=${fetchCalls[findIdx].url}`);
    assert(fetchCalls[findIdx].init.method === "POST", `method=${fetchCalls[findIdx].init.method}`);
  });
  sub("find body", () => {
    assert(fetchCalls[findIdx].init.body === '{"owner":"u1"}', `body=${fetchCalls[findIdx].init.body}`);
  });

  globalThis.fetch = origFetch;
  console.log("\nALL DATA-CLIENT TESTS PASSED");
}

await run();
