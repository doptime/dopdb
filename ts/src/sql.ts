// sql.ts — the SQL front end, the TypeScript twin of Go's sql.go.
//
// WHY IT EXISTS. Once the query moved in-process (query.ts), the filter document
// stopped being a database's native language and became dopdb's own. At that
// point "write the filter as JSON" is a choice, not a constraint — and for a
// human writing an ad-hoc query, or a tool generating one, SQL is the better
// surface. So SQL is a FRONT END: it parses to exactly the same (filter, sort,
// skip, limit, projection) shape `find` already uses, and hands it to the same
// evaluator. No second execution path, no second set of semantics.
//
// WHY HASH ONLY. A Hash collection IS a table: the hash field is the primary key
// and the CBOR value is a row with named columns. The other four collection
// types are not tabular — a list has ordered elements and no columns, a set has
// unordered members, a zset is a fixed (member, score) pair already better
// served by ZRANGEBYSCORE. SQL is rejected on them rather than half-supported.
//
// WHY READ-ONLY. Every write invariant lives in the write commands: owner-scope
// forcing, @-binding, prepareWrite (modifiers, timestamps, validation), unique
// index claims, change publication. A SQL write would bypass or duplicate all of
// it. SELECT is the whole language.
//
// NOT SUPPORTED, deliberately: JOIN/subquery (each collection is authorized
// separately, so a join would read one the caller was never granted);
// GROUP BY/HAVING/SUM/AVG (they do not fit the filter+sort+page shape — COUNT(*)
// does and is supported); AS aliases (the Go engine decodes rows into a typed
// value, where a renamed column has nowhere to land — supporting it here only
// would split the two engines); INSERT/UPDATE/DELETE/DDL.
//
// GRAMMAR — identical to Go's; see sql.go for the full statement of it.

import type { Filter } from "./sanitize.js";
import type { SortKey } from "./query.js";
import { DopdbError } from "./errors.js";

export interface SqlQuery {
  /** The collection named in FROM. Callers MUST check it — see checkTable. */
  table: string;
  /** The compiled WHERE clause ({} when absent). */
  filter: Filter;
  /** Selected columns, as a projection map; undefined for SELECT *. */
  projection?: Record<string, number>;
  sortKeys: SortKey[];
  limit?: number;
  offset?: number;
  /** true for SELECT COUNT(*), which returns a number, not rows. */
  count: boolean;
}

/** Every error the SQL front end raises. It carries 400/"validation" — a
 * statement that will not parse, names the wrong table, or asks for something
 * dopdb does not implement is the caller's mistake, not a server failure. This
 * mirrors Go's ErrSQL sentinel. */
export class SqlError extends DopdbError {
  constructor(message: string) {
    super(message, 400, "validation");
  }
}

/** Does the FROM clause name this collection?
 *
 * Enforcing this is what keeps SQL inside the per-collection permission model:
 * without it, `SELECT * FROM secrets` sent to an allowed collection would read a
 * collection the caller was never granted. */
export function checkTable(q: SqlQuery, coll: string): void {
  if (q.table !== coll) {
    throw new SqlError(`dopdb: SQL FROM "${q.table}" does not match collection "${coll}"`);
  }
}

// ---- lexer ------------------------------------------------------------------

type TokKind = "eof" | "ident" | "number" | "string" | "symbol";
interface Tok { kind: TokKind; text: string; num?: number }

const isIdentStart = (c: string) => /[A-Za-z_]/.test(c);
const isDigit = (c: string) => c >= "0" && c <= "9";

function checkIdent(name: string): void {
  if (!name || !/^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$/.test(name)) {
    throw new SqlError(`dopdb: SQL: illegal identifier "${name}"`);
  }
}

/** Does a '-' here start a negative literal? dopdb's SQL has no arithmetic. */
function wantsValue(prev: Tok[]): boolean {
  if (prev.length === 0) return true;
  const last = prev[prev.length - 1];
  if (last.kind === "symbol") return true;
  if (last.kind === "ident") {
    return ["AND", "OR", "NOT", "IN", "BETWEEN", "LIMIT", "OFFSET"].includes(last.text.toUpperCase());
  }
  return false;
}

function lex(s: string): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (c === " " || c === "\t" || c === "\n" || c === "\r") { i++; continue; }
    if (c === ";") {
      i++;
      while (i < s.length && /\s/.test(s[i])) i++;
      if (i < s.length) throw new SqlError("dopdb: SQL: only one statement is allowed");
      continue;
    }
    if (c === "'") {
      let lit = "";
      let j = i + 1;
      let closed = false;
      while (j < s.length) {
        if (s[j] === "'") {
          if (s[j + 1] === "'") { lit += "'"; j += 2; continue; } // '' escapes a quote
          closed = true; j++; break;
        }
        lit += s[j]; j++;
      }
      if (!closed) throw new SqlError("dopdb: SQL: unterminated string literal");
      out.push({ kind: "string", text: lit });
      i = j;
      continue;
    }
    if (c === '"' || c === "`") {
      const end = s.indexOf(c, i + 1);
      if (end < 0) throw new SqlError("dopdb: SQL: unterminated quoted identifier");
      const name = s.slice(i + 1, end);
      checkIdent(name);
      out.push({ kind: "ident", text: name });
      i = end + 1;
      continue;
    }
    if (isIdentStart(c)) {
      let j = i;
      while (j < s.length && (isIdentStart(s[j]) || isDigit(s[j]))) j++;
      out.push({ kind: "ident", text: s.slice(i, j) });
      i = j;
      continue;
    }
    if (isDigit(c) || (c === "-" && isDigit(s[i + 1] ?? "") && wantsValue(out))) {
      let j = i;
      if (s[j] === "-") j++;
      while (j < s.length && (isDigit(s[j]) || s[j] === ".")) j++;
      if (s[j] === "e" || s[j] === "E") {
        j++;
        if (s[j] === "+" || s[j] === "-") j++;
        while (j < s.length && isDigit(s[j])) j++;
      }
      const raw = s.slice(i, j);
      const n = Number(raw);
      if (!Number.isFinite(n)) throw new SqlError(`dopdb: SQL: bad number "${raw}"`);
      out.push({ kind: "number", text: raw, num: n });
      i = j;
      continue;
    }
    const two = s.slice(i, i + 2);
    if (["<=", ">=", "<>", "!="].includes(two)) { out.push({ kind: "symbol", text: two }); i += 2; continue; }
    if ("=<>(),.*".includes(c)) { out.push({ kind: "symbol", text: c }); i++; continue; }
    throw new SqlError(`dopdb: SQL: unexpected character "${c}"`);
  }
  out.push({ kind: "eof", text: "" });
  return out;
}

// ---- parser -----------------------------------------------------------------

/** Statement keywords dopdb deliberately does not implement. Naming them gives
 * the caller a real answer instead of a parse error. */
const REJECTED: Record<string, string> = {
  INSERT: "SQL is read-only; use hset",
  UPDATE: "SQL is read-only; use hset or hincrby",
  DELETE: "SQL is read-only; use hdel",
  REPLACE: "SQL is read-only; use hset",
  CREATE: "dopdb has no DDL; a collection is declared in code",
  DROP: "dopdb has no DDL; delete rows with hdel",
  ALTER: "dopdb has no DDL",
  TRUNCATE: "dopdb has no DDL; delete rows with hdel",
};

const CLAUSE_KEYWORDS = ["WHERE", "ORDER", "LIMIT", "OFFSET", "GROUP", "HAVING", "JOIN", "UNION"];

class Parser {
  private pos = 0;
  constructor(private readonly toks: Tok[]) {}

  peek(): Tok { return this.toks[this.pos]; }
  next(): Tok { return this.toks[this.pos++]; }
  backup(): void { this.pos--; }
  atEnd(): boolean { return this.peek().kind === "eof"; }

  kw(w: string): boolean {
    const t = this.peek();
    if (t.kind === "ident" && t.text.toUpperCase() === w) { this.pos++; return true; }
    return false;
  }
  sym(w: string): boolean {
    const t = this.peek();
    if (t.kind === "symbol" && t.text === w) { this.pos++; return true; }
    return false;
  }
  expectSym(w: string): void {
    if (!this.sym(w)) throw new SqlError(`dopdb: SQL: expected "${w}", got "${this.peek().text}"`);
  }

  parseSelect(): SqlQuery {
    const first = this.peek();
    if (first.kind === "ident") {
      const why = REJECTED[first.text.toUpperCase()];
      if (why) throw new SqlError(`dopdb: SQL: ${first.text.toUpperCase()} is not supported (${why})`);
    }
    if (!this.kw("SELECT")) throw new SqlError("dopdb: SQL: statement must start with SELECT");

    const q: SqlQuery = { table: "", filter: {}, sortKeys: [], count: false };

    if (this.sym("*")) {
      // all columns
    } else if (this.kw("COUNT")) {
      this.expectSym("(");
      if (!this.sym("*")) throw new SqlError("dopdb: SQL: only COUNT(*) is supported");
      this.expectSym(")");
      q.count = true;
    } else {
      const proj: Record<string, number> = {};
      for (;;) {
        const col = this.parseColumn();
        if (this.peek().kind === "symbol" && this.peek().text === "(") {
          throw new SqlError(`dopdb: SQL: ${col.toUpperCase()}(...) is not supported; COUNT(*) is the only aggregate (aggregation beyond it has no equivalent in the query engine)`);
        }
        if (this.kw("AS")) {
          throw new SqlError("dopdb: SQL: column aliases (AS) are not supported; rows decode into the declared type");
        }
        proj[col] = 1;
        if (!this.sym(",")) break;
      }
      q.projection = proj;
    }

    if (!this.kw("FROM")) throw new SqlError("dopdb: SQL: expected FROM");
    const table = this.parseColumn();
    if (table.includes(".")) throw new SqlError("dopdb: SQL: qualified table names are not supported");
    q.table = table;
    if (this.kw("AS") || (this.peek().kind === "ident" && !CLAUSE_KEYWORDS.includes(this.peek().text.toUpperCase()))) {
      throw new SqlError("dopdb: SQL: table aliases and JOINs are not supported (one collection per query)");
    }
    if (this.sym(",")) throw new SqlError("dopdb: SQL: multiple tables are not supported (one collection per query)");

    if (this.kw("WHERE")) q.filter = this.parseOr();

    if (this.kw("ORDER")) {
      if (!this.kw("BY")) throw new SqlError("dopdb: SQL: expected BY after ORDER");
      if (q.count) throw new SqlError("dopdb: SQL: ORDER BY is meaningless with COUNT(*)");
      for (;;) {
        const field = this.parseColumn();
        let asc = true;
        if (this.kw("DESC")) asc = false;
        else this.kw("ASC");
        q.sortKeys.push({ field, asc });
        if (!this.sym(",")) break;
      }
    }

    if (this.kw("LIMIT")) q.limit = this.parseInt("LIMIT");
    if (this.kw("OFFSET")) q.offset = this.parseInt("OFFSET");

    if (this.kw("GROUP") || this.kw("HAVING")) {
      throw new SqlError("dopdb: SQL: GROUP BY / HAVING are not supported (aggregation beyond COUNT(*) has no equivalent in the query engine)");
    }
    if (this.kw("JOIN") || this.kw("UNION")) {
      throw new SqlError("dopdb: SQL: JOIN / UNION are not supported (each collection is authorized separately)");
    }
    return q;
  }

  parseInt(what: string): number {
    const t = this.next();
    if (t.kind !== "number" || t.num! < 0 || !Number.isInteger(t.num)) {
      throw new SqlError(`dopdb: SQL: ${what} needs a non-negative integer, got "${t.text}"`);
    }
    return t.num!;
  }

  parseColumn(): string {
    const t = this.next();
    if (t.kind !== "ident") throw new SqlError(`dopdb: SQL: expected a column name, got "${t.text}"`);
    let name = t.text;
    while (this.sym(".")) {
      const part = this.next();
      if (part.kind !== "ident") throw new SqlError("dopdb: SQL: expected a field name after '.'");
      name += "." + part.text;
    }
    checkIdent(name);
    return name;
  }

  parseOr(): Filter {
    const left = this.parseAnd();
    const terms: unknown[] = [];
    while (this.kw("OR")) {
      if (terms.length === 0) terms.push(left);
      terms.push(this.parseAnd());
    }
    return terms.length === 0 ? left : ({ $or: terms } as Filter);
  }

  parseAnd(): Filter {
    const left = this.parseNot();
    const terms: unknown[] = [];
    while (this.kw("AND")) {
      if (terms.length === 0) terms.push(left);
      terms.push(this.parseNot());
    }
    // $and rather than key-merging: two predicates on the same column must both
    // hold, and merging would silently drop one.
    return terms.length === 0 ? left : ({ $and: terms } as Filter);
  }

  parseNot(): Filter {
    if (this.kw("NOT")) return { $nor: [this.parseNot()] } as Filter;
    return this.parsePrimary();
  }

  parsePrimary(): Filter {
    if (this.sym("(")) {
      const inner = this.parseOr();
      this.expectSym(")");
      return inner;
    }
    return this.parsePredicate();
  }

  parsePredicate(): Filter {
    const col = this.parseColumn();
    const negate = this.kw("NOT");

    if (this.kw("IN")) {
      this.expectSym("(");
      const vals: unknown[] = [];
      for (;;) {
        vals.push(this.parseLiteral());
        if (!this.sym(",")) break;
      }
      this.expectSym(")");
      return { [col]: { [negate ? "$nin" : "$in"]: vals } } as Filter;
    }

    if (this.kw("BETWEEN")) {
      const lo = this.parseLiteral();
      if (!this.kw("AND")) throw new SqlError("dopdb: SQL: expected AND in BETWEEN");
      const hi = this.parseLiteral();
      const range = { $gte: lo, $lte: hi };
      return (negate ? { $nor: [{ [col]: range }] } : { [col]: range }) as Filter;
    }

    if (this.peek().kind === "ident" && ["LIKE", "ILIKE"].includes(this.peek().text.toUpperCase())) {
      const insensitive = this.next().text.toUpperCase() === "ILIKE";
      const pat = this.parseLiteral();
      if (typeof pat !== "string") throw new SqlError("dopdb: SQL: LIKE needs a string pattern");
      const cond: Record<string, unknown> = { $regex: likeToRegex(pat) };
      if (insensitive) cond.$options = "i";
      return (negate ? { $nor: [{ [col]: cond }] } : { [col]: cond }) as Filter;
    }

    if (this.kw("IS")) {
      if (negate) throw new SqlError("dopdb: SQL: write IS NOT NULL, not NOT ... IS");
      const isNot = this.kw("NOT");
      if (!this.kw("NULL")) throw new SqlError("dopdb: SQL: expected NULL after IS");
      return (isNot ? { [col]: { $ne: null } } : { [col]: null }) as Filter;
    }

    if (negate) throw new SqlError("dopdb: SQL: NOT must be followed by IN, BETWEEN or LIKE");

    const op = this.next();
    if (op.kind !== "symbol") {
      throw new SqlError(`dopdb: SQL: expected a comparison operator after "${col}", got "${op.text}"`);
    }
    const val = this.parseLiteral();
    switch (op.text) {
      case "=":
        // SQL's = NULL is never true; IS NULL is the intended spelling, and
        // saying so beats silently returning nothing.
        if (val === null) throw new SqlError("dopdb: SQL: use IS NULL instead of = NULL");
        return { [col]: val } as Filter;
      case "!=":
      case "<>": return { [col]: { $ne: val } } as Filter;
      case ">": return { [col]: { $gt: val } } as Filter;
      case ">=": return { [col]: { $gte: val } } as Filter;
      case "<": return { [col]: { $lt: val } } as Filter;
      case "<=": return { [col]: { $lte: val } } as Filter;
    }
    throw new SqlError(`dopdb: SQL: unsupported operator "${op.text}"`);
  }

  parseLiteral(): unknown {
    const t = this.next();
    if (t.kind === "string") return t.text;
    if (t.kind === "number") return t.num!;
    if (t.kind === "ident") {
      switch (t.text.toUpperCase()) {
        case "TRUE": return true;
        case "FALSE": return false;
        case "NULL": return null;
      }
      throw new SqlError(`dopdb: SQL: expected a literal value, got identifier "${t.text}" (string literals use single quotes)`);
    }
    throw new SqlError(`dopdb: SQL: expected a literal value, got "${t.text}"`);
  }
}

/** Convert a SQL LIKE pattern to an anchored regular expression. '%' matches any
 * run of characters, '_' exactly one; everything else is literal. */
export function likeToRegex(pat: string): string {
  let r = "^";
  for (const ch of pat) {
    if (ch === "%") r += ".*";
    else if (ch === "_") r += ".";
    else if (".+*?()|[]{}^$\\".includes(ch)) r += "\\" + ch;
    else r += ch;
  }
  return r + "$";
}

/** Parse a single SELECT statement into the engine's own query shape. The
 * compiled filter still passes through sanitizeFilter before it runs. */
export function parseSql(text: string): SqlQuery {
  const p = new Parser(lex(text));
  const q = p.parseSelect();
  if (!p.atEnd()) throw new SqlError(`dopdb: SQL: unexpected "${p.peek().text}" after the statement`);
  return q;
}
