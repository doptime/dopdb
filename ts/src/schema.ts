// schema.ts — the single source of truth.
//
// A collection's shape is declared ONCE, in TypeScript. From that one
// declaration you get, with zero codegen:
//   - native types         Infer<typeof X> / InferInput<typeof X>   (the z.infer analogue)
//   - runtime validation   validate() / prepareWrite()              (ported from modifiers.go)
//   - write-time modifiers  trim / lowercase / default / @-binding
//   - index specs          .unique() / .index()
//   - a server spec        toSpec()                                  (schema-as-data)
//
// The same object is imported by the browser (→ fetch) and the server (→ Mongo);
// it is never redefined and never generated.

import { ValidationError, type FieldIssue } from "./errors.js";

// ----------------------------------------------------------------------------
// Field rules (the runtime descriptor) + Field builder (carries the TS type)
// ----------------------------------------------------------------------------

export type FieldKind = "string" | "number" | "boolean" | "date";

export interface FieldRules {
  kind: FieldKind;
  optional: boolean; // type-level optional + validation skips when absent
  required: boolean; // validation: must be present and (for strings) non-empty
  trim: boolean;
  lowercase: boolean;
  uppercase: boolean;
  title?: boolean; // Title Case (lowercase, then capitalize each word)
  counter?: boolean; // numeric: +1 on every write (server-managed)
  createdAt?: boolean; // date: filled with now when absent (server-managed)
  updatedAt?: boolean; // date: set to now on every write (server-managed)
  unique: boolean; // → unique index
  index: false | "asc" | "desc" | "text";
  default?: unknown; // literal value OR a server token: "@uuid" / "@nanoid[N]" / "@field" / "@<claim>"
  bind?: string; // server fills from this context key (a JWT claim or @field/@remoteAddr/...)
  min?: number; // number: value bound; string: length bound
  max?: number;
  pattern?: string; // RegExp source (string fields)
  enumVals?: readonly unknown[];
  ttl?: number; // date fields: expire documents this many seconds after the value
}

function base(kind: FieldKind): FieldRules {
  return {
    kind, optional: false, required: false, trim: false,
    lowercase: false, uppercase: false, unique: false, index: false,
  };
}

/**
 * A typed field. The three phantom type params carry compile-time information:
 *   TOut — the value type
 *   Opt  — true if `.optional()` (optional in both input and output types)
 *   Srv  — true if server-filled (`.bind()` / `.default()`): optional on INPUT only
 */
export class Field<TOut, Opt extends boolean = false, Srv extends boolean = false> {
  declare readonly __t: TOut;
  declare readonly __opt: Opt;
  declare readonly __srv: Srv;
  readonly rules: FieldRules;
  constructor(rules: FieldRules) {
    this.rules = rules;
  }
  private next<R = this>(patch: Partial<FieldRules>): R {
    return new Field({ ...this.rules, ...patch }) as unknown as R;
  }
  optional(): Field<TOut, true, Srv> {
    return this.next({ optional: true });
  }
  required(): this {
    return this.next({ required: true });
  }
  trim(): this {
    return this.next({ trim: true });
  }
  lowercase(): this {
    return this.next({ lowercase: true });
  }
  uppercase(): this {
    return this.next({ uppercase: true });
  }
  /** Title Case on write (lowercase, then capitalize each word). Strings only. */
  title(): this {
    return this.next({ title: true });
  }
  /** Numeric field auto-incremented by 1 on every write (server-managed →
   * optional on InferInput). */
  counter(): Field<TOut, Opt, true> {
    return this.next({ counter: true });
  }
  /** Date field set to "now" when absent on write; preserved if supplied.
   * Server-managed → optional on InferInput. The doptime CreatedAt convention. */
  createdAt(): Field<TOut, Opt, true> {
    return this.next({ createdAt: true });
  }
  /** Date field set to "now" on every write. Server-managed → optional on
   * InferInput. The doptime UpdatedAt convention. */
  updatedAt(): Field<TOut, Opt, true> {
    return this.next({ updatedAt: true });
  }
  unique(): this {
    return this.next({ unique: true, index: this.rules.index || "asc" });
  }
  index(dir: "asc" | "desc" | "text" = "asc"): this {
    return this.next({ index: dir });
  }
  /** Default value or server token ("@uuid", "@nanoid", "@nanoid12"). Marks the
   * field server-filled → optional on InferInput. */
  default(v: TOut | string): Field<TOut, Opt, true> {
    return this.next({ default: v });
  }
  /** Bind to a verified JWT claim (server-controlled). E.g. `.bind("@uid")`
   * fills the field from claim `uid`; any client-supplied value is ignored.
   * Marks the field server-filled → optional on InferInput. */
  bind(claim: string): Field<TOut, Opt, true> {
    return this.next({ bind: claim.startsWith("@") ? claim.slice(1) : claim });
  }
  min(n: number): this {
    return this.next({ min: n });
  }
  max(n: number): this {
    return this.next({ max: n });
  }
  pattern(re: RegExp | string): this {
    return this.next({ pattern: typeof re === "string" ? re : re.source });
  }
  enum<E extends TOut>(...vals: E[]): Field<E, Opt, Srv> {
    return this.next({ enumVals: vals });
  }
  /** TTL: documents expire `seconds` after this date field's value. Implies an
   * index on the field. Only meaningful for `f.date()`. */
  ttl(seconds: number): this {
    return this.next({ ttl: seconds, index: this.rules.index || "asc" });
  }
}

/** Field factory. `f.string()`, `f.number()`, `f.bool()`, `f.date()`. */
export const f = {
  string: () => new Field<string>(base("string")),
  number: () => new Field<number>(base("number")),
  bool: () => new Field<boolean>(base("boolean")),
  date: () => new Field<Date>(base("date")),
};

// ----------------------------------------------------------------------------
// Collection
// ----------------------------------------------------------------------------

export type Shape = Record<string, Field<any, any, any>>;

export interface CollectionOpts {
  name?: string; // collection name (defaults to the variable name via define())
  ownerField?: string; // row-level owner scope field
  db?: string; // non-default datasource/database
}

export class Collection<S extends Shape> {
  readonly shape: S;
  readonly opts: CollectionOpts;
  constructor(shape: S, opts: CollectionOpts = {}) {
    this.shape = shape;
    this.opts = opts;
  }
  named(name: string): Collection<S> {
    return new Collection(this.shape, { ...this.opts, name });
  }
  inDb(db: string): Collection<S> {
    return new Collection(this.shape, { ...this.opts, db });
  }
  /** Declare row-level ownership: every read/write is filtered by
   * `field == <caller's claim>`, so users only ever see their own rows. */
  ownerScope(field: keyof S & string): Collection<S> {
    return new Collection(this.shape, { ...this.opts, ownerField: field });
  }
}

export function collection<S extends Shape>(shape: S): Collection<S> {
  return new Collection(shape);
}

// ----------------------------------------------------------------------------
// Type inference (the z.infer analogue) — zero codegen
// ----------------------------------------------------------------------------

type TypeOf<F> = F extends Field<infer T, any, any> ? T : never;
type IsOpt<F> = F extends Field<any, true, any> ? true : false;
type IsSrv<F> = F extends Field<any, any, true> ? true : false;

type OptKeys<S extends Shape> = { [K in keyof S]: IsOpt<S[K]> extends true ? K : never }[keyof S];
type ReqKeys<S extends Shape> = Exclude<keyof S, OptKeys<S>>;

// input: optional OR server-filled fields are optional; the key field `_id`
// is also optional on input because it is passed as the method argument.
type InOptKeys<S extends Shape> = {
  [K in keyof S]: IsOpt<S[K]> extends true
    ? K
    : IsSrv<S[K]> extends true
      ? K
      : K extends "_id"
        ? K
        : never;
}[keyof S];
type InReqKeys<S extends Shape> = Exclude<keyof S, InOptKeys<S>>;

type Pretty<T> = { [K in keyof T]: T[K] } & {};

/** The stored / returned document type (all non-optional fields present). */
export type Infer<C> = C extends Collection<infer S>
  ? Pretty<{ [K in ReqKeys<S>]: TypeOf<S[K]> } & { [K in OptKeys<S>]?: TypeOf<S[K]> }>
  : never;

/** The write input type (server-filled fields like @-bound owner are optional). */
export type InferInput<C> = C extends Collection<infer S>
  ? Pretty<{ [K in InReqKeys<S>]: TypeOf<S[K]> } & { [K in InOptKeys<S>]?: TypeOf<S[K]> }>
  : never;

// ----------------------------------------------------------------------------
// Tokens (uuid / nanoid) — match the Go alphabet so ids are interchangeable
// ----------------------------------------------------------------------------

const NANO_ALPHABET = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict";

export function nanoid(n = 21): string {
  const bytes = new Uint8Array(n);
  globalThis.crypto.getRandomValues(bytes);
  let s = "";
  for (let i = 0; i < n; i++) s += NANO_ALPHABET[bytes[i] & 63];
  return s;
}

function uuid(): string {
  return globalThis.crypto.randomUUID();
}

/** Resolve an `@`-token against the request context (verified JWT claims plus
 * server context such as @field / @collection / @remoteAddr). `@uuid` and
 * `@nanoid[N]` are generated. A non-`@` value is returned unchanged; an unknown
 * token resolves to `undefined` (callers decide fail-closed). */
export function resolveToken(tok: unknown, ctx: Record<string, unknown> | undefined): unknown {
  if (typeof tok !== "string" || !tok.startsWith("@")) return tok;
  if (tok === "@uuid") return uuid();
  if (tok.startsWith("@nanoid")) {
    const n = parseInt(tok.slice("@nanoid".length), 10);
    return nanoid(Number.isFinite(n) && n > 0 ? n : 21);
  }
  // any other @name resolves from the request context (claims + server context)
  return ctx?.[tok.slice(1)];
}

// ----------------------------------------------------------------------------
// Modifiers + validation (ported from modifiers.go, applied on WRITE)
// ----------------------------------------------------------------------------

function applyStringMods(rules: FieldRules, v: unknown): unknown {
  if (typeof v !== "string") return v;
  let s = v;
  if (rules.trim) s = s.trim();
  if (rules.lowercase) s = s.toLowerCase();
  if (rules.uppercase) s = s.toUpperCase();
  if (rules.title) s = s.toLowerCase().replace(/(^|\s)\S/g, (c) => c.toUpperCase());
  return s;
}

function isAbsent(v: unknown): boolean {
  return v === undefined || v === null;
}

function checkField(name: string, rules: FieldRules, v: unknown, issues: FieldIssue[]): void {
  const add = (message: string): void => {
    issues.push({ field: name, message });
  };

  if (isAbsent(v)) {
    // A non-optional field must be present — except the key field `_id` (passed
    // as the method argument) and server-managed fields (bind/default, filled on
    // the server). Partial mode (handled by the caller) skips this entirely.
    const serverManaged = rules.bind !== undefined || rules.default !== undefined
      || rules.createdAt === true || rules.updatedAt === true || rules.counter === true;
    if (!rules.optional && name !== "_id" && !serverManaged) add("is required");
    return; // absent → nothing more to check
  }
  switch (rules.kind) {
    case "string":
      if (typeof v !== "string") return add("must be a string");
      if (rules.required && v.length === 0) add("must not be empty");
      if (rules.min !== undefined && v.length < rules.min) add(`must be at least ${rules.min} characters`);
      if (rules.max !== undefined && v.length > rules.max) add(`must be at most ${rules.max} characters`);
      if (rules.pattern && !new RegExp(rules.pattern).test(v)) add("has an invalid format");
      break;
    case "number":
      if (typeof v !== "number" || Number.isNaN(v)) return add("must be a number");
      if (rules.min !== undefined && v < rules.min) add(`must be ≥ ${rules.min}`);
      if (rules.max !== undefined && v > rules.max) add(`must be ≤ ${rules.max}`);
      break;
    case "boolean":
      if (typeof v !== "boolean") add("must be a boolean");
      break;
    case "date":
      if (!(v instanceof Date) && typeof v !== "string") add("must be a date");
      break;
  }
  if (rules.enumVals && !rules.enumVals.includes(v)) {
    add(`must be one of: ${rules.enumVals.join(", ")}`);
  }
}

export interface ValidateOpts {
  /** Skip "is required" for absent fields (partial updates). */
  partial?: boolean;
}

/** Validate a value against a collection's shape. Throws ValidationError. */
export function validate<S extends Shape>(
  coll: Collection<S>,
  value: Record<string, unknown>,
  opts: ValidateOpts = {},
): void {
  const issues: FieldIssue[] = [];
  for (const [name, field] of Object.entries(coll.shape)) {
    const rules = field.rules;
    if (opts.partial && isAbsent(value[name])) continue;
    checkField(name, rules, value[name], issues);
  }
  if (issues.length) throw new ValidationError(issues);
}

export interface WriteSide {
  /** HTTP server path: verified JWT claims fill `.bind()` fields and resolve
   * `@`-token defaults. A bound field's client value is overwritten. */
  claims?: Record<string, unknown>;
  /** Full request context for `@`-resolution (preferred over `claims` on the
   * server): the JWT claims merged with server context — `@field` (the resolved
   * record key), `@collection`, `@remoteAddr`, `@host`, `@method`, `@path`,
   * `@rawQuery`. When set, behaves like the http path. */
  ctx?: Record<string, unknown>;
  /** Raw server call (inside an API handler / trusted code): keep the bound
   * field values the caller supplied; fill literal/token defaults; validate
   * fully. No JWT involved. */
  trusted?: boolean;
}

/**
 * Prepare a value for writing: apply string modifiers, fill `.default()` and
 * `.bind()` fields, then validate. Three modes:
 *   - client  (neither): strip bound fields (the client may never set them),
 *     skip server-only defaults, validate the provided fields so bad input
 *     fails before the network. Server-filled fields may be absent.
 *   - http    (claims):  fill bound fields from the JWT authoritatively, fill
 *     defaults, validate fully.
 *   - trusted (trusted): keep caller-supplied bound fields, fill defaults,
 *     validate fully. The dopdb analogue of a raw Collection.HSet in Go.
 */
export function prepareWrite<S extends Shape>(
  coll: Collection<S>,
  input: Record<string, unknown>,
  side: WriteSide = {},
): Record<string, unknown> {
  const rc = side.ctx ?? side.claims; // resolution context (claims + server context)
  const http = rc !== undefined && side.trusted !== true;
  const full = http || side.trusted === true; // server-authoritative pass
  const out: Record<string, unknown> = { ...input };

  for (const [name, field] of Object.entries(coll.shape)) {
    const rules = field.rules;

    if (rules.bind) {
      if (http) {
        const v = rc?.[rules.bind];
        if (isAbsent(v)) {
          throw new ValidationError([{ field: name, message: `missing context "${rules.bind}" for @-binding` }]);
        }
        out[name] = v; // server-controlled: overwrite anything the client sent
      } else if (!side.trusted) {
        delete out[name]; // client may never set a bound field
      }
      // trusted: keep caller-supplied value as-is
      if (!isAbsent(out[name])) out[name] = applyStringMods(rules, out[name]);
      continue;
    }
    if (isAbsent(out[name]) && rules.default !== undefined && full) {
      out[name] = resolveToken(rules.default, rc);
    }
    if (full) {
      // server-managed write-time fields (doptime modifiers, on WRITE)
      if (rules.createdAt && isAbsent(out[name])) out[name] = new Date();
      if (rules.updatedAt) out[name] = new Date(); // always refreshed
      if (rules.counter) out[name] = (typeof out[name] === "number" ? (out[name] as number) : 0) + 1;
    }
    if (!isAbsent(out[name])) out[name] = applyStringMods(rules, out[name]);
  }

  validate(coll, out, { partial: !full });
  return out;
}

// ----------------------------------------------------------------------------
// Server spec (schema-as-data) — consumed by the server to register routes /
// indexes, and available to a non-TS engine. NOT codegen: it is runtime data
// derived from this single TS source, never compiled source in another language.
// ----------------------------------------------------------------------------

export interface FieldSpec {
  name: string;
  kind: FieldKind;
  optional: boolean;
  required: boolean;
  unique: boolean;
  index: false | "asc" | "desc" | "text";
  trim: boolean;
  lowercase: boolean;
  uppercase: boolean;
  title?: boolean;
  counter?: boolean;
  createdAt?: boolean;
  updatedAt?: boolean;
  bind?: string;
  default?: unknown;
  min?: number;
  max?: number;
  pattern?: string;
  enumVals?: readonly unknown[];
  ttl?: number;
}

export interface IndexSpec {
  name: string;
  field: string;
  kind: "asc" | "desc" | "text";
  unique: boolean;
  expireAfterSeconds?: number;
}

export interface CollectionSpec {
  name: string;
  db: string;
  ownerField?: string;
  fields: FieldSpec[];
  indexes: IndexSpec[];
}

/** Emit the data spec for one collection (needs the resolved name). */
export function specOf<S extends Shape>(name: string, coll: Collection<S>): CollectionSpec {
  const fields: FieldSpec[] = [];
  const indexes: IndexSpec[] = [];
  for (const [fname, field] of Object.entries(coll.shape)) {
    const r = field.rules;
    fields.push({
      name: fname, kind: r.kind, optional: r.optional, required: r.required,
      unique: r.unique, index: r.index, trim: r.trim, lowercase: r.lowercase,
      uppercase: r.uppercase, title: r.title, counter: r.counter,
      createdAt: r.createdAt, updatedAt: r.updatedAt, bind: r.bind, default: r.default,
      min: r.min, max: r.max, pattern: r.pattern, enumVals: r.enumVals, ttl: r.ttl,
    });
    if (r.ttl != null) {
      indexes.push({ name: `${fname}_ttl`, field: fname, kind: "asc", unique: false, expireAfterSeconds: r.ttl });
    } else if (r.unique || r.index) {
      indexes.push({
        name: `${fname}_idx`,
        field: fname,
        kind: r.index === "text" ? "text" : r.index === "desc" ? "desc" : "asc",
        unique: r.unique,
      });
    }
  }
  return { name: coll.opts.name ?? name, db: coll.opts.db ?? "default", ownerField: coll.opts.ownerField, fields, indexes };
}

/** The whole schema as one data document: `{ version, collections }`. This is
 * the artifact a non-TS engine (e.g. the optional Go gateway) consumes — it is
 * derived from this single TS source at build time, never compiled source in
 * another language, so it is not a second definition. */
export interface SchemaSpec {
  version: 1;
  collections: CollectionSpec[];
}

export function buildSpec(schema: Record<string, Collection<any>>): SchemaSpec {
  return {
    version: 1,
    collections: Object.keys(schema).map((name) => specOf(name, schema[name])),
  };
}
