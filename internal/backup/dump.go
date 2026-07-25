package backup

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is the narrow read surface the dumper needs. Both *pgxpool.Pool
// and *pgx.Conn satisfy it.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Dump produces a pure-Go logical dump of the database reachable through q
// (D2: no pg_dump shell-out — the atlas runtime is distroless/static with no
// shell, no psql, no pg_dump, and applying the Atlas migration set requires
// the bootstrap container's psql, not the atlas binary).
//
// The dump is SELF-CONTAINED: it captures a CREATE TABLE for every public
// base table (columns + types + nullability + a per-row INSERT data section),
// so the verifier can replay it into an empty ephemeral database with no
// dependency on the migration files. The DDL captured is sufficient for the
// restore-verification smoke check (schema lands, data lands, a sentinel
// query returns); it deliberately does NOT reproduce RLS policies, functions,
// FK constraints, indexes, or sequences — those are not needed to prove a
// backup is faithfully restorable, and reproducing the full catalog is what
// the manual `pg_dump` path (slice-432 runbook) exists for. See the decisions
// log (D2) for the fidelity bound + revisit trigger.
//
// The dump runs as the BYPASSRLS migrator role, so it sees ALL tenants' rows
// — the deliberate RLS-boundary crossing the slice doc names, contained at
// the deployment privilege tier (the caller is the in-process scheduler).
func Dump(ctx context.Context, q querier) ([]byte, error) {
	tables, err := listUserTables(ctx, q)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("-- security-atlas automated logical backup (slice 510)\n")
	buf.WriteString("-- self-contained: schema (DDL) + data; replay into an empty DB.\n")
	// Enum types first — table columns reference them by name (format_type
	// renders the bare enum name), so the type must exist before CREATE TABLE.
	enums, err := enumDDL(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: enum ddl: %w", err)
	}
	buf.WriteString(enums)
	// NOTE: the data section deliberately recreates NO foreign-key
	// constraints (tableDDL emits columns + nullability only), so there is
	// no FK ordering problem and no need for `SET session_replication_role =
	// replica` (which requires superuser/replication privilege the BYPASSRLS
	// migrator role does not hold — and must not be granted). The DDL is
	// sufficient for the restore-verification smoke check; full catalog
	// fidelity (FKs/indexes/policies) is the manual pg_dump path (slice 432).
	// Schema section first so every data INSERT has its table.
	for _, tbl := range tables {
		ddl, derr := tableDDL(ctx, q, tbl)
		if derr != nil {
			return nil, fmt.Errorf("backup: ddl %s: %w", tbl, derr)
		}
		buf.WriteString(ddl)
	}
	// Data section.
	for _, tbl := range tables {
		if err := dumpTableData(ctx, q, &buf, tbl); err != nil {
			return nil, fmt.Errorf("backup: dump table %s: %w", tbl, err)
		}
	}
	return buf.Bytes(), nil
}

// listUserTables returns the public-schema base tables in a stable order.
func listUserTables(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public'
		ORDER BY tablename`)
	if err != nil {
		return nil, fmt.Errorf("backup: list tables: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// enumDDL emits a DROP TYPE + CREATE TYPE for every public-schema enum, with
// its labels in sort order. DO-block-wrapped so a re-run is idempotent. The
// table DDL references these by bare name (via format_type), so they MUST
// precede the CREATE TABLE statements.
func enumDDL(ctx context.Context, q querier) (string, error) {
	rows, err := q.Query(ctx, `
		SELECT t.typname,
		       string_agg(quote_literal(e.enumlabel), ', ' ORDER BY e.enumsortorder)
		FROM pg_catalog.pg_type t
		JOIN pg_catalog.pg_enum e ON e.enumtypid = t.oid
		JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname = 'public'
		GROUP BY t.typname
		ORDER BY t.typname`)
	if err != nil {
		return "", fmt.Errorf("list enums: %w", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var name, labels string
		if err := rows.Scan(&name, &labels); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "DROP TYPE IF EXISTS %s CASCADE;\n", quoteIdent(name))
		fmt.Fprintf(&b, "CREATE TYPE %s AS ENUM (%s);\n", quoteIdent(name), labels)
	}
	return b.String(), rows.Err()
}

type columnDef struct {
	name     string
	dataType string
	nullable bool
}

// tableDDL emits a CREATE TABLE for one table, columns rendered with their
// formatted Postgres type and nullability.
func tableDDL(ctx context.Context, q querier, table string) (string, error) {
	cols, err := tableColumns(ctx, q, table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "DROP TABLE IF EXISTS %s CASCADE;\n", quoteIdent(table))
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", quoteIdent(table))
	for i, c := range cols {
		nn := ""
		if !c.nullable {
			nn = " NOT NULL"
		}
		comma := ","
		if i == len(cols)-1 {
			comma = ""
		}
		fmt.Fprintf(&b, "    %s %s%s%s\n", quoteIdent(c.name), c.dataType, nn, comma)
	}
	b.WriteString(");\n")
	return b.String(), nil
}

// tableColumns returns ordered columns with their formatted type +
// nullability. format_type renders the exact Postgres type (incl. enums,
// numeric(p,s), arrays).
func tableColumns(ctx context.Context, q querier, table string) ([]columnDef, error) {
	rows, err := q.Query(ctx, `
		SELECT a.attname,
		       pg_catalog.format_type(a.atttypid, a.atttypmod),
		       NOT a.attnotnull
		FROM pg_catalog.pg_attribute a
		JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = $1
		  AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, table)
	if err != nil {
		return nil, fmt.Errorf("list columns: %w", err)
	}
	defer rows.Close()
	var out []columnDef
	for rows.Next() {
		var c columnDef
		if err := rows.Scan(&c.name, &c.dataType, &c.nullable); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// dumpTableData emits per-row INSERT statements for one table.
func dumpTableData(ctx context.Context, q querier, buf *bytes.Buffer, table string) error {
	cols, err := tableColumns(ctx, q, table)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return nil
	}
	qt := quoteIdent(table)
	colList := make([]string, len(cols))
	for i, c := range cols {
		colList[i] = quoteIdent(c.name)
	}
	selectCols := strings.Join(colList, ", ")

	rows, err := q.Query(ctx, fmt.Sprintf("SELECT %s FROM %s", selectCols, qt)) //nolint:gosec // idents quoted from pg_catalog
	if err != nil {
		return fmt.Errorf("select rows: %w", err)
	}
	defer rows.Close()

	insertPrefix := fmt.Sprintf("INSERT INTO %s (%s) VALUES ", qt, selectCols)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return fmt.Errorf("row values: %w", err)
		}
		lits := make([]string, len(vals))
		for i, v := range vals {
			// Pass the column's declared type so the literal is rendered +
			// cast correctly (a text[] decodes to []any just like a jsonb,
			// so the value alone is ambiguous — the column type disambiguates).
			lits[i] = sqlLiteralForType(v, cols[i].dataType)
		}
		fmt.Fprintf(buf, "%s(%s);\n", insertPrefix, strings.Join(lits, ", "))
	}
	return rows.Err()
}
