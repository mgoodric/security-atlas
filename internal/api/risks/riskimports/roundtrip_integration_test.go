//go:build integration

// #2840 — a register exported by security-atlas's own risk export
// re-imports, in CSV and XLSX, with no column map. Same harness as
// integration_test.go.
package riskimports_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// TestRiskImport_ExportRoundTrip exports a tenant's register and uploads
// the file into another tenant with no column map. The fields the import
// maps arrive on the new risks; every other export column (the uuid,
// computed severity, timestamps, ids, and fields not yet mapped) lands in
// unmapped and rejects nothing.
func TestRiskImport_ExportRoundTrip(t *testing.T) {
	src := newEnv(t)
	due := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	store := risk.NewStore(src.app)
	for _, in := range []risk.CreateInput{
		{Title: "Vendor outage", Description: "The payments vendor is down for a day.", CategoryRef: "operational",
			ExternalRef: "R-001", ReviewDueAt: &due},
		{Title: "Records kept too long", CategoryRef: "privacy", ExternalRef: "R-002"},
		{Title: "Risk without an ID", CategoryRef: "financial"},
	} {
		in.Actor = "test-2840"
		if _, err := store.Create(dbtest.WithTenantCtx(t, src.tenant), in); err != nil {
			t.Fatalf("seed %q: %v", in.Title, err)
		}
	}
	dst := newEnvFor(t, src.admin, src.app, dbtest.SeedTenant(t, src.admin, cleanupTables()...))

	wantUnmapped := strings.Join([]string{
		"id", "methodology",
		"inherent_score", "residual_score", "severity", "org_unit_id", "themes",
		"created_at", "updated_at", "acceptance_status", "parent_risk_ids", "category_name", "status",
		"closure_reason", "owner_user_id", "owner_name",
	}, ",")

	// The subtests share the destination tenant and run in order: csv
	// creates the three risks, xlsx then finds R-001 and R-002 already
	// there and creates only the row without an ID.
	for _, tc := range []struct {
		format              string
		approved, duplicate int
	}{
		{"csv", 3, 0},
		{"xlsx", 1, 2},
	} {
		t.Run(tc.format, func(t *testing.T) {
			status, exported := call(t, src, http.MethodGet, src.apprJWT, "/v1/risks/export?format="+tc.format, nil)
			if status != http.StatusOK {
				t.Fatalf("export = %d; body=%s", status, exported)
			}
			status, raw := upload(t, dst, dst.grcJWT, "risk-register."+tc.format, exported)
			if status != http.StatusCreated {
				t.Fatalf("upload export = %d; body=%s", status, raw)
			}
			b := decode[riskimport.Batch](t, raw)
			if b.SourceFormat != tc.format || b.StagedCount != 3 || b.RejectedCount != 0 {
				t.Fatalf("batch format=%s staged=%d rejected=%d (%+v)", b.SourceFormat, b.StagedCount, b.RejectedCount, b.Rejected)
			}
			if got := strings.Join(b.UnmappedColumns, ","); got != wantUnmapped {
				t.Errorf("unmapped columns =\n%s\nwant\n%s", got, wantUnmapped)
			}
			r1 := stagedByRef(t, b, "R-001")
			if r1.Title != "Vendor outage" || r1.Description != "The payments vendor is down for a day." ||
				r1.Category != "operational" || r1.ReviewDue != "2027-02-01" {
				t.Errorf("R-001 staged as %+v", r1)
			}
			if r1.Unmapped["category_name"] != "Operational" || r1.Unmapped["status"] != "open" || r1.Unmapped["id"] == "" {
				t.Errorf("R-001 unmapped = %v", r1.Unmapped)
			}
			if r := stagedByRef(t, b, ""); r.Title != "Risk without an ID" || r.Category != "financial" {
				t.Errorf("row without an ID staged as %+v", r)
			}

			status, raw = approve(t, dst, dst.apprJWT, b.ID)
			if status != http.StatusOK {
				t.Fatalf("approve = %d; body=%s", status, raw)
			}
			res := decode[riskimport.ApproveResult](t, raw)
			if res.ApprovedCount != tc.approved || len(res.Duplicates) != tc.duplicate || len(res.Skipped) != 0 {
				t.Fatalf("approve result = %+v", res)
			}
			var title, desc, cat, review string
			if err := dst.admin.QueryRow(context.Background(), `
				SELECT r.title, r.description, c.slug, to_char(r.review_due_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')
				FROM risks r JOIN org_risk_categories c ON c.id = r.category_id
				WHERE r.tenant_id = $1 AND r.external_ref = 'R-001'`, dst.tenant).Scan(&title, &desc, &cat, &review); err != nil {
				t.Fatalf("read imported R-001: %v", err)
			}
			if title != "Vendor outage" || desc != "The payments vendor is down for a day." || cat != "operational" || review != "2027-02-01" {
				t.Errorf("imported R-001 = %q %q %q %q", title, desc, cat, review)
			}
		})
	}
}
