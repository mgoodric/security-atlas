//go:build integration

// #2101 integration tests: the organisation's Allow self-approval setting.
// With it on, the submitter approves their own policy; the policy is
// flagged self-approved on the wire and policy_audit_log records it. A
// machine credential is still refused. Real Postgres under RLS.

package policies_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// allowSelfApproval sets the tenant's Allow self-approval setting,
// creating its tenants row when the harness tenant has none.
func allowSelfApproval(t *testing.T, s setupResult, allow bool) {
	t.Helper()
	tag, err := s.admin.Exec(context.Background(),
		`UPDATE tenants SET allow_self_approval = $2 WHERE id = $1`, s.tenant, allow)
	if err != nil {
		t.Fatalf("set allow_self_approval: %v", err)
	}
	if tag.RowsAffected() > 0 {
		return
	}
	if _, err := s.admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name, allow_self_approval) VALUES ($1, $2, $3)`,
		s.tenant, "policies-2101-"+s.tenant[:8], allow); err != nil {
		t.Fatalf("insert tenants row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, s.tenant)
	})
}

func TestApprove_SelfApprovalOptIn(t *testing.T) {
	s := setup(t)
	allowSelfApproval(t, s, true)

	// Even with self-approval on, a machine credential never approves.
	machineID := createDraftPolicy(t, s, "Machine Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+machineID+"/submit", nil, userJWT(t, s, s.adminUser), http.StatusOK)
	if code, body := approveWithKey(t, s, machineID, nil); code != http.StatusForbidden || !strings.Contains(body, "only a person can approve") {
		t.Fatalf("unbound key with self-approval on: status %d (want 403) body=%s", code, body)
	}

	// The submitter approves their own policy from another session.
	id := createDraftPolicy(t, s, "Self Approved Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, userJWT(t, s, s.adminUser), http.StatusOK)
	raw := s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, userJWT(t, s, s.adminUser), http.StatusOK)
	var approved struct {
		Policy struct {
			Status       string `json:"status"`
			ApprovedBy   string `json:"approved_by"`
			SelfApproved bool   `json:"self_approved"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(raw, &approved); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if approved.Policy.Status != "approved" || approved.Policy.ApprovedBy != s.adminUser.String() || !approved.Policy.SelfApproved {
		t.Fatalf("self-approval: %+v, want approved by the submitter and flagged", approved.Policy)
	}
	var action, actor string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT action, actor FROM policy_audit_log WHERE tenant_id = $1 AND policy_id = $2`,
		s.tenant, id).Scan(&action, &actor); err != nil {
		t.Fatalf("read policy_audit_log: %v", err)
	}
	if action != "self_approved" || actor != s.adminUser.String() {
		t.Fatalf("audit row: action %q actor %q, want self_approved by %s", action, actor, s.adminUser)
	}

	// A second person's approval is not flagged.
	other := createDraftPolicy(t, s, "Second Approver Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+other+"/submit", nil, userJWT(t, s, s.adminUser), http.StatusOK)
	second := seedUser(t, s.admin, s.tenant, "second-approver@2101.test")
	code, body := approveWithKey(t, s, other, &second)
	if code != http.StatusOK || !strings.Contains(body, `"self_approved":false`) {
		t.Fatalf("second approver: status %d body=%s, want 200 and unflagged", code, body)
	}

	// Turned back off, the submitter is refused again.
	allowSelfApproval(t, s, false)
	off := createDraftPolicy(t, s, "Off Again Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+off+"/submit", nil, userJWT(t, s, s.adminUser), http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+off+"/approve", nil, userJWT(t, s, s.adminUser), http.StatusForbidden)
}

// TestDB_SelfApprovedFlagNeedsTheSetting covers the database leg: a direct
// write that marks a policy self-approved, skipping the application rule,
// is refused by the self_approval_requires_setting trigger while the
// setting is off.
func TestDB_SelfApprovedFlagNeedsTheSetting(t *testing.T) {
	s := setup(t)
	allowSelfApproval(t, s, false)
	id := createDraftPolicy(t, s, "Direct Write Policy", "1.0", nil)

	const flag = `UPDATE policies SET self_approved = true WHERE id = $1`
	_, err := s.admin.Exec(context.Background(), flag, id)
	if err == nil || !strings.Contains(err.Error(), "Allow self-approval") {
		t.Fatalf("direct self_approved write with the setting off: err = %v, want the trigger to refuse", err)
	}
	allowSelfApproval(t, s, true)
	if _, err := s.admin.Exec(context.Background(), flag, id); err != nil {
		t.Fatalf("direct self_approved write with the setting on: %v", err)
	}
}
