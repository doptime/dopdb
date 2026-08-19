package dopdb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ----------------------------------------------------------------------------
// SQL.
//
// WHY IT EXISTS. Once the query moved in-process (query.go), the filter document
// stopped being a database's native language and became dopdb's own. At that
// point "write the filter as JSON" is a choice, not a constraint — and for a
// human writing an ad-hoc query, or a tool generating one, SQL is the better
// surface. So SQL is a FRONT END: it parses to exactly the same (filter, sort,
// skip, limit, projection) shape FIND already uses, and hands it to the same
// evaluator. There is no second execution path, no second set of semantics, and
// nothing here that FIND cannot already do.
//
// WHY HASH ONLY. A Hash collection IS a table: the hash field is the primary
// key and the CBOR value is a row with named columns. SELECT/WHERE/ORDER BY map
// onto it exactly. The other four collection types are not tabular — a list has
// ordered elements and no columns, a set has unordered members, a zset is a
// fixed (member, score) pair already better served by ZRANGEBYSCORE. Putting SQL
// on them would mean inventing a fake schema to query it with, and the result
// would be strictly less expressive than the native command. So SQL is rejected
// on those collections rather than half-supported.
//
// WHY READ-ONLY. Every write invariant lives in the write commands: owner-scope
// forcing, @-binding, the write plan (modifiers, timestamps, validation), unique
// index claims, change publication. A SQL INSERT/UPDATE/DELETE would either
// bypass all of that or duplicate it. SELECT is the whole language.
//
// NOT SUPPORTED, deliberately, each with a reason:
//   - JOIN / subquery — a collection's permission bitmask is per collection.
//     A join would read a second collection the caller was never granted.
//   - GROUP BY / HAVING / SUM / AVG — these do not fit the (filter, sort, page)
//     shape, so they would need a second execution path. COUNT(*) does fit and
//     is supported.
//   - AS aliases — the Go engine decodes rows into the typed V; a renamed column
//     has nowhere to land. Supporting it in TypeScript only would split the two
//     engines, which is worse than not having it.
//   - INSERT / UPDATE / DELETE / DDL — see above.
//
// GRAMMAR
//
//	SELECT * | COUNT(*) | col [, col ...]
//	FROM <collection>
//	[WHERE <expr>]
//	[ORDER BY col [ASC|DESC] [, ...]]
//	[LIMIT <n>] [OFFSET <n>]
//
//	<expr> := <expr> OR <expr> | <expr> AND <expr> | NOT <expr> | ( <expr> )
//	        | col <op> <literal>            -- = != <> < <= > >=
//	        | col [NOT] IN ( <literal>, ...)
//	        | col [NOT] BETWEEN <lit> AND <lit>
//	        | col [NOT] LIKE <string>       -- % and _ wildcards; ILIKE for
//	                                        -- case-insensitive
//	        | col IS [NOT] NULL
//
// Column references may be dotted paths into a nested document (`addr.city`),
// and `_id` refers to the document key. Identifiers may be quoted with double
// quotes or backticks. String literals use single quotes, with '' for a literal
// quote.
// ----------------------------------------------------------------------------

// ErrSQL marks every error produced by the SQL front end. The HTTP layer matches
// it with errors.Is to answer 400 rather than 500: a statement that will not
// parse, names the wrong table, or asks for something dopdb does not implement is
// the caller's mistake, not a server failure.
var ErrSQL = errors.New("dopdb: SQL")

// sqlErr wraps a message so it satisfies errors.Is(err, ErrSQL).
func sqlErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSQL, fmt.Sprintf(format, args...))
}

// SQLQuery is a parsed SELECT, already compiled into the engine's own shape.
type SQLQuery struct {
	// Table is the collection named in FROM. The caller MUST check it against
	// the collection it is serving — see CheckTable.
	Table string
	// Filter is the compiled WHERE clause ({} when absent).
	Filter M
	// Opt carries ORDER BY / LIMIT / OFFSET / the selected columns.
	Opt FindOpt
	// Count is true for SELECT COUNT(*), which returns a number, not rows.
	Count bool
}

// CheckTable reports whether the FROM clause names the given collection.
// Enforcing this is what keeps SQL inside the per-collection permission model:
// without it, `SELECT * FROM secrets` sent to an allowed collection would read a
// collection the caller was never granted.
func (q *SQLQuery) CheckTable(coll string) error {
	if q.Table != coll {
		return sqlErr("FROM %q does not match collection %q", q.Table, coll)
	}
	return nil
}

// ParseSQL parses a single SELECT statement. The returned query is engine-neutral;
// it still passes through SanitizeFilter before it runs.
func ParseSQL(text string) (*SQLQuery, error) {
	toks, err := sqlLex(text)
	if err != nil {
		return nil, err
	}
	p := &sqlParser{toks: toks}
	q, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	if !p.atEnd() {
		return nil, sqlErr("unexpected %q after the statement", p.peek().text)
	}
	return q, nil
}

// ---- lexer ------------------------------------------------------------------

type sqlTokKind int

const (
	tkEOF sqlTokKind = iota
	tkIdent
	tkNumber
	tkString
	tkSymbol // operators and punctuation
)

type sqlTok struct {
	kind sqlTokKind
	text string // for tkIdent: the raw identifier; keywords compared case-insensitively
	num  float64
}

func sqlLex(s string) ([]sqlTok, error) {
	var out []sqlTok
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == ';':
			// a single trailing semicolon is tolerated; anything after it is not
			i++
			for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
				i++
			}
			if i < len(s) {
				return nil, sqlErr("only one statement is allowed")
			}
		case c == '\'':
			lit, n, err := sqlString(s[i:])
			if err != nil {
				return nil, err
			}
			out = append(out, sqlTok{kind: tkString, text: lit})
			i += n
		case c == '"' || c == '`':
			quote := c
			j := i + 1
			for j < len(s) && s[j] != quote {
				j++
			}
			if j >= len(s) {
				return nil, sqlErr("unterminated quoted identifier")
			}
			name := s[i+1 : j]
			if err := checkSQLIdent(name); err != nil {
				return nil, err
			}
			out = append(out, sqlTok{kind: tkIdent, text: name})
			i = j + 1
		case isIdentStart(c):
			j := i
			for j < len(s) && (isIdentStart(s[j]) || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			out = append(out, sqlTok{kind: tkIdent, text: s[i:j]})
			i = j
		case c >= '0' && c <= '9' || (c == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && wantsValue(out)):
			j := i
			if s[j] == '-' {
				j++
			}
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
				j++
				if j < len(s) && (s[j] == '+' || s[j] == '-') {
					j++
				}
				for j < len(s) && s[j] >= '0' && s[j] <= '9' {
					j++
				}
			}
			f, err := strconv.ParseFloat(s[i:j], 64)
			if err != nil {
				return nil, sqlErr("bad number %q", s[i:j])
			}
			out = append(out, sqlTok{kind: tkNumber, text: s[i:j], num: f})
			i = j
		default:
			// two-character operators first
			if i+1 < len(s) {
				two := s[i : i+2]
				if two == "<=" || two == ">=" || two == "<>" || two == "!=" {
					out = append(out, sqlTok{kind: tkSymbol, text: two})
					i += 2
					continue
				}
			}
			if strings.IndexByte("=<>(),.*", c) >= 0 {
				out = append(out, sqlTok{kind: tkSymbol, text: string(c)})
				i++
				continue
			}
			return nil, sqlErr("unexpected character %q", string(c))
		}
	}
	out = append(out, sqlTok{kind: tkEOF})
	return out, nil
}

// wantsValue reports whether a '-' at this point starts a negative literal
// rather than being stray punctuation (dopdb's SQL has no arithmetic).
func wantsValue(prev []sqlTok) bool {
	if len(prev) == 0 {
		return true
	}
	last := prev[len(prev)-1]
	if last.kind == tkSymbol {
		return true
	}
	if last.kind == tkIdent {
		switch strings.ToUpper(last.text) {
		case "AND", "OR", "NOT", "IN", "BETWEEN", "LIMIT", "OFFSET":
			return true
		}
	}
	return false
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func checkSQLIdent(name string) error {
	if name == "" {
		return sqlErr("empty identifier")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isIdentStart(c) || (c >= '0' && c <= '9') || c == '.' {
			continue
		}
		return sqlErr("illegal identifier %q", name)
	}
	return nil
}

func sqlString(s string) (string, int, error) {
	var b strings.Builder
	i := 1
	for i < len(s) {
		if s[i] == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' { // '' is an escaped quote
				b.WriteByte('\'')
				i += 2
				continue
			}
			return b.String(), i + 1, nil
		}
		b.WriteByte(s[i])
		i++
	}
	return "", 0, sqlErr("unterminated string literal")
}

// ---- parser -----------------------------------------------------------------

// maxSQLDepth bounds expression nesting.
//
// parsePrimary recurses through parseOr/parseAnd/parseNot for every '(' — about
// four stack frames per level — and the request body limit allows on the order of
// a quarter-million of them. A single POST of nested parentheses would grow one
// goroutine's stack into the tens of megabytes, and a handful of concurrent ones
// would take the process out. FIND's JSON filter has had a depth limit all along;
// SQL had none.
const maxSQLDepth = 64

type sqlParser struct {
	toks  []sqlTok
	pos   int
	depth int
}

// enter/leave track expression nesting so the parser fails with a syntax error
// instead of a stack overflow.
func (p *sqlParser) enter() error {
	p.depth++
	if p.depth > maxSQLDepth {
		return sqlErr("expression nested deeper than %d levels", maxSQLDepth)
	}
	return nil
}

func (p *sqlParser) leave() { p.depth-- }

func (p *sqlParser) peek() sqlTok { return p.toks[p.pos] }
func (p *sqlParser) next() sqlTok { t := p.toks[p.pos]; p.pos++; return t }
func (p *sqlParser) atEnd() bool  { return p.peek().kind == tkEOF }
func (p *sqlParser) backup()      { p.pos-- }
func (p *sqlParser) kw(w string) bool {
	t := p.peek()
	if t.kind == tkIdent && strings.EqualFold(t.text, w) {
		p.pos++
		return true
	}
	return false
}
func (p *sqlParser) sym(w string) bool {
	t := p.peek()
	if t.kind == tkSymbol && t.text == w {
		p.pos++
		return true
	}
	return false
}
func (p *sqlParser) expectSym(w string) error {
	if !p.sym(w) {
		return sqlErr("expected %q, got %q", w, p.peek().text)
	}
	return nil
}

// rejected are the statement keywords dopdb deliberately does not implement.
// Naming them gives a caller a real answer instead of a parse error.
var sqlRejected = map[string]string{
	"INSERT":   "SQL is read-only; use HSET",
	"UPDATE":   "SQL is read-only; use HSET or HINCRBY",
	"DELETE":   "SQL is read-only; use HDEL",
	"REPLACE":  "SQL is read-only; use HSET",
	"CREATE":   "dopdb has no DDL; a collection is declared in code",
	"DROP":     "dopdb has no DDL; delete rows with HDEL",
	"ALTER":    "dopdb has no DDL",
	"TRUNCATE": "dopdb has no DDL; delete rows with HDEL",
}

func (p *sqlParser) parseSelect() (*SQLQuery, error) {
	if t := p.peek(); t.kind == tkIdent {
		if why, bad := sqlRejected[strings.ToUpper(t.text)]; bad {
			return nil, sqlErr("%s is not supported (%s)", strings.ToUpper(t.text), why)
		}
	}
	if !p.kw("SELECT") {
		return nil, sqlErr("statement must start with SELECT")
	}
	q := &SQLQuery{Filter: M{}}

	// projection
	switch {
	case p.sym("*"):
		// all columns
	case p.kw("COUNT"):
		if err := p.expectSym("("); err != nil {
			return nil, err
		}
		if !p.sym("*") {
			return nil, sqlErr("only COUNT(*) is supported")
		}
		if err := p.expectSym(")"); err != nil {
			return nil, err
		}
		q.Count = true
	default:
		proj := M{}
		for {
			col, err := p.parseColumn()
			if err != nil {
				return nil, err
			}
			if p.peek().kind == tkSymbol && p.peek().text == "(" {
				// an aggregate other than COUNT(*): say so, rather than letting
				// it fail later as a confusing syntax error
				return nil, sqlErr("%s(...) is not supported; COUNT(*) is the only aggregate (aggregation beyond it has no equivalent in the query engine)", strings.ToUpper(col))
			}
			if p.kw("AS") {
				return nil, sqlErr("column aliases (AS) are not supported; rows decode into the declared type")
			}
			proj[col] = 1
			if !p.sym(",") {
				break
			}
		}
		q.Opt.Projection = proj
	}

	if !p.kw("FROM") {
		return nil, sqlErr("expected FROM")
	}
	table, err := p.parseColumn()
	if err != nil {
		return nil, err
	}
	if strings.Contains(table, ".") {
		return nil, sqlErr("qualified table names are not supported")
	}
	q.Table = table
	if p.kw("AS") || (p.peek().kind == tkIdent && !isClauseKeyword(p.peek().text)) {
		return nil, sqlErr("table aliases and JOINs are not supported (one collection per query)")
	}
	if p.sym(",") {
		return nil, sqlErr("multiple tables are not supported (one collection per query)")
	}

	if p.kw("WHERE") {
		f, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		q.Filter = f
	}

	if p.kw("ORDER") {
		if !p.kw("BY") {
			return nil, sqlErr("expected BY after ORDER")
		}
		if q.Count {
			return nil, sqlErr("ORDER BY is meaningless with COUNT(*)")
		}
		for {
			col, err := p.parseColumn()
			if err != nil {
				return nil, err
			}
			asc := true
			if p.kw("DESC") {
				asc = false
			} else {
				p.kw("ASC")
			}
			q.Opt.SortKeys = append(q.Opt.SortKeys, SortKey{Field: col, Asc: asc})
			if !p.sym(",") {
				break
			}
		}
	}

	if p.kw("LIMIT") {
		n, err := p.parseInt("LIMIT")
		if err != nil {
			return nil, err
		}
		q.Opt.Limit = n
	}
	if p.kw("OFFSET") {
		n, err := p.parseInt("OFFSET")
		if err != nil {
			return nil, err
		}
		q.Opt.Skip = n
	}
	if p.kw("GROUP") || p.kw("HAVING") {
		return nil, sqlErr("GROUP BY / HAVING are not supported (aggregation beyond COUNT(*) has no equivalent in the query engine)")
	}
	if p.kw("JOIN") || p.kw("UNION") {
		return nil, sqlErr("JOIN / UNION are not supported (each collection is authorized separately)")
	}
	return q, nil
}

func isClauseKeyword(w string) bool {
	switch strings.ToUpper(w) {
	case "WHERE", "ORDER", "LIMIT", "OFFSET", "GROUP", "HAVING", "JOIN", "UNION":
		return true
	}
	return false
}

func (p *sqlParser) parseInt(what string) (int64, error) {
	t := p.next()
	if t.kind != tkNumber || t.num < 0 || t.num != float64(int64(t.num)) {
		return 0, sqlErr("%s needs a non-negative integer, got %q", what, t.text)
	}
	return int64(t.num), nil
}

// parseColumn reads a (possibly dotted) column path.
func (p *sqlParser) parseColumn() (string, error) {
	t := p.next()
	if t.kind != tkIdent {
		return "", sqlErr("expected a column name, got %q", t.text)
	}
	name := t.text
	for p.sym(".") {
		part := p.next()
		if part.kind != tkIdent {
			return "", sqlErr("expected a field name after '.'")
		}
		name += "." + part.text
	}
	if err := checkSQLIdent(name); err != nil {
		return "", err
	}
	return name, nil
}

// ---- expression → filter ----------------------------------------------------

func (p *sqlParser) parseOr() (M, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	var terms []any
	for p.kw("OR") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		if terms == nil {
			terms = []any{map[string]any(left)}
		}
		terms = append(terms, map[string]any(right))
	}
	if terms == nil {
		return left, nil
	}
	return M{"$or": terms}, nil
}

func (p *sqlParser) parseAnd() (M, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	var terms []any
	for p.kw("AND") {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		if terms == nil {
			terms = []any{map[string]any(left)}
		}
		terms = append(terms, map[string]any(right))
	}
	if terms == nil {
		return left, nil
	}
	// $and rather than key-merging: two predicates on the same column must both
	// hold, and merging would silently drop one.
	return M{"$and": terms}, nil
}

func (p *sqlParser) parseNot() (M, error) {
	if p.kw("NOT") {
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return M{"$nor": []any{map[string]any(inner)}}, nil
	}
	return p.parsePrimary()
}

func (p *sqlParser) parsePrimary() (M, error) {
	if p.sym("(") {
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expectSym(")"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	return p.parsePredicate()
}

func (p *sqlParser) parsePredicate() (M, error) {
	col, err := p.parseColumn()
	if err != nil {
		return nil, err
	}

	negate := false
	if p.kw("NOT") {
		negate = true
	}

	switch {
	case p.kw("IN"):
		if err := p.expectSym("("); err != nil {
			return nil, err
		}
		var vals []any
		for {
			v, err := p.parseLiteral()
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
			if !p.sym(",") {
				break
			}
		}
		if err := p.expectSym(")"); err != nil {
			return nil, err
		}
		op := "$in"
		if negate {
			op = "$nin"
		}
		return M{col: M{op: vals}}, nil

	case p.kw("BETWEEN"):
		lo, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		if !p.kw("AND") {
			return nil, sqlErr("expected AND in BETWEEN")
		}
		hi, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		rng := M{"$gte": lo, "$lte": hi}
		if negate {
			return M{"$nor": []any{map[string]any{col: map[string]any(rng)}}}, nil
		}
		return M{col: rng}, nil

	case p.kw("LIKE"), p.kw("ILIKE"):
		p.backup()
		insensitive := strings.EqualFold(p.next().text, "ILIKE")
		lit, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		pat, ok := lit.(string)
		if !ok {
			return nil, sqlErr("LIKE needs a string pattern")
		}
		cond := M{"$regex": likeToRegex(pat)}
		if insensitive {
			cond["$options"] = "i"
		}
		if negate {
			return M{"$nor": []any{map[string]any{col: map[string]any(cond)}}}, nil
		}
		return M{col: cond}, nil

	case p.kw("IS"):
		if negate {
			return nil, sqlErr("write IS NOT NULL, not NOT ... IS")
		}
		isNot := p.kw("NOT")
		if !p.kw("NULL") {
			return nil, sqlErr("expected NULL after IS")
		}
		if isNot {
			return M{col: M{"$ne": nil}}, nil
		}
		return M{col: nil}, nil
	}

	if negate {
		return nil, sqlErr("NOT must be followed by IN, BETWEEN or LIKE")
	}

	// comparison operator
	t := p.next()
	if t.kind != tkSymbol {
		return nil, sqlErr("expected a comparison operator after %q, got %q", col, t.text)
	}
	val, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	switch t.text {
	case "=":
		if val == nil {
			// SQL's = NULL is never true; IS NULL is the intended spelling and
			// saying so beats silently returning nothing.
			return nil, sqlErr("use IS NULL instead of = NULL")
		}
		return M{col: val}, nil
	case "!=", "<>":
		return M{col: M{"$ne": val}}, nil
	case ">":
		return M{col: M{"$gt": val}}, nil
	case ">=":
		return M{col: M{"$gte": val}}, nil
	case "<":
		return M{col: M{"$lt": val}}, nil
	case "<=":
		return M{col: M{"$lte": val}}, nil
	}
	return nil, sqlErr("unsupported operator %q", t.text)
}

func (p *sqlParser) parseLiteral() (any, error) {
	t := p.next()
	switch t.kind {
	case tkString:
		return t.text, nil
	case tkNumber:
		return t.num, nil
	case tkIdent:
		switch strings.ToUpper(t.text) {
		case "TRUE":
			return true, nil
		case "FALSE":
			return false, nil
		case "NULL":
			return nil, nil
		}
		return nil, sqlErr("expected a literal value, got identifier %q (string literals use single quotes)", t.text)
	}
	return nil, sqlErr("expected a literal value, got %q", t.text)
}

// likeToRegex converts a SQL LIKE pattern to an anchored regular expression.
// '%' matches any run of characters, '_' matches exactly one; everything else is
// literal.
func likeToRegex(pat string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pat {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		case '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '^', '$', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("$")
	return b.String()
}

// ---- native API -------------------------------------------------------------

// Query runs a SELECT against this collection and returns the matching values.
// The FROM clause must name this collection. Use QueryCount for SELECT COUNT(*).
//
//	users.Query("SELECT * FROM users WHERE age >= 18 ORDER BY name LIMIT 20")
//
// This is the trusted path: no owner-scope and no limit clamping, exactly like
// Find. The HTTP path (command SQL) adds both.
func (c *Collection[K, V]) Query(sqlText string) ([]V, error) {
	q, err := ParseSQL(sqlText)
	if err != nil {
		return nil, err
	}
	if err := q.CheckTable(c.coll); err != nil {
		return nil, err
	}
	if q.Count {
		return nil, fmt.Errorf("dopdb: Query got SELECT COUNT(*); use QueryCount")
	}
	return c.Find(q.Filter, q.Opt)
}

// QueryCount runs a SELECT COUNT(*) and returns the number of matching rows.
func (c *Collection[K, V]) QueryCount(sqlText string) (int64, error) {
	q, err := ParseSQL(sqlText)
	if err != nil {
		return 0, err
	}
	if err := q.CheckTable(c.coll); err != nil {
		return 0, err
	}
	if !q.Count {
		return 0, fmt.Errorf("dopdb: QueryCount needs SELECT COUNT(*); use Query")
	}
	safe, err := SanitizeFilter(q.Filter)
	if err != nil {
		return 0, err
	}
	return c.backend("").countFilter(context.Background(), c.coll, safe)
}
