//go:build integration

package risks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// caller makes one request as some credential and returns the status and
// decoded body.
type caller func(method, path string, payload any) (int, map[string]any)

// TestTransition_ProposeIsPeopleOnly (#2623): proposing an acceptance is
// for people only, on PATCH /acceptance/propose, on POST /v1/risks with
// acceptance_status=proposed and on a confirmed MCP proposal (create_risk,
// update_risk_treatment). An unbound API key and an OAuth client get 403
// on each and nothing is written; a key bound to a user and a JWT session
// succeed. The unbound and bound keys are handed to the handler on the
// request context, as the HTTP router admits JWTs only; the OAuth client
// reaches the router as a JWT whose subject names it.
func TestTransition_ProposeIsPeopleOnly(t *testing.T) {
	env := newTransitionEnv(t)
	handler := risks.New(risk.NewStore(env.app))
	viaHandler := func(cred credstore.Credential) caller {
		return func(method, path string, payload any) (int, map[string]any) {
			ctx, err := tenancy.WithTenant(authctx.WithCredential(context.Background(), cred), env.tenant)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			_ = json.NewEncoder(&buf).Encode(payload)
			req := httptest.NewRequest(method, path, &buf)
			rec := httptest.NewRecorder()
			if method == http.MethodPost {
				handler.CreateRisk(rec, req.WithContext(ctx))
			} else {
				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("id", strings.Split(path, "/")[3])
				handler.ProposeAcceptance(rec, req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx)))
			}
			var body map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			return rec.Code, body
		}
	}
	viaRouter := func(bearer string) caller {
		return func(method, path string, payload any) (int, map[string]any) {
			return env.do(t, method, path, bearer, payload)
		}
	}
	oauthClaims := testjwt.AdminFor(uuid.MustParse(env.tenant))
	oauthClaims.Subject = "oauth_client:ci-2623"
	oauthJWT := env.srv.IssueTestJWT(t, oauthClaims)
	unboundID := "key_" + uuid.NewString()

	countRisks := func() float64 {
		t.Helper()
		code, body := env.do(t, http.MethodGet, "/v1/risks", env.ownerJWT, nil)
		if code != http.StatusOK {
			t.Fatalf("list risks: status %d: %v", code, body)
		}
		return body["count"].(float64)
	}
	refusedAsMachine := func(code int, body map[string]any) bool {
		msg, _ := body["error"].(string)
		return code == http.StatusForbidden && strings.Contains(msg, "only a person can propose")
	}

	machines := []struct {
		name string
		call caller
	}{
		{"unbound key", viaHandler(credstore.Credential{
			ID: unboundID, TenantID: env.tenant, UserID: unboundID, IsAdmin: true, IsApprover: true,
		})},
		{"OAuth client", viaRouter(oauthJWT)},
	}
	for _, m := range machines {
		id := env.newRisk(t)
		before := countRisks()
		if code, body := m.call(http.MethodPatch, "/v1/risks/"+id+"/acceptance/propose", proposal()); !refusedAsMachine(code, body) {
			t.Errorf("%s: propose: status %d, want 403 naming the person rule: %v", m.name, code, body)
		}
		if code, body := m.call(http.MethodPost, "/v1/risks", createProposed(nil)); !refusedAsMachine(code, body) {
			t.Errorf("%s: create proposed: status %d, want 403 naming the person rule: %v", m.name, code, body)
		}
		// Nothing written: the risk is still untreated with no proposal in
		// its audit trail, and no risk was created.
		_, got := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
		if riskField(t, got, "treatment") != "untreated" || riskField(t, got, "acceptance_status") != nil {
			t.Errorf("%s: risk after refused propose: %v", m.name, got["risk"])
		}
		for _, r := range env.auditRows(t, id) {
			if r["action"] == "acceptance_proposed" {
				t.Errorf("%s: refused propose wrote an audit row: %v", m.name, r)
			}
		}
		if after := countRisks(); after != before {
			t.Errorf("%s: refused create wrote a risk: count %v -> %v", m.name, before, after)
		}
	}

	// A machine creating a risk that proposes nothing is not refused: the
	// rule is about proposals, not about creating risks.
	if code, body := machines[1].call(http.MethodPost, "/v1/risks", createProposed(map[string]any{
		"treatment": nil, "acceptance_status": nil, "justification": nil, "proposed_until": nil,
	})); code != http.StatusCreated {
		t.Errorf("OAuth client: plain create: status %d, want 201: %v", code, body)
	}

	// MCP: the confirmer is the proposer, so an OAuth client confirming a
	// proposal that proposes an acceptance is refused, and nothing is
	// written; a person then confirms both.
	id := env.newRisk(t)
	treatment := env.fileTreatmentProposal(t, map[string]any{
		"risk_id": id, "treatment": "accept", "acceptance_status": "proposed",
		"justification": "Low exposure.", "accepted_until": future(90),
	})
	if body := env.confirm(t, treatment, oauthJWT, http.StatusForbidden); body["code"] != "approval_refused" {
		t.Errorf("OAuth client confirm update_risk_treatment: code %v", body["code"])
	}
	raw, _ := json.Marshal(createProposed(nil))
	code, body := env.do(t, http.MethodPost, "/v1/mcp/write-proposals", env.ownerJWT, map[string]any{
		"tool_name": "create_risk", "tool_input": json.RawMessage(raw),
		"ai_model_name": "test-model", "ai_model_version": "1",
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("file create_risk: status %d: %v", code, body)
	}
	createID := body["proposal"].(map[string]any)["id"].(string)
	before := countRisks()
	if body := env.confirm(t, createID, oauthJWT, http.StatusForbidden); body["code"] != "approval_refused" {
		t.Errorf("OAuth client confirm create_risk: code %v", body["code"])
	}
	if after := countRisks(); after != before {
		t.Errorf("refused create_risk confirm wrote a risk: count %v -> %v", before, after)
	}
	_, got := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
	if riskField(t, got, "treatment") != "untreated" {
		t.Errorf("risk after refused MCP propose: %v", got["risk"])
	}
	env.confirm(t, treatment, env.ownerJWT, http.StatusOK)
	env.confirm(t, createID, env.ownerJWT, http.StatusOK)

	// People: a key bound to a user and a JWT session both propose, on
	// both paths, and are recorded as that user.
	people := []struct {
		name string
		call caller
	}{
		// TenantRoles stands in for the owner's live role, which authzmw
		// would resolve onto a bound key before the handler's guard (#2664).
		{"bound key", viaHandler(credstore.Credential{ID: "key_" + uuid.NewString(), TenantID: env.tenant, UserID: env.owner.String(), TenantRoles: []string{"grc_engineer"}})},
		{"JWT session", viaRouter(env.ownerJWT)},
	}
	for _, p := range people {
		id := env.newRisk(t)
		code, body := p.call(http.MethodPatch, "/v1/risks/"+id+"/acceptance/propose", proposal())
		if code != http.StatusOK {
			t.Errorf("%s: propose: status %d, want 200: %v", p.name, code, body)
		} else if acc, _ := riskField(t, body, "acceptance").(map[string]any); acc["proposed_by"] != env.owner.String() {
			t.Errorf("%s: proposed_by = %v, want %s", p.name, acc["proposed_by"], env.owner)
		}
		code, body = p.call(http.MethodPost, "/v1/risks", createProposed(nil))
		if code != http.StatusCreated {
			t.Errorf("%s: create proposed: status %d, want 201: %v", p.name, code, body)
		} else if acc, _ := riskField(t, body, "acceptance").(map[string]any); acc["proposed_by"] != env.owner.String() {
			t.Errorf("%s: create proposed_by = %v, want %s", p.name, acc["proposed_by"], env.owner)
		}
	}
}
