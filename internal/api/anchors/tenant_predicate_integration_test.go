//go:build integration

// Issue #1832 — the catalog reads that join `controls` carry an explicit
// `c.tenant_id = sqlc.arg(tenant_id)` predicate, not RLS alone.
//
// The test runs the queries on the migrate pool. `atlas_migrate` is the
// table owner with BYPASSRLS (migrations/bootstrap/01-roles.sql), so no
// policy filters `controls` or `control_evaluations` here: the predicate
// is the only thing keeping Tenant A's control out of Tenant B's read.

package anchors_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestAnchorQueries_TenantPredicateIsolatesWithoutRLS(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()

	var bypass bool
	if err := admin.QueryRow(ctx, `SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass); err != nil {
		t.Fatalf("read rolbypassrls: %v", err)
	}
	if !bypass {
		t.Fatalf("migrate pool role must bypass RLS for this test to prove anything")
	}

	const tenantB = "22222222-2222-2222-2222-222222222222"
	cleanupTenantState(t, admin, tenantA)
	cleanupTenantState(t, admin, tenantB)
	t.Cleanup(func() { cleanupTenantState(t, admin, tenantA) })

	anchorUUID := anchorIDBySCFID(t, admin, "IAC-06")
	seedAnchorState(t, admin, tenantA, anchorUUID, "fail", "stale", time.Now().Add(-time.Hour))

	q := dbx.New(admin)
	tenantBID := pgtype.UUID{Bytes: uuid.MustParse(tenantB), Valid: true}

	counts, err := q.CountSCFAnchorsLatest(ctx, tenantBID)
	if err != nil {
		t.Fatalf("CountSCFAnchorsLatest: %v", err)
	}
	if counts.WithControlTotal != 0 {
		t.Errorf("tenant B with_control_total = %d; want 0 (tenant A's control leaked)", counts.WithControlTotal)
	}

	rows, err := q.ListSCFAnchorsLatestWithState(ctx, dbx.ListSCFAnchorsLatestWithStateParams{
		Limit:    int32(counts.CatalogTotal),
		Offset:   0,
		TenantID: tenantBID,
	})
	if err != nil {
		t.Fatalf("ListSCFAnchorsLatestWithState: %v", err)
	}
	for _, r := range rows {
		if r.ControlID.Valid || r.ControlCount != 0 || r.StateResult != nil {
			t.Errorf("tenant B row %s carries tenant A data: control_id=%v count=%d has_state=%t",
				r.ScfID, r.ControlID, r.ControlCount, r.StateResult != nil)
		}
	}

	var fvID pgtype.UUID
	if err := admin.QueryRow(ctx, `SELECT framework_version_id FROM scf_anchors WHERE id = $1`, anchorUUID).Scan(&fvID); err != nil {
		t.Fatalf("anchor framework_version_id: %v", err)
	}
	vrows, err := q.ListSCFAnchorsForVersionWithState(ctx, dbx.ListSCFAnchorsForVersionWithStateParams{
		FrameworkVersionID: fvID,
		Limit:              int32(counts.CatalogTotal),
		Offset:             0,
		TenantID:           tenantBID,
	})
	if err != nil {
		t.Fatalf("ListSCFAnchorsForVersionWithState: %v", err)
	}
	for _, r := range vrows {
		if r.StateResult != nil {
			t.Errorf("tenant B version row %s carries tenant A state %v", r.ScfID, *r.StateResult)
		}
	}

	// Tenant A, same pool, still sees its own control: the predicate
	// filters to the named tenant rather than to nothing.
	countsA, err := q.CountSCFAnchorsLatest(ctx, pgtype.UUID{Bytes: uuid.MustParse(tenantA), Valid: true})
	if err != nil {
		t.Fatalf("CountSCFAnchorsLatest A: %v", err)
	}
	if countsA.WithControlTotal != 1 {
		t.Errorf("tenant A with_control_total = %d; want 1", countsA.WithControlTotal)
	}
}
