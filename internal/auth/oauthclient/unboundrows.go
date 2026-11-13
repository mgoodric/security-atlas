package oauthclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Rows in the all-zeros tenant (security-atlas#1876).
//
// Before OAuth clients were bound to a tenant (#1739), a client_credentials
// token from an unbound client carried uuid.Nil as its tenant, and its
// writes (bootstrap.sh's `atlas-cli controls upload`, an evidence push)
// landed in the all-zeros tenant: invisible to every user, and readable by
// every other unbound client token. tenancymw now refuses those writes, but
// rows already written stay. ListUnboundRows finds them and PurgeUnboundRows
// removes the ones that may be removed.
//
// Append-only ledger tables are never purged. ADR-0012 allows disposal from
// the evidence ledger only by tombstone, and ADR-0020 rules out raw
// BYPASSRLS deletes from it. A ledger table is one the application itself
// cannot delete from: no permissive RLS policy grants DELETE, atlas_app
// holds no DELETE privilege on it, or a BEFORE DELETE trigger refuses it.
// A table whose all-zeros rows a retained table still
// references, by any foreign key, is retained too: deleting it would either
// fail, or cascade into the ledger (ON DELETE CASCADE / SET NULL). Retained
// tables are listed, with the reason, and are not counted toward a purge.
//
// Both run on a caller-supplied transaction over the BYPASSRLS
// (atlas_migrate) connection: every tenant-scoped table is under FORCE ROW
// LEVEL SECURITY, so an RLS-bound role sees none of these rows. The caller
// commits or rolls back. A purge writes its own tenant_data_purges row in
// that transaction, so a committed purge always leaves a record.
//
// There is no in-place reassign. Tenant-scoped tables reference each other
// through composite (tenant_id, id) foreign keys, so moving one row means
// moving every row it references in the same statement, and a moved row can
// collide with a target-tenant row on a (tenant_id, ...) unique key. The
// operator re-creates the data under a bound client and then purges; see
// docs-site/docs/migration/oauth.md.

// pgErrForeignKeyViolation is the SQLSTATE a DELETE raises while a row in
// another table still references the deleted row.
const pgErrForeignKeyViolation = "23503"

// RetainedLedger is the reason given for an append-only ledger table.
const RetainedLedger = "append-only ledger (ADR-0012, tombstone only)"

// UnboundTableCount is one tenant-scoped table and how many of its rows
// carry the all-zeros tenant. Retained is empty when a purge deletes the
// rows, and otherwise says why it keeps them.
type UnboundTableCount struct {
	Table    string `json:"table"`
	Rows     int64  `json:"rows"`
	Retained string `json:"retained,omitempty"`
}

// PurgeResult is what a committed purge did.
type PurgeResult struct {
	// AuditID is the tenant_data_purges row the purge wrote.
	AuditID  uuid.UUID
	Purged   []UnboundTableCount
	Retained []UnboundTableCount
}

// ErrPurgeNotConfirmed is returned by PurgeUnboundRows when the caller's
// confirmed row count does not match what the purge would delete.
var ErrPurgeNotConfirmed = errors.New("oauthclient: purge not confirmed")

// tenantTablesSQL lists every ordinary or partitioned table (never a single
// partition, which its parent already counts) outside the system schemas
// that has a uuid tenant_id column, and whether it is an append-only
// ledger. Any one of three signals makes it a ledger, so an unsure
// classification retains:
//
//   - no permissive RLS policy allows DELETE (a restrictive policy only
//     narrows what a permissive one grants; on its own it allows nothing);
//   - atlas_app has no DELETE privilege on it, or there is no atlas_app
//     role to ask about;
//   - a user BEFORE DELETE row trigger.
const tenantTablesSQL = `
SELECT n.nspname, c.relname,
       NOT EXISTS (
           SELECT 1 FROM pg_policy p
           WHERE p.polrelid = c.oid AND p.polcmd IN ('d', '*')
             AND p.polpermissive
       ) OR NOT EXISTS (
           SELECT 1 FROM pg_roles r
           WHERE r.rolname = 'atlas_app'
             AND has_table_privilege(r.oid, c.oid, 'DELETE')
       ) OR EXISTS (
           SELECT 1 FROM pg_trigger tg
           WHERE tg.tgrelid = c.oid AND NOT tg.tgisinternal
             AND (tg.tgtype & 1) <> 0  -- ROW
             AND (tg.tgtype & 2) <> 0  -- BEFORE
             AND (tg.tgtype & 8) <> 0  -- DELETE
       ) AS ledger
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp\_%'
  AND c.relkind IN ('r', 'p')
  AND NOT c.relispartition
  AND a.attname = 'tenant_id'
  AND NOT a.attisdropped
  AND a.atttypid = 'uuid'::regtype
ORDER BY n.nspname, c.relname`

// tenantForeignKeysSQL lists the foreign keys between tables with a
// tenant_id column, with their column pairs in key order.
const tenantForeignKeysSQL = `
SELECT cn.nspname, cc.relname, pn.nspname, pc.relname,
       array_agg(ca.attname ORDER BY k.ord), array_agg(pa.attname ORDER BY k.ord)
FROM pg_constraint con
CROSS JOIN LATERAL unnest(con.conkey, con.confkey) WITH ORDINALITY AS k(ck, pk, ord)
JOIN pg_class cc ON cc.oid = con.conrelid
JOIN pg_namespace cn ON cn.oid = cc.relnamespace
JOIN pg_class pc ON pc.oid = con.confrelid
JOIN pg_namespace pn ON pn.oid = pc.relnamespace
JOIN pg_attribute ca ON ca.attrelid = con.conrelid AND ca.attnum = k.ck
JOIN pg_attribute pa ON pa.attrelid = con.confrelid AND pa.attnum = k.pk
WHERE con.contype = 'f'
GROUP BY con.oid, cn.nspname, cc.relname, pn.nspname, pc.relname
ORDER BY 1, 2, 3, 4`

type tableRef struct{ schema, name string }

func (t tableRef) ident() string { return pgx.Identifier{t.schema, t.name}.Sanitize() }

// String is the table's display name: bare in public, qualified elsewhere.
func (t tableRef) String() string {
	if t.schema == "public" {
		return t.name
	}
	return t.schema + "." + t.name
}

type foreignKey struct {
	child, parent         tableRef
	childCols, parentCols []string
}

// ListUnboundRows returns every tenant-scoped table holding rows in the
// all-zeros tenant, with the row count and, for a table a purge keeps, why.
// Tables with no such rows are left out. It only reads.
func ListUnboundRows(ctx context.Context, tx pgx.Tx) ([]UnboundTableCount, error) {
	counts, _, err := listUnbound(ctx, tx)
	return counts, err
}

// LedgerTables returns the display names of the tenant-scoped tables a
// purge never deletes from because they are append-only ledgers.
func LedgerTables(ctx context.Context, tx pgx.Tx) ([]string, error) {
	tables, ledger, err := tenantTables(ctx, tx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range tables {
		if ledger[t] {
			out = append(out, t.String())
		}
	}
	return out, nil
}

// PurgeableRows sums the rows a purge would delete: those in tables that
// are not retained.
func PurgeableRows(counts []UnboundTableCount) int64 {
	var total int64
	for _, c := range counts {
		if c.Retained == "" {
			total += c.Rows
		}
	}
	return total
}

// PurgeUnboundRows deletes the all-zeros-tenant rows of every table that is
// not retained, records the purge in tenant_data_purges, and returns what
// it deleted and kept, per table.
//
// confirmRows must equal PurgeableRows of a ListUnboundRows in the same
// transaction; otherwise nothing is deleted and the error wraps
// ErrPurgeNotConfirmed. That makes the operator restate what they looked
// at, and refuses a purge whose rows changed since. Run it in a
// REPEATABLE READ transaction so the listing and the purge see one
// snapshot.
//
// Tables are deleted in name order, each under a savepoint. A table whose
// delete trips a foreign key is retried on the next pass, after its
// children are gone; a purge that stops making progress fails. A purge that
// would change a retained table's all-zeros rows fails. On failure the
// caller should roll the transaction back.
func PurgeUnboundRows(ctx context.Context, tx pgx.Tx, confirmRows int64, osUser string) (PurgeResult, error) {
	before, refs, err := listUnbound(ctx, tx)
	if err != nil {
		return PurgeResult{}, err
	}
	ledgerBefore, err := ledgerTotals(ctx, tx)
	if err != nil {
		return PurgeResult{}, err
	}
	if total := PurgeableRows(before); total != confirmRows {
		return PurgeResult{}, fmt.Errorf("%w: %d purgeable row(s) in the all-zeros tenant, %d confirmed",
			ErrPurgeNotConfirmed, total, confirmRows)
	}

	var pending []tableRef
	for i, c := range before {
		if c.Retained == "" {
			pending = append(pending, refs[i])
		}
	}
	for len(pending) > 0 {
		var blocked []tableRef
		for _, table := range pending {
			ok, err := deleteUnbound(ctx, tx, table)
			if err != nil {
				return PurgeResult{}, err
			}
			if !ok {
				blocked = append(blocked, table)
			}
		}
		if len(blocked) == len(pending) {
			names := make([]string, len(blocked))
			for i, b := range blocked {
				names[i] = b.String()
			}
			return PurgeResult{}, fmt.Errorf("oauthclient: purge made no progress; rows in %s are still referenced by a foreign key",
				strings.Join(names, ", "))
		}
		pending = blocked
	}

	after, err := ListUnboundRows(ctx, tx)
	if err != nil {
		return PurgeResult{}, err
	}
	result, err := comparePurge(before, after)
	if err != nil {
		return PurgeResult{}, err
	}
	ledgerAfter, err := ledgerTotals(ctx, tx)
	if err != nil {
		return PurgeResult{}, err
	}
	for table, n := range ledgerBefore {
		if ledgerAfter[table] != n {
			return PurgeResult{}, fmt.Errorf("oauthclient: purge changed ledger table %s (%d rows across all tenants, now %d)",
				table, n, ledgerAfter[table])
		}
	}
	result.AuditID, err = recordPurge(ctx, tx, result, osUser)
	if err != nil {
		return PurgeResult{}, err
	}
	return result, nil
}

// ledgerTotals counts every row, in every tenant, of each ledger table, so
// a purge can prove no foreign-key cascade reached a ledger by any path.
func ledgerTotals(ctx context.Context, tx pgx.Tx) (map[string]int64, error) {
	tables, ledger, err := tenantTables(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, t := range tables {
		if !ledger[t] {
			continue
		}
		var n int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+t.ident()).Scan(&n); err != nil {
			return nil, fmt.Errorf("oauthclient: count rows in %s: %w", t, err)
		}
		out[t.String()] = n
	}
	return out, nil
}

// comparePurge checks a purge emptied every purgeable table and left every
// retained table's all-zeros rows as they were, and returns what it did.
func comparePurge(before, after []UnboundTableCount) (PurgeResult, error) {
	left := make(map[string]int64, len(after))
	for _, c := range after {
		left[c.Table] = c.Rows
	}
	var res PurgeResult
	for _, c := range before {
		if c.Retained != "" {
			if left[c.Table] != c.Rows {
				return PurgeResult{}, fmt.Errorf("oauthclient: purge changed retained table %s (%d all-zeros rows, now %d)",
					c.Table, c.Rows, left[c.Table])
			}
			res.Retained = append(res.Retained, c)
			continue
		}
		if left[c.Table] != 0 {
			return PurgeResult{}, fmt.Errorf("oauthclient: purge left %d all-zeros row(s) in %s", left[c.Table], c.Table)
		}
		res.Purged = append(res.Purged, c)
	}
	return res, nil
}

func recordPurge(ctx context.Context, tx pgx.Tx, res PurgeResult, osUser string) (uuid.UUID, error) {
	purged, err := json.Marshal(nonNil(res.Purged))
	if err != nil {
		return uuid.Nil, fmt.Errorf("oauthclient: encode purge record: %w", err)
	}
	retained, err := json.Marshal(nonNil(res.Retained))
	if err != nil {
		return uuid.Nil, fmt.Errorf("oauthclient: encode purge record: %w", err)
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenant_data_purges (os_user, purged, retained, total_rows)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		osUser, purged, retained, PurgeableRows(res.Purged),
	).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("oauthclient: record purge: %w", err)
	}
	return id, nil
}

func nonNil(c []UnboundTableCount) []UnboundTableCount {
	if c == nil {
		return []UnboundTableCount{}
	}
	return c
}

// listUnbound is ListUnboundRows, also returning each row's table ref.
func listUnbound(ctx context.Context, tx pgx.Tx) ([]UnboundTableCount, []tableRef, error) {
	tables, ledger, err := tenantTables(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	retained, err := retainedTables(ctx, tx, ledger)
	if err != nil {
		return nil, nil, err
	}
	var (
		out  []UnboundTableCount
		refs []tableRef
	)
	for _, table := range tables {
		n, err := countUnbound(ctx, tx, table)
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			out = append(out, UnboundTableCount{Table: table.String(), Rows: n, Retained: retained[table]})
			refs = append(refs, table)
		}
	}
	return out, refs, nil
}

func tenantTables(ctx context.Context, tx pgx.Tx) ([]tableRef, map[tableRef]bool, error) {
	rows, err := tx.Query(ctx, tenantTablesSQL)
	if err != nil {
		return nil, nil, fmt.Errorf("oauthclient: list tenant-scoped tables: %w", err)
	}
	defer rows.Close()
	var tables []tableRef
	ledger := map[tableRef]bool{}
	for rows.Next() {
		var (
			t      tableRef
			isLedg bool
		)
		if err := rows.Scan(&t.schema, &t.name, &isLedg); err != nil {
			return nil, nil, fmt.Errorf("oauthclient: list tenant-scoped tables: %w", err)
		}
		tables = append(tables, t)
		ledger[t] = isLedg
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("oauthclient: list tenant-scoped tables: %w", err)
	}
	return tables, ledger, nil
}

// retainedTables returns, for every tenant-scoped table a purge keeps, the
// reason. It starts from the ledger tables and adds, until nothing changes,
// each table whose all-zeros rows a kept table references.
func retainedTables(ctx context.Context, tx pgx.Tx, ledger map[tableRef]bool) (map[tableRef]string, error) {
	fks, err := tenantForeignKeys(ctx, tx, ledger)
	if err != nil {
		return nil, err
	}
	retained := map[tableRef]string{}
	for t, isLedger := range ledger {
		if isLedger {
			retained[t] = RetainedLedger
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fk := range fks {
			if retained[fk.child] == "" || retained[fk.parent] != "" {
				continue
			}
			n, err := countReferencing(ctx, tx, fk)
			if err != nil {
				return nil, err
			}
			if n > 0 {
				retained[fk.parent] = fmt.Sprintf("referenced by %d retained row(s) in %s", n, fk.child)
				changed = true
			}
		}
	}
	return retained, nil
}

func tenantForeignKeys(ctx context.Context, tx pgx.Tx, tenantTables map[tableRef]bool) ([]foreignKey, error) {
	rows, err := tx.Query(ctx, tenantForeignKeysSQL)
	if err != nil {
		return nil, fmt.Errorf("oauthclient: list foreign keys: %w", err)
	}
	defer rows.Close()
	var out []foreignKey
	for rows.Next() {
		var fk foreignKey
		if err := rows.Scan(&fk.child.schema, &fk.child.name, &fk.parent.schema, &fk.parent.name,
			&fk.childCols, &fk.parentCols); err != nil {
			return nil, fmt.Errorf("oauthclient: list foreign keys: %w", err)
		}
		_, childScoped := tenantTables[fk.child]
		_, parentScoped := tenantTables[fk.parent]
		if childScoped && parentScoped && fk.child != fk.parent {
			out = append(out, fk)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oauthclient: list foreign keys: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].child.String() < out[j].child.String() })
	return out, nil
}

// countReferencing counts the child rows that reference an all-zeros row
// of the parent through fk.
func countReferencing(ctx context.Context, tx pgx.Tx, fk foreignKey) (int64, error) {
	on := make([]string, len(fk.childCols))
	for i := range fk.childCols {
		on[i] = "c." + pgx.Identifier{fk.childCols[i]}.Sanitize() + " = p." + pgx.Identifier{fk.parentCols[i]}.Sanitize()
	}
	q := "SELECT count(*) FROM " + fk.child.ident() + " c JOIN " + fk.parent.ident() + " p ON " +
		strings.Join(on, " AND ") + " WHERE p.tenant_id = $1"
	var n int64
	if err := tx.QueryRow(ctx, q, uuid.Nil).Scan(&n); err != nil {
		return 0, fmt.Errorf("oauthclient: count %s rows referencing %s: %w", fk.child, fk.parent, err)
	}
	return n, nil
}

func countUnbound(ctx context.Context, tx pgx.Tx, table tableRef) (int64, error) {
	var n int64
	q := "SELECT count(*) FROM " + table.ident() + " WHERE tenant_id = $1"
	if err := tx.QueryRow(ctx, q, uuid.Nil).Scan(&n); err != nil {
		return 0, fmt.Errorf("oauthclient: count all-zeros rows in %s: %w", table, err)
	}
	return n, nil
}

// deleteUnbound deletes a table's all-zeros rows under a savepoint. It
// returns false, with the savepoint rolled back, when a foreign key still
// references them.
func deleteUnbound(ctx context.Context, tx pgx.Tx, table tableRef) (bool, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("oauthclient: savepoint for %s: %w", table, err)
	}
	q := "DELETE FROM " + table.ident() + " WHERE tenant_id = $1"
	if _, err := sp.Exec(ctx, q, uuid.Nil); err != nil {
		_ = sp.Rollback(ctx)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrForeignKeyViolation {
			return false, nil
		}
		return false, fmt.Errorf("oauthclient: delete all-zeros rows in %s: %w", table, err)
	}
	if err := sp.Commit(ctx); err != nil {
		return false, fmt.Errorf("oauthclient: release savepoint for %s: %w", table, err)
	}
	return true, nil
}
