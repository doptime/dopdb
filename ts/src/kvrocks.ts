// kvrocks.ts — the storage backend, the TypeScript twin of Go's kvrocks.go.
//
// LAYOUT (identical to Go, so the two engines are interchangeable on one server
// as long as they use different namespaces):
//
//   <ns>:<coll>                  HASH   Hash collection: field = document id,
//                                       value = the CBOR document
//   <ns>:<coll>:<key>            native STRING/LIST/SET/ZSET for the String/List/
//                                       Set/ZSet command families
//   <ns>:<coll>:__owner          HASH   key -> owner, row isolation for the
//                                       non-Hash types (a native list has no
//                                       document to carry an owner field)
//   <ns>:<coll>:__uniq:<field>   HASH   unique-index claims
//   <ns>:<coll>:__events         channel for watch
//
// Server-only: imported by server.ts, never by client.ts or index.ts.

import { Redis } from "ioredis";
import type { Redis as RedisClient } from "ioredis";

import { decodeDoc, encodeValue, uniqueSlot } from "./codec.js";
import { ConflictError, ForbiddenError, ValidationError } from "./errors.js";
import { matchFilter, type Doc } from "./query.js";
import type { Filter } from "./sanitize.js";

/** How many hash fields one HSCAN round trip asks for while walking a whole
 * collection. Bounds memory per round trip without making a large collection
 * chatty. */
const SCAN_PAGE = 512;

/** Connection-pool-free clients still need a sane command timeout; ioredis
 * queues by default, which is what we want for a single connection. */
export interface KvConn {
  /** redis:// or rediss:// URL. */
  uri: string;
  /** Key namespace: the KVRocks stand-in for a Mongo database name. */
  namespace: string;
  /** Optional auth token. On KVRocks the password also selects the server-side
   * namespace when namespace tokens are configured. */
  password?: string;
}

export interface ChangeEvent {
  op: "insert" | "replace" | "update" | "delete";
  id: string;
  doc?: Doc | null;
}

/** One KVRocks namespace dopdb talks to directly. */
export class KvBackend {
  readonly redis: RedisClient;
  readonly ns: string;
  /** Whether this backend owns (and must close) the client. */
  private readonly owned: boolean;
  /** collection -> unique-tagged fields, populated by ensureIndexes. */
  private readonly uniqueFields = new Map<string, string[]>();
  /** Dedicated connections for WATCH/MULTI, each with its own queue. */
  private txPool: (RedisClient | null)[] = new Array(TX_LANES).fill(null);
  private txQueues: Promise<unknown>[] = new Array(TX_LANES).fill(null).map(() => Promise.resolve());

  constructor(redis: RedisClient, namespace: string, owned = false) {
    this.redis = redis;
    this.ns = namespace;
    this.owned = owned;
  }

  static connect(conn: KvConn): KvBackend {
    const client = new Redis(conn.uri, conn.password ? { password: conn.password } : {});
    return new KvBackend(client, conn.namespace, true);
  }

  async close(): Promise<void> {
    for (let i = 0; i < this.txPool.length; i++) {
      const tx = this.txPool[i];
      this.txPool[i] = null;
      if (tx) await tx.quit().catch(() => tx.disconnect());
    }
    if (this.owned) await this.redis.quit().catch(() => this.redis.disconnect());
  }

  /** Run fn on a connection that is exclusively ours for its duration.
   *
   * WATCH is per-CONNECTION state, and ioredis multiplexes every command of the
   * whole process over ONE socket. Two concurrent read-modify-writes sharing
   * that socket would share a watch set: the first EXEC clears it, and the
   * second transaction then commits unprotected — a silently lost update. So
   * transactions need a connection to themselves.
   *
   * A single dedicated connection gave that correctness but serialised EVERY
   * scoped write and increment in the process behind one socket: one contended
   * transaction (up to 64 WATCH retries, ~0.6-1.9s) blocked writes to unrelated
   * collections behind it. So there is a small pool instead, and the lane is
   * chosen by the watched key — transactions that would contend on the server
   * anyway share a lane, unrelated ones run in parallel. Go gets the same
   * property from its connection pool. */
  private tx<T>(lane: string, fn: (r: RedisClient) => Promise<T>): Promise<T> {
    const i = laneFor(lane);
    if (!this.txPool[i]) this.txPool[i] = this.redis.duplicate();
    const conn = this.txPool[i]!;
    const run = this.txQueues[i].then(
      () => fn(conn),
      () => fn(conn), // a previous transaction's failure must not block the lane
    );
    this.txQueues[i] = run.catch(() => undefined);
    return run;
  }

  // ---- key layout ----
  private prefix(): string { return this.ns ? this.ns + ":" : ""; }
  hashKey(coll: string): string { return this.prefix() + coll; }
  memberKey(coll: string, key: string): string { return `${this.prefix()}${coll}:${key}`; }

  /** memberKey with the reserved-name check every String/List/Set/ZSet path must
   * pass.
   *
   * User entries and dopdb's own bookkeeping (the owner index, the change
   * channel, the unique-index claim hashes) share one keyspace, so an entry
   * literally named "__owner" resolves to the same Redis key as the collection's
   * isolation index. Enumeration already skipped those names; the WRITE paths did
   * not, and that is the dangerous direction: on KVRocks a SET over an existing
   * hash key does not raise WRONGTYPE the way stock Redis does — it converts the
   * key to a string, destroying the collection's owner index and breaking every
   * later scoped write. The names are refused instead. */
  entryKey(coll: string, key: string): string {
    const rk = this.memberKey(coll, key);
    if (!key || rk.endsWith(":__owner") || rk.endsWith(":__events") || rk.includes(":__uniq:")) {
      throw new ValidationError([{ field: "f", message: "reserved key name" }], `dopdb: reserved key name: ${JSON.stringify(key)}`);
    }
    return rk;
  }
  memberPattern(coll: string, glob: string): string { return `${this.prefix()}${coll}:${glob || "*"}`; }
  ownerKey(coll: string): string { return `${this.prefix()}${coll}:__owner`; }
  uniqKey(coll: string, field: string): string { return `${this.prefix()}${coll}:__uniq:${field}`; }
  eventChannel(coll: string): string { return `${this.prefix()}${coll}:__events`; }

  // ---- indexes ----

  /** Record the unique-tagged fields of a collection. KVRocks has no secondary
   * indexes, so `unique` is the only index kind with runtime meaning and dopdb
   * enforces it itself; asc/desc/text declarations are accepted and inert. */
  registerUnique(coll: string, fields: string[]): void {
    if (fields.length > 0) this.uniqueFields.set(coll, fields);
  }
  uniqueOf(coll: string): string[] { return this.uniqueFields.get(coll) ?? []; }

  /** Verify and take the unique-value slots a document needs, returning a
   * `release` to run once the document has actually been written.
   *
   * Two honest limitations (documented in docs/01-data.md): the claim and the
   * write are separate commands, so a crash between them can leave a stale claim
   * (self-healing on the next write of the same id); and an absent value is not
   * claimed, so several documents may omit a unique field. */
  async enforceUnique(coll: string, id: string, newDoc: Doc): Promise<() => Promise<void>> {
    const fields = this.uniqueOf(coll);
    if (fields.length === 0) return async () => {};

    const prev = await this.redis.hgetBuffer(this.hashKey(coll), id);
    const oldDoc = prev ? decodeDoc(prev) : null;

    for (const f of fields) {
      const slot = uniqueSlot(newDoc[f]);
      if (slot === null) continue;
      const holder = await this.redis.hget(this.uniqKey(coll, f), slot);
      if (holder !== null && holder !== id) {
        // A ConflictError, not a bare Error: it must surface as 409/"conflict",
        // which is what Go's ErrDuplicate maps to. A plain Error would fall
        // through the HTTP error mapper as a 500.
        throw new ConflictError(`dopdb: duplicate key: ${coll}.${f}`);
      }
      await this.redis.hset(this.uniqKey(coll, f), slot, id);
    }

    return async () => { await this.releaseUnique(coll, id, oldDoc, newDoc, fields); };
  }

  private async releaseUnique(coll: string, id: string, oldDoc: Doc | null, newDoc: Doc | null, fields: string[]): Promise<void> {
    if (!oldDoc) return;
    for (const f of fields) {
      const oldSlot = uniqueSlot(oldDoc[f]);
      if (oldSlot === null) continue;
      if (newDoc && uniqueSlot(newDoc[f]) === oldSlot) continue; // still held by this document
      const holder = await this.redis.hget(this.uniqKey(coll, f), oldSlot);
      if (holder === id) await this.redis.hdel(this.uniqKey(coll, f), oldSlot);
    }
  }

  /** Release every slot held by the given ids (used by delete). */
  async dropUnique(coll: string, ids: string[]): Promise<void> {
    const fields = this.uniqueOf(coll);
    if (fields.length === 0 || ids.length === 0) return;
    const raws = await this.redis.hmgetBuffer(this.hashKey(coll), ...ids);
    for (let i = 0; i < ids.length; i++) {
      const raw = raws[i];
      if (!raw) continue;
      await this.releaseUnique(coll, ids[i], decodeDoc(raw), null, fields);
    }
  }

  // ---- change events ----
  //
  // Mongo change streams are replaced by an explicit publication: every mutating
  // Hash operation publishes to <ns>:<coll>:__events. This works on any
  // Redis-protocol server without notify-keyspace-events being enabled, and it
  // carries the decoded document rather than a resume-token cursor. The
  // trade-off is stated plainly: only writes made THROUGH dopdb are seen.

  async publish(coll: string, op: ChangeEvent["op"], id: string, doc: Doc | null): Promise<void> {
    const payload = JSON.stringify({ op, id, doc: doc ?? undefined });
    await this.redis.publish(this.eventChannel(coll), payload).catch(() => {});
  }

  // ---- Hash collection ----

  async get(coll: string, id: string): Promise<Doc | null> {
    const raw = await this.redis.hgetBuffer(this.hashKey(coll), id);
    return raw ? decodeDoc(raw) : null;
  }

  async put(coll: string, id: string, doc: Doc): Promise<void> {
    const release = await this.enforceUnique(coll, id, doc);
    const existed = (await this.redis.hexists(this.hashKey(coll), id)) === 1;
    await this.redis.hset(this.hashKey(coll), id, encodeValue(doc));
    await release();
    await this.publish(coll, existed ? "replace" : "insert", id, doc);
  }

  /** The atomic scoped write: succeeds only when the stored document is absent or
   * already owned. WATCH/MULTI/EXEC closes the check-then-act window a plain
   * read-then-write would open (Mongo got the same guarantee from a filtered
   * upsert). Returns false when the row belongs to someone else. */
  async putScoped(coll: string, id: string, doc: Doc, ownerField: string, ownerVal: unknown): Promise<boolean> {
    const hk = this.hashKey(coll);
    const release = await this.enforceUnique(coll, id, doc);
    const ok = await this.tx(hk, async (r) => {
      for (let attempt = 0; attempt < WATCH_ATTEMPTS; attempt++) {
        await r.watch(hk);
        const prev = await r.hgetBuffer(hk, id);
        if (prev) {
          const cur = decodeDoc(prev)[ownerField];
          if (cur !== undefined && String(cur) !== String(ownerVal)) {
            await r.unwatch();
            return false;
          }
        }
        const res = await r.multi().hset(hk, id, encodeValue(doc)).exec();
        if (res !== null) return true;
        await backoff(attempt);
      }
      throw new Error(`dopdb: write contention on ${coll}:${id}`);
    });
    if (ok) {
      await release();
      await this.publish(coll, "replace", id, doc);
    }
    return ok;
  }

  async putIfAbsent(coll: string, id: string, doc: Doc): Promise<boolean> {
    const release = await this.enforceUnique(coll, id, doc);
    const inserted = (await this.redis.hsetnx(this.hashKey(coll), id, encodeValue(doc))) === 1;
    if (inserted) {
      await release();
      await this.publish(coll, "insert", id, doc);
    }
    return inserted;
  }

  async putMany(coll: string, entries: Record<string, Doc>): Promise<void> {
    const ids = Object.keys(entries);
    if (ids.length === 0) return;
    if (this.uniqueOf(coll).length > 0) {
      for (const id of ids) await this.put(coll, id, entries[id]);
      return;
    }
    const flat: (string | Buffer)[] = [];
    for (const id of ids) flat.push(id, encodeValue(entries[id]));
    await this.redis.hset(this.hashKey(coll), ...flat);
    for (const id of ids) await this.publish(coll, "replace", id, entries[id]);
  }

  async del(coll: string, ids: string[]): Promise<number> {
    if (ids.length === 0) return 0;
    await this.dropUnique(coll, ids);
    const n = await this.redis.hdel(this.hashKey(coll), ...ids);
    // Only announce a deletion when something was actually removed (mirrors Go).
    if (n > 0) for (const id of ids) await this.publish(coll, "delete", id, null);
    return n;
  }

  async exists(coll: string, id: string): Promise<boolean> {
    return (await this.redis.hexists(this.hashKey(coll), id)) === 1;
  }

  async count(coll: string): Promise<number> {
    return await this.redis.hlen(this.hashKey(coll));
  }

  async getMany(coll: string, ids: string[]): Promise<(Doc | null)[]> {
    if (ids.length === 0) return [];
    const raws = await this.redis.hmgetBuffer(this.hashKey(coll), ...ids);
    return raws.map((r) => (r ? decodeDoc(r) : null));
  }

  /** Stream the whole collection hash, page by page. `visit` returning false
   * stops the walk. */
  async walk(coll: string, visit: (id: string, doc: Doc) => boolean): Promise<void> {
    const hk = this.hashKey(coll);
    let cursor = "0";
    do {
      const [next, flat] = await this.redis.hscanBuffer(hk, cursor, "COUNT", SCAN_PAGE);
      cursor = next.toString();
      for (let i = 0; i + 1 < flat.length; i += 2) {
        if (!visit(flat[i].toString(), decodeDoc(flat[i + 1]))) return;
      }
    } while (cursor !== "0");
  }

  /** The find/findone/count path: scan the collection, evaluate the (already
   * sanitized) filter in-process. `_id` is injected because the id lives in the
   * hash field, not in the document. */
  async findDocs(coll: string, filter: Filter): Promise<Doc[]> {
    const out: Doc[] = [];
    await this.walk(coll, (id, doc) => {
      doc._id = id;
      if (matchFilter(doc, filter)) out.push(doc);
      return true;
    });
    // A stable base order keeps paging and sorting reproducible; HSCAN order is
    // unspecified and may differ between two servers holding the same data.
    out.sort((a, b) => (String(a._id) < String(b._id) ? -1 : String(a._id) > String(b._id) ? 1 : 0));
    return out;
  }

  /** Atomically add delta to a numeric field (dot path) of a document.
   *
   * Redis HINCRBYFLOAT increments a hash FIELD, but a field here holds a whole
   * CBOR document, so this is an optimistic read-modify-write guarded by WATCH —
   * still atomic, just not one command.
   *
   * ownerField/ownerVal make it SCOPED, and the ownership test runs inside the
   * transaction beside the write. It used to live in the dispatcher — "does a
   * document with this id and this owner exist?", then a separate unscoped
   * increment — which is a check-then-act with two failure modes, both reachable
   * by an owner racing their own delete-and-recreate: the increment could land
   * after the delete and UPSERT a document with no owner field (a permanent
   * ghost row, invisible to every scoped read and delete), or land on a third
   * party's freshly recreated document. Scoped, a missing document is now
   * forbidden rather than an upsert. */
  async incr(coll: string, id: string, fieldPath: string, delta: number, ownerField?: string, ownerVal?: unknown): Promise<void> {
    const hk = this.hashKey(coll);
    const scoped = !!ownerField;
    const written = await this.tx(hk, async (r) => {
      for (let attempt = 0; attempt < WATCH_ATTEMPTS; attempt++) {
        await r.watch(hk);
        const prev = await r.hgetBuffer(hk, id);
        if (!prev && scoped) {
          await r.unwatch();
          throw new ForbiddenError();
        }
        const doc: Doc = prev ? decodeDoc(prev) : {};
        if (scoped) {
          const cur = doc[ownerField!];
          if (cur === undefined || String(cur) !== String(ownerVal)) {
            await r.unwatch();
            throw new ForbiddenError();
          }
        }
        const parts = fieldPath.split(".");
        let cur: Doc = doc;
        for (let i = 0; i < parts.length - 1; i++) {
          const nxt = cur[parts[i]];
          if (nxt === null || typeof nxt !== "object" || Array.isArray(nxt)) cur[parts[i]] = {};
          cur = cur[parts[i]] as Doc;
        }
        const leaf = parts[parts.length - 1];
        const before = cur[leaf];
        // A field already holding a string, bool, array or object is REFUSED
        // rather than replaced by a number: Mongo's $inc refused it too, and
        // silently overwriting is data loss dressed up as success. Absent or
        // null starts from zero, which destroys nothing.
        if (before !== undefined && before !== null && typeof before !== "number") {
          await r.unwatch();
          throw new ConflictError(`dopdb: field is not numeric: ${fieldPath} holds ${Array.isArray(before) ? "array" : typeof before}`);
        }
        cur[leaf] = (typeof before === "number" ? before : 0) + delta;
        const res = await r.multi().hset(hk, id, encodeValue(doc)).exec();
        if (res !== null) return doc;
        await backoff(attempt);
      }
      throw new Error(`dopdb: incr contention on ${coll}:${id}`);
    });
    await this.publish(coll, "update", id, written);
  }

  /** Redis HRANDFIELD. With an owner scope the population has to be filtered
   * first, so it falls back to a scan. */
  async sample(coll: string, count: number, scope: Filter): Promise<string[]> {
    const n = count > 0 ? count : 1;
    if (Object.keys(scope).length === 0) {
      const ids = await this.redis.hrandfield(this.hashKey(coll), n);
      return Array.isArray(ids) ? ids.map(String) : [];
    }
    const docs = await this.findDocs(coll, scope);
    return docs.slice(0, n).map((d) => String(d._id));
  }

  /** Redis HSCAN. The cursor is the server's own opaque cursor, so — exactly as
   * with real HSCAN — a page may come back short (or empty) with a non-zero
   * cursor, and only cursor 0 means the iteration is done. */
  async scan(coll: string, match: string, cursor: number, count: number, scope: Filter): Promise<{ cursor: number; ids: string[]; docs: Doc[] }> {
    const n = count > 0 ? count : 10;
    const pattern = match || "*";
    const [next, flat] = await this.redis.hscanBuffer(this.hashKey(coll), String(cursor), "MATCH", pattern, "COUNT", n);
    const page: { id: string; doc: Doc }[] = [];
    for (let i = 0; i + 1 < flat.length; i += 2) {
      page.push({ id: flat[i].toString(), doc: decodeDoc(flat[i + 1]) });
    }
    page.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    const ids: string[] = [];
    const docs: Doc[] = [];
    const scoped = Object.keys(scope).length > 0;
    for (const e of page) {
      e.doc._id = e.id;
      if (scoped && !matchFilter(e.doc, scope)) continue;
      ids.push(e.id);
      docs.push(e.doc);
    }
    return { cursor: Number(next.toString()), ids, docs };
  }

  // ---- owner index for the non-Hash collection types ----
  //
  // A native Redis list/set/zset has no document in which to store an owner, so
  // row isolation for those types lives in a side hash. checkOwner guards reads;
  // claimOwner guards writes (first writer owns the key).

  private scopeOwner(scope: Filter): string | null {
    const keys = Object.keys(scope);
    if (keys.length === 0) return null;
    return String((scope as Doc)[keys[0]]);
  }

  /** True when key may be read/written under scope. An unclaimed key is open (it
   * does not exist yet); a key owned by someone else is not. */
  async checkOwner(coll: string, key: string, scope: Filter): Promise<boolean> {
    const want = this.scopeOwner(scope);
    if (want === null) return true;
    const got = await this.redis.hget(this.ownerKey(coll), key);
    return got === null || got === want;
  }

  /** Take ownership of key for the caller, atomically. False when already held by
   * someone else.
   *
   * It also reclaims STALE claims. A claim outlives its data in three ordinary
   * ways: Redis drops a list/set/zset key the moment its last element is
   * removed, a String key can expire on its TTL, and a crash can land between
   * the claim and the write. Without this, the first user to touch a key name
   * owns it forever — everyone else gets a permanent 403 for a key that does not
   * exist, and the owner hash grows without bound. */
  async claimOwner(coll: string, key: string, scope: Filter): Promise<boolean> {
    const want = this.scopeOwner(scope);
    if (want === null) return true;
    const claimed = await this.redis.hsetnx(this.ownerKey(coll), key, want);
    if (claimed === 1) return true;
    const got = await this.redis.hget(this.ownerKey(coll), key);
    if (got === want) return true;
    return await this.takeOverStaleClaim(coll, key, got, want);
  }

  /** Transfer a claim whose data no longer exists, under a WATCH so two racing
   * claimants cannot both win. */
  private async takeOverStaleClaim(coll: string, key: string, holder: string | null, want: string): Promise<boolean> {
    const ok = this.ownerKey(coll);
    const mk = this.memberKey(coll, key);
    if ((await this.redis.exists(mk)) > 0) return false; // the holder's data is really there
    return await this.tx(ok, async (r) => {
      for (let attempt = 0; attempt < WATCH_ATTEMPTS; attempt++) {
        await r.watch(ok, mk);
        const cur = await r.hget(ok, key);
        if (cur !== null && cur !== holder && cur !== want) { await r.unwatch(); return false; }
        if ((await r.exists(mk)) > 0) { await r.unwatch(); return false; }
        const res = await r.multi().hset(ok, key, want).exec();
        if (res !== null) return true;
        await backoff(attempt);
      }
      return false;
    });
  }

  /** Drop the ownership record when the entry key no longer exists.
   *
   * Redis deletes a list/set/zset key as soon as its last element goes, so every
   * path that can empty one has to call this — otherwise the claim outlives the
   * data and the owner hash grows forever. claimOwner can recover from a missed
   * call, but recovering is not a reason to leak. */
  async releaseIfEmpty(coll: string, key: string, scope: Filter): Promise<void> {
    if (this.scopeOwner(scope) === null) return;
    if ((await this.redis.exists(this.memberKey(coll, key))) === 0) {
      await this.releaseOwner(coll, [key]);
    }
  }

  async releaseOwner(coll: string, keys: string[]): Promise<void> {
    if (keys.length > 0) await this.redis.hdel(this.ownerKey(coll), ...keys);
  }

  /** List the entry keys of a non-Hash collection, filtered by scope. Uses SCAN
   * (never KEYS) so a large namespace stays responsive. */
  async ownedKeys(coll: string, glob: string, scope: Filter): Promise<string[]> {
    const pattern = this.memberPattern(coll, glob);
    const trim = `${this.prefix()}${coll}:`;
    const out: string[] = [];
    let cursor = "0";
    do {
      const [next, keys] = await this.redis.scan(cursor, "MATCH", pattern, "COUNT", SCAN_PAGE);
      cursor = next;
      for (const k of keys) {
        if (k.endsWith(":__owner") || k.endsWith(":__events") || k.includes(":__uniq:")) continue;
        out.push(k.slice(trim.length));
      }
    } while (cursor !== "0");
    out.sort();
    const want = this.scopeOwner(scope);
    if (want === null) return out;
    const owners = await this.redis.hgetall(this.ownerKey(coll));
    return out.filter((k) => !(k in owners) || owners[k] === want);
  }
}

// ---- optimistic transactions ------------------------------------------------
//
// WATCH is Redis's compare-and-set primitive and dopdb uses it for the two
// operations that must read-then-write a document: the scoped write and the
// in-document increment. Its granularity is the KEY, and a Hash collection is
// ONE key — so a concurrent write to any document in the collection aborts the
// transaction, not just a write to the same document. That is the price of the
// one-hash-per-collection layout (which is what makes HGET/HSET/HSCAN the real
// commands), and it is paid in retries rather than in correctness: an aborted
// transaction never writes.

const WATCH_ATTEMPTS = 64;

/** How many dedicated transaction connections a backend keeps. Small: the point
 * is to stop unrelated collections queueing behind each other, not to make every
 * write concurrent. */
const TX_LANES = 8;

/** Pick a transaction lane from the watched key, so writes that would contend on
 * the server share a lane and unrelated ones do not. */
function laneFor(key: string): number {
  let h = 5381;
  for (let i = 0; i < key.length; i++) h = ((h << 5) + h + key.charCodeAt(i)) | 0;
  return Math.abs(h) % TX_LANES;
}

function backoff(attempt: number): Promise<void> {
  const base = Math.min((attempt + 1) * 0.2, 20); // ms
  const delay = base / 2 + Math.random() * base;
  return new Promise((r) => setTimeout(r, delay));
}
