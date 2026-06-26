// Filter sanitization — a faithful TS port of sanitize.go.
//
// Mongo's query surface is an open document; exposing it raw to an untrusted
// caller is NoSQL injection by construction ({$where:"..."}, $function, cross
// collection $lookup, writes via $out/$merge, ...). SanitizeFilter walks a
// filter and admits ONLY a vetted operator allowlist, rejecting anything that
// can execute code, reach other collections, or write.
//
// This is the operator-level floor every Find passes through. Field-level
// scoping (which fields a collection exposes, the mandatory owner == @uid
// predicate) is layered on top by the server. Running it on the client too is
// a courtesy: bad filters fail before the network round-trip.

import { ValidationError } from "./errors.js";

export type Filter = Record<string, unknown>;

const ALLOWED = new Set<string>([
  // comparison
  "$eq", "$ne", "$gt", "$gte", "$lt", "$lte", "$in", "$nin",
  // logical
  "$and", "$or", "$nor", "$not",
  // element
  "$exists", "$type",
  // array
  "$all", "$elemMatch", "$size",
  // evaluation (safe subset)
  "$regex", "$options", "$mod",
]);

const FORBIDDEN = new Set<string>([
  "$where", "$function", "$accumulator",
  "$expr", // can embed $function/$let — disallow wholesale
  "$lookup", "$graphLookup", "$unionWith",
  "$merge", "$out", "$facet",
]);

const MAX_DEPTH = 12;

/** Validate a query filter, returning a safe copy. Throws ValidationError on a
 * forbidden operator, an unknown operator, or an illegal field path. The input
 * is not mutated. */
export function sanitizeFilter(filter: Filter | null | undefined): Filter {
  if (filter == null) return {};
  return sanitizeDoc(filter, 0) as Filter;
}

function sanitizeDoc(v: unknown, depth: number): unknown {
  if (depth > MAX_DEPTH) {
    throw new ValidationError([], `dopdb: filter nested too deeply (>${MAX_DEPTH})`);
  }
  if (Array.isArray(v)) {
    return v.map((e) => sanitizeDoc(e, depth + 1));
  }
  if (v !== null && typeof v === "object" && !(v instanceof Date)) {
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      checkKey(k);
      out[k] = sanitizeDoc(val, depth + 1);
    }
    return out;
  }
  // scalar leaf (string/number/bool/null/Date) — safe as-is
  return v;
}

function checkKey(k: string): void {
  if (!k.startsWith("$")) {
    // a normal field path — reject dollar signs hidden mid-path
    if (k.includes("$")) {
      throw new ValidationError([], `dopdb: illegal field path "${k}"`);
    }
    return;
  }
  if (FORBIDDEN.has(k)) {
    throw new ValidationError([], `dopdb: operator "${k}" is not permitted`);
  }
  if (!ALLOWED.has(k)) {
    throw new ValidationError([], `dopdb: operator "${k}" is not in the query allowlist`);
  }
}
