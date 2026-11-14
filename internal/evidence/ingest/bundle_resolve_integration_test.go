//go:build integration

package ingest_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// seedBundleControl inserts an active control with bundleID under tenant
// and returns its UUID.
func seedBundleControl(t *testing.T, admin *pgxpool.Pool, tenant, bundleID string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'bundle-resolve control', 'IAC', 'manual_attested', $3)
	`, id, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM controls WHERE id = $1`, id)
	})
	return id
}

// ledgerControl reads back the stored control_id and control_ref of one
// evidence record, as the owner role so RLS does not hide it.
func ledgerControl(t *testing.T, admin *pgxpool.Pool, recordID string) (pgtype.UUID, string) {
	t.Helper()
	var controlID pgtype.UUID
	var controlRef string
	if err := admin.QueryRow(context.Background(),
		`SELECT control_id, control_ref FROM evidence_records WHERE id = $1`, recordID,
	).Scan(&controlID, &controlRef); err != nil {
		t.Fatalf("read ledger row: %v", err)
	}
	return controlID, controlRef
}

// #2100: a push whose control_id is a control bundle ID links the record
// to the tenant's active control. Before the fix the row kept only
// control_ref, which readers match against the control UUID, so the
// evidence was accepted and never reached the control.
func TestPushBundleIDResolvesToControl(t *testing.T) {
	svc, _, _ := boot(t)
	admin := dbtest.NewMigratePool(t)

	bundleID := "bundle_resolve_" + uuid.NewString()[:8]
	want := seedBundleControl(t, admin, tenantA, bundleID)
	// The same bundle ID in another tenant must not be what links.
	seedBundleControl(t, admin, tenantB, bundleID)

	receipt, _, err := svc.Process(context.Background(),
		record(t, func(r *evidencev1.EvidenceRecord) { r.ControlId = bundleID }),
		mkCred(tenantA, nil, ""))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	got, ref := ledgerControl(t, admin, receipt.RecordID)
	if !got.Valid || uuid.UUID(got.Bytes) != want {
		t.Fatalf("control_id = %v, want %s (tenant A's active control for %q)", got, want, bundleID)
	}
	// control_ref keeps the wire value: verify rebuilds the hashed record
	// from it.
	if ref != bundleID {
		t.Fatalf("control_ref = %q, want the wire value %q", ref, bundleID)
	}
}

// A control_id that names no bundle stays a free-form control_ref, as it
// did before #2100.
func TestPushUnknownRefStaysUnlinked(t *testing.T) {
	svc, _, _ := boot(t)
	admin := dbtest.NewMigratePool(t)

	receipt, _, err := svc.Process(context.Background(), record(t), mkCred(tenantA, nil, ""))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	got, ref := ledgerControl(t, admin, receipt.RecordID)
	if got.Valid {
		t.Fatalf("control_id = %v, want NULL for free-form ref %q", got, ref)
	}
	if ref != "scf:VPM-04" {
		t.Fatalf("control_ref = %q, want scf:VPM-04", ref)
	}
}
