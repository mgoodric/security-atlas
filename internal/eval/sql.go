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

// schemaQualifiedRef matches `identifier.identifier` — a schema-qualified
// reference (e.g. `public.evidence_records`, `pg_catalog.pg_authid`). The ONLY
// legitimate dotted reference in an evidence query is `evidence.<column>` (the
// CTE this slice exposes); EVERYTHING else is a reach to a real schema/table
// and is rejected. This is the static half of the table-reachability seal; the
// runtime half is an empty search_path (which alone does not block a
// schema-qualified name — see decisions log 495 D-SQL-2). The two together mean
// the author SQL can name ONLY the `evidence` CTE.
var schemaQualifiedRef = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\.[a-z_"*]`)

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
	for _, m := range schemaQualifiedRef.FindAllStringSubmatch(noTrailing, -1) {
		if strings.ToLower(m[1]) != "evidence" {
			return fmt.Errorf("%w: schema-qualified reference %q is not allowed; query may only read the `evidence` relation", ErrSQLQuery, m[0])
		}
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
		return "", unevaluable(err)
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
			return "", unevaluable(fmt.Errorf("%w: query must SELECT exactly one column, got %d", ErrSQLQuery, len(vals)))
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

// payloadTimeCast matches a cast of a payload text field to a date or time
// type, capturing the field name.
var payloadTimeCast = regexp.MustCompile(`(?i)\(\s*payload\s*->>\s*'([^']+)'\s*\)\s*::\s*(?:timestamptz|timestamp|date|time)\b`)

// SQLUnguardedTimestampCasts returns, in order of first appearance, the
// payload fields a SQL evidence query casts to a date or time type without
// the guard the spec documents: the field matched against RFC3339Pattern
// and accepted by pg_input_is_valid(payload->>'field', 'timestamptz'). A
// cast without both errors the whole query on one malformed record, so one
// bad host makes the control inconclusive and hides every other host
// (#2939). The stock bundles under controls/ are held to it by a test; the
// engine does not refuse a tenant's query on it.
func SQLUnguardedTimestampCasts(expr string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range payloadTimeCast.FindAllStringSubmatch(expr, -1) {
		field := m[1]
		if seen[field] {
			continue
		}
		seen[field] = true
		shape := "payload->>'" + field + "' ~ '" + RFC3339Pattern + "'"
		valid := "pg_input_is_valid(payload->>'" + field + "', 'timestamptz')"
		if !strings.Contains(expr, shape) || !strings.Contains(expr, valid) {
			out = append(out, field)
		}
	}
	return out
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
			return "", unevaluable(fmt.Errorf("%w: result %q is not one of pass|fail|na|inconclusive (or a boolean)", ErrSQLQuery, t))
		}
	case nil:
		return "", unevaluable(fmt.Errorf("%w: result column is NULL", ErrSQLQuery))
	default:
		return "", unevaluable(fmt.Errorf("%w: result column is %T, want text result or boolean", ErrSQLQuery, v))
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
