//go:build integration

// #3087: a POA&M item taken from a failing replay run carries the instant
// the replay judged (evaluated_as_of), and its due date counts from that
// instant rather than from the clock that wrote the row. A live failing
// row is unchanged: no evaluated_as_of, due date from last_observed_at or
// evaluated_at.
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

func TestExport_ReplayPOAMItemCarriesJudgedInstant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := context.Background()
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)
	cell := seedScopeCell(t, admin, tenant, "replay-3087")

	seedRow := func(control uuid.UUID, result, freshness string, evaluatedAt time.Time, asOf, lastObserved *time.Time, trigger string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO control_evaluations
			  (id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
			   evaluated_as_of, result, freshness_status, last_observed_at,
			   evidence_count_in_window, trigger)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 0, $11)
		`, id, tenant, control, cell, uuid.New(), evaluatedAt, asOf, result, freshness, lastObserved, trigger); err != nil {
			t.Fatalf("seed %s evaluation: %v", trigger, err)
		}
		return id
	}

	// Control R: live pass at frozen-5h, then a failing replay written at
	// frozen-1h of T = frozen-3h, with no last_observed_at. The replay judged later,
	// so it is the finding, and it names T.
	ctrlR := seedControl(t, admin, tenant, "IAC")
	seedRow(ctrlR, "pass", "fresh", frozenAt.Add(-5*time.Hour), nil, nil, "manual")
	judged := frozenAt.Add(-3 * time.Hour)
	replayR := seedRow(ctrlR, "fail", "stale", frozenAt.Add(-time.Hour), &judged, nil, "replay")

	// Control L: a live failing row with no last_observed_at. Its due date counts
	// from evaluated_at, and it carries no evaluated_as_of.
	ctrlL := seedControl(t, admin, tenant, "AST")
	liveAtL := frozenAt.Add(-2 * time.Hour)
	liveL := seedRow(ctrlL, "fail", "stale", liveAtL, nil, nil, "manual")

	// Control O: a live failing row with last_observed_at set. Its due date counts from
	// last_observed_at, as before.
	ctrlO := seedControl(t, admin, tenant, "BCD")
	observedO := frozenAt.Add(-6 * time.Hour)
	liveO := seedRow(ctrlO, "fail", "stale", frozenAt.Add(-4*time.Hour), nil, &observedO, "manual")

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
	var poam []byte
	for _, m := range bundle.Members {
		if m.Filename == "poam.json" {
			poam = m.JSON
		}
	}
	if poam == nil {
		t.Fatal("bundle is missing poam.json")
	}
	// captureBridge serializes the PoamInput proto as JSON.
	var in struct {
		Items []struct {
			ID            string `json:"id"`
			DueDate       string `json:"dueDate"`
			EvaluatedAsOf string `json:"evaluatedAsOf"`
		} `json:"items"`
	}
	if err := json.Unmarshal(poam, &in); err != nil {
		t.Fatalf("decode POA&M input: %v\n%s", err, poam)
	}
	type item struct{ due, asOf string }
	byID := map[string]item{}
	for _, it := range in.Items {
		byID[it.ID] = item{it.DueDate, it.EvaluatedAsOf}
	}
	window := 90 * 24 * time.Hour
	rfc := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }

	cases := []struct {
		name     string
		id       uuid.UUID
		wantDue  string
		wantAsOf string
	}{
		{"replay R", replayR, rfc(judged.Add(window)), rfc(judged)},
		{"live L", liveL, rfc(liveAtL.Add(window)), ""},
		{"live O", liveO, rfc(observedO.Add(window)), ""},
	}
	for _, c := range cases {
		got, ok := byID[c.id.String()]
		if !ok {
			t.Errorf("%s: row %s is not a POA&M item\n%s", c.name, c.id, poam)
			continue
		}
		if got.asOf != c.wantAsOf {
			t.Errorf("%s: evaluated_as_of = %q, want %q", c.name, got.asOf, c.wantAsOf)
		}
		if got.due != c.wantDue {
			t.Errorf("%s: due_date = %q, want %q", c.name, got.due, c.wantDue)
		}
	}
}
