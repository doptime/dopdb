// server.ts — the α server runtime. Binds KVRocks directly (no Store
// abstraction; point 14) and uses Redis data structures as Redis data structures.
//
// Two surfaces, mirroring Go's Collection (trusted) + httpserve (scoped):
//   - serverDb(schema, backend)  raw, trusted, typed db for handler/server code.
//   - serve(cfg)                 HTTP server enforcing JWT @-binding, owner-scope,
//                                permissions; dispatches data commands + /api/<name>.
//
// One schema, imported here and on the client. Nothing is generated.
//
// The storage layout, the CBOR value format and the query dialect are shared
// with the Go engine command-for-command — see kvrocks.ts, codec.ts and query.ts,
// each the twin of the identically named Go file.

import { createHmac, timingSafeEqual, createVerify, createPublicKey } from "node:crypto";
import { createServer, type IncomingMessage, type ServerResponse, type Server } from "node:http";

import { KvBackend, type KvConn } from "./kvrocks.js";
import { decodeValue, encodeValue } from "./codec.js";
import { applyProjection, matchFilter, sortDocs, sortKeysFromMap, type SortKey } from "./query.js";
import { parseSql, checkTable, SqlError } from "./sql.js";

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
  type KvrocksSource,
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
// Low-level command execution against a KVRocks namespace (shared by serverDb
// and the HTTP dispatcher). Values are already prepared; `scope` is the owner
// predicate ({} = unscoped).
// ----------------------------------------------------------------------------

const empty = (m: Filter) => Object.keys(m).length === 0;

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
// illegal field paths from reaching the query engine.
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
  sql?: string;
  opt?: FindOpt;
}

// ---- helpers over the stored shape ------------------------------------------
//
// The document id lives in the Redis hash FIELD, not inside the document, so it
// is stripped on write and injected on read. Callers see the same `_id`-carrying
// object the Mongo build returned.

const withId = (id: string, doc: Doc): Doc => ({ ...doc, _id: id });
const stripId = (doc: Doc): Doc => {
  const out = { ...doc };
  delete out._id;
  return out;
};

/** The (field, value) pair of an owner scope, or null when unscoped. */
function scopePair(scope: Filter): [string, unknown] | null {
  const keys = Object.keys(scope);
  if (keys.length === 0) return null;
  return [keys[0], (scope as Doc)[keys[0]]];
}

function listIdx(n: number, i: number): number { return i < 0 ? n + i : i; }

/** Render a score bound the way Redis range commands expect, including the
 * infinities the HTTP layer uses as "unbounded". */
function scoreArg(f: number): string {
  if (f === -Infinity) return "-inf";
  if (f === Infinity) return "+inf";
  return String(f);
}

type ZM = { m: string; score: number };

/** Redis returns [member, score, member, score, ...] for WITHSCORES. */
function zPairs(flat: string[]): ZM[] {
  const out: ZM[] = [];
  for (let i = 0; i + 1 < flat.length; i += 2) out.push({ m: flat[i], score: Number(flat[i + 1]) });
  return out;
}

function zRender(ms: ZM[], withScores: boolean): unknown {
  return withScores ? ms.map((x) => ({ m: x.m, score: x.score })) : ms.map((x) => x.m);
}

/** Shape a matched result set: sort, skip, limit, project. The in-process
 * stand-in for the cursor options the driver used to send. */
function shape(rows: Doc[], opt: FindOpt | undefined, sortKeys?: SortKey[]): Doc[] {
  let out = rows;
  if (sortKeys && sortKeys.length > 0) sortDocs(out, sortKeys);
  else if (opt?.sort) sortDocs(out, sortKeysFromMap(opt.sort as Record<string, unknown>));
  if (opt?.skip != null && opt.skip > 0) out = out.slice(opt.skip);
  const lim = opt?.limit;
  const cap = lim != null ? Math.min(Math.max(lim, 0), MAX_LIMIT) || DEFAULT_LIMIT : DEFAULT_LIMIT;
  if (out.length > cap) out = out.slice(0, cap);
  if (opt?.projection) out = out.map((d) => applyProjection(d, opt.projection as Record<string, unknown>));
  return out;
}

// ---- command execution -------------------------------------------------------

async function exec(b: KvBackend, coll: string, cmd: string, a: ExecArgs, scope: Filter): Promise<unknown> {
  const scoped = !empty(scope);
  const key = a.key as string;

  // Non-Hash collection types keep their row isolation in the collection's owner
  // index, because a native list/set/zset has no document to hold an owner field.
  //
  // Both guards validate the key name FIRST. Ordering matters: claiming
  // ownership before rejecting a reserved name would write a claim for a key the
  // request is about to be refused, leaving the name owned by someone who never
  // stored anything under it.
  const guardRead = async (): Promise<boolean> => {
    b.entryKey(coll, key);
    return scoped ? await b.checkOwner(coll, key, scope) : true;
  };
  const guardWrite = async (): Promise<void> => {
    b.entryKey(coll, key);
    if (scoped && !(await b.claimOwner(coll, key, scope))) throw new ForbiddenError();
  };

  switch (cmd) {
    // ---- Hash family ----
    case "hget": {
      const doc = await b.get(coll, key);
      if (!doc) throw new NotFoundError();
      const full = withId(key, doc);
      if (scoped && !matchFilter(full, scope)) throw new NotFoundError();
      return full;
    }
    case "hset": {
      const doc = stripId(a.value as Doc);
      if (!scoped) {
        await b.put(coll, key, doc);
        return { ok: true };
      }
      const [field, val] = scopePair(scope)!;
      if (!(await b.putScoped(coll, key, doc, field, val))) throw new ForbiddenError();
      return { ok: true };
    }
    case "hsetnx": {
      // insert-if-absent. prepareWrite already stamps the owner onto the value
      // (bind rule), so a scoped insert is owned. An existing id — no matter who
      // owns it — returns inserted=false, so it never distinguishes "exists for
      // me" from "exists for another tenant".
      const inserted = await b.putIfAbsent(coll, key, stripId(a.value as Doc));
      return { inserted };
    }
    case "hdel":
    case "del": {
      const keys = a.keys ?? [];
      if (!scoped) {
        await b.del(coll, keys);
        return { ok: true };
      }
      const mine: string[] = [];
      for (const k of keys) {
        const d = await b.get(coll, k);
        if (d && matchFilter(withId(k, d), scope)) mine.push(k);
      }
      await b.del(coll, mine);
      return { ok: true };
    }
    case "hexists": {
      const doc = await b.get(coll, key);
      if (!doc) return { exists: false };
      return { exists: !scoped || matchFilter(withId(key, doc), scope) };
    }
    case "hgetall": {
      const out: Record<string, Doc> = {};
      for (const d of await b.findDocs(coll, scope)) out[String(d._id)] = d;
      return out;
    }
    case "hkeys": {
      return (await b.findDocs(coll, scope)).map((d) => String(d._id));
    }
    case "hvals": {
      return await b.findDocs(coll, scope);
    }
    case "hlen": {
      if (!scoped) return { len: await b.count(coll) };
      return { len: (await b.findDocs(coll, scope)).length };
    }
    case "hrandfield": {
      // Redis HRANDFIELD — the native command when unscoped; a filtered scan
      // otherwise, since the population has to be restricted first.
      return await b.sample(coll, a.count && a.count > 0 ? a.count : 1, scope);
    }
    case "hscan":
    case "hscannovalues": {
      // Redis HSCAN, natively. The cursor is the server's own, so a page may be
      // short (or empty) with a non-zero cursor; only cursor 0 means done.
      const r = await b.scan(coll, a.match ?? "*", a.cursor ?? 0, a.count && a.count > 0 ? a.count : 10, scope);
      if (cmd === "hscannovalues") return { cursor: r.cursor, keys: r.ids };
      return { cursor: r.cursor, keys: r.ids, values: r.docs };
    }
    case "hincrby":
    case "hincrbyfloat": {
      // The ownership test runs INSIDE the increment's transaction, not here: a
      // check followed by an unscoped increment is a check-then-act, and the
      // window is reachable by an owner racing their own delete-and-recreate.
      const pair = scoped ? scopePair(scope) : null;
      await b.incr(coll, key, a.field as string, a.n ?? 0, pair?.[0], pair?.[1]);
      return { ok: true };
    }
    case "hmset": {
      const entries = a.entries ?? {};
      if (Object.keys(entries).length === 0) return { ok: true };
      if (!scoped) {
        const prepared: Record<string, Doc> = {};
        for (const [k, v] of Object.entries(entries)) prepared[k] = stripId(v);
        await b.putMany(coll, prepared);
        return { ok: true };
      }
      const [field, val] = scopePair(scope)!;
      for (const [k, v] of Object.entries(entries)) {
        if (!(await b.putScoped(coll, k, stripId(v), field, val))) throw new ForbiddenError();
      }
      return { ok: true };
    }
    case "hmget": {
      const keys = a.keys ?? [];
      if (keys.length === 0) return [];
      const docs = await b.getMany(coll, keys);
      return docs.map((d, i) => {
        if (!d) return null;
        const full = withId(keys[i], d);
        return scoped && !matchFilter(full, scope) ? null : full;
      });
    }
    case "count": {
      const safe = sanitizeFilter(a.filter);
      return { count: (await b.findDocs(coll, mergeScope(scope, safe))).length };
    }
    case "find": {
      const safe = sanitizeFilter(a.filter);
      return shape(await b.findDocs(coll, mergeScope(scope, safe)), a.opt);
    }
    case "sql": {
      // SQL is a front end: it compiles to the same (filter, sort, page) shape
      // `find` uses and runs on the same evaluator. Three things happen here
      // that the trusted path does not do — the FROM clause is checked against
      // this collection (so SQL cannot reach a collection the caller was never
      // granted), the owner scope is AND-ed in, and LIMIT is defaulted/clamped.
      const q = parseSql(a.sql ?? "");
      checkTable(q, coll);
      const merged = mergeScope(scope, sanitizeFilter(q.filter));
      if (q.count) return { count: (await b.findDocs(coll, merged)).length };
      return shape(await b.findDocs(coll, merged), {
        sort: undefined,
        skip: q.offset,
        limit: q.limit,
        projection: q.projection,
      } as FindOpt, q.sortKeys);
    }
    case "findone": {
      const safe = sanitizeFilter(a.filter);
      const rows = shape(await b.findDocs(coll, mergeScope(scope, safe)), { ...a.opt, limit: 1 });
      if (rows.length === 0) throw new NotFoundError();
      return rows[0];
    }

    // ---- String family: a native Redis string per key, with a native TTL ----
    case "strget": {
      if (!(await guardRead())) throw new NotFoundError();
      const raw = await b.redis.getBuffer(b.entryKey(coll, key));
      if (!raw) throw new NotFoundError();
      return decodeValue(raw);
    }
    case "strset": {
      await guardWrite();
      const rk = b.entryKey(coll, key);
      const buf = encodeValue(a.value);
      if (a.n && a.n > 0) await b.redis.set(rk, buf, "EX", a.n);
      else await b.redis.set(rk, buf);
      return { ok: true };
    }
    case "strdel": {
      const keys = a.keys ?? [];
      const owned: string[] = [];
      for (const k of keys) {
        if (scoped && !(await b.checkOwner(coll, k, scope))) continue;
        owned.push(k);
      }
      if (owned.length > 0) {
        await b.redis.del(...owned.map((k) => b.memberKey(coll, k)));
        await b.releaseOwner(coll, owned);
      }
      return { ok: true };
    }
    case "strgetall": {
      const keys = await b.ownedKeys(coll, a.match ?? "*", scope);
      const out: Record<string, unknown> = {};
      if (keys.length === 0) return out;
      const raws = await b.redis.mgetBuffer(...keys.map((k) => b.memberKey(coll, k)));
      raws.forEach((raw, i) => { if (raw) out[keys[i]] = decodeValue(raw); });
      return out;
    }
    case "strsetall": {
      const entries = Object.entries(a.entries ?? {});
      if (entries.length === 0) return { ok: true };
      const pipe = b.redis.pipeline();
      for (const [k, v] of entries) {
        if (scoped && !(await b.claimOwner(coll, k, scope))) throw new ForbiddenError();
        pipe.set(b.memberKey(coll, k), encodeValue(v));
      }
      await pipe.exec();
      return { ok: true };
    }

    // ---- Set family: a native Redis set per key ----
    case "sadd": {
      await guardWrite();
      const ms = a.members ?? [];
      if (ms.length > 0) await b.redis.sadd(b.entryKey(coll, key), ...ms.map(encodeValue));
      return { ok: true };
    }
    case "srem": {
      await guardWrite();
      const ms = a.members ?? [];
      if (ms.length > 0) await b.redis.srem(b.entryKey(coll, key), ...ms.map(encodeValue));
      await b.releaseIfEmpty(coll, key, scope);
      return { ok: true };
    }
    case "smembers": {
      if (!(await guardRead())) return [];
      const raws = await b.redis.smembersBuffer(b.entryKey(coll, key));
      // Redis set iteration order is unspecified; sort so the answer is stable.
      raws.sort(Buffer.compare);
      return raws.map((r) => decodeValue(r));
    }
    case "sismember": {
      if (!(await guardRead())) return { member: false };
      const n = await b.redis.sismember(b.entryKey(coll, key), encodeValue(a.member));
      return { member: n === 1 };
    }
    case "scard": {
      if (!(await guardRead())) return { card: 0 };
      return { card: await b.redis.scard(b.entryKey(coll, key)) };
    }

    // ---- List family: a native Redis list per key ----
    case "lpush":
    case "rpush": {
      await guardWrite();
      const its = a.items ?? [];
      if (its.length === 0) return { ok: true };
      const rk = b.entryKey(coll, key);
      if (cmd === "lpush") {
        // reverse so the batch lands head-first in the order the caller gave —
        // the behaviour the Mongo build had ($each with $position:0). Raw Redis
        // LPUSH would reverse it.
        await b.redis.lpush(rk, ...[...its].reverse().map(encodeValue));
      } else {
        await b.redis.rpush(rk, ...its.map(encodeValue));
      }
      return { ok: true };
    }
    case "lpop":
    case "rpop": {
      await guardWrite();
      const rk = b.entryKey(coll, key);
      const raw = cmd === "lpop" ? await b.redis.lpopBuffer(rk) : await b.redis.rpopBuffer(rk);
      if (!raw) throw new NotFoundError();
      // Redis drops the key when the last element goes; the claim must go too.
      await b.releaseIfEmpty(coll, key, scope);
      return decodeValue(raw);
    }
    case "lrange": {
      if (!(await guardRead())) return [];
      const raws = await b.redis.lrangeBuffer(b.entryKey(coll, key), a.start ?? 0, a.stop ?? -1);
      return raws.map((r) => decodeValue(r));
    }
    case "llen": {
      if (!(await guardRead())) return { len: 0 };
      return { len: await b.redis.llen(b.entryKey(coll, key)) };
    }
    case "lindex": {
      if (!(await guardRead())) return null;
      const raw = await b.redis.lindexBuffer(b.entryKey(coll, key), a.index ?? 0);
      return raw ? decodeValue(raw) : null;
    }
    case "lset": {
      await guardWrite();
      try {
        await b.redis.lset(b.entryKey(coll, key), a.index ?? 0, encodeValue(a.item));
      } catch (e) {
        // "index out of range" / "no such key" — the previous engine's NotFound
        const msg = String((e as Error)?.message ?? "").toLowerCase();
        if (msg.includes("out of range") || msg.includes("no such key")) throw new NotFoundError();
        throw e;
      }
      return { ok: true };
    }
    case "lrem": {
      await guardWrite();
      await b.redis.lrem(b.entryKey(coll, key), a.count ?? 0, encodeValue(a.item));
      await b.releaseIfEmpty(coll, key, scope);
      return { ok: true };
    }
    case "ltrim": {
      await guardWrite();
      await b.redis.ltrim(b.entryKey(coll, key), a.start ?? 0, a.stop ?? -1);
      await b.releaseIfEmpty(coll, key, scope);
      return { ok: true };
    }
    case "linsertbefore":
    case "linsertafter": {
      await guardWrite();
      // LINSERT answers -1 when the pivot is absent; that is not an error here,
      // matching the previous "pivot not found -> no change" behaviour.
      const rk = b.entryKey(coll, key);
      const pivot = encodeValue(a.pivot);
      const item = encodeValue(a.item);
      if (cmd === "linsertbefore") await b.redis.linsert(rk, "BEFORE", pivot, item);
      else await b.redis.linsert(rk, "AFTER", pivot, item);
      return { ok: true };
    }

    // ---- ZSet family: a native Redis sorted set per key ----
    case "zadd": {
      await guardWrite();
      const pairs = Object.entries(a.pairs ?? {});
      if (pairs.length === 0) return 0;
      // ZADD score member [score member ...]; ioredis types the variadic form
      // loosely, so the argument list is built as strings.
      const flat: string[] = [];
      for (const [m, s] of pairs) flat.push(String(s), m);
      return await b.redis.zadd(b.entryKey(coll, key), ...flat);
    }
    case "zrem": {
      await guardWrite();
      const ms = (a.members as string[] | undefined) ?? [];
      if (ms.length === 0) return 0;
      const removed = await b.redis.zrem(b.entryKey(coll, key), ...ms);
      await b.releaseIfEmpty(coll, key, scope);
      return removed;
    }
    case "zscore": {
      if (!(await guardRead())) throw new NotFoundError();
      const s = await b.redis.zscore(b.entryKey(coll, key), a.member as string);
      if (s === null) throw new NotFoundError();
      return Number(s);
    }
    case "zcard": {
      if (!(await guardRead())) return { card: 0 };
      return { card: await b.redis.zcard(b.entryKey(coll, key)) };
    }
    case "zcount": {
      if (!(await guardRead())) return { count: 0 };
      const n = await b.redis.zcount(b.entryKey(coll, key), scoreArg(a.min ?? -Infinity), scoreArg(a.max ?? Infinity));
      return { count: n };
    }
    case "zincrby": {
      await guardWrite();
      return Number(await b.redis.zincrby(b.entryKey(coll, key), a.n ?? 0, a.member as string));
    }
    case "zrange":
    case "zrevrange": {
      if (!(await guardRead())) return zRender([], !!a.withscores);
      const rk = b.entryKey(coll, key);
      const st = String(a.start ?? 0), en = String(a.stop ?? -1);
      const flat = cmd === "zrevrange"
        ? await b.redis.zrevrange(rk, st, en, "WITHSCORES")
        : await b.redis.zrange(rk, st, en, "WITHSCORES");
      return zRender(zPairs(flat), !!a.withscores);
    }
    case "zrangebyscore":
    case "zrevrangebyscore": {
      if (!(await guardRead())) return zRender([], !!a.withscores);
      const rk = b.entryKey(coll, key);
      const min = scoreArg(a.min ?? -Infinity), max = scoreArg(a.max ?? Infinity);
      const flat = cmd === "zrevrangebyscore"
        ? await b.redis.zrevrangebyscore(rk, max, min, "WITHSCORES")
        : await b.redis.zrangebyscore(rk, min, max, "WITHSCORES");
      return zRender(zPairs(flat), !!a.withscores);
    }
    case "zrank":
    case "zrevrank": {
      if (!(await guardRead())) return { rank: -1 };
      const rk = b.entryKey(coll, key);
      const n = cmd === "zrevrank"
        ? await b.redis.zrevrank(rk, a.member as string)
        : await b.redis.zrank(rk, a.member as string);
      return { rank: n === null ? -1 : n };
    }
    case "zpopmin":
    case "zpopmax": {
      await guardWrite();
      const rk = b.entryKey(coll, key);
      let count = a.count ?? 1;
      if (count <= 0) count = 1;
      const flat = cmd === "zpopmax" ? await b.redis.zpopmax(rk, count) : await b.redis.zpopmin(rk, count);
      await b.releaseIfEmpty(coll, key, scope);
      return zRender(zPairs(flat), true);
    }
    case "zremrangebyrank": {
      await guardWrite();
      const n = await b.redis.zremrangebyrank(b.entryKey(coll, key), a.start ?? 0, a.stop ?? -1);
      await b.releaseIfEmpty(coll, key, scope);
      return n;
    }
    case "zremrangebyscore": {
      await guardWrite();
      const n = await b.redis.zremrangebyscore(
        b.entryKey(coll, key),
        scoreArg(a.min ?? -Infinity),
        scoreArg(a.max ?? Infinity),
      );
      await b.releaseIfEmpty(coll, key, scope);
      return n;
    }
    default:
      throw new NotFoundError(`unknown command: ${cmd}`);
  }
}

// ----------------------------------------------------------------------------
// serverDb — raw, trusted, typed db for handler/server code (no scope, no JWT).
// ----------------------------------------------------------------------------

function makeServerApi<C extends Collection<any>>(coll: C, b: KvBackend, storage: string): DbApi<C> {
  const w = (v: Record<string, unknown>) => prepareWrite(coll, v, { trusted: true });
  const run = (cmd: string, a: ExecArgs) => exec(b, storage, cmd, a, {});
  const hget = async (key: string) => {
    try {
      return (await run("hget", { key })) as any;
    } catch (e) {
      if (e instanceof NotFoundError) return null;
      throw e;
    }
  };
  const hset = async (key: string, value: any) => {
    await run("hset", { key, value: w(value) });
  };
  return {
    hget,
    hset,
    get: hget,
    set: hset,
    hsetnx: async (key, value) => ((await run("hsetnx", { key, value: w(value as Doc) })) as any).inserted,
    hdel: async (...keys) => void (await run("hdel", { keys })),
    del: async (...keys) => void (await run("del", { keys })),
    save: async (value) => {
      const v = w(value as Record<string, unknown>);
      await run("hset", { key: String((v as Doc)._id), value: v });
    },
    hexists: async (key) => ((await run("hexists", { key })) as any).exists,
    hgetall: async () => (await run("hgetall", {})) as any,
    hkeys: async () => (await run("hkeys", {})) as any,
    hvals: async () => (await run("hvals", {})) as any,
    hlen: async () => ((await run("hlen", {})) as any).len,
    hincrby: async (key, field, n) => void (await run("hincrby", { key, field, n })),
    hincrbyfloat: async (key, field, n) => void (await run("hincrbyfloat", { key, field, n })),
    hmset: async (entries) => {
      const prepared: Record<string, Doc> = {};
      for (const [k, v] of Object.entries(entries)) prepared[k] = w(v as Record<string, unknown>) as Doc;
      await run("hmset", { entries: prepared });
    },
    hmget: async (keys) => (await run("hmget", { keys })) as any,
    hrandfield: async (count) => (await run("hrandfield", count ? { count } : {})) as string[],
    hscan: async (cursor = 0, match, count) => (await run("hscan", { cursor, match, count })) as any,
    hscannovalues: async (cursor = 0, match, count) => (await run("hscannovalues", { cursor, match, count })) as any,
    count: async (filter = {}) => ((await run("count", { filter })) as any).count ?? 0,
    find: async (filter = {}, opt = {}) => (await run("find", { filter, opt })) as any,
    sql: async (statement: string) => (await run("sql", { sql: statement })) as any,
    sqlCount: async (statement: string) => ((await run("sql", { sql: statement })) as any).count ?? 0,
    findone: async (filter = {}) => {
      try {
        return (await run("findone", { filter })) as any;
      } catch (e) {
        if (e instanceof NotFoundError) return null;
        throw e;
      }
    },
    watch: async (onEvent: WatchHandler<any>): Promise<Unsubscribe> => {
      const sub = await subscribeChanges(b, storage, {}, (ev) => onEvent(ev as WatchEvent<any>));
      return () => { void sub(); };
    },
  };
}

/** Build a raw, trusted, typed db bound directly to a KVRocks namespace. Use
 * inside API handlers and server code. No owner-scope and no JWT — full access. */
export function serverDb<M extends Record<string, Collection<any>>>(schema: M, backend: KvBackend): Db<M> {
  const out = {} as Db<M>;
  for (const name of Object.keys(schema) as (keyof M & string)[]) {
    const storage = schema[name].opts.name ?? name;
    (out as any)[name] = makeServerApi(schema[name], backend, storage);
  }
  return out;
}

/** Register the index declarations the schema carries.
 *
 * KVRocks has no secondary indexes, so this is no longer "create indexes on the
 * server". Only `unique` has runtime meaning and dopdb enforces it itself (see
 * kvrocks.ts enforceUnique); asc/desc/text declarations are recorded for
 * declaration fidelity and are inert, and a TTL declaration on a Hash field has
 * no equivalent at all — a Hash collection is ONE Redis key, so a per-document
 * expiry cannot be expressed. Per-key TTL survives where it is native: the
 * String family's `?expiration=`.
 *
 * Kept as an exported function, and still called at startup, so the schema stays
 * the single place indexes are declared. */
export async function ensureIndexes(schema: Record<string, Collection<any>>, backend: KvBackend): Promise<void> {
  for (const name of Object.keys(schema)) {
    const spec = specOf(name, schema[name]);
    const unique = spec.indexes.filter((i) => i.unique && i.expireAfterSeconds == null).map((i) => i.field);
    backend.registerUnique(spec.name, unique);
  }
}

// ---- change events ----------------------------------------------------------
//
// Mongo change streams are gone. watch consumes dopdb's own publication channel
// (kvrocks.ts publish), on a dedicated subscriber connection. The observable
// contract is unchanged: writes are delivered, and a scoped watcher never sees
// deletes (they carry no document to scope on).
//
// What does NOT survive is resume: Redis pub/sub is fire-and-forget, so a
// reconnect starts fresh and events during the gap are missed. The server
// therefore emits no SSE `id:` lines, and the client sends no Last-Event-ID.

async function subscribeChanges(
  b: KvBackend,
  coll: string,
  scope: Filter,
  onEvent: (ev: WatchEvent<Doc>) => void,
): Promise<() => Promise<void>> {
  const sub = b.redis.duplicate();
  await sub.subscribe(b.eventChannel(coll));
  const scoped = Object.keys(scope).length > 0;
  sub.on("message", (_ch: string, payload: string) => {
    let ev: { op?: string; id?: string; doc?: Doc | null };
    try { ev = JSON.parse(payload); } catch { return; }
    const op = ev.op;
    if (op !== "insert" && op !== "update" && op !== "replace" && op !== "delete") return;
    const doc = ev.doc ?? null;
    if (scoped) {
      if (!doc) return; // deletes carry no document to scope on
      if (!matchFilter({ ...doc, _id: ev.id }, scope)) return;
    }
    onEvent({ type: op, key: String(ev.id ?? ""), doc: doc ? { ...doc, _id: ev.id } : null });
  });
  return async () => {
    try { await sub.unsubscribe(); } catch { /* already gone */ }
    sub.disconnect();
  };
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
  "hkeys", "hvals", "hlen", "hincrby", "hincrbyfloat", "hmset", "hmget", "count", "find", "findone", "sql",
  "hrandfield", "hscan", "hscannovalues",
  "strget", "strset", "strsetall", "strgetall", "strdel",
  "sadd", "srem", "smembers", "sismember", "scard",
  "lpush", "rpush", "lpop", "rpop", "lrange", "llen", "lindex", "lset", "lrem", "ltrim", "linsertbefore", "linsertafter",
  "zadd", "zrem", "zscore", "zcard", "zcount", "zincrby", "zrange", "zrevrange", "zrangebyscore", "zrevrangebyscore",
  "zrank", "zrevrank", "zpopmin", "zpopmax", "zremrangebyrank", "zremrangebyscore",
]);
const STREAM_COMMANDS = new Set(["watch"]);
const ROUTED_COMMANDS = new Set([...DATA_COMMANDS, ...STREAM_COMMANDS]);

/** A datasource: either an already-open backend, or the connection details to
 * open one. `namespace` is the KVRocks stand-in for a Mongo database name — it
 * prefixes every key this datasource writes. */
export type KvSource = KvBackend | KvConn;

export interface ServeConfig {
  /** The single schema map. */
  schema: Record<string, Collection<any>>;
  /** A single datasource (registered as "default"); or omit and use datasources. */
  kvrocks?: KvSource;
  /** Several named datasources; a request selects one with ?ds=<name>. */
  datasources?: { name: string; kvrocks: KvSource }[];
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
  | { kind: "watch"; backend: KvBackend; coll: string; scope: Filter };

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

// streamWatch subscribes to the collection's change channel and pumps SSE lines
// into a transport-neutral sink. Shared by the Node and Web adapters. Scoped
// watchers never see deletes (no document to scope on).
//
// Redis pub/sub has no replay, so there is no resume token and no SSE `id:`
// line: a reconnect starts fresh and events during the gap are missed. That is
// weaker than a change-stream resume token and is called out in docs/02-http.md.
async function streamWatch(b: KvBackend, coll: string, scope: Filter, sink: SSESink): Promise<void> {
  let unsubscribe: (() => Promise<void>) | null = null;
  let finished = false;
  const finish = () => {
    if (finished) return;
    finished = true;
    clearInterval(ping);
    void unsubscribe?.();
    sink.close();
  };
  const ping = setInterval(() => {
    try { sink.write(": ping\n\n"); } catch { finish(); }
  }, 25000);
  sink.onAbort(finish);
  try {
    unsubscribe = await subscribeChanges(b, coll, scope, (ev) => {
      try { sink.write(`data: ${JSON.stringify(ev)}\n\n`); } catch { finish(); }
    });
  } catch {
    try { sink.write(`event: error\ndata: {"code":"watch_error"}\n\n`); } catch { /* gone */ }
    finish();
    return;
  }
  sink.write(": connected\n\n");
}

// ---- the runtime (shared by every adapter) ----------------------------------

async function buildRuntime(cfg: ServeConfig): Promise<Runtime> {
  // Validate schema config BEFORE any side effects (connecting). Fail-closed:
  // a row-scoped collection MUST bind its owner field to an identity claim, or
  // prepareWrite never sets the owner and the {owner:@uid} predicate matches
  // nothing — scoping would silently break. Reject at startup; needs no server.
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

  // Datasource registry: name -> backend. A single `kvrocks` registers as
  // "default"; `datasources` registers several. ?ds=<name> selects one per request.
  const dbs = new Map<string, KvBackend>();
  const owned: KvBackend[] = [];
  const open = (src: KvSource): KvBackend => {
    if (src instanceof KvBackend) return src;
    const b = KvBackend.connect(src);
    owned.push(b);
    return b;
  };
  if (cfg.datasources) for (const d of cfg.datasources) dbs.set(d.name, open(d.kvrocks));
  if (cfg.kvrocks) dbs.set("default", open(cfg.kvrocks));
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
    const backend = dbs.get(ds) ?? defaultDb;

    if (r.cmd === "watch") {
      // No resume token: Redis pub/sub does not replay, so Last-Event-ID is
      // deliberately ignored rather than silently pretending to resume.
      return { kind: "watch", backend, coll: entry.storage, scope };
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

    // A SQL statement is not JSON, so both a raw body and {"sql":"..."} are
    // accepted; ?q= carries it on a GET.
    let sqlText: string | undefined;
    if (r.cmd === "sql") {
      sqlText = input.url.searchParams.get("q") ?? undefined;
      if (!sqlText && bodyText) {
        try {
          const wrapper = JSON.parse(bodyText) as { sql?: string };
          sqlText = typeof wrapper?.sql === "string" ? wrapper.sql : bodyText;
        } catch {
          sqlText = bodyText;
        }
      }
      if (!sqlText || !sqlText.trim()) {
        return { kind: "json", status: 400, body: { error: 'sql requires ?q=<statement> or a body of {"sql":"..."}', code: "validation" } };
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

    const out = await exec(backend, entry.storage, r.cmd, {
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
      sql: sqlText,
      opt,
    }, scope);
    return { kind: "json", status: 200, body: out ?? null };
  }

  async function close(): Promise<void> {
    for (const b of owned) await b.close();
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
  await streamWatch(outcome.backend, outcome.coll, outcome.scope, sink);
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
 *     createNextHandler({ schema, kvrocks: { uri: process.env.KVROCKS_URI!, namespace: "appdb" },
 *                         jwtSecret: process.env.JWT_SECRET!, permissions: perms });
 *   export const runtime = "nodejs"; // the redis client is not Edge-compatible
 *
 * The route prefix is just the folder you mount in (the catch-all segments are
 * used directly), so renaming `api` to anything else needs no code change — set
 * the matching `apiBase` on the client.
 *
 * The KVRocks connection is opened lazily on the first request and reused.
 */
export function createNextHandler(cfg: ServeConfig): {
  GET: NextRouteHandler;
  POST: NextRouteHandler;
  OPTIONS: NextRouteHandler;
} {
  let rtPromise: Promise<Runtime> | null = null;
  const getRuntime = () => (rtPromise ??= buildRuntime(cfg));
  const handler: NextRouteHandler = async (req, ctx) => {
    // Answer CORS preflight without opening a KVRocks connection.
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
    const { backend, coll, scope } = outcome;
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        const enc = new TextEncoder();
        let closed = false;
        const sink: SSESink = {
          write: (s) => { if (!closed) try { controller.enqueue(enc.encode(s)); } catch { /* closed */ } },
          close: () => { if (!closed) { closed = true; try { controller.close(); } catch { /* closed */ } } },
          onAbort: (cb) => req.signal?.addEventListener("abort", cb),
        };
        void streamWatch(backend, coll, scope, sink);
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
 * URIs and passwords are resolved from environment variables per the config,
 * never from the file. All [[kvrocks]] sources are loaded as datasources
 * (?ds= selects one). */
export async function serveFromConfig(path: string, opts: ServeFromConfigOptions): Promise<DopdbServer> {
  const cfg = loadConfig(path);
  return serve({
    schema: opts.schema,
    api: opts.api,
    permit: opts.permit,
    permissions: opts.permissions,
    basePath: opts.basePath,
    datasources: cfg.kvrocks.map((k) => ({ name: k.name, kvrocks: { uri: k.uri, namespace: k.namespace, password: k.password } })),
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
    datasources: cfg.kvrocks.map((k) => ({ name: k.name, kvrocks: { uri: k.uri, namespace: k.namespace, password: k.password } })),
    jwtSecret: cfg.http.jwtSecret,
    cors: cfg.http.corsOrigins,
  });
}
