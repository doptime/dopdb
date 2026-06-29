// server.ts — the α server runtime. Binds the MongoDB driver directly (no Store
// abstraction; point 14) and uses Mongo features as Mongo features.
//
// Two surfaces, mirroring Go's Collection (trusted) + httpserve (scoped):
//   - serverDb(schema, db)  raw, trusted, typed db for handler/server code.
//   - serve(cfg)            HTTP server enforcing JWT @-binding, owner-scope,
//                           permissions; dispatches data commands + /api/<name>.
//
// One schema, imported here and on the client. Nothing is generated.

import { createHmac, timingSafeEqual, createVerify, createPublicKey } from "node:crypto";
import { createServer, type IncomingMessage, type ServerResponse, type Server } from "node:http";
import { MongoClient, type Db as MongoDb, type Collection as MongoCollection, type ChangeStream, type ChangeStreamOptions } from "mongodb";

import {
  type Collection,
  prepareWrite,
  resolveToken,
  specOf,
  CMD_BIT,
  type CollectionSpec,
} from "./schema.js";
import { sanitizeFilter, type Filter } from "./sanitize.js";
import { loadConfig, portFromAddr } from "./config.js";

// Re-export the config loader from the node entry: `import { serveFromConfig,
// loadConfig, type Config } from "@kequnyang/dopdb/server".
export {
  loadConfig,
  defaultSource,
  source,
  portFromAddr,
  type Config,
  type HttpConfig,
  type MongoSource,
} from "./config.js";
import { runEndpoint, type ApiMap, type ApiContext } from "./api.js";
import {
  DopdbError,
  ForbiddenError,
  NotFoundError,
  UnauthorizedError,
  ConflictError,
  ValidationError,
} from "./errors.js";
import type { Db, DbApi, FindOpt, WatchEvent, WatchHandler, Unsubscribe } from "./client.js";
import { Permissions } from "./permission.js";
export { Permissions } from "./permission.js";

type Doc = Record<string, unknown>;
type Claims = Record<string, unknown>;

// ----------------------------------------------------------------------------
// JWT (HS256) — no dependency
// ----------------------------------------------------------------------------

function b64url(s: string): Buffer {
  return Buffer.from(s.replace(/-/g, "+").replace(/_/g, "/"), "base64");
}

function verifyJWT(token: string, secret: string): Claims {
  const parts = token.split(".");
  if (parts.length !== 3) throw new UnauthorizedError("malformed token");
  const [h, p, sig] = parts;
  const header = JSON.parse(b64url(h).toString("utf8")) as { alg?: string };
  const signed = `${h}.${p}`;
  const signature = b64url(sig);
  switch (header.alg) {
    case "HS256": {
      const expected = createHmac("sha256", secret).update(signed).digest();
      if (expected.length !== signature.length || !timingSafeEqual(expected, signature)) {
        throw new UnauthorizedError("bad signature");
      }
      break;
    }
    case "RS256": {
      // `secret` carries the RSA public key in PEM (SPKI/PKIX) form.
      const ok = createVerify("RSA-SHA256").update(signed).verify(createPublicKey(secret), signature);
      if (!ok) throw new UnauthorizedError("bad signature");
      break;
    }
    default:
      throw new UnauthorizedError("unsupported JWT alg"); // incl. "none"
  }
  const claims = JSON.parse(b64url(p).toString("utf8")) as Claims;
  const exp = claims["exp"];
  if (typeof exp === "number" && Date.now() / 1000 > exp) {
    throw new UnauthorizedError("token expired");
  }
  return claims;
}

// ----------------------------------------------------------------------------
// Low-level command execution against a Mongo collection (shared by serverDb and
// the HTTP dispatcher). Values are already prepared; `scope` is the owner
// predicate ({} = unscoped). _id holds the collection key.
// ----------------------------------------------------------------------------

const empty = (m: Filter) => Object.keys(m).length === 0;
const isDupKey = (e: unknown) => !!e && typeof e === "object" && (e as { code?: number }).code === 11000;

// Guardrails (mirrored on the Go side).
const DEFAULT_LIMIT = 100; // find with no explicit limit is capped here
const MAX_LIMIT = 1000; // an explicit limit is clamped to this
const MAX_BODY = 1_000_000; // 1 MB request-body ceiling (413 above it)

// Combine the server-forced owner scope with the caller's (already sanitized)
// filter so the caller can NEVER widen or override the scope. Identical to Go's
// mergeScope: when both are non-empty we AND them, so a hostile filter like
// {owner:"someone-else"} intersects with {owner:me} and matches nothing.
function mergeScope(scope: Filter, filter: Filter): Filter {
  if (empty(scope)) return filter;
  if (empty(filter)) return scope;
  return { $and: [scope, filter] } as Filter;
}

// Sort/projection come straight off the query string; admit only plain
// field -> number/boolean maps. This blocks operator smuggling ($-keys) and
// illegal field paths from reaching the driver.
function checkSortProj(o: unknown, what: string): void {
  if (o == null) return;
  if (typeof o !== "object" || Array.isArray(o)) throw new ValidationError([], `dopdb: invalid ${what}`);
  for (const [k, v] of Object.entries(o as Record<string, unknown>)) {
    if (k.startsWith("$") || k.includes("$")) throw new ValidationError([], `dopdb: illegal ${what} field "${k}"`);
    if (typeof v !== "number" && typeof v !== "boolean") throw new ValidationError([], `dopdb: illegal ${what} value for "${k}"`);
  }
}

interface ExecArgs {
  key?: string;
  keys?: string[];
  value?: Doc;
  entries?: Record<string, Doc>;
  filter?: Filter;
  field?: string;
  n?: number;
  cursor?: number;
  count?: number;
  match?: string;
  members?: unknown[];
  member?: unknown;
  items?: unknown[];
  item?: unknown;
  pivot?: unknown;
  index?: number;
  start?: number;
  stop?: number;
  pairs?: Record<string, number>;
  min?: number;
  max?: number;
  withscores?: boolean;
  opt?: FindOpt;
}

// A Mongo change event → our wire-friendly WatchEvent. Returns null for op types
// we don't surface (drop, rename, invalidate, ...).
function toWatchEvent(ev: Record<string, unknown>): WatchEvent<Doc> | null {
  const op = ev.operationType as string;
  if (op !== "insert" && op !== "update" && op !== "replace" && op !== "delete") return null;
  const dk = (ev.documentKey ?? {}) as Record<string, unknown>;
  return {
    type: op,
    key: String(dk._id ?? ""),
    doc: (ev.fullDocument as Doc | undefined) ?? null,
  };
}

// Owner-scoped change-stream pipeline. Scoped streams match on fullDocument.<owner>,
// so deletes (no fullDocument) are not delivered to scoped watchers.
function ownerPipeline(scope: Filter): Doc[] {
  const keys = Object.keys(scope);
  if (keys.length === 0) return [];
  const match: Doc = {};
  for (const k of keys) match[`fullDocument.${k}`] = scope[k];
  return [{ $match: match }];
}

async function exec(m: MongoCollection<Doc>, cmd: string, a: ExecArgs, scope: Filter): Promise<unknown> {
  switch (cmd) {
    case "hget": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter);
      if (!doc) throw new NotFoundError();
      return doc;
    }
    case "hset": {
      const doc = { ...(a.value as Doc), _id: a.key };
      try {
        await m.replaceOne({ _id: a.key, ...scope } as Filter, doc as Doc, { upsert: true });
      } catch (e) {
        if (isDupKey(e) && !empty(scope)) throw new ForbiddenError();
        throw e;
      }
      return { ok: true };
    }
    case "hsetnx": {
      // hsetnx = insert-if-absent. prepareWrite already stamps the owner onto
      // the value (bind rule), so a scoped insert is owned. A dup on _id (no
      // matter who owns it) returns inserted=false — uniform, so it never
      // distinguishes "exists for me" from "exists for another tenant".
      const doc = { ...(a.value as Doc), _id: a.key };
      try {
        await m.insertOne(doc as Doc);
        return { inserted: true };
      } catch (e) {
        if (isDupKey(e)) return { inserted: false };
        throw e;
      }
    }
    case "hdel":
    case "del": {
      await m.deleteMany({ _id: { $in: a.keys }, ...scope } as Filter);
      return { ok: true };
    }
    case "hexists": {
      const n = await m.countDocuments({ _id: a.key, ...scope } as Filter);
      return { exists: n > 0 };
    }
    case "hgetall": {
      const out: Record<string, Doc> = {};
      for await (const d of m.find(scope)) out[String((d as Doc)._id)] = d as Doc;
      return out;
    }
    case "hkeys": {
      const ids: string[] = [];
      for await (const d of m.find(scope).project({ _id: 1 })) ids.push(String((d as Doc)._id));
      return ids;
    }
    case "hvals": {
      return await m.find(scope).toArray();
    }
    case "hlen": {
      return { len: await m.countDocuments(scope) };
    }
    case "hrandfield": {
      // Redis HRANDFIELD — random field keys via $sample. scope restricts the
      // population (owner-scope). Mirrors Go's backend.sample.
      const size = a.count && a.count > 0 ? a.count : 1;
      const pipe: Doc[] = [];
      if (!empty(scope)) pipe.push({ $match: scope });
      pipe.push({ $sample: { size } }, { $project: { _id: 1 } });
      const ids: string[] = [];
      for await (const d of m.aggregate(pipe)) ids.push(String((d as Doc)._id));
      return ids;
    }
    case "hscan":
    case "hscannovalues": {
      // Redis HSCAN over Mongo: glob match -> regex on _id, paginated by a
      // numeric cursor (offset), sorted by _id. nextCursor = cursor+len when
      // the page is full, else 0 (done). Mirrors Go's backend.scan.
      const count = a.count && a.count > 0 ? a.count : 10;
      const cursor = a.cursor ?? 0;
      const f: Filter = { ...scope };
      if (a.match && a.match !== "*") (f as Doc)._id = { $regex: globToRegex(a.match) };
      let q = m.find(f);
      if (cmd === "hscannovalues") q = q.project({ _id: 1 });
      const docs = await q.sort({ _id: 1 }).skip(cursor).limit(count).toArray();
      const keys = docs.map((d) => String((d as Doc)._id));
      const next = docs.length === count ? cursor + docs.length : 0;
      if (cmd === "hscannovalues") return { cursor: next, keys };
      return { cursor: next, keys, values: docs };
    }
    case "lpush":
    case "rpush": {
      const each = a.items ?? [];
      const upd = cmd === "lpush" ? { $push: { items: { $each: each, $position: 0 } } } : { $push: { items: { $each: each } } };
      await m.updateOne({ _id: a.key, ...scope } as Filter, upd as never, { upsert: true });
      return { ok: true };
    }
    case "lpop":
    case "rpop": {
      const before = await m.findOneAndUpdate({ _id: a.key, ...scope } as Filter, { $pop: { items: cmd === "lpop" ? -1 : 1 } } as never) as Doc | null;
      if (!before) throw new NotFoundError();
      const its = (before.items as unknown[]) ?? [];
      if (its.length === 0) return null;
      return cmd === "lpop" ? its[0] : its[its.length - 1];
    }
    case "lrange": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter) as Doc | null;
      const its = (doc?.items as unknown[]) ?? [];
      const n = its.length;
      let st = listIdx(n, a.start ?? 0); if (st < 0) st = 0;
      let en = listIdx(n, a.stop ?? -1) + 1; if (en > n) en = n; if (en < st) en = st;
      return st >= n ? [] : its.slice(st, en);
    }
    case "llen": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter) as Doc | null;
      return { len: ((doc?.items as unknown[]) ?? []).length };
    }
    case "lindex": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter) as Doc | null;
      const its = (doc?.items as unknown[]) ?? [];
      const i = listIdx(its.length, a.index ?? 0);
      return i >= 0 && i < its.length ? its[i] : null;
    }
    case "lset": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter) as Doc | null;
      const its = (doc?.items as unknown[]) ?? [];
      const i = listIdx(its.length, a.index ?? 0);
      if (i < 0 || i >= its.length) throw new NotFoundError();
      await m.updateOne({ _id: a.key, ...scope } as Filter, { $set: { [`items.${i}`]: a.item } } as never);
      return { ok: true };
    }
    case "lrem": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter) as Doc | null;
      const its = (doc?.items as unknown[]) ?? [];
      const cnt = a.count ?? 0;
      const kept: unknown[] = [];
      let rm = 0;
      if (cnt < 0) {
        for (let i = its.length - 1; i >= 0; i--) {
          if (its[i] === a.item && (cnt === 0 || rm < -cnt)) rm++;
          else kept.unshift(its[i]);
        }
      } else {
        for (const v of its) {
          if (v === a.item && (cnt === 0 || rm < cnt)) rm++;
          else kept.push(v);
        }
      }
      await m.updateOne({ _id: a.key, ...scope } as Filter, { $set: { items: kept } } as never);
      return { ok: true };
    }
    case "ltrim": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter) as Doc | null;
      const its = (doc?.items as unknown[]) ?? [];
      const n = its.length;
      let st = listIdx(n, a.start ?? 0); if (st < 0) st = 0;
      let en = listIdx(n, a.stop ?? -1) + 1; if (en > n) en = n; if (en < st) en = st;
      await m.updateOne({ _id: a.key, ...scope } as Filter, { $set: { items: st < n ? its.slice(st, en) : [] } } as never);
      return { ok: true };
    }
    case "linsertbefore":
    case "linsertafter": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter) as Doc | null;
      const its = (doc?.items as unknown[]) ?? [];
      const out: unknown[] = [];
      let ins = false;
      const before = cmd === "linsertbefore";
      for (const v of its) {
        if (v === a.pivot && !ins) {
          if (before) out.push(a.item, v);
          else out.push(v, a.item);
          ins = true;
          continue;
        }
        out.push(v);
      }
      if (ins) await m.updateOne({ _id: a.key, ...scope } as Filter, { $set: { items: out } } as never);
      return { ok: true };
    }
    case "sadd": {
      await m.updateOne({ _id: a.key, ...scope } as Filter, { $addToSet: { members: { $each: a.members ?? [] } } } as never, { upsert: true });
      return { ok: true };
    }
    case "srem": {
      await m.updateOne({ _id: a.key, ...scope } as Filter, { $pull: { members: { $in: a.members ?? [] } } } as never);
      return { ok: true };
    }
    case "smembers": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter);
      return (doc as Doc | null)?.members ?? [];
    }
    case "sismember": {
      const n = await m.countDocuments({ _id: a.key, ...scope, members: a.member } as Filter, { limit: 1 } as never);
      return { member: n > 0 };
    }
    case "scard": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter);
      return { card: ((doc as Doc | null)?.members as unknown[] | undefined)?.length ?? 0 };
    }
    case "strget": {
      const doc = await m.findOne({ _id: a.key, ...scope } as Filter);
      if (!doc) throw new NotFoundError();
      return (doc as Doc).v;
    }
    case "strset": {
      const setDoc: Doc = { v: a.value };
      if (a.n && a.n > 0) setDoc.expireAt = new Date(Date.now() + a.n * 1000);
      try {
        await m.updateOne({ _id: a.key, ...scope } as Filter, { $set: setDoc } as never, { upsert: true });
      } catch (e) {
        if (isDupKey(e) && !empty(scope)) throw new ForbiddenError();
        throw e;
      }
      return { ok: true };
    }
    case "strdel": {
      await m.deleteMany({ _id: { $in: a.keys ?? [] }, ...scope } as Filter);
      return { ok: true };
    }
    case "strgetall": {
      const f: Filter = { ...scope };
      if (a.match && a.match !== "*") (f as Doc)._id = { $regex: globToRegex(a.match) };
      const out: Record<string, unknown> = {};
      for await (const d of m.find(f)) out[String((d as Doc)._id)] = (d as Doc).v;
      return out;
    }
    case "strsetall": {
      const entries = Object.entries(a.entries ?? {});
      if (entries.length === 0) return { ok: true };
      const ops = entries.map(([k, v]) => ({
        updateOne: { filter: { _id: k, ...scope } as Filter, update: { $set: { v } } as never, upsert: true },
      }));
      try {
        await m.bulkWrite(ops as never, { ordered: false });
      } catch (e) {
        if (isDupKey(e) && !empty(scope)) throw new ForbiddenError();
        throw e;
      }
      return { ok: true };
    }
    case "count": {
      const safe = sanitizeFilter(a.filter);
      return { count: await m.countDocuments(mergeScope(scope, safe)) };
    }
    case "hincrby":
    case "hincrbyfloat": {
      const r = await m.updateOne(
        { _id: a.key, ...scope } as Filter,
        { $inc: { [a.field as string]: a.n } } as Doc,
        { upsert: empty(scope) },
      );
      if (!empty(scope) && r.matchedCount === 0 && r.upsertedCount === 0) throw new ForbiddenError();
      return { ok: true };
    }
    case "hmset": {
      const entries = Object.entries(a.entries ?? {});
      if (entries.length === 0) return { ok: true };
      const ops = entries.map(([k, doc]) => ({
        replaceOne: { filter: { _id: k, ...scope } as Filter, replacement: { ...doc, _id: k } as Doc, upsert: true },
      }));
      try {
        await m.bulkWrite(ops as never, { ordered: false });
      } catch (e) {
        if (isDupKey(e) && !empty(scope)) throw new ForbiddenError();
        throw e;
      }
      return { ok: true };
    }
    case "hmget": {
      const keys = a.keys ?? [];
      if (keys.length === 0) return [];
      const docs = await m.find({ _id: { $in: keys }, ...scope } as Filter).toArray();
      const byId = new Map(docs.map((d) => [String((d as Doc)._id), d as Doc]));
      return keys.map((k) => byId.get(k) ?? null);
    }
    case "find": {
      const safe = sanitizeFilter(a.filter);
      let cur = m.find(mergeScope(scope, safe));
      if (a.opt?.sort) cur = cur.sort(a.opt.sort);
      if (a.opt?.projection) cur = cur.project(a.opt.projection);
      if (a.opt?.skip != null) cur = cur.skip(a.opt.skip);
      const lim = a.opt?.limit;
      cur = cur.limit(lim != null ? Math.min(Math.max(lim, 0), MAX_LIMIT) : DEFAULT_LIMIT);
      return await cur.toArray();
    }
    case "findone": {
      const safe = sanitizeFilter(a.filter);
      const doc = await m.findOne(mergeScope(scope, safe));
      if (!doc) throw new NotFoundError();
      return doc;
    }
    // ---- ZSet (Z*) family ----
    case "zadd": {
      const ms = await zLoad(m, a.key, scope);
      const idx = new Map(ms.map((x, i) => [x.m, i]));
      let added = 0;
      for (const [k, v] of Object.entries(a.pairs ?? {})) {
        const i = idx.get(k);
        if (i !== undefined) ms[i].score = v;
        else { ms.push({ m: k, score: v }); idx.set(k, ms.length - 1); added++; }
      }
      zSort(ms, false);
      await zSave(m, a.key, scope, ms);
      return added;
    }
    case "zrem": {
      const ms = await zLoad(m, a.key, scope);
      const gone = new Set((a.members as string[] | undefined) ?? []);
      let removed = 0;
      const kept = ms.filter((x) => { if (gone.has(x.m)) { removed++; return false; } return true; });
      await zSave(m, a.key, scope, kept);
      return removed;
    }
    case "zscore": {
      const ms = await zLoad(m, a.key, scope);
      const hit = ms.find((x) => x.m === (a.member as string | undefined));
      if (!hit) throw new NotFoundError();
      return hit.score;
    }
    case "zcard": {
      const ms = await zLoad(m, a.key, scope);
      return { card: ms.length };
    }
    case "zcount": {
      const ms = await zLoad(m, a.key, scope);
      const min = a.min ?? -Infinity, max = a.max ?? Infinity;
      return { count: ms.filter((x) => x.score >= min && x.score <= max).length };
    }
    case "zincrby": {
      const ms = await zLoad(m, a.key, scope);
      const member = a.member as string;
      const inc = a.n ?? 0;
      const hit = ms.find((x) => x.m === member);
      let ns: number;
      if (hit) { hit.score += inc; ns = hit.score; }
      else { ms.push({ m: member, score: inc }); ns = inc; }
      zSort(ms, false);
      await zSave(m, a.key, scope, ms);
      return ns;
    }
    case "zrange":
    case "zrevrange": {
      const ms = await zLoad(m, a.key, scope);
      zSort(ms, cmd === "zrevrange");
      const n = ms.length;
      let st = listIdx(n, a.start ?? 0), en = listIdx(n, a.stop ?? -1) + 1;
      if (st < 0) st = 0; if (en > n) en = n; if (en < st) en = st;
      return zRender(st >= n ? [] : ms.slice(st, en), !!a.withscores);
    }
    case "zrangebyscore":
    case "zrevrangebyscore": {
      const ms = await zLoad(m, a.key, scope);
      const min = a.min ?? -Infinity, max = a.max ?? Infinity;
      const sub = ms.filter((x) => x.score >= min && x.score <= max);
      zSort(sub, cmd === "zrevrangebyscore");
      return zRender(sub, !!a.withscores);
    }
    case "zrank":
    case "zrevrank": {
      const ms = await zLoad(m, a.key, scope);
      zSort(ms, cmd === "zrevrank");
      return { rank: ms.findIndex((x) => x.m === (a.member as string | undefined)) };
    }
    case "zpopmin":
    case "zpopmax": {
      const ms = await zLoad(m, a.key, scope);
      zSort(ms, cmd === "zpopmax");
      let count = a.count ?? 1;
      if (count <= 0) count = 1;
      if (count > ms.length) count = ms.length;
      const popped = ms.slice(0, count);
      await zSave(m, a.key, scope, ms.slice(count));
      return zRender(popped, true);
    }
    case "zremrangebyrank": {
      const ms = await zLoad(m, a.key, scope);
      zSort(ms, false);
      const n = ms.length;
      let st = listIdx(n, a.start ?? 0), en = listIdx(n, a.stop ?? -1) + 1;
      if (st < 0) st = 0; if (en > n) en = n; if (en < st) en = st;
      let removed = 0;
      const kept = ms.filter((_x, i) => { if (st < n && i >= st && i < en) { removed++; return false; } return true; });
      await zSave(m, a.key, scope, kept);
      return removed;
    }
    case "zremrangebyscore": {
      const ms = await zLoad(m, a.key, scope);
      const min = a.min ?? -Infinity, max = a.max ?? Infinity;
      let removed = 0;
      const kept = ms.filter((x) => { if (x.score >= min && x.score <= max) { removed++; return false; } return true; });
      await zSave(m, a.key, scope, kept);
      return removed;
    }
    default:
      throw new NotFoundError(`unknown command: ${cmd}`);
  }
}

// globToRegex converts a Redis glob (used by HSCAN match) to an anchored regex,
// mirroring Go's globToRegex.
function listIdx(n: number, i: number): number { return i < 0 ? n + i : i; }
type ZM = { m: string; score: number };
async function zLoad(m: MongoCollection<Doc>, key: string | undefined, scope: Filter): Promise<ZM[]> {
  const doc = await m.findOne({ _id: key, ...scope } as Filter) as Doc | null;
  return ((doc?.members as ZM[]) ?? []);
}
async function zSave(m: MongoCollection<Doc>, key: string | undefined, scope: Filter, ms: ZM[]): Promise<void> {
  await m.updateOne({ _id: key, ...scope } as Filter, { $set: { members: ms } } as never, { upsert: true });
}
function zSort(ms: ZM[], rev: boolean): void {
  ms.sort((x, y) => x.score !== y.score ? (rev ? y.score - x.score : x.score - y.score) : (rev ? (y.m > x.m ? 1 : -1) : (x.m < y.m ? -1 : 1)));
}
function zRender(ms: ZM[], withScores: boolean): unknown {
  return withScores ? ms.map((x) => ({ m: x.m, score: x.score })) : ms.map((x) => x.m);
}

function globToRegex(glob: string): string {
  let r = "^";
  for (const ch of glob) {
    if (ch === "*") r += ".*";
    else if (ch === "?") r += ".";
    else if (".+()|[]{}^$\\".includes(ch)) r += "\\" + ch;
    else r += ch;
  }
  return r + "$";
}

// ----------------------------------------------------------------------------
// serverDb — raw, trusted, typed db for handler/server code (no scope, no JWT).
// ----------------------------------------------------------------------------

function makeServerApi<C extends Collection<any>>(coll: C, m: MongoCollection<Doc>): DbApi<C> {
  const w = (v: Record<string, unknown>) => prepareWrite(coll, v, { trusted: true });
  const hget = async (key: string) => {
    try {
      return (await exec(m, "hget", { key }, {})) as any;
    } catch (e) {
      if (e instanceof NotFoundError) return null;
      throw e;
    }
  };
  const hset = async (key: string, value: any) => {
    await exec(m, "hset", { key, value: w(value) }, {});
  };
  return {
    hget,
    hset,
    get: hget,
    set: hset,
    hsetnx: async (key, value) => ((await exec(m, "hsetnx", { key, value: w(value as Doc) }, {})) as any).inserted,
    hdel: async (...keys) => void (await exec(m, "hdel", { keys }, {})),
    del: async (...keys) => void (await exec(m, "del", { keys }, {})),
    save: async (value) => {
      const v = w(value as Record<string, unknown>);
      await exec(m, "hset", { key: String((v as Doc)._id), value: v }, {});
    },
    hexists: async (key) => ((await exec(m, "hexists", { key }, {})) as any).exists,
    hgetall: async () => (await exec(m, "hgetall", {}, {})) as any,
    hkeys: async () => (await exec(m, "hkeys", {}, {})) as any,
    hvals: async () => (await exec(m, "hvals", {}, {})) as any,
    hlen: async () => ((await exec(m, "hlen", {}, {})) as any).len,
    hincrby: async (key, field, n) => void (await exec(m, "hincrby", { key, field, n }, {})),
    hincrbyfloat: async (key, field, n) => void (await exec(m, "hincrbyfloat", { key, field, n }, {})),
    hmset: async (entries) => {
      const prepared: Doc = {};
      for (const [k, v] of Object.entries(entries)) prepared[k] = w(v as Record<string, unknown>) as Doc;
      await exec(m, "hmset", { entries: prepared as Record<string, Doc> }, {});
    },
    hmget: async (keys) => (await exec(m, "hmget", { keys }, {})) as any,
    hrandfield: async (count) => (await exec(m, "hrandfield", count ? { count } : {}, {})) as string[],
    hscan: async (cursor = 0, match, count) => (await exec(m, "hscan", { cursor, match, count }, {})) as any,
    hscannovalues: async (cursor = 0, match, count) => (await exec(m, "hscannovalues", { cursor, match, count }, {})) as any,
    count: async (filter = {}) => ((await exec(m, "count", { filter }, {})) as any).count ?? 0,
    find: async (filter = {}, opt = {}) => (await exec(m, "find", { filter, opt }, {})) as any,
    findone: async (filter = {}) => {
      try {
        return (await exec(m, "findone", { filter }, {})) as any;
      } catch (e) {
        if (e instanceof NotFoundError) return null;
        throw e;
      }
    },
    watch: async (onEvent: WatchHandler<any>): Promise<Unsubscribe> => {
      const stream = m.watch(ownerPipeline({}), { fullDocument: "updateLookup" });
      void (async () => {
        try {
          for await (const ev of stream) {
            const w = toWatchEvent(ev as unknown as Record<string, unknown>);
            if (w) onEvent(w);
          }
        } catch {
          /* stream closed */
        }
      })();
      return () => {
        void (stream as { close?: () => Promise<void> }).close?.();
      };
    },
  };
}

/** Build a raw, trusted, typed db bound directly to a Mongo database. Use inside
 * API handlers and server code. No owner-scope and no JWT — full access. */
export function serverDb<M extends Record<string, Collection<any>>>(schema: M, db: MongoDb): Db<M> {
  const out = {} as Db<M>;
  for (const name of Object.keys(schema) as (keyof M & string)[]) {
    (out as any)[name] = makeServerApi(schema[name], db.collection<Doc>(schema[name].opts.name ?? name));
  }
  return out;
}

/** Ensure every index declared in the schema exists (idempotent). */
export async function ensureIndexes(schema: Record<string, Collection<any>>, db: MongoDb): Promise<void> {
  for (const name of Object.keys(schema)) {
    const spec = specOf(name, schema[name]);
    const m = db.collection<Doc>(spec.name);
    for (const idx of spec.indexes) {
      const dir: 1 | -1 | "text" = idx.kind === "text" ? "text" : idx.kind === "desc" ? -1 : 1;
      const key: Record<string, 1 | -1 | "text"> = { [idx.field]: dir };
      const opts: { name: string; unique?: boolean; expireAfterSeconds?: number } = { name: idx.name };
      if (idx.expireAfterSeconds != null) opts.expireAfterSeconds = idx.expireAfterSeconds;
      else opts.unique = idx.unique;
      await m.createIndex(key, opts);
    }
  }
}

// ----------------------------------------------------------------------------
// HTTP serve — one I/O-agnostic core (resolve) behind three adapters:
//   - serve(cfg)              standalone Node http.Server (and a Pages-router
//                             listener).
//   - createNextHandler(cfg)  Next.js App-Router Route Handler (Web Request ->
//                             Response), drop-in at app/<base>/[...slug]/route.ts.
//   - serverDb(schema, db)    (above) raw trusted db for handler code.
//
// JWT @-binding, owner-scope, permissions and the closed command vocabulary are
// enforced once, in resolve(). The route prefix (default "/api") is configurable
// (cfg.basePath); under Next.js the catch-all segments are used directly, so the
// prefix is simply the folder you mount the handler in.
// ----------------------------------------------------------------------------

const DATA_COMMANDS = new Set([
  "hget", "hset", "hsetnx", "hdel", "del", "hexists", "hgetall",
  "hkeys", "hvals", "hlen", "hincrby", "hincrbyfloat", "hmset", "hmget", "count", "find", "findone",
  "hrandfield", "hscan", "hscannovalues",
  "strget", "strset", "strsetall", "strgetall", "strdel",
  "sadd", "srem", "smembers", "sismember", "scard",
  "lpush", "rpush", "lpop", "rpop", "lrange", "llen", "lindex", "lset", "lrem", "ltrim", "linsertbefore", "linsertafter",
  "zadd", "zrem", "zscore", "zcard", "zcount", "zincrby", "zrange", "zrevrange", "zrangebyscore", "zrevrangebyscore",
  "zrank", "zrevrank", "zpopmin", "zpopmax", "zremrangebyrank", "zremrangebyscore",
]);
const STREAM_COMMANDS = new Set(["watch"]);
const ROUTED_COMMANDS = new Set([...DATA_COMMANDS, ...STREAM_COMMANDS]);

type MongoConn = MongoDb | { uri: string; db: string };

export interface ServeConfig {
  /** The single schema map. */
  schema: Record<string, Collection<any>>;
  /** A single datasource (registered as "default"); or omit and use datasources. */
  mongo?: MongoConn;
  /** Several named datasources; a request selects one with ?ds=<name>. */
  datasources?: { name: string; mongo: MongoConn }[];
  /** Secret for verifying bearer tokens: an HS256 key, or an RS256 public key
   * (PEM/SPKI). Omit only in trusted dev. */
  jwtSecret?: string;
  /** Permission gate (Grant/Deny/Allowed), equivalent to the Go server. Default
   * is DENY-ALL — grant entries explicitly. */
  permissions?: Permissions;
  /** Optional override gate; if set it takes precedence over `permissions`. */
  permit?: (cmd: string, coll: string, claims: Claims) => boolean;
  /** Registered API endpoints (also auto-discovered from the registry). */
  api?: ApiMap;
  /** Route prefix for the standalone/Pages path parser (default "/api"). Under
   * App Router the catch-all segments are used directly, so this is ignored. */
  basePath?: string;
  /** If set, serve() starts an http server on this port. */
  port?: number;
  /** Allowed CORS origins (sets Access-Control-Allow-* and answers preflight).
   * Use ["*"] to allow any origin. */
  cors?: string[];
  dev?: boolean;
}

export interface DopdbServer {
  /** Raw trusted db (same one your handlers use). */
  db: Db<any>;
  /** Node request listener: `createServer(server.listener)` (also usable from a
   * Pages-router API route). */
  listener: (req: IncomingMessage, res: ServerResponse) => void;
  /** The http.Server, if `port` was given. */
  http?: Server;
  /** The resolved collection specs (schema-as-data). */
  specs: CollectionSpec[];
  close(): Promise<void>;
}

type RouteResult =
  | { kind: "data"; cmd: string; coll: string }
  | { kind: "api"; name: string };

// Engine-neutral request, built by each adapter from its native request type.
interface ReqInput {
  method: string;
  url: URL;
  header: (name: string) => string | undefined;
  remoteAddr: string;
  bodyText: string;
  /** Pre-split path segments after the mount point (Next.js catch-all); when
   * present the basePath parser is bypassed (prefix-agnostic). */
  segments?: string[];
}

// resolve()'s result: either a finished JSON response, or a watch directive the
// adapter turns into an SSE stream in its own transport.
type Outcome =
  | { kind: "json"; status: number; body: unknown }
  | { kind: "watch"; m: MongoCollection<Doc>; scope: Filter; resumeAfter: unknown };

interface Runtime {
  db: Db<any>;
  specs: CollectionSpec[];
  cors?: string[];
  resolve(input: ReqInput): Promise<Outcome>;
  close(): Promise<void>;
}

function ownerScope(coll: Collection<any>, claims: Claims): Filter {
  const ownerField = coll.opts.ownerField;
  if (!ownerField) return {};
  const bound = coll.shape[ownerField]?.rules.bind;
  const claimName = bound ?? ownerField;
  const v = claims[claimName];
  if (v === undefined || v === null) throw new UnauthorizedError(`authentication required (claim "${claimName}")`);
  return { [ownerField]: v };
}

function sendJSON(res: ServerResponse, status: number, body: unknown): void {
  const s = JSON.stringify(body ?? null);
  res.statusCode = status;
  res.setHeader("Content-Type", "application/json");
  res.end(s);
}

function sendError(res: ServerResponse, e: unknown): void {
  if (e instanceof DopdbError) {
    sendJSON(res, e.status, { error: e.message, code: e.code });
  } else {
    sendJSON(res, 500, { error: (e as Error)?.message ?? "internal error", code: "error" });
  }
}

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    let data = "";
    let size = 0;
    req.on("data", (c) => {
      size += (c as Buffer).length;
      if (size > MAX_BODY) {
        reject(new DopdbError("request body too large", 413, "too_large"));
        req.destroy();
        return;
      }
      data += c;
    });
    req.on("end", () => resolve(data));
    req.on("error", reject);
  });
}

// ---- route parsing -----------------------------------------------------------

function routeSegments(segs: string[]): RouteResult | null {
  const s = segs.filter(Boolean).map((x) => {
    try { return decodeURIComponent(x); } catch { return x; }
  });
  if (s.length === 0) return null;
  // Any 2+ segment path is a data-command route (/api/<cmd>/<coll>).
  // Unknown commands are caught at line 667 → 400 validation error.
  if (s.length >= 2) {
    return { kind: "data", cmd: s[0].toLowerCase(), coll: s[1] };
  }
  return { kind: "api", name: s.join("/") };
}

function routePath(pathname: string, basePath: string): RouteResult | null {
  const marker = basePath.endsWith("/") ? basePath : basePath + "/";
  const i = pathname.indexOf(marker);
  if (i < 0) return null;
  const rest = pathname.slice(i + marker.length).replace(/\/+$/, "");
  if (rest === "") return null;
  return routeSegments(rest.split("/"));
}

// ---- shared SSE pump ---------------------------------------------------------

interface SSESink {
  write(s: string): void;
  close(): void;
  onAbort(cb: () => void): void;
}

// streamWatch opens an owner-scoped change stream and pumps SSE lines into a
// transport-neutral sink. Shared by the Node and Web adapters. Owner-scoped
// streams match on fullDocument.<owner>, so deletes are not delivered to scoped
// watchers. Requires MongoDB to run as a replica set.
async function streamWatch(m: MongoCollection<Doc>, scope: Filter, resumeAfter: unknown, sink: SSESink): Promise<void> {
  let stream: ChangeStream<Doc>;
  try {
    const opts = { fullDocument: "updateLookup" } as ChangeStreamOptions;
    if (resumeAfter) (opts as { resumeAfter?: unknown }).resumeAfter = resumeAfter;
    stream = m.watch(ownerPipeline(scope), opts);
  } catch {
    try { sink.write(`event: error\ndata: {"code":"watch_error"}\n\n`); } catch { /* gone */ }
    sink.close();
    return;
  }
  sink.write(": connected\n\n");
  const ping = setInterval(() => sink.write(": ping\n\n"), 25000);
  let finished = false;
  const finish = () => {
    if (finished) return;
    finished = true;
    clearInterval(ping);
    void stream.close().catch(() => {});
    sink.close();
  };
  sink.onAbort(finish);
  try {
    for await (const ev of stream) {
      const rec = ev as unknown as Record<string, unknown>;
      const w = toWatchEvent(rec);
      if (!w) continue;
      const token = rec._id;
      if (token !== undefined) sink.write(`id: ${Buffer.from(JSON.stringify(token)).toString("base64")}\n`);
      sink.write(`data: ${JSON.stringify(w)}\n\n`);
    }
  } catch {
    // resume token likely invalid/expired → tell the client to reconnect fresh
    try { sink.write(`event: error\ndata: {"code":"watch_error"}\n\n`); } catch { /* gone */ }
  }
  finish();
}

// ---- the runtime (shared by every adapter) ----------------------------------

async function buildRuntime(cfg: ServeConfig): Promise<Runtime> {
  // Validate schema config BEFORE any side effects (Mongo connect). Fail-closed:
  // a row-scoped collection MUST bind its owner field to an identity claim, or
  // prepareWrite never sets the owner and the {owner:@uid} predicate matches
  // nothing — scoping would silently break. Reject at startup; needs no Mongo.
  for (const key of Object.keys(cfg.schema)) {
    const c = cfg.schema[key];
    const of = c.opts.ownerField;
    if (of && !c.shape[of]?.rules.bind) {
      throw new Error(
        `dopdb serve: collection "${c.opts.name ?? key}" declares ownerField "${of}" but that field is not bound ` +
        `to a claim (declare it as f.string().bind("uid")). Owner scope would silently fail otherwise.`,
      );
    }
  }

  // Datasource registry: name -> Db. A single `mongo` registers as "default";
  // `datasources` registers several. ?ds=<name> selects one per request.
  const dbs = new Map<string, MongoDb>();
  const ownClients: MongoClient[] = [];
  const open = async (conn: MongoConn): Promise<MongoDb> => {
    if ("uri" in conn) {
      const client = new MongoClient(conn.uri);
      await client.connect();
      ownClients.push(client);
      return client.db(conn.db);
    }
    return conn;
  };
  if (cfg.datasources) for (const d of cfg.datasources) dbs.set(d.name, await open(d.mongo));
  if (cfg.mongo) dbs.set("default", await open(cfg.mongo));
  if (!dbs.has("default")) {
    const first = dbs.keys().next().value as string | undefined;
    if (!first) throw new Error("dopdb serve: no datasources configured");
    dbs.set("default", dbs.get(first)!);
  }
  const defaultDb = dbs.get("default")!;

  for (const d of dbs.values()) await ensureIndexes(cfg.schema, d);
  const rawDb = serverDb(cfg.schema, defaultDb);
  const specs = Object.keys(cfg.schema).map((n) => specOf(n, cfg.schema[n]));
  const basePath = cfg.basePath ?? "/api";

  // Permission gate: explicit `permit` wins; else the Permissions map; else
  // DENY-ALL (the secure default — grants must be explicit).
  const gate: (cmd: string, coll: string, claims: Claims) => boolean =
    cfg.permit ?? (cfg.permissions ? (cmd, coll) => cfg.permissions!.allowed(cmd, coll) : () => false);

  // Resolve collections by their public name (.named() value, or the map key).
  const byName = new Map<string, { coll: Collection<any>; storage: string }>();
  for (const key of Object.keys(cfg.schema)) {
    const coll = cfg.schema[key];
    const storage = coll.opts.name ?? key;
    byName.set(storage, { coll, storage });
  }

  // httpOn bitmask check (mirrors Go dopdb.HttpAllowed): a collection's
  // .httpOn(...) flags are the primary grant for data commands; the `gate`
  // (permit / permissions / deny-all) still applies as a fallback / override.
  const httpAllows = (cmd: string, coll: string): boolean => {
    const perm = byName.get(coll)?.coll.opts.httpPerm ?? 0n;
    const bit = CMD_BIT[cmd.toUpperCase()] ?? 0n;
    return bit !== 0n && (perm & bit) !== 0n;
  };

  async function resolve(input: ReqInput): Promise<Outcome> {
    const r = input.segments ? routeSegments(input.segments) : routePath(input.url.pathname, basePath);
    if (!r) return { kind: "json", status: 404, body: { error: "not a dopdb route", code: "not_found" } };

    // Verify JWT (if present).
    let claims: Claims = {};
    const auth = input.header("authorization")?.replace(/^Bearer\s+/i, "");
    if (auth) {
      if (!cfg.jwtSecret) throw new UnauthorizedError("server has no JWT secret configured");
      claims = verifyJWT(auth, cfg.jwtSecret);
    }

    const method = input.method.toUpperCase();
    const bodyText = input.bodyText;

    if (r.kind === "api") {
      if (!gate("API", r.name, claims)) throw new ForbiddenError(`not permitted: API::${r.name}`);
      const params: Record<string, unknown> = {};
      input.url.searchParams.forEach((v, k) => (params[k] = v));
      if (bodyText) {
        try { Object.assign(params, JSON.parse(bodyText)); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      for (const k of Object.keys(params)) if (k.startsWith("@")) delete params[k];
      const ctx: ApiContext = { claims };
      const out = await runEndpoint(r.name, params, ctx);
      return { kind: "json", status: 200, body: out ?? null };
    }

    if (!ROUTED_COMMANDS.has(r.cmd)) return { kind: "json", status: 400, body: { error: `unknown command: ${r.cmd}`, code: "validation" } };
    const entry = byName.get(r.coll);
    if (!entry) return { kind: "json", status: 404, body: { error: `collection not registered: ${r.coll}`, code: "not_found" } };
    const coll = entry.coll;
    if (!httpAllows(r.cmd, r.coll) && !gate(r.cmd, r.coll, claims)) throw new ForbiddenError(`not permitted: ${r.cmd}::${r.coll}`);

    const scope = ownerScope(coll, claims);
    const ds = input.url.searchParams.get("ds") || "default";
    const reqDb = dbs.get(ds) ?? defaultDb;
    const m = reqDb.collection<Doc>(entry.storage);

    if (r.cmd === "watch") {
      const rawResume = input.header("last-event-id");
      let resumeAfter: unknown;
      if (rawResume) {
        try { resumeAfter = JSON.parse(Buffer.from(rawResume, "base64").toString("utf8")); } catch { resumeAfter = undefined; }
      }
      return { kind: "watch", m, scope, resumeAfter };
    }

    // Request context for @-resolution: verified claims + server-injected context.
    // Client-supplied @-keys never enter here, so identity can't be forged.
    const ctx: Record<string, unknown> = {
      ...claims,
      collection: r.coll,
      remoteAddr: input.remoteAddr,
      host: input.header("host") ?? input.url.host ?? "",
      method,
      path: input.url.pathname,
      rawQuery: input.url.search.replace(/^\?/, ""),
    };

    const resolveKey = (raw: string): string => {
      if (!raw.startsWith("@")) return raw;
      const v = resolveToken(raw, ctx);
      if (v === undefined || v === null) throw new UnauthorizedError(`cannot resolve key token "${raw}"`);
      return String(v);
    };
    const rawKey = input.url.searchParams.get("f");
    const key = rawKey != null ? resolveKey(rawKey) : undefined;
    const keys = input.url.searchParams.getAll("f").map(resolveKey);
    if (key !== undefined) ctx.field = key; // @field default → the record key

    const stripForged = (o: Record<string, unknown>): Record<string, unknown> => {
      for (const k of Object.keys(o)) if (k.startsWith("@")) delete o[k];
      return o;
    };

    let value: Doc | undefined;
    if (r.cmd === "hset" || r.cmd === "hsetnx") {
      let body: Doc = {};
      if (bodyText) {
        try { body = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      value = prepareWrite(coll, stripForged(body), { ctx });
    }

    let entries: Record<string, Doc> | undefined;
    let members: unknown[] | undefined;
    let items: unknown[] | undefined;
    let item: unknown;
    let pivot: unknown;
    let pairs: Record<string, number> | undefined;
    if (r.cmd === "hmset") {
      let body: Record<string, Doc> = {};
      if (bodyText) {
        try { body = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      entries = {};
      for (const [k, v] of Object.entries(body)) {
        const ectx = { ...ctx, field: k };
        entries[k] = prepareWrite(coll, stripForged(v as Record<string, unknown>), { ctx: ectx }) as Doc;
      }
    }

    if (r.cmd === "strset") {
      let body: Doc = {};
      if (bodyText) {
        try { body = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      value = body.v as Doc; // raw value, no @-binding
    }
    if (r.cmd === "strsetall") {
      let body: Record<string, unknown> = {};
      if (bodyText) {
        try { body = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      entries = body as Record<string, Doc>; // {key:value} raw
    }
    if (r.cmd === "sadd" || r.cmd === "srem" || r.cmd === "zrem") {
      let body: { members?: unknown[] } = {};
      if (bodyText) {
        try { body = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      members = body.members;
    }
    if (r.cmd === "lpush" || r.cmd === "rpush") {
      let body: { items?: unknown[] } = {};
      if (bodyText) {
        try { body = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      items = body.items;
    }
    if (r.cmd === "lset" || r.cmd === "lrem" || r.cmd === "linsertbefore" || r.cmd === "linsertafter") {
      let body: { item?: unknown; pivot?: unknown } = {};
      if (bodyText) {
        try { body = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      item = body.item;
      pivot = body.pivot;
    }
    if (r.cmd === "zadd") {
      let body: Record<string, unknown> = {};
      if (bodyText) {
        try { body = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid JSON body", code: "validation" } }; }
      }
      pairs = {} as Record<string, number>;
      for (const [k, v] of Object.entries(body)) {
        const n = typeof v === "number" ? v : Number(v);
        if (Number.isFinite(n)) (pairs as Record<string, number>)[k] = n;
      }
    }

    let filter: Filter | undefined;
    if (r.cmd === "find" || r.cmd === "findone" || r.cmd === "count") {
      if (bodyText) {
        try { filter = JSON.parse(bodyText); }
        catch { return { kind: "json", status: 400, body: { error: "invalid filter json", code: "validation" } }; }
      }
    }

    const opt: FindOpt = {};
    const lim = input.url.searchParams.get("limit");
    const skp = input.url.searchParams.get("skip");
    const srt = input.url.searchParams.get("s");
    const prj = input.url.searchParams.get("p");
    if (lim) opt.limit = parseInt(lim, 10);
    if (skp) opt.skip = parseInt(skp, 10);
    try {
      if (srt) { opt.sort = JSON.parse(srt); checkSortProj(opt.sort, "sort"); }
      if (prj) { opt.projection = JSON.parse(prj); checkSortProj(opt.projection, "projection"); }
    } catch (e) {
      const msg = e instanceof ValidationError ? e.message : "invalid sort/projection json";
      return { kind: "json", status: 400, body: { error: msg, code: "validation" } };
    }

    const out = await exec(m, r.cmd, {
      key,
      keys,
      value,
      entries,
      filter,
      field: input.url.searchParams.get("field") ?? undefined,
      n: input.url.searchParams.has("n") ? Number(input.url.searchParams.get("n"))
        : input.url.searchParams.has("expiration") ? Number(input.url.searchParams.get("expiration"))
        : undefined,
      cursor: input.url.searchParams.has("cursor") ? Number(input.url.searchParams.get("cursor")) : undefined,
      count: input.url.searchParams.has("count") ? Number(input.url.searchParams.get("count")) : undefined,
      match: input.url.searchParams.get("match") ?? undefined,
      members,
      member: input.url.searchParams.get("member") ?? undefined,
      items, item, pivot,
      index: input.url.searchParams.has("index") ? Number(input.url.searchParams.get("index")) : undefined,
      start: input.url.searchParams.has("start") ? Number(input.url.searchParams.get("start")) : undefined,
      stop: input.url.searchParams.has("stop") ? Number(input.url.searchParams.get("stop")) : undefined,
      pairs,
      min: input.url.searchParams.has("min") ? Number(input.url.searchParams.get("min")) : undefined,
      max: input.url.searchParams.has("max") ? Number(input.url.searchParams.get("max")) : undefined,
      withscores: input.url.searchParams.get("withscores") === "true" || input.url.searchParams.get("withscores") === "1",
      opt,
    }, scope);
    return { kind: "json", status: 200, body: out ?? null };
  }

  async function close(): Promise<void> {
    for (const c of ownClients) await c.close();
  }

  return { db: rawDb, specs, cors: cfg.cors, resolve, close };
}

// ---- Node adapter (standalone http.Server + Pages-router listener) ----------

const SSE_HEADERS: Record<string, string> = {
  "Content-Type": "text/event-stream",
  "Cache-Control": "no-cache, no-transform",
  Connection: "keep-alive",
  "X-Accel-Buffering": "no", // disable proxy buffering (nginx)
};

function applyCorsNode(cors: string[] | undefined, req: IncomingMessage, res: ServerResponse): boolean {
  if (cors && cors.length) {
    const origin = req.headers.origin as string | undefined;
    const allow = cors.includes("*") ? "*" : origin && cors.includes(origin) ? origin : "";
    if (allow) {
      res.setHeader("Access-Control-Allow-Origin", allow);
      res.setHeader("Vary", "Origin");
      res.setHeader("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID");
      res.setHeader("Access-Control-Allow-Methods", "GET, POST, OPTIONS");
    }
  }
  if ((req.method || "").toUpperCase() === "OPTIONS") {
    res.writeHead(204);
    res.end();
    return true;
  }
  return false;
}

async function nodeHandle(rt: Runtime, req: IncomingMessage, res: ServerResponse): Promise<void> {
  if (applyCorsNode(rt.cors, req, res)) return;
  const method = (req.method || "GET").toUpperCase();
  const bodyText = method === "GET" || method === "HEAD" ? "" : await readBody(req);
  const url = new URL(req.url || "/", `http://${req.headers.host || "localhost"}`);
  const input: ReqInput = {
    method,
    url,
    bodyText,
    header: (n) => {
      const v = req.headers[n.toLowerCase()];
      return Array.isArray(v) ? v[0] : v;
    },
    remoteAddr: req.socket?.remoteAddress ?? "",
  };
  const outcome = await rt.resolve(input);
  if (outcome.kind === "json") return sendJSON(res, outcome.status, outcome.body);

  // watch → SSE over the Node response.
  res.writeHead(200, SSE_HEADERS);
  const sink: SSESink = {
    write: (s) => { try { res.write(s); } catch { /* socket gone */ } },
    close: () => { try { res.end(); } catch { /* already ended */ } },
    onAbort: (cb) => req.on("close", cb),
  };
  await streamWatch(outcome.m, outcome.scope, outcome.resumeAfter, sink);
}

/** Start a standalone server (and/or obtain a Node request listener usable from
 * a Pages-router API route: `export default (req, res) => srv.listener(req, res)`). */
export async function serve(cfg: ServeConfig): Promise<DopdbServer> {
  const rt = await buildRuntime(cfg);
  const listener = (req: IncomingMessage, res: ServerResponse): void => {
    nodeHandle(rt, req, res).catch((e) => sendError(res, e));
  };
  let http: Server | undefined;
  if (cfg.port != null) {
    http = createServer(listener);
    await new Promise<void>((resolve) => http!.listen(cfg.port, resolve));
    if (cfg.dev) console.log(`dopdb listening on :${cfg.port}`);
  }
  return {
    db: rt.db,
    listener,
    http,
    specs: rt.specs,
    async close() {
      if (http) await new Promise<void>((r) => http!.close(() => r()));
      await rt.close();
    },
  };
}

// ---- Web adapter (Next.js App-Router Route Handler) -------------------------

export type NextRouteHandler = (
  req: Request,
  ctx?: { params?: Record<string, string | string[]> | Promise<Record<string, string | string[]>> },
) => Promise<Response>;

/**
 * Build a Next.js App-Router Route Handler set. Drop it into a catch-all route to
 * take over that path with no extra config:
 *
 *   // app/api/[...slug]/route.ts
 *   import { createNextHandler, Permissions } from "@kequnyang/dopdb/server";
 *   import { schema } from "@/dopdb-schema";
 *   const perms = new Permissions().grant("HGET", "users").grant("HSET", "users");
 *   export const { GET, POST, OPTIONS } =
 *     createNextHandler({ schema, mongo: { uri: process.env.MONGO_URI!, db: "appdb" },
 *                         jwtSecret: process.env.JWT_SECRET!, permissions: perms });
 *   export const runtime = "nodejs"; // the MongoDB driver is not Edge-compatible
 *
 * The route prefix is just the folder you mount in (the catch-all segments are
 * used directly), so renaming `api` to anything else needs no code change — set
 * the matching `apiBase` on the client.
 *
 * The Mongo connection is opened lazily on the first request and reused.
 */
export function createNextHandler(cfg: ServeConfig): {
  GET: NextRouteHandler;
  POST: NextRouteHandler;
  OPTIONS: NextRouteHandler;
} {
  let rtPromise: Promise<Runtime> | null = null;
  const getRuntime = () => (rtPromise ??= buildRuntime(cfg));
  const handler: NextRouteHandler = async (req, ctx) => {
    // Answer CORS preflight without opening a Mongo connection.
    if (req.method.toUpperCase() === "OPTIONS") {
      return new Response(null, { status: 204, headers: corsHeadersWeb(cfg.cors, req.headers.get("origin") ?? undefined) });
    }
    const rt = await getRuntime();
    return webHandle(rt, req, ctx);
  };
  return { GET: handler, POST: handler, OPTIONS: handler };
}

function corsHeadersWeb(cors: string[] | undefined, origin: string | undefined): Record<string, string> {
  const h: Record<string, string> = {};
  if (cors && cors.length) {
    const allow = cors.includes("*") ? "*" : origin && cors.includes(origin) ? origin : "";
    if (allow) {
      h["Access-Control-Allow-Origin"] = allow;
      h["Vary"] = "Origin";
      h["Access-Control-Allow-Headers"] = "Authorization, Content-Type, Last-Event-ID";
      h["Access-Control-Allow-Methods"] = "GET, POST, OPTIONS";
    }
  }
  return h;
}

async function webHandle(
  rt: Runtime,
  req: Request,
  ctx?: { params?: Record<string, string | string[]> | Promise<Record<string, string | string[]>> },
): Promise<Response> {
  const cors = corsHeadersWeb(rt.cors, req.headers.get("origin") ?? undefined);
  if (req.method.toUpperCase() === "OPTIONS") return new Response(null, { status: 204, headers: cors });

  // Next.js catch-all params (e.g. { slug: ["hget","users"] }) give the segments
  // after the mount point directly — prefix-agnostic, zero config.
  let segments: string[] | undefined;
  if (ctx?.params) {
    const params = await ctx.params;
    const vals = Object.values(params);
    const arr = vals.find((v) => Array.isArray(v)) as string[] | undefined;
    segments = arr ?? (vals.length ? vals.flatMap((v) => (Array.isArray(v) ? v : [v])) as string[] : undefined);
  }

  const url = new URL(req.url);
  const method = req.method.toUpperCase();
  if (Number(req.headers.get("content-length") ?? "0") > MAX_BODY) {
    return new Response(JSON.stringify({ error: "request body too large", code: "too_large" }), {
      status: 413,
      headers: { "Content-Type": "application/json", ...cors },
    });
  }
  const bodyText = method === "GET" || method === "HEAD" ? "" : await req.text();
  const input: ReqInput = {
    method,
    url,
    bodyText,
    segments,
    header: (n) => req.headers.get(n) ?? undefined,
    remoteAddr: req.headers.get("x-forwarded-for")?.split(",")[0]?.trim() ?? "",
  };

  try {
    const outcome = await rt.resolve(input);
    if (outcome.kind === "json") {
      return new Response(JSON.stringify(outcome.body ?? null), {
        status: outcome.status,
        headers: { "Content-Type": "application/json", ...cors },
      });
    }
    // watch → SSE via a ReadableStream.
    const { m, scope, resumeAfter } = outcome;
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        const enc = new TextEncoder();
        let closed = false;
        const sink: SSESink = {
          write: (s) => { if (!closed) try { controller.enqueue(enc.encode(s)); } catch { /* closed */ } },
          close: () => { if (!closed) { closed = true; try { controller.close(); } catch { /* closed */ } } },
          onAbort: (cb) => req.signal?.addEventListener("abort", cb),
        };
        void streamWatch(m, scope, resumeAfter, sink);
      },
    });
    return new Response(stream, {
      status: 200,
      headers: { ...SSE_HEADERS, ...cors },
    });
  } catch (e) {
    const status = e instanceof DopdbError ? e.status : 500;
    const code = e instanceof DopdbError ? e.code : "error";
    const message = e instanceof DopdbError ? e.message : ((e as Error)?.message ?? "internal error");
    return new Response(JSON.stringify({ error: message, code }), {
      status,
      headers: { "Content-Type": "application/json", ...cors },
    });
  }
}

export interface ServeFromConfigOptions {
  /** The single schema map. */
  schema: Record<string, Collection<any>>;
  /** Registered API endpoints (also auto-discovered from the registry). */
  api?: ApiMap;
  /** Permission gate (default DENY-ALL). */
  permissions?: Permissions;
  /** Optional override gate; takes precedence over `permissions`. */
  permit?: (cmd: string, coll: string, claims: Claims) => boolean;
  /** Route prefix for the path parser (default "/api"). */
  basePath?: string;
  dev?: boolean;
}

/** Start a standalone Node server from a config file (TOML/JSON). Secrets and the
 * Mongo URIs are resolved from environment variables per the config, never from
 * the file. All [[mongo]] sources are loaded as datasources (?ds= selects one). */
export async function serveFromConfig(path: string, opts: ServeFromConfigOptions): Promise<DopdbServer> {
  const cfg = loadConfig(path);
  return serve({
    schema: opts.schema,
    api: opts.api,
    permit: opts.permit,
    permissions: opts.permissions,
    basePath: opts.basePath,
    datasources: cfg.mongo.map((m) => ({ name: m.name, mongo: { uri: m.uri, db: m.db } })),
    jwtSecret: cfg.http.jwtSecret,
    cors: cfg.http.corsOrigins,
    port: portFromAddr(cfg.http.addr),
    dev: opts.dev,
  });
}

/** Build a Next.js App-Router Route Handler set from a config file. Same drop-in
 * as createNextHandler, but datasources/secret/CORS come from the config. */
export function nextHandlerFromConfig(path: string, opts: ServeFromConfigOptions): {
  GET: NextRouteHandler;
  POST: NextRouteHandler;
  OPTIONS: NextRouteHandler;
} {
  const cfg = loadConfig(path);
  return createNextHandler({
    schema: opts.schema,
    api: opts.api,
    permit: opts.permit,
    permissions: opts.permissions,
    basePath: opts.basePath,
    datasources: cfg.mongo.map((m) => ({ name: m.name, mongo: { uri: m.uri, db: m.db } })),
    jwtSecret: cfg.http.jwtSecret,
    cors: cfg.http.corsOrigins,
  });
}
