//go:build integration

// #2269: an excepted cell reaches the assessment results as a risk-accepted
// observation linked to the exception that accepted it, never as a POA&M
// finding. Runs against real Postgres through the capturing fake bridge, so
// the frozen-horizon reads and the exception reconstruction are proven on
// every CI run; the OSCAL shape is validated by the bridge's own pytest.
//
// Run with: go test -tags=integration -p 1 ./internal/oscal/...

package oscal_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/oscal"
)

func TestAR_ExceptedCellIsRiskAcceptedObservationNotFinding(t *testing.T) {
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
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)
	control := seedControl(t, admin, tenant, "IAC")

	excID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate, justification,
			requested_by, requested_at, approved_by, approved_at,
			activated_by, activated_at, effective_from, expires_at, status
		)
		VALUES ($1, $2, $3, '{}'::jsonb, 'Legacy host retires in Q3.',
		        'requester-2269', $4, 'approver-2269', $4,
		        'approver-2269', $4, $4, $5, 'active')
	`, excID, tenant, control, frozenAt.Add(-48*time.Hour), frozenAt.Add(30*24*time.Hour)); err != nil {
		t.Fatalf("seed exception: %v", err)
	}
	evalID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations
		  (id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
		   result, freshness_status, evidence_count_in_window, trigger)
		VALUES ($1, $2, $3, NULL, $4, $5, 'excepted', 'no_evidence', 0, 'manual')
	`, evalID, tenant, control, uuid.New(), frozenAt.Add(-time.Hour)); err != nil {
		t.Fatalf("seed excepted evaluation: %v", err)
	}

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
	if ar == nil {
		t.Fatal("bundle has no assessment-results.json member")
	}
	for _, want := range []string{"exceptedCells", evalID.String(), excID.String(), "approver-2269"} {
		if !bytes.Contains(ar, []byte(want)) {
			t.Errorf("AR missing %q.\ngot: %s", want, ar)
		}
	}
	if bytes.Contains(poam, []byte(evalID.String())) {
		t.Errorf("POA&M carries the excepted evaluation %s as a finding.\ngot: %s", evalID, poam)
	}
}
