//go:build integration

// #2116 integration tests — PATCH /v1/policies/{id} edits a draft in place.
// Only a draft is editable: every later state answers 409 and keeps its
// text, so a published policy changes only through a new version. Real
// Postgres under RLS, driven through the full HTTP stack.

package policies_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

type draftWire struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Version          string   `json:"version"`
	BodyMd           string   `json:"body_md"`
	Status           string   `json:"status"`
	LinkedControlIDs []string `json:"linked_control_ids"`
	AckRoles         []string `json:"acknowledgment_required_roles"`
}

func decodeDraft(t *testing.T, raw []byte) draftWire {
	t.Helper()
	var env struct {
		Policy draftWire `json:"policy"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode policy: %v (%s)", err, raw)
	}
	return env.Policy
}

func editBody(title string, links []string) []byte {
	body, _ := json.Marshal(map[string]any{
		"title":                         title,
		"body_md":                       "# " + title + " edited",
		"owner_role":                    "tenant_admin",
		"approver_role":                 "security_lead",
		"linked_control_ids":            links,
		"acknowledgment_required_roles": []string{"viewer"},
	})
	return body
}

func TestUpdateDraft_EditsDraftInPlace(t *testing.T) {
	s := setup(t)
	id := createDraftPolicy(t, s, "Access Policy", "1.0", nil)

	got := decodeDraft(t, s.do(t, http.MethodPatch, "/v1/policies/"+id,
		editBody("Access Control Policy", []string{}), s.adminBear, http.StatusOK))
	if got.ID != id || got.Status != "draft" || got.Version != "1.0" {
		t.Fatalf("edit changed identity/state: %+v", got)
	}
	if got.Title != "Access Control Policy" || got.BodyMd != "# Access Control Policy edited" {
		t.Errorf("edit not applied: %+v", got)
	}
	if len(got.LinkedControlIDs) != 0 || len(got.AckRoles) != 1 || got.AckRoles[0] != "viewer" {
		t.Errorf("links/ack roles = %v / %v", got.LinkedControlIDs, got.AckRoles)
	}

	// A required field left empty is 400 and changes nothing.
	s.do(t, http.MethodPatch, "/v1/policies/"+id, editBody("", nil), s.adminBear, http.StatusBadRequest)
	s.do(t, http.MethodPatch, "/v1/policies/"+id,
		[]byte(`{"title":"x","body_md":"x","owner_role":"a","approver_role":"b","linked_control_ids":["nope"]}`),
		s.adminBear, http.StatusBadRequest)
	after := decodeDraft(t, s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK))
	if after.Title != "Access Control Policy" {
		t.Errorf("refused edit changed the title to %q", after.Title)
	}
}

// Every state past draft refuses the edit with 409 and keeps its text.
func TestUpdateDraft_RefusesNonDraftStates(t *testing.T) {
	s := setup(t)
	underReview := createDraftPolicy(t, s, "Under Review Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+underReview+"/submit", nil, s.adminBear, http.StatusOK)

	approved := createDraftPolicy(t, s, "Approved Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+approved+"/submit", nil, s.adminBear, http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+approved+"/approve", nil, s.approverBear, http.StatusOK)

	published := publishWithReview(t, s, "Published Policy", "1.0", "")

	for name, id := range map[string]string{
		"under_review": underReview,
		"approved":     approved,
		"published":    published.ID,
	} {
		t.Run(name, func(t *testing.T) {
			raw := s.do(t, http.MethodPatch, "/v1/policies/"+id,
				editBody("Rewritten", []string{s.ctrlID.String()}), s.adminBear, http.StatusConflict)
			if !strings.Contains(string(raw), "only a draft") {
				t.Errorf("409 body = %s", raw)
			}
			got := decodeDraft(t, s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK))
			if got.Title == "Rewritten" || got.Status != name {
				t.Errorf("refused edit changed the row: %+v", got)
			}
		})
	}
}

func TestUpdateDraft_TenantAndValidation(t *testing.T) {
	s := setup(t)
	id := createDraftPolicy(t, s, "Draft Policy", "0.1", nil)

	tenantB := freshTenant(t, s.admin)
	bearerB := s.apiServer.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenantB)))
	s.do(t, http.MethodPatch, "/v1/policies/"+id, editBody("Stolen", nil), bearerB, http.StatusNotFound)
	s.do(t, http.MethodPatch, "/v1/policies/not-a-uuid", editBody("x", nil), s.adminBear, http.StatusBadRequest)
	s.do(t, http.MethodPatch, "/v1/policies/"+uuid.NewString(), editBody("x", nil), s.adminBear, http.StatusNotFound)
	s.do(t, http.MethodPatch, "/v1/policies/"+id, []byte(`{`), s.adminBear, http.StatusBadRequest)

	got := decodeDraft(t, s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK))
	if got.Title != "Draft Policy" {
		t.Fatalf("draft title = %q after refused edits", got.Title)
	}
}
