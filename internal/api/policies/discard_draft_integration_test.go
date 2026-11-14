//go:build integration

// #1987 integration tests — DELETE /v1/policies/{id} discards a draft. An
// abandoned next-version draft holds its predecessor's single successor
// slot; discarding it frees the slot so a new version can be created. Only
// drafts are deletable, the discard is audit-logged, and the role is the
// one that writes policies. Real Postgres under RLS, driven through the
// full HTTP stack (JWT, tenancy, authz middleware).

package policies_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// createNextVersion posts a draft naming predecessorID and returns the raw
// response body.
func createNextVersion(t *testing.T, s setupResult, predecessorID, version string, want int) []byte {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"title":              "Retention Policy",
		"version":            version,
		"body_md":            "# Retention Policy " + version,
		"owner_role":         "tenant_admin",
		"approver_role":      "security_lead",
		"linked_control_ids": []string{s.ctrlID.String()},
		"source_attribution": "tenant_authored",
		"predecessor_id":     predecessorID,
	})
	return s.do(t, http.MethodPost, "/v1/policies", body, s.adminBear, want)
}

func discardDraft(t *testing.T, s setupResult, id, bearer string, want int) []byte {
	t.Helper()
	return s.do(t, http.MethodDelete, "/v1/policies/"+id, nil, bearer, want)
}

type discardAuditRow struct {
	Actor   string
	Title   *string
	Version *string
}

func discardAuditRows(t *testing.T, s setupResult, policyID string) []discardAuditRow {
	t.Helper()
	rows, err := s.admin.Query(context.Background(), `
		SELECT actor, policy_title, policy_version
		FROM policy_audit_log
		WHERE tenant_id = $1 AND policy_id = $2 AND action = 'draft_discarded'
		ORDER BY created_at, id`, s.tenant, policyID)
	if err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	defer rows.Close()
	var out []discardAuditRow
	for rows.Next() {
		var r discardAuditRow
		if err := rows.Scan(&r.Actor, &r.Title, &r.Version); err != nil {
			t.Fatalf("scan audit log: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// An abandoned next-version draft blocks a new version with 409 whose
// message names the discard route; discarding it frees the slot.
func TestDiscardDraft_FreesPredecessorForNewVersion(t *testing.T) {
	s := setup(t)
	v1 := publishWithReview(t, s, "Retention Policy", "1.0", "")
	abandoned := decodePolicy(t, createNextVersion(t, s, v1.ID, "2.0", http.StatusCreated))

	blocked := createNextVersion(t, s, v1.ID, "2.1", http.StatusConflict)
	if !strings.Contains(string(blocked), "DELETE /v1/policies/{id}") {
		t.Fatalf("409 does not point at the discard route: %s", blocked)
	}

	// A grc_engineer (the role that writes policies, not an approver)
	// discards the draft.
	engineerUser := seedUser(t, s.admin, s.tenant, "engineer@1987.test")
	claims := testjwt.OwnerFor(uuid.MustParse(s.tenant), []string{"grc_engineer"})
	claims.Subject = engineerUser.String()
	engineer := s.apiServer.IssueTestJWT(t, claims)
	if raw := discardDraft(t, s, abandoned.ID, engineer, http.StatusNoContent); len(raw) != 0 {
		t.Fatalf("204 carried a body: %s", raw)
	}
	s.do(t, http.MethodGet, "/v1/policies/"+abandoned.ID, nil, s.adminBear, http.StatusNotFound)

	audit := discardAuditRows(t, s, abandoned.ID)
	if len(audit) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audit))
	}
	if audit[0].Actor != engineerUser.String() {
		t.Errorf("audit actor = %q, want %s", audit[0].Actor, engineerUser)
	}
	if audit[0].Title == nil || *audit[0].Title != "Retention Policy" ||
		audit[0].Version == nil || *audit[0].Version != "2.0" {
		t.Errorf("audit title/version = %v/%v", audit[0].Title, audit[0].Version)
	}

	// The predecessor is untouched and takes a new successor.
	got := decodePolicy(t, s.do(t, http.MethodGet, "/v1/policies/"+v1.ID, nil, s.adminBear, http.StatusOK))
	if got.Status != "published" {
		t.Fatalf("predecessor status = %s, want published", got.Status)
	}
	next := decodePolicy(t, createNextVersion(t, s, v1.ID, "2.1", http.StatusCreated))
	if next.PredecessorID == nil || *next.PredecessorID != v1.ID {
		t.Fatalf("new version predecessor = %v, want %s", next.PredecessorID, v1.ID)
	}

	// A discarded draft is gone: a second delete is 404 and logs nothing.
	discardDraft(t, s, abandoned.ID, s.adminBear, http.StatusNotFound)
	if n := len(discardAuditRows(t, s, abandoned.ID)); n != 1 {
		t.Fatalf("second delete wrote audit rows: %d total", n)
	}
}

// Only a draft is deletable: every later state answers 409 and keeps its
// row, with no audit entry.
func TestDiscardDraft_RefusesNonDraftStates(t *testing.T) {
	s := setup(t)
	underReview := createDraftPolicy(t, s, "Under Review Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+underReview+"/submit", nil, s.adminBear, http.StatusOK)

	approved := createDraftPolicy(t, s, "Approved Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+approved+"/submit", nil, s.adminBear, http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+approved+"/approve", nil, s.approverBear, http.StatusOK)

	superseded := publishWithReview(t, s, "Retention Policy", "1.0", "")
	v2 := decodePolicy(t, createNextVersion(t, s, superseded.ID, "2.0", http.StatusCreated))
	published := publishDraft(t, s, v2.ID, "2.0", "")

	before := tenantPolicyCount(t, s)
	for name, id := range map[string]string{
		"under_review": underReview,
		"approved":     approved,
		"published":    published.ID,
		"superseded":   superseded.ID,
	} {
		t.Run(name, func(t *testing.T) {
			raw := discardDraft(t, s, id, s.adminBear, http.StatusConflict)
			if !strings.Contains(string(raw), "only a draft") {
				t.Errorf("409 body = %s", raw)
			}
			got := decodePolicy(t, s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK))
			if got.Status != name {
				t.Errorf("status after refused delete = %s, want %s", got.Status, name)
			}
			if n := len(discardAuditRows(t, s, id)); n != 0 {
				t.Errorf("refused delete wrote %d audit rows", n)
			}
		})
	}
	if after := tenantPolicyCount(t, s); after != before {
		t.Fatalf("policy rows %d -> %d after refused deletes", before, after)
	}
}

// Another tenant cannot see the draft; a bad id is 400. The role gate is
// the authz middleware's write-on-policies rule, which this harness does not
// mount (no authz engine); internal/authz/policy_discard_1987_test.go pins
// that the discard admits exactly the roles that create a policy.
func TestDiscardDraft_TenantAndValidation(t *testing.T) {
	s := setup(t)
	draft := createDraftPolicy(t, s, "Draft Policy", "0.1", nil)

	tenantB := freshTenant(t, s.admin)
	bearerB := s.apiServer.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenantB)))
	discardDraft(t, s, draft, bearerB, http.StatusNotFound)

	discardDraft(t, s, "not-a-uuid", s.adminBear, http.StatusBadRequest)
	discardDraft(t, s, uuid.NewString(), s.adminBear, http.StatusNotFound)

	// The draft survived every refused attempt, with no audit entry.
	got := decodePolicy(t, s.do(t, http.MethodGet, "/v1/policies/"+draft, nil, s.adminBear, http.StatusOK))
	if got.Status != "draft" {
		t.Fatalf("draft status = %s", got.Status)
	}
	if n := len(discardAuditRows(t, s, draft)); n != 0 {
		t.Fatalf("refused deletes wrote %d audit rows", n)
	}
}
