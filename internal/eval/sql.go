// sql.go — SQL evidence-query evaluation in a read-only, evidence-only sandbox.
//
// A control bundle's `evidence_queries[]` (slice 009) may declare a `sql`-
// language expression over the evidence ledger. When the engine evaluates such
// a control it runs the expression HERE. The sandbox contract mirrors the Rego
// sandbox (rego.go) deny-by-default posture, applied to SQL:
//
//   - The author SQL is NEVER given a raw DB handle or arbitrary-SQL surface
//     over the live schema (anti-criterion P0-495-1). It runs against a SINGLE
//     read-only relation named `evidence`, materialised from the in-window
//     record set the engine already loaded (the same set the Rego / JSON-path
//     paths receive). The records are passed as a JSONB parameter and unnested
//     into the `evidence` CTE — the author SQL cannot name evidence_records,
//     api_keys, users, or any other table (threat-model I / P0-495-2).
//   - It runs in a READ ONLY transaction (SET TRANSACTION READ ONLY) so even a
//     query that somehow reached a base table cannot write — invariant #2 / the
//     evaluator is a read-only consumer of the ledger (P0-495-3).
//   - A static guard rejects multi-statement input, DDL, and DML keywords
//     BEFORE the SQL is sent to the server (defense-in-depth; the read-only txn
//     is the load-bearing control, the guard is the early loud failure).
//   - A per-query statement_timeout bounds runtime; a timeout yields
//     inconclusive with an error, never a hang (threat-model D / AC-3).
//
// EVALUATION INSTANT (#2911, ADR 0028 d1): every row of `evidence` carries
// the same `evaluated_at` timestamptz, the instant the engine evaluates at (the
// as-of instant for a replay, the case's pinned instant for a bundle test). A
// time-window check compares against it, never against now(): the wall clock
// would give a replay or a frozen audit period the wrong answer and make a
// bundle test stop passing once its fixture dates age. The stock-bundle guard
// (SQLUsesWallClock) refuses the wall-clock functions outright.
//
// RESULT CONTRACT (decisions log 495 D-SQL-1): the author SQL must SELECT a
// single column from `evidence`. The column is interpreted as:
//
//   - a TEXT/result value in {pass,fail,na,inconclusive} → used directly; or
//   - a BOOLEAN → true=pass, false=fail.
//
// Zero rows → fail (the asserted condition matched nothing). >1 row → the rows
// are rolled up through computeResult (any fail → fail, else any pass → pass,
// …) so a per-record SQL query is consistent with the Rego / JSON-path /
// per-record rollups (AC-6).
package eval

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrSQLQuery wraps any failure evaluating a SQL evidence query: a static guard
// rejection (multi-statement, DDL/DML), a compile/exec error, a timeout, or a
// result row that is not a usable result value. A SQL query that cannot run
// NEVER silently yields no-result — it returns this error, mapped by the engine
// to an inconclusive control state with the error surfaced.
var ErrSQLQuery = errors.New("eval: sql evidence query failed")

// sqlStatementTimeout bounds each SQL evidence query (threat-model D / AC-3).
// Applied as a Postgres statement_timeout inside the read-only txn AND mirrored
// as a Go context deadline so a hung connection cannot outlive it.
const sqlStatementTimeout = 5 * time.Second

// QueryDryRunTimeout bounds the control-upload dry-run of one declared
// evidence query (#1865). The API server sets no WriteTimeout, so without it a
// non-terminating Rego module would run for as long as the client waits. It
// matches sqlStatementTimeout: a query that cannot finish on an empty ledger
// in the time a SQL query is allowed will not finish on a live one either.
const QueryDryRunTimeout = sqlStatementTimeout

// forbiddenSQLKeyword matches any statement-level keyword that has no place in
// a read-only single-SELECT evidence query. The READ ONLY transaction is the
// load-bearing control; this static guard is the early, loud rejection so an
// author gets a clear error instead of a server-side permission failure. The
// word boundaries avoid false positives on column names like `update_count`.
var forbiddenSQLKeyword = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|copy|merge|call|do|vacuum|analyze|reindex|cluster|comment|lock|set|reset|begin|commit|rollback|savepoint|prepare|execute|deallocate|listen|notify|attach|detach|pg_sleep|dblink|pg_read_file|lo_import|lo_export)\b`)

// schemaQualifiedRef returns the first qualified reference in toks whose
// qualifier is not the `evidence` CTE: an identifier, quoted or not, then a
// `.` token, then an identifier or `*` (e.g. `public.evidence_records`,
// `pg_catalog . pg_authid`, `"public".users`). The ONLY legitimate dotted
// reference in an evidence query is `evidence.<column>` (the CTE this slice
// exposes); EVERYTHING else is a reach to a real schema/table and is
// rejected. This is the static half of the table-reachability seal; the
// runtime half is an empty search_path (which alone does not block a
// schema-qualified name — see decisions log 495 D-SQL-2). The two together
// mean the author SQL can name ONLY the `evidence` CTE.
//
// It reads tokens (sqllex.go), so a dotted word inside a string literal or a
// comment is not a reference (#2973), and whitespace or a comment around the
// dot does not hide one.
func schemaQualifiedRef(toks []sqlToken) (string, bool) {
	for i := 0; i+2 < len(toks); i++ {
		q, dot, name := toks[i], toks[i+1], toks[i+2]
		if (q.kind != tokIdent && q.kind != tokQuotedIdent) || dot.kind != tokPunct || dot.val != "." {
			continue
		}
		if name.kind != tokIdent && name.kind != tokQuotedIdent && !(name.kind == tokOp && strings.HasPrefix(name.val, "*")) {
			continue
		}
		if q.val != "evidence" {
			return q.val + "." + name.val, true
		}
	}
	return "", false
}

// validateSQLEvidenceQuery is the static guard. It rejects empty input,
// multi-statement input, anything that is not a single SELECT/WITH, any
// forbidden keyword, and any schema-qualified reference other than the
// `evidence` CTE. Returns nil only for a single read-only SELECT over the
// `evidence` relation.
func validateSQLEvidenceQuery(expr string) error {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return fmt.Errorf("%w: expression is empty", ErrSQLQuery)
	}
	// Strip a single trailing semicolon (a common, harmless author habit), then
	// reject any remaining semicolon — no statement batching.
	noTrailing := strings.TrimRight(trimmed, ";")
	noTrailing = strings.TrimSpace(noTrailing)
	if strings.Contains(noTrailing, ";") {
		return fmt.Errorf("%w: multiple statements are not allowed", ErrSQLQuery)
	}
	lower := strings.ToLower(noTrailing)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return fmt.Errorf("%w: query must be a single SELECT (or WITH ... SELECT)", ErrSQLQuery)
	}
	if forbiddenSQLKeyword.MatchString(noTrailing) {
		return fmt.Errorf("%w: query contains a forbidden (non-read-only) keyword", ErrSQLQuery)
	}
	toks, err := lexSQL(noTrailing)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSQLQuery, err)
	}
	if ref, ok := schemaQualifiedRef(toks); ok {
		return fmt.Errorf("%w: schema-qualified reference %q is not allowed; query may only read the `evidence` relation", ErrSQLQuery, ref)
	}
	return nil
}

// evalSQLQuery runs a SQL evidence query against an `evidence` CTE built from
// the in-window records, inside a read-only transaction with a statement
// timeout. `tx` is the engine's already-open, tenant-GUC-applied transaction;
// the SQL evaluator runs a NESTED read-only subtransaction (savepoint) so the
// SET TRANSACTION READ ONLY + statement_timeout are scoped to this query and do
// not leak to the engine's writes.
//
// The records are passed as a JSONB array parameter ($1) and the evaluation
// instant as $2, exposed as the constant `evaluated_at` column. The author's SQL is
// wrapped so it can only SELECT FROM the `evidence` relation:
//
//	WITH evidence AS (
//	    SELECT (r->>'result')::text AS result,
//	           (r->>'observed_at')::timestamptz AS observed_at,
//	           (r->'payload') AS payload,
//	           $2::timestamptz AS evaluated_at
//	    FROM jsonb_array_elements($1::jsonb) AS r
//	)
//	<author SELECT>
//
// The author never names a base table, so RLS + the read-only txn together
// foreclose cross-tenant / non-evidence reach (P0-495-2).
func evalSQLQuery(ctx context.Context, tx pgx.Tx, expr string, records []inWindowRecord, evaluatedAt time.Time) (string, error) {
	if err := validateSQLEvidenceQuery(expr); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, sqlStatementTimeout)
	defer cancel()

	recordsJSON, err := inWindowRecordsToJSON(records)
	if err != nil {
		return "", fmt.Errorf("%w: marshal records: %v", ErrSQLQuery, err)
	}

	// Run inside a nested read-only subtransaction so the read-only mode + the
	// statement_timeout are scoped to this query and rolled back afterwards,
	// leaving the engine's outer (writable) transaction untouched.
	sub, err := tx.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: begin subtxn: %v", ErrSQLQuery, err)
	}
	// ALWAYS roll the subtxn back — the SQL evaluator is read-only by
	// construction; there is nothing to commit, and rollback is the belt to the
	// READ ONLY suspenders (P0-495-3).
	defer func() { _ = sub.Rollback(ctx) }()

	if _, err := sub.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
		return "", fmt.Errorf("%w: set read only: %v", ErrSQLQuery, err)
	}
	if _, err := sub.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", sqlStatementTimeout.Milliseconds())); err != nil {
		return "", fmt.Errorf("%w: set statement_timeout: %v", ErrSQLQuery, err)
	}
	// Empty search_path: a bare (unqualified) table name like
	// `evidence_records` no longer resolves to public.*, so the author SQL
	// cannot reach a base table without a schema qualifier — and the static
	// guard (schemaQualifiedRef) already rejected qualified names other than
	// the `evidence` CTE. The CTE name resolves before any schema, so the
	// legitimate `evidence` relation still works. This is the runtime half of
	// the table-reachability seal (P0-495-2). SET LOCAL scopes it to this
	// subtxn only.
	if _, err := sub.Exec(ctx, "SET LOCAL search_path TO ''"); err != nil {
		return "", fmt.Errorf("%w: set search_path: %v", ErrSQLQuery, err)
	}
	// The static guard reads '...' with standard_conforming_strings on
	// (sqllex.go). Pin the server to the same reading, so a deployment
	// configured off cannot end a literal later than the guard did and run a
	// reference the guard read as string content (#2973).
	if _, err := sub.Exec(ctx, "SET LOCAL standard_conforming_strings TO on"); err != nil {
		return "", fmt.Errorf("%w: set standard_conforming_strings: %v", ErrSQLQuery, err)
	}

	wrapped := sqlEvidenceCTE + "\n" + strings.TrimRight(strings.TrimSpace(expr), ";")

	rows, err := sub.Query(ctx, wrapped, recordsJSON, evaluatedAt.UTC())
	if err != nil {
		return "", classifySQLError(err)
	}
	defer rows.Close()

	results := make([]inWindowRecord, 0, len(records))
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return "", fmt.Errorf("%w: scan row: %v", ErrSQLQuery, err)
		}
		if len(vals) != 1 {
			return "", fmt.Errorf("%w: query must SELECT exactly one column, got %d", ErrSQLQuery, len(vals))
		}
		res, err := classifySQLValue(vals[0])
		if err != nil {
			return "", err
		}
		results = append(results, inWindowRecord{result: res})
	}
	if err := rows.Err(); err != nil {
		return "", classifySQLError(err)
	}

	// Zero rows → fail: the asserted condition matched nothing. (No in-window
	// evidence is handled upstream as inconclusive; here a non-empty record set
	// that the query selected nothing from is a genuine fail.)
	if len(results) == 0 {
		return ResultFail, nil
	}
	return computeResult(results), nil
}

// sqlEvidenceCTE is the fixed, author-immutable prelude. The author SELECT is
// appended after it and may only reference the `evidence` relation it defines.
const sqlEvidenceCTE = `WITH evidence AS (
    SELECT (r->>'result')::text AS result,
           (r->>'observed_at')::timestamptz AS observed_at,
           (r->'payload') AS payload,
           $2::timestamptz AS evaluated_at
    FROM jsonb_array_elements($1::jsonb) AS r
)`

// wallClockSQL matches the Postgres functions and special literals that read
// the wall clock: now(), current_timestamp, clock_timestamp() and their
// relatives, and the 'now' / 'today' / 'tomorrow' / 'yesterday' input
// strings. A SQL evidence query that uses one gives a different verdict on
// replay than it gave live (#2911).
var wallClockSQL = regexp.MustCompile(`(?i)\bnow\s*\(|\b(?:current_timestamp|current_date|current_time|localtimestamp|localtime|clock_timestamp|statement_timestamp|transaction_timestamp|timeofday)\b|'\s*(?:now|today|tomorrow|yesterday)\s*'`)

// SQLUsesWallClock reports whether a SQL evidence query reads the wall clock
// instead of the `evaluated_at` column. The stock bundles under controls/ are
// held to it by a test; the engine does not refuse a tenant's query on it.
func SQLUsesWallClock(expr string) bool {
	return wallClockSQL.MatchString(expr)
}

// RFC3339Pattern is the POSIX regex a stock SQL check matches a payload
// timestamp against before casting it: the full RFC 3339 shape, anchored at
// both ends. It still admits a month 13 or a February 30th, so the guard
// pairs it with pg_input_is_valid (docs/spec/control-bundle.md, "Reading a
// payload timestamp"; #2939).
const RFC3339Pattern = `^[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?([Zz]|[+-][0-9]{2}:[0-9]{2})$`

// SQLUnguardedTimestampCasts returns, in order of first appearance, each
// payload text value a SQL evidence query converts to a date or time outside
// the guard the spec documents. Each is rendered as written, minus
// whitespace, e.g. payload->>'last_seen_at' or e.payload#>>'{a,b}'.
//
// A payload text value is payload (optionally alias-qualified) followed by
// any -> / #> steps and a final ->> or #>>. A conversion is (value)::T, or a
// chain such as (value)::text::T, CAST(value AS T), to_timestamp(value...)
// or to_date(value...), where T is timestamptz, timestamp, date, time or
// timetz.
//
// Guarded means structurally guarded (#2973): the conversion sits in the
// THEN branch of a CASE WHEN whose condition, at its own CASE level and
// with no OR or NOT in it, holds both `value ~ '<RFC3339Pattern>'` and
// `pg_input_is_valid(value, 'timestamptz')` for the same value, alias
// included. A guard elsewhere in the query does not count, and neither does
// a WHERE clause, whose conjuncts Postgres may evaluate in any order. A
// conversion without the guard errors the whole query on one malformed
// record, so one bad host makes the control inconclusive and hides every
// other host (#2939).
//
// The stock bundles under controls/ are held to it by a test; the engine
// does not refuse a tenant's query on it. A query the lexer cannot read is
// an error.
func SQLUnguardedTimestampCasts(expr string) ([]string, error) {
	toks, err := lexSQL(expr)
	if err != nil {
		return nil, err
	}

	type frame struct {
		depth     int  // CASE nesting level of this CASE
		inResult  bool // between THEN and the next WHEN / ELSE / END
		condStart int
		guarded   map[string]bool // the current WHEN's guarded values
	}
	var stack []*frame
	depthAt := make([]int, len(toks))
	guards := payloadGuards(toks)

	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(toks); i++ {
		depthAt[i] = len(stack)
		t := toks[i]
		if t.kind == tokIdent {
			switch t.val {
			case "case":
				stack = append(stack, &frame{depth: len(stack) + 1})
				continue
			case "when":
				if len(stack) > 0 {
					f := stack[len(stack)-1]
					f.inResult, f.condStart, f.guarded = false, i+1, nil
				}
				continue
			case "then":
				if len(stack) > 0 {
					f := stack[len(stack)-1]
					f.guarded = guardedInCond(toks, depthAt, guards, f.condStart, i, f.depth)
					f.inResult = true
				}
				continue
			case "else":
				if len(stack) > 0 {
					stack[len(stack)-1].inResult = false
				}
				continue
			case "end":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				continue
			}
		}
		v, end, ok := payloadTextValue(toks, i)
		if !ok {
			continue
		}
		if timeConversion(toks, i, end) {
			covered := false
			for _, f := range stack {
				if f.inResult && f.guarded[v] {
					covered = true
					break
				}
			}
			if !covered && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		// Step past the value; its depth does not change.
		for k := i + 1; k < end; k++ {
			depthAt[k] = len(stack)
		}
		i = end - 1
	}
	return out, nil
}

// payloadTextValue reads a payload text value starting at toks[i] and
// returns its rendering and the index just past it.
func payloadTextValue(toks []sqlToken, i int) (string, int, bool) {
	start := i
	var b strings.Builder
	if i+2 < len(toks) && isIdentTok(toks[i]) && isDot(toks[i+1]) && isPayload(toks[i+2]) {
		b.WriteString(toks[i].val + ".")
		i += 2
	}
	if i >= len(toks) || !isPayload(toks[i]) {
		return "", 0, false
	}
	// Only a payload not itself preceded by a qualifier starts here; the
	// qualified form is read from its qualifier.
	if start == i && i >= 2 && isDot(toks[i-1]) && isIdentTok(toks[i-2]) {
		return "", 0, false
	}
	b.WriteString("payload")
	i++
	for i+1 < len(toks) && toks[i].kind == tokOp {
		op, arg := toks[i].val, toks[i+1]
		if arg.kind != tokString && arg.kind != tokNumber {
			return "", 0, false
		}
		lit := arg.val
		if arg.kind == tokString {
			lit = "'" + strings.ReplaceAll(arg.val, "'", "''") + "'"
		}
		switch op {
		case "->", "#>":
			b.WriteString(op + lit)
			i += 2
		case "->>", "#>>":
			b.WriteString(op + lit)
			return b.String(), i + 2, true
		default:
			return "", 0, false
		}
	}
	return "", 0, false
}

// timeConversion reports whether the payload text value at toks[start:end]
// is converted to a date or time type.
func timeConversion(toks []sqlToken, start, end int) bool {
	// (value)::T, ((value))::text::T
	k := 0
	for start-1-k >= 0 && end+k < len(toks) && isPunct(toks[start-1-k], "(") && isPunct(toks[end+k], ")") {
		k++
	}
	if k > 0 {
		for j := end + k; j+1 < len(toks) && isPunct(toks[j], "::"); j += 2 {
			if isTimeType(toks[j+1]) {
				return true
			}
		}
	}
	if start >= 2 && isPunct(toks[start-1], "(") && toks[start-2].kind == tokIdent {
		switch toks[start-2].val {
		case "cast": // CAST(value AS T)
			return end+1 < len(toks) && toks[end].kind == tokIdent && toks[end].val == "as" && isTimeType(toks[end+1])
		case "to_timestamp", "to_date":
			return true
		}
	}
	return false
}

// guardTerm is one half of the timestamp guard on one payload text value.
type guardTerm struct {
	value string
	shape bool // value ~ RFC3339Pattern; else pg_input_is_valid(value, 'timestamptz')
}

// payloadGuards returns the guard terms in toks, keyed by the index of the
// token each starts at.
func payloadGuards(toks []sqlToken) map[int]guardTerm {
	out := map[int]guardTerm{}
	for i := range toks {
		if v, end, ok := payloadTextValue(toks, i); ok {
			if end+1 < len(toks) && toks[end].kind == tokOp && toks[end].val == "~" &&
				toks[end+1].kind == tokString && toks[end+1].val == RFC3339Pattern {
				out[i] = guardTerm{value: v, shape: true}
			}
		}
		if toks[i].kind == tokIdent && toks[i].val == "pg_input_is_valid" && i+1 < len(toks) && isPunct(toks[i+1], "(") {
			if v, end, ok := payloadTextValue(toks, i+2); ok && end+2 < len(toks) &&
				isPunct(toks[end], ",") && toks[end+1].kind == tokString && toks[end+1].val == "timestamptz" && isPunct(toks[end+2], ")") {
				out[i] = guardTerm{value: v}
			}
		}
	}
	return out
}

// guardedInCond returns the values both halves of the guard cover in the WHEN
// condition toks[from:to] at CASE level depth. A condition with OR or NOT at
// that level guards nothing.
func guardedInCond(toks []sqlToken, depthAt []int, guards map[int]guardTerm, from, to, depth int) map[string]bool {
	shape, valid := map[string]bool{}, map[string]bool{}
	for i := from; i < to; i++ {
		if depthAt[i] != depth {
			continue
		}
		if toks[i].kind == tokIdent && (toks[i].val == "or" || toks[i].val == "not") {
			return nil
		}
		if g, ok := guards[i]; ok {
			if g.shape {
				shape[g.value] = true
			} else {
				valid[g.value] = true
			}
		}
	}
	out := map[string]bool{}
	for v := range shape {
		if valid[v] {
			out[v] = true
		}
	}
	return out
}

func isIdentTok(t sqlToken) bool { return t.kind == tokIdent || t.kind == tokQuotedIdent }
func isDot(t sqlToken) bool      { return isPunct(t, ".") }
func isPayload(t sqlToken) bool  { return isIdentTok(t) && t.val == "payload" }
func isPunct(t sqlToken, v string) bool {
	return t.kind == tokPunct && t.val == v
}

func isTimeType(t sqlToken) bool {
	if !isIdentTok(t) {
		return false
	}
	switch strings.ToLower(t.val) {
	case "timestamptz", "timestamp", "date", "time", "timetz":
		return true
	}
	return false
}

// classifySQLValue maps one selected value to a control result. A bool maps
// true→pass / false→fail; a string must already be a result enum member;
// anything else is an error (loud, not silent).
func classifySQLValue(v interface{}) (string, error) {
	switch t := v.(type) {
	case bool:
		if t {
			return ResultPass, nil
		}
		return ResultFail, nil
	case string:
		switch t {
		case ResultPass, ResultFail, ResultNA, ResultInconclusive:
			return t, nil
		default:
			return "", fmt.Errorf("%w: result %q is not one of pass|fail|na|inconclusive (or a boolean)", ErrSQLQuery, t)
		}
	case nil:
		return "", fmt.Errorf("%w: result column is NULL", ErrSQLQuery)
	default:
		return "", fmt.Errorf("%w: result column is %T, want text result or boolean", ErrSQLQuery, v)
	}
}

// classifySQLError distinguishes a statement_timeout (→ a typed timeout error
// the engine maps to inconclusive) from other exec failures.
func classifySQLError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "statement timeout") || strings.Contains(msg, "57014") || strings.Contains(strings.ToLower(msg), "canceling statement due to") {
		return fmt.Errorf("%w: statement timeout after %s", ErrSQLQuery, sqlStatementTimeout)
	}
	return fmt.Errorf("%w: exec: %v", ErrSQLQuery, err)
}
