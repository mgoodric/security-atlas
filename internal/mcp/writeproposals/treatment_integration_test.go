//go:build integration

// #1746 integration coverage for the risk appliers: create_risk stores
// untreated when the proposal omits a treatment, and update_risk_treatment
// runs risk.ValidateTreatment against the risk's current links, and the
// #2105 acceptance transitions and approval rule.
//
// Run with:  go test -tags=integration ./internal/mcp/writeproposals/...

package writeproposals_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/approval"
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
// confirms it as an unbound key (a machine), returning the confirm error.
func proposeAndConfirm(t *testing.T, store *writeproposals.Store, ctx context.Context, tool string, input string) (writeproposals.Proposal, error) {
	t.Helper()
	key := "key_" + uuid.NewString()
	return proposeAndConfirmBy(t, store, ctx, tool, input, writeproposals.Confirmer{
		Approver: approval.Approver{CredentialID: key},
	})
}

// proposeAndConfirmAsPerson is proposeAndConfirm with a person confirming
// (a key bound to a user): only a person proposes an acceptance (#2623).
func proposeAndConfirmAsPerson(t *testing.T, store *writeproposals.Store, ctx context.Context, tool string, input string) (writeproposals.Proposal, error) {
	t.Helper()
	key, person := "key_"+uuid.NewString(), uuid.NewString()
	return proposeAndConfirmBy(t, store, ctx, tool, input, writeproposals.Confirmer{
		Approver:  approval.Approver{UserID: person, CredentialID: key},
		Principal: person,
	})
}

func proposeAndConfirmBy(t *testing.T, store *writeproposals.Store, ctx context.Context, tool, input string, by writeproposals.Confirmer) (writeproposals.Proposal, error) {
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
	return store.ConfirmAs(ctx, created.ID, by.CredentialID, by)
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
	// Risk appliers only; the evidence appliers' ingest service is not needed.
	store := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(app), nil)
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

	// A proposed acceptance runs the propose transition (#2105): it needs
	// a justification and a proposed expiry, and a person confirming
	// (#2623).
	_, err = proposeAndConfirmAsPerson(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"accept","acceptance_status":"proposed"}`)
	if !risk.IsTransitionInput(err) {
		t.Fatalf("proposal without justification: want transition input error, got %v", err)
	}
	proposed := `{"risk_id":"` + riskID + `","treatment":"accept","acceptance_status":"proposed",` +
		`"justification":"Compensated by the WAF","accepted_until":"2099-01-31"}`
	if _, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment, proposed); !errors.Is(err, risk.ErrAcceptanceProposerNotPerson) {
		t.Fatalf("proposed accept confirmed by a machine: want ErrAcceptanceProposerNotPerson, got %v", err)
	}
	if tr, _ := readTreatment(t, admin, riskID); tr != "mitigate" {
		t.Fatalf("after refused machine propose: treatment = %q; want mitigate", tr)
	}
	if _, err := proposeAndConfirmAsPerson(t, store, ctx, writeproposals.ToolUpdateRiskTreatment, proposed); err != nil {
		t.Fatalf("proposed accept: %v", err)
	}
	if tr, st := readTreatment(t, admin, riskID); tr != "accept" || st == nil || *st != "proposed" {
		t.Fatalf("after proposed accept: treatment=%q status=%v", tr, st)
	}

	// Approval needs accepted_until, refuses an accepter (the approver is
	// the accepter) and a machine confirmer, and succeeds for a person.
	approve := `{"risk_id":"` + riskID + `","treatment":"accept","acceptance_status":"approved","accepted_until":"2099-06-30"}`
	_, err = proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"accept","acceptance_status":"approved"}`)
	if !risk.IsTransitionInput(err) {
		t.Fatalf("approval without accepted_until: want transition input error, got %v", err)
	}
	_, err = proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"accept","acceptance_status":"approved",`+
			`"accepter":"ciso@example.com","accepted_until":"2099-06-30"}`)
	if !risk.IsTransitionInput(err) {
		t.Fatalf("approval naming an accepter: want transition input error, got %v", err)
	}
	if _, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment, approve); !risk.IsApprovalRefusal(err) {
		t.Fatalf("approval confirmed by a machine: want approval refusal, got %v", err)
	}
	created, err := store.Create(ctx, writeproposals.CreateInput{
		ToolName:       writeproposals.ToolUpdateRiskTreatment,
		ToolInput:      json.RawMessage(approve),
		AIModelName:    "llama3.1:8b-instruct-q5",
		AIModelVersion: "2026-05-01",
		CreatedBy:      "key_" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Create approval proposal: %v", err)
	}
	cred := "jwt:" + uuid.NewString()
	if _, err := store.ConfirmAs(ctx, created.ID, cred, writeproposals.Confirmer{
		Approver:  approval.Approver{UserID: owner.String(), CredentialID: cred},
		Principal: owner.String(),
	}); err != nil {
		t.Fatalf("approval confirmed by a person: %v", err)
	}
	if _, st := readTreatment(t, admin, riskID); st == nil || *st != "approved" {
		t.Fatalf("after approval: status=%v; want approved", st)
	}

	// An approved acceptance is revoked, not replaced by a treatment change.
	_, err = proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"avoid"}`)
	if !errors.Is(err, risk.ErrTransitionState) {
		t.Fatalf("avoid over an approved acceptance: want transition state error, got %v", err)
	}
	if tr, st := readTreatment(t, admin, riskID); tr != "accept" || st == nil || *st != "approved" {
		t.Fatalf("after refused avoid: treatment=%q status=%v; want accept/approved", tr, st)
	}
}

// TestApplyCreateRisk_ProposedAcceptanceRecordsConfirmer pins #2371 on the
// MCP surface: create_risk refuses an approved acceptance, records the
// confirming person as the proposer of a proposed one, and that person
// cannot then approve it while a different person can.
func TestApplyCreateRisk_ProposedAcceptanceRecordsConfirmer(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := treatmentTenant(t, admin)
	store := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(app), nil)
	ctx := ctxFor(t, tenant)

	people := make([]uuid.UUID, 2)
	for i := range people {
		people[i] = uuid.New()
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO users (id, tenant_id, email, display_name, status)
			VALUES ($1, $2, $3, 'Person', 'active')
		`, people[i], tenant, people[i].String()+"@example.com"); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	confirmAs := func(tool, input string, person uuid.UUID) (writeproposals.Proposal, error) {
		t.Helper()
		created, err := store.Create(ctx, writeproposals.CreateInput{
			ToolName:       tool,
			ToolInput:      json.RawMessage(input),
			AIModelName:    "llama3.1:8b-instruct-q5",
			AIModelVersion: "2026-05-01",
			CreatedBy:      "jwt:" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("Create %s proposal: %v", tool, err)
		}
		cred := "jwt:" + uuid.NewString()
		return store.ConfirmAs(ctx, created.ID, cred, writeproposals.Confirmer{
			Approver:  approval.Approver{UserID: person.String(), CredentialID: cred},
			Principal: person.String(),
		})
	}

	_, err := confirmAs(writeproposals.ToolCreateRisk,
		`{"title":"Approved at create","category":"operational","treatment":"accept","acceptance_status":"approved"}`, people[0])
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("create_risk with an approved acceptance: want treatment validation, got %v", err)
	}

	// #2391: a proposed acceptance needs the terms POST /v1/risks and the
	// propose transition need. The proposal is refused when filed (a 400
	// on the REST surface), before anyone is asked to confirm it.
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	for name, input := range map[string]string{
		"no justification": `{"title":"T","category":"operational","treatment":"accept","acceptance_status":"proposed","proposed_until":"2099-06-30"}`,
		"no expiry":        `{"title":"T","category":"operational","treatment":"accept","acceptance_status":"proposed","justification":"j"}`,
		"past expiry":      `{"title":"T","category":"operational","treatment":"accept","acceptance_status":"proposed","justification":"j","proposed_until":"` + yesterday + `"}`,
	} {
		_, err := store.Create(ctx, writeproposals.CreateInput{
			ToolName:       writeproposals.ToolCreateRisk,
			ToolInput:      json.RawMessage(input),
			AIModelName:    "llama3.1:8b-instruct-q5",
			AIModelVersion: "2026-05-01",
			CreatedBy:      "jwt:" + uuid.NewString(),
		})
		if !errors.Is(err, writeproposals.ErrInvalidInput) {
			t.Errorf("file create_risk, %s: want ErrInvalidInput, got %v", name, err)
		}
	}

	applied, err := confirmAs(writeproposals.ToolCreateRisk,
		`{"title":"Proposed at create","category":"operational","treatment":"accept","acceptance_status":"proposed",`+
			`"justification":"Compensated by the edge filter.","proposed_until":"2099-06-30"}`, people[0])
	if err != nil {
		t.Fatalf("create_risk with a proposed acceptance: %v", err)
	}
	riskID := *applied.AppliedSubject
	var proposedBy, justification, proposedUntil string
	if err := admin.QueryRow(context.Background(),
		`SELECT acceptance_proposed_by, acceptance_justification, acceptance_proposed_until::text FROM risks WHERE id = $1`,
		riskID).Scan(&proposedBy, &justification, &proposedUntil); err != nil {
		t.Fatalf("read proposer: %v", err)
	}
	if proposedBy != people[0].String() {
		t.Fatalf("acceptance_proposed_by = %q; want the confirming person %s", proposedBy, people[0])
	}
	if justification != "Compensated by the edge filter." || proposedUntil != "2099-06-30" {
		t.Fatalf("acceptance terms = %q, %q; want the proposal's", justification, proposedUntil)
	}

	approve := `{"risk_id":"` + riskID + `","treatment":"accept","acceptance_status":"approved","accepted_until":"2099-06-30"}`
	if _, err := confirmAs(writeproposals.ToolUpdateRiskTreatment, approve, people[0]); !errors.Is(err, risk.ErrAcceptanceSelfApproval) {
		t.Fatalf("approval by the proposer: want self-approval refusal, got %v", err)
	}
	if _, err := confirmAs(writeproposals.ToolUpdateRiskTreatment, approve, people[1]); err != nil {
		t.Fatalf("approval by a different person: %v", err)
	}
	if _, st := readTreatment(t, admin, riskID); st == nil || *st != "approved" {
		t.Fatalf("after approval: status=%v; want approved", st)
	}
}
