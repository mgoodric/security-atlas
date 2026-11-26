//go:build integration

// #2526: the AR's excepted cells and the POA&M's findings come only from
// each control's latest evaluation run as of the frozen horizon, as the
// dashboard's control_latest_run does (#2269 D1). A cell that was failing or
// excepted in an earlier run and no longer applies (it has no row in the
// latest run) is neither a finding nor a risk-accepted observation.
//
// Run with: go test -tags=integration -p 1 ./internal/oscal/...

package oscal_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/oscal"
)

func TestExport_BoundsEvaluationsToControlsLatestRun(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM exception_audit_log WHERE tenant_id = $1`,
			`DELETE FROM exceptions WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("exception cleanup %s: %v", stmt, err)
			}
		}
	})
	ctx := context.Background()
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)
	cellA := seedScopeCell(t, admin, tenant, "a")
	cellB := seedScopeCell(t, admin, tenant, "b")

	failing := seedControl(t, admin, tenant, "IAC")
	excepted := seedControl(t, admin, tenant, "AST")

	excID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate, justification,
			requested_by, requested_at, approved_by, approved_at,
			activated_by, activated_at, effective_from, expires_at, status
		)
		VALUES ($1, $2, $3, '{}'::jsonb, 'Legacy host retires in Q3.',
		        'requester-2526', $4, 'approver-2526', $4,
		        'approver-2526', $4, $4, $5, 'active')
	`, excID, tenant, excepted, frozenAt.Add(-48*time.Hour), frozenAt.Add(30*24*time.Hour)); err != nil {
		t.Fatalf("seed exception: %v", err)
	}

	// For each control: run 1 covers cells A and B with the result under
	// test; run 2 covers only cell B, so cell A stopped applying. The run-1
	// cell-A row is the stale one that must not reach the bundle.
	seedTwoRuns := func(control uuid.UUID, result string) (stale, current uuid.UUID) {
		t.Helper()
		run1, run2 := uuid.New(), uuid.New()
		stale, current = uuid.New(), uuid.New()
		for _, r := range []struct {
			id, cell, run uuid.UUID
			at            time.Time
		}{
			{stale, cellA, run1, frozenAt.Add(-3 * time.Hour)},
			{uuid.New(), cellB, run1, frozenAt.Add(-3 * time.Hour)},
			{current, cellB, run2, frozenAt.Add(-2 * time.Hour)},
		} {
			if _, err := admin.Exec(ctx, `
				INSERT INTO control_evaluations
				  (id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
				   result, freshness_status, evidence_count_in_window, trigger)
				VALUES ($1, $2, $3, $4, $5, $6, $7, 'stale', 0, 'manual')
			`, r.id, tenant, control, r.cell, r.run, r.at, result); err != nil {
				t.Fatalf("seed %s evaluation: %v", result, err)
			}
		}
		return stale, current
	}
	staleFail, currentFail := seedTwoRuns(failing, "fail")
	staleExcepted, currentExcepted := seedTwoRuns(excepted, "excepted")

	bridge := &captureBridge{}
	signer, _ := oscal.NewEphemeralSigner()
	bundle, err := oscal.NewExporter(app, bridge, signer).Export(dbtest.WithTenantCtx(t, tenant), oscal.ExportInput{
		AuditPeriodID:     periodID,
		OrganizationName:  "Acme Security Inc.",
		SystemName:        "Acme Compliance Platform",
		SystemDescription: "The SaaS platform under SOC 2 assessment.",
		RequestedBy:       "tester",
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	var ar, poam []byte
	for _, m := range bundle.Members {
		switch m.Filename {
		case "assessment-results.json":
			ar = m.JSON
		case "poam.json":
			poam = m.JSON
		}
	}
	if ar == nil || poam == nil {
		t.Fatal("bundle is missing assessment-results.json or poam.json")
	}

	if !bytes.Contains(poam, []byte(currentFail.String())) {
		t.Errorf("POA&M missing the latest run's failing cell %s.\ngot: %s", currentFail, poam)
	}
	if bytes.Contains(poam, []byte(staleFail.String())) {
		t.Errorf("POA&M carries %s, a failing cell that no longer applies.\ngot: %s", staleFail, poam)
	}
	if !bytes.Contains(ar, []byte(currentExcepted.String())) {
		t.Errorf("AR missing the latest run's excepted cell %s.\ngot: %s", currentExcepted, ar)
	}
	if bytes.Contains(ar, []byte(staleExcepted.String())) {
		t.Errorf("AR carries %s, an excepted cell that no longer applies.\ngot: %s", staleExcepted, ar)
	}
}

func seedScopeCell(t *testing.T, admin *pgxpool.Pool, tenant, label string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
		 VALUES ($1, $2, $3, jsonb_build_object('env', $3::text), $4)`,
		id, tenant, label, "h-2526-"+label); err != nil {
		t.Fatalf("seed scope cell %s: %v", label, err)
	}
	return id
}
