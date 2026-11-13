//go:build integration

// #1746 integration coverage for the risk appliers: create_risk stores
// untreated when the proposal omits a treatment, and update_risk_treatment
// runs risk.ValidateTreatment against the risk's current links.
//
// Run with:  go test -tags=integration ./internal/mcp/writeproposals/...

package writeproposals_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/risk"
)

func treatmentTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"mcp_write_proposals", "action_plan_risks", "action_plans", "risks", "users")
}

// proposeAndConfirm files a proposal through the default appliers and
// confirms it, returning the confirm error.
func proposeAndConfirm(t *testing.T, store *writeproposals.Store, ctx context.Context, tool string, input string) (writeproposals.Proposal, error) {
	t.Helper()
	created, err := store.Create(ctx, writeproposals.CreateInput{
		ToolName:       tool,
		ToolInput:      json.RawMessage(input),
		AIModelName:    "llama3.1:8b-instruct-q5",
		AIModelVersion: "2026-05-01",
		CreatedBy:      "key_" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Create %s proposal: %v", tool, err)
	}
	return store.Confirm(ctx, created.ID, "key_"+uuid.NewString())
}

func readTreatment(t *testing.T, admin *pgxpool.Pool, id string) (string, *string) {
	t.Helper()
	var treatment string
	var status *string
	if err := admin.QueryRow(context.Background(),
		`SELECT treatment::text, acceptance_status::text FROM risks WHERE id = $1`, id,
	).Scan(&treatment, &status); err != nil {
		t.Fatalf("read risk %s: %v", id, err)
	}
	return treatment, status
}

func TestApplyRiskTreatment_UntreatedDefaultAndValidation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := treatmentTenant(t, admin)
	store := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(app))
	ctx := ctxFor(t, tenant)

	// create_risk with no treatment stores untreated.
	applied, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolCreateRisk,
		`{"title":"Intake risk","category":"operational"}`)
	if err != nil {
		t.Fatalf("confirm create_risk: %v", err)
	}
	riskID := *applied.AppliedSubject
	if tr, _ := readTreatment(t, admin, riskID); tr != "untreated" {
		t.Fatalf("created treatment = %q; want untreated", tr)
	}

	// mitigate with no controls and no action plans fails validation and
	// leaves the risk untouched.
	_, err = proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"mitigate"}`)
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("mitigate with nothing linked: want treatment validation, got %v", err)
	}
	if tr, _ := readTreatment(t, admin, riskID); tr != "untreated" {
		t.Fatalf("treatment after rejected update = %q; want untreated", tr)
	}

	// Link an action plan (no controls): mitigate is now valid.
	owner := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, 'Owner', 'active')
	`, owner, tenant, owner.String()+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	planID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO action_plans (id, tenant_id, title, owner_id)
		VALUES ($1, $2, 'Planned mitigation', $3)
	`, planID, tenant, owner); err != nil {
		t.Fatalf("seed action plan: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO action_plan_risks (action_plan_id, risk_id, tenant_id, linked_by)
		VALUES ($1, $2, $3, $4)
	`, planID, riskID, tenant, owner); err != nil {
		t.Fatalf("link action plan: %v", err)
	}
	if _, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"mitigate"}`); err != nil {
		t.Fatalf("mitigate with a linked action plan: %v", err)
	}
	if tr, _ := readTreatment(t, admin, riskID); tr != "mitigate" {
		t.Fatalf("treatment = %q; want mitigate", tr)
	}

	// Proposed acceptance needs no accepter or expiry.
	if _, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"accept","acceptance_status":"proposed"}`); err != nil {
		t.Fatalf("proposed accept: %v", err)
	}
	if tr, st := readTreatment(t, admin, riskID); tr != "accept" || st == nil || *st != "proposed" {
		t.Fatalf("after proposed accept: treatment=%q status=%v", tr, st)
	}

	// Approving without accepter / expiry fails; with them it succeeds.
	_, err = proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"accept","acceptance_status":"approved"}`)
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("unsigned approval: want treatment validation, got %v", err)
	}
	if _, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"accept","acceptance_status":"approved",`+
			`"accepter":"ciso@example.com","accepted_until":"2027-06-30"}`); err != nil {
		t.Fatalf("signed approval: %v", err)
	}
	if _, st := readTreatment(t, admin, riskID); st == nil || *st != "approved" {
		t.Fatalf("after approval: status=%v; want approved", st)
	}

	// Moving off accept clears the acceptance status.
	if _, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"avoid"}`); err != nil {
		t.Fatalf("avoid: %v", err)
	}
	if tr, st := readTreatment(t, admin, riskID); tr != "avoid" || st != nil {
		t.Fatalf("after avoid: treatment=%q status=%v; want avoid/nil", tr, st)
	}
}
