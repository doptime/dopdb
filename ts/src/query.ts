// query.ts — the query engine, the TypeScript twin of Go's query.go.
//
// Mongo evaluated filters, sorts and projections server-side. KVRocks has no
// query language at all, so the same work happens here, over the decoded
// documents of one collection hash. The FILTER DIALECT IS UNCHANGED — the
// operators are exactly the allowlist in sanitize.ts, so the HTTP wire protocol,
// the client types and the docs did not have to move. Only the place of
// evaluation changed.
//
// The Go and TypeScript engines must agree command-for-command, so this file and
// query.go are written to the same rules: identical operator semantics, identical
// type ordering, identical projection behaviour.
//
// Consequences worth stating plainly:
//   - find/count/findone read every field of the collection hash and decode each
//     document. Cost is O(collection), not O(result).
//   - There is no index-backed ordering; sort runs on the matched array.
//   - Type ordering is dopdb's own (typeRank below), not BSON's.

import type { Filter } from "./sanitize.js";

export type Doc = Record<string, unknown>;

const isObj = (v: unknown): v is Doc => v !== null && typeof v === "object" && !Array.isArray(v) && !(v instanceof Date);

/** Does doc satisfy filter? An operator the engine does not implement matches
 * NOTHING — an unknown filter must never widen a result set. */
export function matchFilter(doc: Doc, filter: Filter): boolean {
  for (const [k, cond] of Object.entries(filter as Doc)) {
    switch (k) {
      case "$and": {
        if (!Array.isArray(cond)) return false;
        for (const s of cond) if (!isObj(s) || !matchFilter(doc, s as Filter)) return false;
        break;
      }
      case "$or": {
        if (!Array.isArray(cond)) return false;
        if (!cond.some((s) => isObj(s) && matchFilter(doc, s as Filter))) return false;
        break;
      }
      case "$nor": {
        if (!Array.isArray(cond)) return false;
        if (cond.some((s) => isObj(s) && matchFilter(doc, s as Filter))) return false;
        break;
      }
      default: {
        if (k.startsWith("$")) return false; // top-level operator outside the logical set
        if (!matchField(doc, k, cond)) return false;
      }
    }
  }
  return true;
}

/** An operator document is a map whose keys ALL start with '$'. A map with plain
 * keys is an embedded-document equality comparison. */
function operatorDoc(cond: unknown): Doc | null {
  if (!isObj(cond)) return null;
  const keys = Object.keys(cond);
  if (keys.length === 0) return null;
  return keys.every((k) => k.startsWith("$")) ? cond : null;
}

function matchField(doc: Doc, path: string, cond: unknown): boolean {
  const [val, present] = lookupPath(doc, path);
  const ops = operatorDoc(cond);
  if (ops) return matchOperators(val, present, ops);
  return valueMatches(val, present, cond);
}

function matchOperators(val: unknown, present: boolean, ops: Doc): boolean {
  if ("$regex" in ops) {
    const opts = typeof ops.$options === "string" ? ops.$options : "";
    if (!matchRegex(val, present, ops.$regex, opts)) return false;
  }
  for (const [op, arg] of Object.entries(ops)) {
    switch (op) {
      case "$regex":
      case "$options":
        break; // handled above
      case "$eq":
        if (!valueMatches(val, present, arg)) return false;
        break;
      case "$ne":
        if (valueMatches(val, present, arg)) return false;
        break;
      case "$gt":
      case "$gte":
      case "$lt":
      case "$lte":
        if (!matchCompare(val, present, op, arg)) return false;
        break;
      case "$in":
        if (!matchIn(val, present, arg)) return false;
        break;
      case "$nin":
        if (matchIn(val, present, arg)) return false;
        break;
      case "$exists":
        if (typeof arg !== "boolean" || present !== arg) return false;
        break;
      case "$type":
        if (!present || !matchType(val, arg)) return false;
        break;
      case "$size":
        if (!present || !Array.isArray(val) || typeof arg !== "number" || val.length !== arg) return false;
        break;
      case "$all": {
        if (!present || !Array.isArray(arg)) return false;
        for (const w of arg) if (!valueMatches(val, true, w)) return false;
        break;
      }
      case "$elemMatch": {
        if (!present || !isObj(arg) || !Array.isArray(val)) return false;
        const sub = arg as Filter;
        const scalarOps: Doc = {};
        for (const [k, v] of Object.entries(sub as Doc)) if (k.startsWith("$")) scalarOps[k] = v;
        const hit = val.some((e) =>
          isObj(e) ? matchFilter(e, sub) : matchOperators(e, true, scalarOps),
        );
        if (!hit) return false;
        break;
      }
      case "$mod": {
        if (!present || !Array.isArray(arg) || arg.length !== 2) return false;
        const [div, rem] = arg as [unknown, unknown];
        if (typeof div !== "number" || typeof rem !== "number" || div === 0) return false;
        if (typeof val !== "number") return false;
        if (Math.trunc(val) % Math.trunc(div) !== rem) return false;
        break;
      }
      case "$not": {
        const sub = operatorDoc(arg);
        if (!sub) return false;
        if (matchOperators(val, present, sub)) return false;
        break;
      }
      default:
        return false; // outside the allowlist: never matches
    }
  }
  return true;
}

/** Equality with array semantics: a field holding an array matches if the array
 * itself equals the operand OR any element does. */
function valueMatches(val: unknown, present: boolean, want: unknown): boolean {
  if (!present) return want === null || want === undefined;
  if (equalValues(val, want)) return true;
  if (Array.isArray(val)) return val.some((e) => equalValues(e, want));
  return false;
}

function matchCompare(val: unknown, present: boolean, op: string, arg: unknown): boolean {
  if (!present) return false;
  const test = (v: unknown): boolean => {
    const c = compareValues(v, arg);
    if (c === null) return false;
    if (op === "$gt") return c > 0;
    if (op === "$gte") return c >= 0;
    if (op === "$lt") return c < 0;
    return c <= 0;
  };
  if (test(val)) return true;
  return Array.isArray(val) && val.some(test);
}

function matchIn(val: unknown, present: boolean, arg: unknown): boolean {
  if (!Array.isArray(arg)) return false;
  return arg.some((w) => valueMatches(val, present, w));
}

/** Bounds a caller-supplied pattern.
 *
 * JavaScript's regex engine backtracks, so an attacker-supplied pattern is a
 * denial-of-service primitive in a way Go's RE2 is not: `(a+)+$` against a long
 * non-matching string is exponential, and `find` evaluates the filter against
 * EVERY document. One request would freeze the event loop for the whole process,
 * taking every other request and every watch stream with it. The length cap and
 * the nested-quantifier check are cheap, conservative guards; they reject some
 * legitimate patterns, which is the right trade for a shared event loop. */
const MAX_REGEX_LEN = 512;

/** A quantifier applied to an already-quantified group — the classic catastrophic
 * backtracking shape. */
const NESTED_QUANTIFIER = /\([^)]*[+*][^)]*\)\s*[+*{]/;

const regexCache = new Map<string, RegExp | null>();

/** Compile a filter pattern once and reuse it.
 *
 * This used to compile inside the per-document loop, so a $regex filter over a
 * 20k-document collection built the same RegExp 20,000 times. */
function compileRegex(pattern: string, flags: string): RegExp | null {
  const key = `${flags}\u0000${pattern}`;
  const hit = regexCache.get(key);
  if (hit !== undefined) return hit;

  let re: RegExp | null = null;
  if (pattern.length <= MAX_REGEX_LEN && !NESTED_QUANTIFIER.test(pattern)) {
    try {
      re = new RegExp(pattern, flags);
    } catch {
      re = null;
    }
  }
  if (regexCache.size > 1000) regexCache.clear(); // crude bound; patterns are few
  regexCache.set(key, re);
  return re;
}

function matchRegex(val: unknown, present: boolean, pattern: unknown, opts: string): boolean {
  if (!present || typeof pattern !== "string") return false;
  const flags = [...opts].filter((c) => c === "i" || c === "m" || c === "s").join("");
  const re = compileRegex(pattern, flags);
  if (re === null) return false;
  const test = (v: unknown) => typeof v === "string" && re.test(v);
  if (test(val)) return true;
  return Array.isArray(val) && val.some(test);
}

/** $type accepts the JSON-flavoured names dopdb's wire protocol uses. Mongo's
 * numeric BSON type codes have no meaning here and are rejected. */
function matchType(val: unknown, arg: unknown): boolean {
  if (typeof arg !== "string") return false;
  const got = typeName(val);
  switch (arg.toLowerCase()) {
    case "number": case "double": case "int": case "long": case "decimal":
      return got === "number";
    case "bool": case "boolean": return got === "bool";
    case "string": return got === "string";
    case "array": return got === "array";
    case "object": return got === "object";
    case "null": return got === "null";
    case "date": case "timestamp": return got === "date";
    default: return false;
  }
}

function typeName(v: unknown): string {
  if (v === null || v === undefined) return "null";
  if (typeof v === "boolean") return "bool";
  if (typeof v === "number") return "number";
  if (typeof v === "string") return "string";
  if (Array.isArray(v)) return "array";
  if (v instanceof Date) return "date";
  if (typeof v === "object") return "object";
  return "unknown";
}

// ---- field access -----------------------------------------------------------

/** Resolve a dot path, traversing intermediate arrays the way the previous
 * engine did: "tags.name" against {tags:[{name:"a"}]} yields ["a"]. */
export function lookupPath(doc: Doc, path: string): [unknown, boolean] {
  const parts = path.split(".");
  let cur: unknown = doc;
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i];
    if (Array.isArray(cur)) {
      const out: unknown[] = [];
      for (const e of cur) if (isObj(e) && p in e) out.push(e[p]);
      if (out.length === 0) return [undefined, false];
      if (i === parts.length - 1) return [out, true];
      cur = out;
      continue;
    }
    if (isObj(cur)) {
      if (!(p in cur)) return [undefined, false];
      cur = cur[p];
      continue;
    }
    return [undefined, false];
  }
  return [cur, true];
}

// ---- value comparison -------------------------------------------------------

/** A total order across types, so sorting a heterogeneous field is deterministic.
 * This is dopdb's own ordering (matching Go's typeRank), not BSON's. */
function typeRank(v: unknown): number {
  if (v === null || v === undefined) return 0;
  if (typeof v === "number") return 1;
  if (typeof v === "string") return 2;
  if (typeof v === "boolean") return 3;
  if (v instanceof Date) return 4;
  if (Array.isArray(v)) return 5;
  if (typeof v === "object") return 6;
  return 7;
}

/** -1 / 0 / +1, or null when the two values are not comparable. */
export function compareValues(a: unknown, b: unknown): number | null {
  const ra = typeRank(a), rb = typeRank(b);
  if (ra !== rb) return ra < rb ? -1 : 1;
  switch (ra) {
    case 0: return 0;
    case 1: {
      const x = a as number, y = b as number;
      return x < y ? -1 : x > y ? 1 : 0;
    }
    case 2: {
      const x = a as string, y = b as string;
      return x < y ? -1 : x > y ? 1 : 0;
    }
    case 3: {
      const x = a as boolean, y = b as boolean;
      return x === y ? 0 : x ? 1 : -1;
    }
    case 4: {
      const x = (a as Date).getTime(), y = (b as Date).getTime();
      return x < y ? -1 : x > y ? 1 : 0;
    }
    default:
      return equalValues(a, b) ? 0 : null;
  }
}

/** Deep equality, with Date/ISO-string coercion so a JSON filter can match a
 * stored timestamp. */
export function equalValues(a: unknown, b: unknown): boolean {
  if (a === null || a === undefined || b === null || b === undefined) {
    return (a ?? null) === (b ?? null);
  }
  if (a instanceof Date) {
    if (b instanceof Date) return a.getTime() === b.getTime();
    if (typeof b === "string") return a.getTime() === Date.parse(b);
    return false;
  }
  if (typeof a === "number" || typeof a === "string" || typeof a === "boolean") return a === b;
  if (Array.isArray(a)) {
    if (!Array.isArray(b) || a.length !== b.length) return false;
    return a.every((x, i) => equalValues(x, b[i]));
  }
  if (isObj(a)) {
    if (!isObj(b)) return false;
    const ka = Object.keys(a), kb = Object.keys(b);
    if (ka.length !== kb.length) return false;
    return ka.every((k) => k in b && equalValues(a[k], b[k]));
  }
  return false;
}

// ---- result shaping ---------------------------------------------------------

export interface SortKey { field: string; asc: boolean }

/** Turn an unordered sort map into ordered directives. Object key order is not a
 * contract, so field names are sorted first to keep a multi-key sort at least
 * deterministic across both engines. */
export function sortKeysFromMap(sort: Record<string, unknown>): SortKey[] {
  return Object.keys(sort).sort().map((field) => {
    const v = sort[field];
    const asc = typeof v === "number" ? v >= 0 : v !== false;
    return { field, asc };
  });
}

/** The comparison a result set is ordered by: the requested sort keys, with the
 * document id as the final tiebreak so the order is total and stable. */
export function rowLess(a: Doc, b: Doc, keys: SortKey[]): boolean {
  for (const k of keys) {
    const [x] = lookupPath(a, k.field);
    const [y] = lookupPath(b, k.field);
    const c = compareValues(x, y);
    if (c === null || c === 0) continue;
    return k.asc ? c < 0 : c > 0;
  }
  return String(a._id ?? "") < String(b._id ?? "");
}

/** Keeps only the best n rows seen, in O(n) memory and O(log n) per row.
 *
 * Without it a query materialised every matching document and only then applied
 * skip/limit — so `find({}, {limit: 10})` against a million-document collection
 * held a million decoded documents to return ten. The scan is still
 * O(collection) (KVRocks has no index to consult), but the memory is now bounded
 * by what the caller actually asked for. */
export class TopN {
  private readonly rows: Doc[] = [];
  constructor(private readonly n: number, private readonly keys: SortKey[]) {}

  /** true when rows[i] should be evicted before rows[j] — it sorts AFTER rows[j]
   * and so is further from the answer. */
  private worse(i: number, j: number): boolean {
    return rowLess(this.rows[j], this.rows[i], this.keys);
  }

  /** Keeps the WORST retained row at the root, so that is the one evicted when
   * the heap is over capacity. */
  push(r: Doc): void {
    this.rows.push(r);
    let i = this.rows.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (!this.worse(i, p)) break; // child no worse than parent
      [this.rows[p], this.rows[i]] = [this.rows[i], this.rows[p]];
      i = p;
    }
    if (this.rows.length > this.n) this.popWorst();
  }

  private popWorst(): void {
    const last = this.rows.length - 1;
    this.rows[0] = this.rows[last];
    this.rows.pop();
    let i = 0;
    for (;;) {
      const l = 2 * i + 1, r = 2 * i + 2;
      let worst = i;
      if (l < this.rows.length && this.worse(l, worst)) worst = l;
      if (r < this.rows.length && this.worse(r, worst)) worst = r;
      if (worst === i) return;
      [this.rows[i], this.rows[worst]] = [this.rows[worst], this.rows[i]];
      i = worst;
    }
  }

  sorted(): Doc[] {
    return this.rows.sort((a, b) => (rowLess(a, b, this.keys) ? -1 : rowLess(b, a, this.keys) ? 1 : 0));
  }
}

export function sortDocs(rows: Doc[], keys: SortKey[]): void {
  rows.sort((x, y) => {
    for (const k of keys) {
      const [a] = lookupPath(x, k.field);
      const [b] = lookupPath(y, k.field);
      const c = compareValues(a, b);
      if (c === null || c === 0) continue;
      return k.asc ? c : -c;
    }
    return 0;
  });
}

const truthyProj = (v: unknown): boolean => (typeof v === "boolean" ? v : typeof v === "number" ? v !== 0 : false);

/** Include mode and exclude mode are mutually exclusive; _id follows its own flag
 * and is included by default in include mode. */
export function applyProjection(doc: Doc, proj: Record<string, unknown>): Doc {
  const include = Object.entries(proj).some(([k, v]) => k !== "_id" && truthyProj(v));
  if (include) {
    const out: Doc = {};
    for (const [k, v] of Object.entries(proj)) {
      if (k === "_id" || !truthyProj(v)) continue;
      const [val, present] = lookupPath(doc, k);
      if (present) setPath(out, k, val);
    }
    if (!("_id" in proj) || truthyProj(proj._id)) {
      if ("_id" in doc) out._id = doc._id;
    }
    return out;
  }
  const out: Doc = { ...doc };
  for (const [k, v] of Object.entries(proj)) if (!truthyProj(v)) delete out[k];
  return out;
}

export function setPath(dst: Doc, path: string, val: unknown): void {
  const parts = path.split(".");
  let cur = dst;
  for (let i = 0; i < parts.length; i++) {
    if (i === parts.length - 1) { cur[parts[i]] = val; return; }
    let next = cur[parts[i]];
    if (!isObj(next)) { next = {}; cur[parts[i]] = next; }
    cur = next as Doc;
  }
}
