// Package demoprov answers "did the demo seed add this row?" for the read
// surfaces and exports that show controls and evidence (#2043).
//
// `atlas-cli demo seed` without --tenant-slug loads the demo dataset into a
// real organisation (#1765) and records the primary key of every row it added
// in demo_seed_rows. Those rows sit beside the organisation's real controls
// and evidence, so every surface an auditor reads marks them "demo", read
// from that table. A separate demo tenant needs no per-row mark: everything in
// it is demo.
//
// Lookups run on the caller's querier, inside its tenant transaction, so RLS
// scopes demo_seed_rows to the caller's tenant like every other read. The
// query also names the context's tenant explicitly (#2153), so a connection
// RLS does not bind still reads one tenant's marks, and a context with no
// tenant reads none.
package demoprov

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Tables whose rows carry the demo mark, named as demo_seed_rows.table_name
// records them.
const (
	TableControls = "controls"
	TableEvidence = "evidence_records"
)

// Querier is the slice of dbx.Querier this package needs.
type Querier interface {
	DemoSeededIDs(ctx context.Context, arg dbx.DemoSeededIDsParams) ([]pgtype.UUID, error)
}

// Set is the subset of looked-up ids the demo seed added. The zero value
// marks nothing.
type Set map[uuid.UUID]struct{}

// Has reports whether id is a demo row.
func (s Set) Has(id uuid.UUID) bool {
	_, ok := s[id]
	return ok
}

// Lookup returns which of ids the in-place demo seed added to table, for the
// tenant in ctx. An empty ids slice issues no query; a ctx with no tenant is
// an error, not an empty set.
func Lookup(ctx context.Context, q Querier, table string, ids []uuid.UUID) (Set, error) {
	if len(ids) == 0 {
		return Set{}, nil
	}
	// tenancy.WithTenant only stores a parseable id, so the Parse cannot
	// fail after TenantFromContext succeeds; it shares the error path.
	var tenantID uuid.UUID
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err == nil {
		tenantID, err = uuid.Parse(tenantStr)
	}
	if err != nil {
		return nil, fmt.Errorf("demoprov: lookup %s: tenant: %w", table, err)
	}
	arg := dbx.DemoSeededIDsParams{
		TenantID:  pgtype.UUID{Bytes: tenantID, Valid: true},
		TableName: table,
		Ids:       make([]pgtype.UUID, len(ids)),
	}
	for i, id := range ids {
		arg.Ids[i] = pgtype.UUID{Bytes: id, Valid: true}
	}
	rows, err := q.DemoSeededIDs(ctx, arg)
	if err != nil {
		return nil, fmt.Errorf("demoprov: lookup %s: %w", table, err)
	}
	out := make(Set, len(rows))
	for _, r := range rows {
		if r.Valid {
			out[uuid.UUID(r.Bytes)] = struct{}{}
		}
	}
	return out, nil
}
