//go:build integration

// Integration test for #2043: after an in-place seed, demoprov.Lookup marks
// exactly the controls and evidence the seed added, under the app role's RLS,
// and never the organisation's own rows or another tenant's.
//
// Run with: go test -tags=integration -p 1 ./internal/demoseed/...

package demoseed_test

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	evidenceapi "github.com/mgoodric/security-atlas/internal/api/evidence"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/demoprov"
	"github.com/mgoodric/security-atlas/internal/demoseed"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// tenantIDs returns every id in table for the tenant (admin pool).
func tenantIDs(t *testing.T, table string, tenantID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := adminPool.Query(context.Background(),
		`SELECT id FROM `+pgx.Identifier{table}.Sanitize()+` WHERE tenant_id = $1`, tenantID)
	if err != nil {
		t.Fatalf("read %s ids: %v", table, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatalf("scan %s ids: %v", table, err)
	}
	return ids
}

// lookupAs runs demoprov.Lookup as the app role inside tenantID's RLS
// context.
func lookupAs(t *testing.T, tenantID uuid.UUID, table string, ids []uuid.UUID) demoprov.Set {
	t.Helper()
	pool := appPoolOrSkip(t)
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	set, err := demoprov.Lookup(ctx, dbx.New(tx), table, ids)
	if err != nil {
		t.Fatalf("Lookup %s: %v", table, err)
	}
	return set
}

func TestDemoProvenance_MarksExactlyTheInPlaceSeedRows(t *testing.T) {
	ref := freshInstallTenant(t)
	stockControls := tenantIDs(t, "controls", ref.ID)
	if len(stockControls) != 1 {
		t.Fatalf("fresh install holds %d controls, want 1 stock control", len(stockControls))
	}
	seeder := soleTenantSeeder(t, ref)
	res, err := seeder.Seed(context.Background(), demoseed.SeedInput{})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	controls := tenantIDs(t, "controls", ref.ID)
	marked := lookupAs(t, ref.ID, demoprov.TableControls, controls)
	if len(marked) != res.Controls {
		t.Errorf("demo controls marked = %d, want %d", len(marked), res.Controls)
	}
	if marked.Has(stockControls[0]) {
		t.Error("the organisation's stock control is marked demo")
	}

	evidence := tenantIDs(t, "evidence_records", ref.ID)
	if got := lookupAs(t, ref.ID, demoprov.TableEvidence, evidence); len(got) != len(evidence) || len(evidence) == 0 {
		t.Errorf("demo evidence marked = %d, want all %d", len(got), len(evidence))
	}

	// RLS: another tenant sees none of this tenant's demo rows.
	other := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $2)`, other, "demo-prov-"+other.String()[:8],
	); err != nil {
		t.Fatalf("insert other tenant: %v", err)
	}
	t.Cleanup(func() { removeTenant(t, other) })
	if got := lookupAs(t, other, demoprov.TableControls, controls); len(got) != 0 {
		t.Errorf("another tenant sees %d demo marks, want 0", len(got))
	}

	// Exports: the demo column reads "true" for exactly the seeded rows.
	app := appPoolOrSkip(t)
	controlsCSV := exportCSV(t, ref.ID, controlsapi.NewExportHandler(app).ExportControls, "/v1/controls/export?format=csv")
	if got := demoColumnCounts(t, controlsCSV); got["true"] != res.Controls || got["false"] != 1 {
		t.Errorf("controls export demo column = %v, want true=%d false=1", got, res.Controls)
	}
	evidenceCSV := exportCSV(t, ref.ID, evidenceapi.NewExportHandler(app).ExportEvidence, "/v1/admin/evidence/export?format=csv")
	if got := demoColumnCounts(t, evidenceCSV); got["true"] != len(evidence) || got["false"] != 0 {
		t.Errorf("evidence export demo column = %v, want true=%d", got, len(evidence))
	}
}

// exportCSV calls an export handler as an admin of tenantID and returns the
// parsed CSV.
func exportCSV(t *testing.T, tenantID uuid.UUID, h http.HandlerFunc, path string) [][]string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID: "test", TenantID: tenantID.String(), IsAdmin: true,
	})
	ctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d: %s", path, rec.Code, rec.Body.String())
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse %s csv: %v", path, err)
	}
	return rows
}

// demoColumnCounts counts the values of the "demo" column below the header.
func demoColumnCounts(t *testing.T, rows [][]string) map[string]int {
	t.Helper()
	col := slices.Index(rows[0], "demo")
	if col < 0 {
		t.Fatalf("export header %v has no demo column", rows[0])
	}
	counts := map[string]int{}
	for _, r := range rows[1:] {
		counts[r[col]]++
	}
	return counts
}
