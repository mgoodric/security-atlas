//go:build integration

// #2105 — integration tests for the risk treatment and acceptance
// transitions: PATCH /v1/risks/{id}/treatment and
// /v1/risks/{id}/acceptance/{propose,approve,reject,revoke}, and the MCP
// update_risk_treatment applier that goes through the same transitions.
// Real Postgres under RLS + the assembled router. Every approval test uses
// two people, plus one with Allow self-approval on.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run Transition

package risks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// transitionEnv is a server with two people in one tenant: the owner, who
// proposes, and the approver. Both are approvers by role, so the approval
// rule, not the role check, is what refuses the owner's own approval.
type transitionEnv struct {
	admin      *pgxpool.Pool
	app        *pgxpool.Pool
	tenant     string
	server     *httptest.Server
	owner      uuid.UUID
	approver   uuid.UUID
	ownerJWT   string
	apprJWT    string
	nonApprJWT string
	// srv mints further JWTs for the tenant (#2107).
	srv *api.Server
}

func newTransitionEnv(t *testing.T) transitionEnv {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin,
		"mcp_write_proposals",
		"risk_audit_log",
		"risks",
		"users",
	)
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	env := transitionEnv{admin: admin, app: app, tenant: tenant, srv: srv}
	env.owner = seedTransitionUser(t, admin, tenant, "owner")
	env.approver = seedTransitionUser(t, admin, tenant, "approver")
	jwtFor := func(u uuid.UUID) string {
		claims := testjwt.AdminFor(uuid.MustParse(tenant))
		claims.Subject = "user:" + u.String()
		return srv.IssueTestJWT(t, claims)
	}
	env.ownerJWT, env.apprJWT = jwtFor(env.owner), jwtFor(env.approver)
	// A grc_engineer without the approver flag.
	env.nonApprJWT = srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"grc_engineer"}))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil")
	}
	env.server = httptest.NewServer(h)
	t.Cleanup(env.server.Close)
	setAllowSelfApproval(t, env, false)
	return env
}

func seedTransitionUser(t *testing.T, admin *pgxpool.Pool, tenant, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := name + "-" + id.String()[:8] + "@2105.test"
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, $4, 'active')`, id, tenant, email, name); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// setAllowSelfApproval sets the tenant's Allow self-approval setting,
// creating its tenants row on first use.
func setAllowSelfApproval(t *testing.T, env transitionEnv, allow bool) {
	t.Helper()
	tag, err := env.admin.Exec(context.Background(),
		`UPDATE tenants SET allow_self_approval = $2 WHERE id = $1`, env.tenant, allow)
	if err != nil {
		t.Fatalf("set allow_self_approval: %v", err)
	}
	if tag.RowsAffected() > 0 {
		return
	}
	if _, err := env.admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name, allow_self_approval) VALUES ($1, $2, $3)`,
		env.tenant, "risks-2105-"+env.tenant[:8], allow); err != nil {
		t.Fatalf("insert tenants row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, env.tenant)
	})
}

func (env transitionEnv) do(t *testing.T, method, path, bearer string, payload any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&buf).Encode(payload)
	}
	req, _ := http.NewRequest(method, env.server.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// step runs one transition and fails unless it returns want.
func (env transitionEnv) step(t *testing.T, id, transition, bearer string, payload any, want int) map[string]any {
	t.Helper()
	code, body := env.do(t, http.MethodPatch, "/v1/risks/"+id+"/"+transition, bearer, payload)
	if code != want {
		t.Fatalf("%s: status %d, want %d: %v", transition, code, want, body)
	}
	return body
}

func (env transitionEnv) newRisk(t *testing.T) string {
	t.Helper()
	code, body := env.do(t, http.MethodPost, "/v1/risks", env.ownerJWT, map[string]any{
		"title":          "transition risk",
		"category":       "operational",
		"inherent_score": map[string]int{"likelihood": 3, "impact": 4},
	})
	if code != http.StatusCreated {
		t.Fatalf("create: status %d: %v", code, body)
	}
	return riskField(t, body, "id").(string)
}

// auditRows reads the risk's audit trail oldest first.
func (env transitionEnv) auditRows(t *testing.T, id string) []map[string]any {
	t.Helper()
	code, body := env.do(t, http.MethodGet, "/v1/risks/"+id+"/audit-log", env.ownerJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("audit-log: status %d: %v", code, body)
	}
	raw, _ := body["entries"].([]any)
	out := make([]map[string]any, len(raw))
	for i, e := range raw {
		out[len(raw)-1-i] = e.(map[string]any)
	}
	return out
}

func actionsOf(rows []map[string]any) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r["action"].(string)
	}
	return out
}

func future(days int) string {
	return time.Now().UTC().AddDate(0, 0, days).Format(time.DateOnly)
}

func proposal() map[string]any {
	return map[string]any{"justification": "Compensated by the WAF; retiring in Q3.", "proposed_until": future(90)}
}

// TestTransition_AuditActionCheck: the CHECK keeps every earlier action,
// takes the five transition actions, and refuses anything else.
func TestTransition_AuditActionCheck(t *testing.T) {
	env := newTransitionEnv(t)
	insert := func(action string) error {
		_, err := env.admin.Exec(context.Background(), `
			INSERT INTO risk_audit_log (tenant_id, risk_id, actor, action_type, before_state, after_state)
			VALUES ($1, gen_random_uuid(), 'test', $2, '{}', '{}')`, env.tenant, action)
		return err
	}
	for _, action := range []string{
		"placement_changed", "category_changed", "fields_changed",
		"treatment_changed", "acceptance_proposed", "acceptance_approved",
		"acceptance_rejected", "acceptance_revoked",
	} {
		if err := insert(action); err != nil {
			t.Fatalf("%s refused: %v", action, err)
		}
	}
	if err := insert("acceptance_expired"); err == nil {
		t.Fatal("an action outside the list was accepted")
	}
}

// TestTransition_ProposeThenSecondPersonApproves: the owner proposes; a
// second person approves, setting accepted, accepted_until and the
// approver. Each step is one audit row.
func TestTransition_ProposeThenSecondPersonApproves(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)

	body := env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)
	if riskField(t, body, "treatment") != "accept" || riskField(t, body, "acceptance_status") != "proposed" {
		t.Fatalf("after propose: %v", body["risk"])
	}
	acc := riskField(t, body, "acceptance").(map[string]any)
	if acc["proposed_by"] != env.owner.String() || acc["proposed_until"] != future(90) {
		t.Fatalf("acceptance after propose: %v", acc)
	}

	until := future(180)
	body = env.step(t, id, "acceptance/approve", env.apprJWT, map[string]any{"accepted_until": until}, http.StatusOK)
	if riskField(t, body, "acceptance_status") != "approved" ||
		riskField(t, body, "accepted_until") != until ||
		riskField(t, body, "accepter") != env.approver.String() {
		t.Fatalf("after approve: %v", body["risk"])
	}
	if acc := riskField(t, body, "acceptance").(map[string]any); acc["self_approved"] != false {
		t.Fatalf("second-person approval flagged self-approved: %v", acc)
	}

	rows := env.auditRows(t, id)
	if got := strings.Join(actionsOf(rows), ","); got != "acceptance_proposed,acceptance_approved" {
		t.Fatalf("audit actions = %s", got)
	}
	if rows[0]["actor"] != env.owner.String() || rows[1]["actor"] != env.approver.String() {
		t.Fatalf("audit actors = %v, %v", rows[0]["actor"], rows[1]["actor"])
	}
	after := rows[1]["after"].(map[string]any)
	if after["accepted_until"] != until || after["accepter"] != env.approver.String() || after["self_approved"] != false {
		t.Fatalf("approve audit after = %v", after)
	}
}

// TestTransition_ProposerApprovalRefusedUnlessSelfApprovalOn: the proposer
// approving their own acceptance is a 403 with nothing written; with
// Allow self-approval on it succeeds and is flagged on the risk and in the
// audit row.
func TestTransition_ProposerApprovalRefusedUnlessSelfApprovalOn(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)
	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)

	until := map[string]any{"accepted_until": future(60)}
	env.step(t, id, "acceptance/approve", env.ownerJWT, until, http.StatusForbidden)
	_, got := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
	if riskField(t, got, "acceptance_status") != "proposed" {
		t.Fatalf("refused approval changed the risk: %v", got["risk"])
	}

	setAllowSelfApproval(t, env, true)
	body := env.step(t, id, "acceptance/approve", env.ownerJWT, until, http.StatusOK)
	acc := riskField(t, body, "acceptance").(map[string]any)
	if riskField(t, body, "acceptance_status") != "approved" || acc["self_approved"] != true {
		t.Fatalf("self-approval: %v", body["risk"])
	}
	rows := env.auditRows(t, id)
	last := rows[len(rows)-1]
	if last["action"] != "acceptance_approved" || last["after"].(map[string]any)["self_approved"] != true {
		t.Fatalf("self-approval audit row = %v", last)
	}
}

// TestTransition_CreateRecordsProposer (#2371): a create may not approve an
// acceptance; one created proposed records its creator as the proposer, so
// the creator cannot approve it and a second person can.
func TestTransition_CreateRecordsProposer(t *testing.T) {
	env := newTransitionEnv(t)
	create := func(status string) (int, map[string]any) {
		return env.do(t, http.MethodPost, "/v1/risks", env.ownerJWT, map[string]any{
			"title":             "accepted at create",
			"category":          "operational",
			"inherent_score":    map[string]int{"likelihood": 3, "impact": 4},
			"treatment":         "accept",
			"acceptance_status": status,
		})
	}
	if code, body := create("approved"); code != http.StatusBadRequest {
		t.Fatalf("create approved: status %d, want 400: %v", code, body)
	}
	code, body := create("proposed")
	if code != http.StatusCreated {
		t.Fatalf("create proposed: status %d: %v", code, body)
	}
	id := riskField(t, body, "id").(string)
	if acc, _ := riskField(t, body, "acceptance").(map[string]any); acc["proposed_by"] != env.owner.String() {
		t.Fatalf("proposed_by after create = %v; want the creator %s", acc, env.owner)
	}

	until := map[string]any{"accepted_until": future(60)}
	env.step(t, id, "acceptance/approve", env.ownerJWT, until, http.StatusForbidden)
	body = env.step(t, id, "acceptance/approve", env.apprJWT, until, http.StatusOK)
	if riskField(t, body, "acceptance_status") != "approved" || riskField(t, body, "accepter") != env.approver.String() {
		t.Fatalf("after second-person approval: %v", body["risk"])
	}
}

// TestTransition_UnknownProposerRefused (#2371): an acceptance with no
// proposer a person can be compared with (none recorded, as on rows
// proposed before #2105, or a pre-#1807 session id) cannot be approved by
// anyone, its creator included: 409, nothing written. Rejecting it and
// proposing again records a proposer and approval works.
func TestTransition_UnknownProposerRefused(t *testing.T) {
	for name, proposer := range map[string]string{
		"empty":   "",
		"session": "jwt:" + uuid.NewString(),
	} {
		t.Run(name, func(t *testing.T) {
			env := newTransitionEnv(t)
			id := env.newRisk(t)
			env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)
			if _, err := env.admin.Exec(context.Background(),
				`UPDATE risks SET acceptance_proposed_by = $2 WHERE id = $1`, id, proposer); err != nil {
				t.Fatalf("clear proposer: %v", err)
			}
			setAllowSelfApproval(t, env, true)

			until := map[string]any{"accepted_until": future(60)}
			for _, bearer := range []string{env.ownerJWT, env.apprJWT} {
				env.step(t, id, "acceptance/approve", bearer, until, http.StatusConflict)
			}
			_, got := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
			if riskField(t, got, "acceptance_status") != "proposed" {
				t.Fatalf("refused approval changed the risk: %v", got["risk"])
			}

			env.step(t, id, "acceptance/reject", env.apprJWT, map[string]any{}, http.StatusOK)
			env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)
			env.step(t, id, "acceptance/approve", env.apprJWT, until, http.StatusOK)
		})
	}
}

// TestTransition_MachineCredentialRefused: an API key bound to no person
// never approves, with or without self-approval.
func TestTransition_MachineCredentialRefused(t *testing.T) {
	env := newTransitionEnv(t)
	setAllowSelfApproval(t, env, true)
	id := env.newRisk(t)
	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)

	cred := credstore.Credential{ID: "key_" + uuid.NewString(), TenantID: env.tenant, IsApprover: true}
	cred.UserID = cred.ID
	ctx, err := tenancy.WithTenant(authctx.WithCredential(context.Background(), cred), env.tenant)
	if err != nil {
		t.Fatal(err)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req := httptest.NewRequest(http.MethodPatch, "/v1/risks/"+id+"/acceptance/approve",
		strings.NewReader(`{"accepted_until":"`+future(30)+`"}`))
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	risks.New(risk.NewStore(env.app)).ApproveAcceptance(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "only a person") {
		t.Fatalf("machine approval: status %d body %s, want 403", rec.Code, rec.Body.String())
	}
}

// TestTransition_ApproveNeedsAcceptedUntilAndProposal: accepted_until is
// required and must be after today; approving with nothing proposed is a
// conflict.
func TestTransition_ApproveNeedsAcceptedUntilAndProposal(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)
	env.step(t, id, "acceptance/approve", env.apprJWT, map[string]any{"accepted_until": future(30)}, http.StatusConflict)
	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)
	env.step(t, id, "acceptance/approve", env.apprJWT, map[string]any{}, http.StatusBadRequest)
	env.step(t, id, "acceptance/approve", env.apprJWT, map[string]any{"accepted_until": future(0)}, http.StatusBadRequest)
	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusConflict)
}

// TestTransition_RejectAndRevokeAreLogged: a rejected proposal and a
// revoked approval each return the risk to untreated and write one audit
// row that keeps the acceptance and the reason. Revoke needs a reason.
func TestTransition_RejectAndRevokeAreLogged(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)

	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)
	body := env.step(t, id, "acceptance/reject", env.apprJWT, map[string]any{"reason": "Needs a compensating control first."}, http.StatusOK)
	if riskField(t, body, "treatment") != "untreated" || riskField(t, body, "acceptance") != nil {
		t.Fatalf("after reject: %v", body["risk"])
	}

	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)
	env.step(t, id, "acceptance/approve", env.apprJWT, map[string]any{"accepted_until": future(30)}, http.StatusOK)
	env.step(t, id, "acceptance/revoke", env.apprJWT, map[string]any{}, http.StatusBadRequest)
	body = env.step(t, id, "acceptance/revoke", env.apprJWT, map[string]any{"reason": "The WAF rule was removed."}, http.StatusOK)
	if riskField(t, body, "treatment") != "untreated" || riskField(t, body, "accepted_until") != nil {
		t.Fatalf("after revoke: %v", body["risk"])
	}
	env.step(t, id, "acceptance/revoke", env.apprJWT, map[string]any{"reason": "again"}, http.StatusConflict)

	rows := env.auditRows(t, id)
	want := "acceptance_proposed,acceptance_rejected,acceptance_proposed,acceptance_approved,acceptance_revoked"
	if got := strings.Join(actionsOf(rows), ","); got != want {
		t.Fatalf("audit actions = %s, want %s", got, want)
	}
	rejected, revoked := rows[1], rows[4]
	if rejected["before"].(map[string]any)["justification"] == "" ||
		rejected["after"].(map[string]any)["reason"] != "Needs a compensating control first." {
		t.Fatalf("reject row = %v", rejected)
	}
	if revoked["before"].(map[string]any)["accepter"] != env.approver.String() ||
		revoked["after"].(map[string]any)["reason"] != "The WAF rule was removed." {
		t.Fatalf("revoke row = %v", revoked)
	}
}

// TestTransition_ChangeTreatment: mitigate with no linked control or plan
// is refused; transfer with an instrument reference succeeds and is
// logged; changing treatment while an acceptance is in flight conflicts.
func TestTransition_ChangeTreatment(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)

	code, body := env.do(t, http.MethodPatch, "/v1/risks/"+id+"/treatment", env.ownerJWT, map[string]any{"treatment": "mitigate"})
	if code != http.StatusBadRequest || !strings.Contains(body["error"].(string), "linked control or linked action plan") {
		t.Fatalf("mitigate without links: status %d %v", code, body)
	}
	env.step(t, id, "treatment", env.ownerJWT, map[string]any{"treatment": "transfer"}, http.StatusBadRequest)
	env.step(t, id, "treatment", env.ownerJWT, map[string]any{"treatment": "accept"}, http.StatusBadRequest)
	body = env.step(t, id, "treatment", env.ownerJWT,
		map[string]any{"treatment": "transfer", "instrument_reference": "CYBER-POL-7"}, http.StatusOK)
	if riskField(t, body, "treatment") != "transfer" || riskField(t, body, "instrument_reference") != "CYBER-POL-7" {
		t.Fatalf("after transfer: %v", body["risk"])
	}
	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)
	env.step(t, id, "treatment", env.ownerJWT, map[string]any{"treatment": "avoid"}, http.StatusConflict)

	rows := env.auditRows(t, id)
	if got := strings.Join(actionsOf(rows), ","); got != "treatment_changed,acceptance_proposed" {
		t.Fatalf("audit actions = %s", got)
	}
	if after := rows[0]["after"].(map[string]any); after["treatment"] != "transfer" || after["instrument_reference"] != "CYBER-POL-7" {
		t.Fatalf("treatment_changed after = %v", after)
	}
}

// TestTransition_ApproverRoleRequired: a non-approver cannot approve,
// reject or revoke.
func TestTransition_ApproverRoleRequired(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)
	env.step(t, id, "acceptance/propose", env.ownerJWT, proposal(), http.StatusOK)

	ownerOnly := env.nonApprJWT
	for _, tr := range []string{"acceptance/approve", "acceptance/reject", "acceptance/revoke"} {
		env.step(t, id, tr, ownerOnly, map[string]any{"accepted_until": future(30), "reason": "x"}, http.StatusForbidden)
	}
}

// fileTreatmentProposal files an update_risk_treatment MCP proposal and
// returns its id.
func (env transitionEnv) fileTreatmentProposal(t *testing.T, input map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(input)
	code, body := env.do(t, http.MethodPost, "/v1/mcp/write-proposals", env.ownerJWT, map[string]any{
		"tool_name":        "update_risk_treatment",
		"tool_input":       json.RawMessage(raw),
		"ai_model_name":    "test-model",
		"ai_model_version": "1",
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("file proposal: status %d: %v", code, body)
	}
	p, _ := body["proposal"].(map[string]any)
	return p["id"].(string)
}

func (env transitionEnv) confirm(t *testing.T, proposalID, bearer string, want int) map[string]any {
	t.Helper()
	code, body := env.do(t, http.MethodPost, "/v1/mcp/write-proposals/"+proposalID+"/confirm", bearer, nil)
	if code != want {
		t.Fatalf("confirm: status %d, want %d: %v", code, want, body)
	}
	return body
}

// TestTransition_MCPProposalProducesTheSameEvents: confirmed
// update_risk_treatment proposals propose and approve through the same
// transitions, writing the same audit actions, and the approval rule binds
// the confirming person.
func TestTransition_MCPProposalProducesTheSameEvents(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)

	propose := env.fileTreatmentProposal(t, map[string]any{
		"risk_id": id, "treatment": "accept", "acceptance_status": "proposed",
		"justification": "Low exposure; vendor fix scheduled.", "accepted_until": future(90),
	})
	env.confirm(t, propose, env.ownerJWT, http.StatusOK)

	approve := env.fileTreatmentProposal(t, map[string]any{
		"risk_id": id, "treatment": "accept", "acceptance_status": "approved", "accepted_until": future(120),
	})
	// The owner proposed (by confirming), so the owner cannot approve.
	body := env.confirm(t, approve, env.ownerJWT, http.StatusForbidden)
	if body["code"] != "approval_refused" {
		t.Fatalf("self-confirm code = %v", body["code"])
	}
	env.confirm(t, approve, env.apprJWT, http.StatusOK)

	_, got := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
	if riskField(t, got, "acceptance_status") != "approved" || riskField(t, got, "accepter") != env.approver.String() {
		t.Fatalf("after MCP approve: %v", got["risk"])
	}

	// A treatment change while the acceptance stands is refused, as on REST.
	avoid := env.fileTreatmentProposal(t, map[string]any{"risk_id": id, "treatment": "avoid"})
	env.confirm(t, avoid, env.apprJWT, http.StatusConflict)

	rows := env.auditRows(t, id)
	if got := strings.Join(actionsOf(rows), ","); got != "acceptance_proposed,acceptance_approved" {
		t.Fatalf("audit actions = %s", got)
	}
	if rows[0]["actor"] != env.owner.String() || rows[1]["actor"] != env.approver.String() {
		t.Fatalf("audit actors = %v, %v", rows[0]["actor"], rows[1]["actor"])
	}
}
