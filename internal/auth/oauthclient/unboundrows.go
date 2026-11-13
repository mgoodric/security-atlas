package oauthclient

import (
	"context"
	"errors"
	"fmt"
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
// removes them.
//
// Both run on a caller-supplied transaction over the BYPASSRLS
// (atlas_migrate) connection: every tenant-scoped table is under FORCE ROW
// LEVEL SECURITY, so an RLS-bound role sees none of these rows. The caller
// commits or rolls back.
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

// UnboundTableCount is one tenant-scoped table and how many of its rows
// carry the all-zeros tenant.
type UnboundTableCount struct {
	Table string
	Rows  int64
}

// ErrPurgeNotConfirmed is returned by PurgeUnboundRows when the caller's
// confirmed row count does not match what the purge would delete.
var ErrPurgeNotConfirmed = errors.New("oauthclient: purge not confirmed")

// tenantScopedTablesSQL names every ordinary or partitioned table (never a
// single partition, which its parent already counts) in the search path
// that has a uuid tenant_id column.
const tenantScopedTablesSQL = `
SELECT c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid
WHERE n.nspname = ANY (current_schemas(false))
  AND c.relkind IN ('r', 'p')
  AND NOT c.relispartition
  AND a.attname = 'tenant_id'
  AND NOT a.attisdropped
  AND a.atttypid = 'uuid'::regtype
ORDER BY c.relname`

// ListUnboundRows returns every tenant-scoped table holding rows in the
// all-zeros tenant, with the row count, ordered by table name. Tables with
// no such rows are left out. It only reads.
func ListUnboundRows(ctx context.Context, tx pgx.Tx) ([]UnboundTableCount, error) {
	tables, err := tenantScopedTables(ctx, tx)
	if err != nil {
		return nil, err
	}
	var out []UnboundTableCount
	for _, table := range tables {
		n, err := countUnbound(ctx, tx, table)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, UnboundTableCount{Table: table, Rows: n})
		}
	}
	return out, nil
}

// TotalRows sums the row counts of a listing.
func TotalRows(counts []UnboundTableCount) int64 {
	var total int64
	for _, c := range counts {
		total += c.Rows
	}
	return total
}

// PurgeUnboundRows deletes every all-zeros-tenant row in every
// tenant-scoped table and returns what it deleted, per table, as counted
// before the purge.
//
// confirmRows must equal the total a ListUnboundRows in the same
// transaction reports; otherwise nothing is deleted and the error wraps
// ErrPurgeNotConfirmed. That makes the operator restate what they looked
// at, and refuses a purge whose rows changed since.
//
// Tables are deleted in name order, each under a savepoint. A table whose
// delete trips a foreign key (a child table still references its rows) is
// retried on the next pass, after its children are gone; a purge that stops
// making progress fails. Any other error fails the purge. On failure the
// caller's transaction is left as it was before the purge, and the caller
// should roll it back.
func PurgeUnboundRows(ctx context.Context, tx pgx.Tx, confirmRows int64) ([]UnboundTableCount, error) {
	before, err := ListUnboundRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	if total := TotalRows(before); total != confirmRows {
		return nil, fmt.Errorf("%w: %d row(s) in the all-zeros tenant, %d confirmed", ErrPurgeNotConfirmed, total, confirmRows)
	}

	pending := make([]string, 0, len(before))
	for _, c := range before {
		pending = append(pending, c.Table)
	}
	for len(pending) > 0 {
		var blocked []string
		for _, table := range pending {
			ok, err := deleteUnbound(ctx, tx, table)
			if err != nil {
				return nil, err
			}
			if !ok {
				blocked = append(blocked, table)
			}
		}
		if len(blocked) == len(pending) {
			return nil, fmt.Errorf("oauthclient: purge made no progress; rows in %s are still referenced by a foreign key",
				strings.Join(blocked, ", "))
		}
		pending = blocked
	}

	after, err := ListUnboundRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(after) > 0 {
		return nil, fmt.Errorf("oauthclient: purge left %d all-zeros row(s), first in %s", TotalRows(after), after[0].Table)
	}
	return before, nil
}

func tenantScopedTables(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, tenantScopedTablesSQL)
	if err != nil {
		return nil, fmt.Errorf("oauthclient: list tenant-scoped tables: %w", err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("oauthclient: list tenant-scoped tables: %w", err)
	}
	return tables, nil
}

func countUnbound(ctx context.Context, tx pgx.Tx, table string) (int64, error) {
	var n int64
	q := "SELECT count(*) FROM " + pgx.Identifier{table}.Sanitize() + " WHERE tenant_id = $1"
	if err := tx.QueryRow(ctx, q, uuid.Nil).Scan(&n); err != nil {
		return 0, fmt.Errorf("oauthclient: count all-zeros rows in %s: %w", table, err)
	}
	return n, nil
}

// deleteUnbound deletes a table's all-zeros rows under a savepoint. It
// returns false, with the savepoint rolled back, when a foreign key still
// references them.
func deleteUnbound(ctx context.Context, tx pgx.Tx, table string) (bool, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("oauthclient: savepoint for %s: %w", table, err)
	}
	q := "DELETE FROM " + pgx.Identifier{table}.Sanitize() + " WHERE tenant_id = $1"
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
