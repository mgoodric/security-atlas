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
// The records are passed as a JSONB array parameter ($1). The author's SQL is
// wrapped so it can only SELECT FROM the `evidence` relation:
//
//	WITH evidence AS (
//	    SELECT (r->>'result')::text AS result,
//	           (r->>'observed_at')::timestamptz AS observed_at,
//	           (r->'payload') AS payload
//	    FROM jsonb_array_elements($1::jsonb) AS r
//	)
//	<author SELECT>
//
// The author never names a base table, so RLS + the read-only txn together
// foreclose cross-tenant / non-evidence reach (P0-495-2).
func evalSQLQuery(ctx context.Context, tx pgx.Tx, expr string, records []inWindowRecord) (string, error) {
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

	wrapped := sqlEvidenceCTE + "\n" + strings.TrimRight(strings.TrimSpace(expr), ";")

	rows, err := sub.Query(ctx, wrapped, recordsJSON)
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
           (r->'payload') AS payload
    FROM jsonb_array_elements($1::jsonb) AS r
)`

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
