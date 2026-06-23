import { loadDopdb, createApi, configure } from './dist/index.js';
import http from 'node:http';

const assert = (c, m) => { if (!c) { console.error('FAIL:', m); process.exit(1); } };

const db = await loadDopdb();
console.log('engine version:', db.version);

// (1) named function — route name comes from fn.name -> "greet"
function Greet(input) { return { msg: 'hi ' + input.name, n: (input.n || 0) + 1 }; }
const greet = db.api(Greet);
assert(greet.apiName === 'greet', 'apiName -> ' + greet.apiName);

// (2) async handler; fn.name "DreamAnalyzer" -> "dreamanalyzer"
const dream = db.api(async function DreamAnalyzer(input) {
  await new Promise(r => setTimeout(r, 3));
  return { text: input.text, mood: 'calm' };
});

// (3) explicit name + (TS) interface-typed function
const add = db.api('add', (i) => ({ sum: Number(i.a) + Number(i.b) }));

// in-process calls (run through the wasm pipeline)
assert((await greet({ name: 'Ada', n: 41 })).n === 42, 'greet in-proc');
assert((await dream({ text: 'flying' })).mood === 'calm', 'dream in-proc');
assert((await add({ a: 2, b: 3 })).sum === 5, 'add in-proc');
console.log('in-process OK; names =', JSON.stringify(db.names().sort()));

// (4) serve every endpoint at /api/<name> using the in-wasm engine
const server = http.createServer(db.nodeListener);
await new Promise(r => server.listen(0, r));
const port = server.address().port;
console.log('serving on', port);

// (5) call via the remote createApi caller (POST {base}/api/greet)
configure({ baseUrl: `http://127.0.0.1:${port}` });
const greetRemote = createApi('greet');
const gr = await greetRemote({ name: 'Bob' });
assert(gr.msg === 'hi Bob' && gr.n === 1, 'remote greet -> ' + JSON.stringify(gr));

// (6) raw fetch, case-insensitive route, async handler
const r2 = await fetch(`http://127.0.0.1:${port}/api/DreamAnalyzer`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: 'ocean' }),
});
const j2 = await r2.json();
assert(j2.text === 'ocean' && j2.mood === 'calm', 'remote dream -> ' + JSON.stringify(j2));

// (7) non-/api/ path -> 404 from the adapter
const nf = await fetch(`http://127.0.0.1:${port}/HGET-User?f=x`);
assert(nf.status === 404, 'non-api route should 404, got ' + nf.status);

server.close();
console.log('ALL TS SDK INTEGRATION TESTS PASSED');
console.log('  remote greet ->', JSON.stringify(gr));
console.log('  remote dream ->', JSON.stringify(j2));
process.exit(0);
