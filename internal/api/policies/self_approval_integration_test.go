//go:build integration

// #1807 integration tests — who may approve a policy. Only a person
// approves (an unbound API key does not), and never the person who
// submitted it: not from a second session, and not through an API key
// bound to them. A policy submitted before #1807 under a session id is
// refused until re-submitted. Real Postgres under RLS; JWT callers are
// driven through the full HTTP stack.

package policies_test

import (
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
	"github.com/mgoodric/security-atlas/internal/api/policies"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type policyStatus struct {
	Policy struct {
		Status      string `json:"status"`
		SubmittedBy string `json:"submitted_by"`
		ApprovedBy  string `json:"approved_by"`
	} `json:"policy"`
}

func decodeStatus(t *testing.T, raw []byte) policyStatus {
	t.Helper()
	var p policyStatus
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode policy: %v body=%s", err, raw)
	}
	return p
}

// userJWT mints an admin JWT in the production subject shape, "user:<uuid>".
func userJWT(t *testing.T, s setupResult, user uuid.UUID) string {
	t.Helper()
	claims := testjwt.AdminFor(uuid.MustParse(s.tenant))
	claims.Subject = "user:" + user.String()
	return s.apiServer.IssueTestJWT(t, claims)
}

// approveWithKey calls the approve handler as an approver API key, in the
// credential shape every key-authenticated path hands a handler: a "key_"
// id, and a UserID that is the bare users(id) uuid when the key is bound to
// a user and the key's own id when it is not. On /v1 the HTTP stack admits
// only JWTs, so the key is placed in the request context directly; the
// handler and store are the real ones. It returns the status and body.
func approveWithKey(t *testing.T, s setupResult, id string, user *uuid.UUID) (int, string) {
	t.Helper()
	cred := credstore.Credential{ID: "key_" + uuid.NewString(), TenantID: s.tenant, IsApprover: true}
	cred.UserID = cred.ID
	if user != nil {
		cred.UserID = user.String()
	}
	ctx, err := tenancy.WithTenant(authctx.WithCredential(context.Background(), cred), s.tenant)
	if err != nil {
		t.Fatal(err)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req := httptest.NewRequest(http.MethodPatch, "/v1/policies/"+id+"/approve", nil)
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	policies.New(policy.NewStore(s.app)).Approve(rec, req)
	return rec.Code, rec.Body.String()
}

func TestApprove_SubmitterRefused_SecondApproverSucceeds(t *testing.T) {
	s := setup(t)
	id := createDraftPolicy(t, s, "Access Policy", "1.0", nil)
	submitterSession := userJWT(t, s, s.adminUser)
	submitted := decodeStatus(t, s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, submitterSession, http.StatusOK))
	if submitted.Policy.SubmittedBy != s.adminUser.String() {
		t.Fatalf("submitted_by = %q, want the bare user id %q", submitted.Policy.SubmittedBy, s.adminUser)
	}

	// The submitter, same session.
	raw := s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, submitterSession, http.StatusForbidden)
	if !strings.Contains(string(raw), "cannot approve their own policy") {
		t.Fatalf("self-approval body = %s", raw)
	}
	// The submitter from a second session.
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, userJWT(t, s, s.adminUser), http.StatusForbidden)
	// The submitter through an API key bound to them.
	if code, body := approveWithKey(t, s, id, &s.adminUser); code != http.StatusForbidden {
		t.Fatalf("submitter's own key: status %d (want 403) body=%s", code, body)
	}

	got := decodeStatus(t, s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK))
	if got.Policy.Status != "under_review" {
		t.Fatalf("status after refused approvals = %q, want under_review", got.Policy.Status)
	}

	// A key bound to a second person approves as that person.
	second := seedUser(t, s.admin, s.tenant, "second-approver@1807.test")
	code, body := approveWithKey(t, s, id, &second)
	if code != http.StatusOK {
		t.Fatalf("second person's key: status %d (want 200) body=%s", code, body)
	}
	approved := decodeStatus(t, []byte(body))
	if approved.Policy.Status != "approved" || approved.Policy.ApprovedBy != second.String() {
		t.Fatalf("after second approver: status %q approved_by %q, want approved by %s",
			approved.Policy.Status, approved.Policy.ApprovedBy, second)
	}
}

func TestApprove_UnboundKeyRefused(t *testing.T) {
	s := setup(t)
	id := createDraftPolicy(t, s, "Backup Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)

	code, body := approveWithKey(t, s, id, nil)
	if code != http.StatusForbidden || !strings.Contains(body, "only a person can approve") {
		t.Fatalf("unbound-key approval: status %d (want 403) body=%s", code, body)
	}
}

func TestApprove_LegacySessionSubmitterMustResubmit(t *testing.T) {
	s := setup(t)
	id := createDraftPolicy(t, s, "Vendor Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)
	// Before #1807 a JWT caller's submitter was recorded as its session id.
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET submitted_by = 'jwt:pre-1807-token' WHERE id = $1`, id); err != nil {
		t.Fatalf("age the submitter: %v", err)
	}

	raw := s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.approverBear, http.StatusConflict)
	if !strings.Contains(string(raw), "re-submitted") {
		t.Fatalf("legacy-submitter body = %s", raw)
	}
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, userJWT(t, s, s.adminUser), http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.approverBear, http.StatusOK)
}
