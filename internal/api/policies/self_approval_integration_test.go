//go:build integration

// #1807 integration tests — segregation of duties on policy approval. The
// person who submitted a policy for review cannot approve it, even from a
// second session (a different JWT for the same user); a different approver
// can. Real Postgres under RLS, driven through the full HTTP stack.

package policies_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

func TestApprove_SubmitterRefused_SecondApproverSucceeds(t *testing.T) {
	s := setup(t)
	id := createDraftPolicy(t, s, "Access Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)

	// The submitter, same token.
	raw := s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.adminBear, http.StatusForbidden)
	if !strings.Contains(string(raw), "cannot approve their own policy") {
		t.Fatalf("self-approval body = %s", raw)
	}

	// The submitter from a second session: a fresh JWT for the same user.
	claims := testjwt.AdminFor(uuid.MustParse(s.tenant))
	claims.Subject = s.adminUser.String()
	secondSession := s.apiServer.IssueTestJWT(t, claims)
	if secondSession == s.adminBear {
		t.Fatal("expected a distinct token for the second session")
	}
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, secondSession, http.StatusForbidden)

	// Refused approvals leave the policy under review.
	var got struct {
		Policy struct {
			Status string `json:"status"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK), &got); err != nil {
		t.Fatal(err)
	}
	if got.Policy.Status != "under_review" {
		t.Fatalf("status after refused approvals = %q, want under_review", got.Policy.Status)
	}

	// A second person approves.
	if err := json.Unmarshal(s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.approverBear, http.StatusOK), &got); err != nil {
		t.Fatal(err)
	}
	if got.Policy.Status != "approved" {
		t.Fatalf("status after second approver = %q, want approved", got.Policy.Status)
	}
}
