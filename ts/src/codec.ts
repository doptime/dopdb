// codec.ts — the storage format: CBOR (RFC 8949).
//
// The Mongo build let the driver decide (BSON). KVRocks stores opaque bytes, so
// dopdb owns the value format outright and uses CBOR: compact, self-describing,
// and — after canonicalisation — deterministic, which matters here because Set
// members are deduplicated by their encoded bytes and unique-index claims are
// keyed by an encoded value.
//
// Server-only: this module is imported by server.ts, never by client.ts or
// index.ts, so the browser bundle stays free of it (see test/browser-safety).

import { encode as cborEncode, decode as cborDecode } from "cbor-x";

/** Recursively sort object keys so two equal values encode to identical bytes.
 * cbor-x preserves insertion order; canonicalising first is what makes SADD
 * deduplication and unique-index claims correct. */
export function canonical(v: unknown): unknown {
  if (v === null || typeof v !== "object") return v;
  if (Array.isArray(v)) return v.map(canonical);
  if (v instanceof Date) return v;
  const src = v as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const k of Object.keys(src).sort()) out[k] = canonical(src[k]);
  return out;
}

/** Encode a value to its stored representation. */
export function encodeValue(v: unknown): Buffer {
  return Buffer.from(cborEncode(canonical(v)) as Uint8Array);
}

/** Decode stored bytes back into a value. */
export function decodeValue(b: Buffer | Uint8Array): unknown {
  return cborDecode(b as Uint8Array);
}

/** Decode a stored document, which is always an object. */
export function decodeDoc(b: Buffer | Uint8Array): Record<string, unknown> {
  const v = decodeValue(b);
  return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

/** The hash-field key a unique index uses to claim one value. Returns null for
 * an absent value: a missing field claims nothing (sparse behaviour). */
export function uniqueSlot(v: unknown): string | null {
  if (v === undefined || v === null) return null;
  return encodeValue(v).toString("base64");
}
