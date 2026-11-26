//go:build integration

// #1844: a treatment-validation refusal on confirm reaches the operator as
// a 400 with the code POST /v1/risks uses, not an opaque 500. The confirm
// rolls back, so the risk is untouched and the proposal stays confirmable.

package mcpwriteproposals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestHTTP_ConfirmInvalidRiskTreatment_Returns400AndWritesNothing(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	h := setupHTTPServer(t, tenant)
	ctx := context.Background()

	propose := func(tool, input string) string {
		t.Helper()
		body := fmt.Sprintf(`{"tool_name": %q, "tool_input": %s, "ai_model_name": "m", "ai_model_version": "v"}`, tool, input)
		resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: %d %s", tool, resp.StatusCode, raw)
		}
		var env struct {
			Proposal struct {
				ID string `json:"id"`
			} `json:"proposal"`
		}
		_ = json.Unmarshal(raw, &env)
		return env.Proposal.ID
	}
	confirm := func(id string) (*http.Response, []byte) {
		t.Helper()
		return doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals/"+id+"/confirm", h.approverBearer, "")
	}
	riskRow := func(riskID string) (treatment, updatedAt string) {
		t.Helper()
		if err := admin.QueryRow(ctx,
			`SELECT treatment::text, updated_at::text FROM risks WHERE id = $1 AND tenant_id = $2`,
			riskID, tenant).Scan(&treatment, &updatedAt); err != nil {
			t.Fatalf("read risk: %v", err)
		}
		return treatment, updatedAt
	}

	createID := propose("create_risk", `{"title": "Vendor SLA breach", "category": "operational"}`)
	resp, raw := confirm(createID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm create_risk: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Proposal struct {
			AppliedSubject string `json:"applied_subject"`
		} `json:"proposal"`
	}
	_ = json.Unmarshal(raw, &created)
	riskID := created.Proposal.AppliedSubject
	beforeTreatment, beforeUpdated := riskRow(riskID)

	// transfer requires instrument_reference, which the risk lacks.
	updateID := propose("update_risk_treatment", fmt.Sprintf(`{"risk_id": %q, "treatment": "transfer"}`, riskID))
	resp, raw = confirm(updateID)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("confirm invalid treatment: status %d, want 400; body %s", resp.StatusCode, raw)
	}
	var errBody struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(raw, &errBody); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, raw)
	}
	if errBody.Code != "treatment_validation_failed" {
		t.Fatalf("code = %q, want treatment_validation_failed; body %s", errBody.Code, raw)
	}

	// Nothing written: the risk row is unchanged.
	if tr, up := riskRow(riskID); tr != beforeTreatment || up != beforeUpdated {
		t.Fatalf("risk changed after refused confirm: treatment %q->%q, updated_at %q->%q",
			beforeTreatment, tr, beforeUpdated, up)
	}
	// The proposal is still ai_proposed, unapproved, unapplied.
	var (
		state         string
		humanApproved bool
		appliedAt     *string
	)
	if err := admin.QueryRow(ctx, `
		SELECT state::text, human_approved, applied_at::text
		FROM mcp_write_proposals WHERE id = $1
	`, updateID).Scan(&state, &humanApproved, &appliedAt); err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	if state != "ai_proposed" || humanApproved || appliedAt != nil {
		t.Fatalf("proposal after refused confirm: state=%q human_approved=%v applied_at=%v; want ai_proposed, false, nil",
			state, humanApproved, appliedAt)
	}

	// Retryable: once the risk carries the instrument, the same proposal confirms.
	if _, err := admin.Exec(ctx,
		`UPDATE risks SET instrument_reference = 'policy-123' WHERE id = $1 AND tenant_id = $2`,
		riskID, tenant); err != nil {
		t.Fatalf("set instrument_reference: %v", err)
	}
	resp, raw = confirm(updateID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry confirm: %d %s", resp.StatusCode, raw)
	}
	if tr, _ := riskRow(riskID); tr != "transfer" {
		t.Fatalf("treatment after retry = %q, want transfer", tr)
	}
}
