//go:build integration

// #3071: the AR ranks a control's runs by the instant each judged
// (COALESCE(evaluated_as_of, evaluated_at)), so a replay of an earlier
// instant does not displace a later live run, and an excepted cell taken
// from a replay run carries evaluated_as_of.
//
// Run with: go test -tags=integration -p 1 ./internal/oscal/...

package oscal_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/oscal"
)

func TestExport_ReplayRunsRankByTheirJudgedInstant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := context.Background()
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)
	cell := seedScopeCell(t, admin, tenant, "replay-3071")

	seedRow := func(control uuid.UUID, result string, evaluatedAt time.Time, asOf *time.Time, trigger string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO control_evaluations
			  (id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
			   evaluated_as_of, result, freshness_status, evidence_count_in_window, trigger)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'fresh', 1, $9)
		`, id, tenant, control, cell, uuid.New(), evaluatedAt, asOf, result, trigger); err != nil {
			t.Fatalf("seed %s evaluation: %v", trigger, err)
		}
		return id
	}

	// Control A: live excepted at frozen-2h, then a replay written at
	// frozen-1h of frozen-3h. The live run judged later, so it is the latest.
	ctrlA := seedControl(t, admin, tenant, "IAC")
	liveA := seedRow(ctrlA, "excepted", frozenAt.Add(-2*time.Hour), nil, "manual")
	asOfA := frozenAt.Add(-3 * time.Hour)
	replayA := seedRow(ctrlA, "excepted", frozenAt.Add(-time.Hour), &asOfA, "replay")

	// Control B: live pass at frozen-5h, then a replay written at frozen-1h
	// of frozen-3h. The replay judged later, so it is the latest, and its
	// excepted cell names the instant it judged.
	ctrlB := seedControl(t, admin, tenant, "AST")
	seedRow(ctrlB, "pass", frozenAt.Add(-5*time.Hour), nil, "manual")
	asOfB := frozenAt.Add(-3 * time.Hour)
	replayB := seedRow(ctrlB, "excepted", frozenAt.Add(-time.Hour), &asOfB, "replay")

	signer, _ := oscal.NewEphemeralSigner()
	bundle, err := oscal.NewExporter(app, &captureBridge{}, signer).Export(dbtest.WithTenantCtx(t, tenant), oscal.ExportInput{
		AuditPeriodID:     periodID,
		OrganizationName:  "Acme Security Inc.",
		SystemName:        "Acme Compliance Platform",
		SystemDescription: "The SaaS platform under SOC 2 assessment.",
		RequestedBy:       "tester",
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	var ar []byte
	for _, m := range bundle.Members {
		if m.Filename == "assessment-results.json" {
			ar = m.JSON
		}
	}
	if ar == nil {
		t.Fatal("bundle is missing assessment-results.json")
	}
	// captureBridge serializes the AssessmentInput proto as JSON.
	var in struct {
		ExceptedCells []struct {
			EvaluationID  string `json:"evaluationId"`
			EvaluatedAsOf string `json:"evaluatedAsOf"`
		} `json:"exceptedCells"`
	}
	if err := json.Unmarshal(ar, &in); err != nil {
		t.Fatalf("decode AR input: %v\n%s", err, ar)
	}
	byID := map[string]string{}
	for _, c := range in.ExceptedCells {
		byID[c.EvaluationID] = c.EvaluatedAsOf
	}

	if asOf, ok := byID[liveA.String()]; !ok || asOf != "" {
		t.Errorf("control A: live row %s in AR = (%q, %v), want present with no evaluated_as_of\n%s", liveA, asOf, ok, ar)
	}
	if _, ok := byID[replayA.String()]; ok {
		t.Errorf("control A: replay row %s of an earlier instant displaced the later live run\n%s", replayA, ar)
	}
	want := asOfB.UTC().Format(time.RFC3339)
	if asOf, ok := byID[replayB.String()]; !ok || asOf != want {
		t.Errorf("control B: replay row %s in AR = (%q, %v), want evaluated_as_of %s\n%s", replayB, asOf, ok, want, ar)
	}
}
