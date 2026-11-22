//go:build integration

// #2898 — a risk export re-imported keeps its scores, for each methodology
// the import offers, and, into the tenant it came from, its linked owner and
// its org unit. Same harness as integration_test.go.
package riskimports_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// scoresByRef reads each risk of tenant with an organisation risk ID as its
// inherent and residual scores, in jsonb's own text form, so equal scores
// compare equal whatever the key order.
func scoresByRef(t *testing.T, e env, tenant string) map[string][2]string {
	t.Helper()
	rows, err := e.admin.Query(context.Background(), `
		SELECT external_ref, inherent_score::text, residual_score::text
		FROM risks WHERE tenant_id = $1 AND external_ref IS NOT NULL`, tenant)
	if err != nil {
		t.Fatalf("read scores: %v", err)
	}
	defer rows.Close()
	out := map[string][2]string{}
	for rows.Next() {
		var ref, inh, res string
		if err := rows.Scan(&ref, &inh, &res); err != nil {
			t.Fatalf("scan scores: %v", err)
		}
		out[ref] = [2]string{inh, res}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read scores: %v", err)
	}
	return out
}

// TestRiskImport_ExportRoundTripScores exports a register scored under one
// methodology and imports it, under the same methodology, into a fresh
// tenant: every risk arrives with the scores it was exported with.
func TestRiskImport_ExportRoundTripScores(t *testing.T) {
	cases := []struct {
		methodology dbx.RiskMethodology
		risks       []risk.CreateInput
	}{
		{dbx.RiskMethodologyNist80030, []risk.CreateInput{
			{ExternalRef: "N-1", InherentScore: []byte(`{"likelihood":4,"impact":5,"impact_dollars_low":1000}`), ResidualScore: []byte(`{"likelihood":2,"impact":3}`)},
			{ExternalRef: "N-2", InherentScore: []byte(`{"likelihood":3,"impact":2}`)},
			{ExternalRef: "N-3"},
		}},
		{dbx.RiskMethodologyQualitative5x5, []risk.CreateInput{
			{ExternalRef: "Q-1", InherentScore: []byte(`{"likelihood":5,"impact":5}`), ResidualScore: []byte(`{"likelihood":1,"impact":2}`)},
			{ExternalRef: "Q-2", InherentScore: []byte(`{"likelihood":1,"impact":1}`)},
			{ExternalRef: "Q-3"},
		}},
		{dbx.RiskMethodologyFair, []risk.CreateInput{
			{ExternalRef: "F-1", InherentScore: []byte(`{"loss_event_frequency":0.5,"loss_magnitude":250000}`), ResidualScore: []byte(`{"lef":0.1,"lm":100000}`)},
			{ExternalRef: "F-2", InherentScore: []byte(`{"loss_event_frequency":2,"loss_magnitude":1000}`)},
			{ExternalRef: "F-3"},
		}},
	}
	for _, tc := range cases {
		t.Run(string(tc.methodology), func(t *testing.T) {
			src := newEnv(t)
			store := risk.NewStore(src.app)
			for _, in := range tc.risks {
				in.Title, in.CategoryRef, in.Methodology, in.Actor = "Risk "+in.ExternalRef, "operational", tc.methodology, "test-2898"
				if _, err := store.Create(dbtest.WithTenantCtx(t, src.tenant), in); err != nil {
					t.Fatalf("seed %s: %v", in.ExternalRef, err)
				}
			}
			dst := newEnvFor(t, src.admin, src.app, dbtest.SeedTenant(t, src.admin, cleanupTables()...))

			status, exported := call(t, src, http.MethodGet, src.apprJWT, "/v1/risks/export?format=csv", nil)
			if status != http.StatusOK {
				t.Fatalf("export = %d; body=%s", status, exported)
			}
			status, raw := uploadWithMethodology(t, dst, dst.grcJWT, string(tc.methodology), exported)
			if status != http.StatusCreated {
				t.Fatalf("upload export = %d; body=%s", status, raw)
			}
			b := decode[riskimport.Batch](t, raw)
			if b.StagedCount != len(tc.risks) || b.RejectedCount != 0 {
				t.Fatalf("batch staged=%d rejected=%d (%+v)", b.StagedCount, b.RejectedCount, b.Rejected)
			}
			for _, col := range []string{"inherent_score", "residual_score"} {
				if slices.Contains(b.UnmappedColumns, col) {
					t.Errorf("%s is unmapped: %q", col, b.UnmappedColumns)
				}
			}
			status, raw = approve(t, dst, dst.apprJWT, b.ID)
			if status != http.StatusOK {
				t.Fatalf("approve = %d; body=%s", status, raw)
			}
			if res := decode[riskimport.ApproveResult](t, raw); res.ApprovedCount != len(tc.risks) {
				t.Fatalf("approve result = %+v", res)
			}
			want, got := scoresByRef(t, src, src.tenant), scoresByRef(t, dst, dst.tenant)
			if len(want) != len(tc.risks) {
				t.Fatalf("source scores = %v", want)
			}
			for ref, w := range want {
				if got[ref] != w {
					t.Errorf("%s: imported scores %v, exported %v", ref, got[ref], w)
				}
			}
		})
	}
}

// TestRiskImport_ExportRoundTripOwnerAndOrgUnit exports a risk with a
// linked owner and an org unit. Imported into the same tenant, the new risk
// has both. Imported into another tenant, where neither id names anything,
// both stay unmapped values and nothing is rejected.
func TestRiskImport_ExportRoundTripOwnerAndOrgUnit(t *testing.T) {
	src := newEnv(t)
	ctx := dbtest.WithTenantCtx(t, src.tenant)
	store := risk.NewStore(src.app)
	unit, err := store.CreateOrgUnit(ctx, risk.OrgUnitInput{Name: "Payments", Level: dbx.RiskLevelOrg})
	if err != nil {
		t.Fatalf("seed org unit: %v", err)
	}
	owner := seedUser(t, src.admin, src.tenant, "owner")
	orig, err := store.Create(ctx, risk.CreateInput{
		Title: "Linked and placed", CategoryRef: "operational", Actor: "test-2898",
		OwnerUserID: &owner, OrgUnitID: &unit.ID, Level: dbx.RiskLevelOrg,
	})
	if err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	status, exported := call(t, src, http.MethodGet, src.apprJWT, "/v1/risks/export?format=csv", nil)
	if status != http.StatusOK {
		t.Fatalf("export = %d; body=%s", status, exported)
	}

	t.Run("same tenant", func(t *testing.T) {
		status, raw := upload(t, src, src.grcJWT, "risk-register.csv", exported)
		if status != http.StatusCreated {
			t.Fatalf("upload export = %d; body=%s", status, raw)
		}
		b := decode[riskimport.Batch](t, raw)
		if b.StagedCount != 1 || b.RejectedCount != 0 {
			t.Fatalf("batch staged=%d rejected=%d (%+v)", b.StagedCount, b.RejectedCount, b.Rejected)
		}
		r := b.Staged[0]
		if r.OwnerUserID != owner.String() || r.OwnerUnlinked != "" || slices.Contains(r.Flags, riskimport.FlagOwnerUnlinked) {
			t.Errorf("owner staged as id=%q unlinked=%q flags=%v", r.OwnerUserID, r.OwnerUnlinked, r.Flags)
		}
		if r.OrgUnitID != unit.ID.String() || r.OrgUnitValue != "Payments" || r.Level != "" {
			t.Errorf("placement staged as unit=%q value=%q level=%q", r.OrgUnitID, r.OrgUnitValue, r.Level)
		}
		for _, col := range []string{"owner_user_id", "org_unit_id"} {
			if _, held := r.Unmapped[col]; held || slices.Contains(b.UnmappedColumns, col) {
				t.Errorf("%s is still unmapped: row %v, batch %q", col, r.Unmapped, b.UnmappedColumns)
			}
		}
		status, raw = approve(t, src, src.apprJWT, b.ID)
		if status != http.StatusOK {
			t.Fatalf("approve = %d; body=%s", status, raw)
		}
		res := decode[riskimport.ApproveResult](t, raw)
		if res.ApprovedCount != 1 {
			t.Fatalf("approve result = %+v", res)
		}
		// The export has no level column: the risk takes its unit's level.
		var gotOwner, gotUnit uuid.UUID
		var gotLevel string
		if err := src.admin.QueryRow(context.Background(), `
			SELECT owner_user_id, org_unit_id, level::text FROM risks
			WHERE tenant_id = $1 AND title = 'Linked and placed' AND id <> $2`,
			src.tenant, orig.ID).Scan(&gotOwner, &gotUnit, &gotLevel); err != nil {
			t.Fatalf("read imported risk: %v", err)
		}
		if gotOwner != owner || gotUnit != unit.ID || gotLevel != "org" {
			t.Errorf("imported risk owner=%s unit=%s level=%s; want %s %s org", gotOwner, gotUnit, gotLevel, owner, unit.ID)
		}
	})

	t.Run("another tenant", func(t *testing.T) {
		dst := newEnvFor(t, src.admin, src.app, dbtest.SeedTenant(t, src.admin, cleanupTables()...))
		status, raw := upload(t, dst, dst.grcJWT, "risk-register.csv", exported)
		if status != http.StatusCreated {
			t.Fatalf("upload export = %d; body=%s", status, raw)
		}
		b := decode[riskimport.Batch](t, raw)
		if b.StagedCount != 1 || b.RejectedCount != 0 {
			t.Fatalf("batch staged=%d rejected=%d (%+v)", b.StagedCount, b.RejectedCount, b.Rejected)
		}
		r := b.Staged[0]
		if r.OwnerUserID != "" || r.OrgUnitID != "" {
			t.Errorf("another tenant's ids were used: owner=%q unit=%q", r.OwnerUserID, r.OrgUnitID)
		}
		if r.Unmapped["owner_user_id"] != owner.String() || r.Unmapped["org_unit_id"] != unit.ID.String() {
			t.Errorf("unmapped = %v", r.Unmapped)
		}
		for _, col := range []string{"owner_user_id", "org_unit_id"} {
			if !slices.Contains(b.UnmappedColumns, col) {
				t.Errorf("%s not among unmapped columns %q", col, b.UnmappedColumns)
			}
		}
	})
}
