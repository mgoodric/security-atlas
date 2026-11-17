//go:build integration

// #2459 integration tests. A draft created or edited through a credential
// that names no person (an unbound API key, an OAuth client, the
// bootstrap admin) has an unknown author: every approver is refused unless
// the organisation allows self-approval, and the approval is then flagged.
// Edits report a missing (404) or non-draft (409) policy before a bad link
// (422), create reports a bad predecessor before one, and a retired
// control does not count as a link. Real Postgres under RLS.

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
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// editAs calls the UpdateDraft handler as cred. On /v1 the HTTP stack
// admits only JWTs, so the credential is placed in the request context
// directly, as approveWithKey does; the handler and store are the real
// ones. It returns the status and body.
func editAs(t *testing.T, s setupResult, cred credstore.Credential, id string, body []byte) (int, string) {
	t.Helper()
	ctx, err := tenancy.WithTenant(authctx.WithCredential(context.Background(), cred), s.tenant)
	if err != nil {
		t.Fatal(err)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req := httptest.NewRequest(http.MethodPatch, "/v1/policies/"+id, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	policies.New(policy.NewStore(s.app)).UpdateDraft(rec, req)
	return rec.Code, rec.Body.String()
}

func TestApprove_NonPersonEditorIsUnknownAuthor(t *testing.T) {
	unbound := "key_" + uuid.NewString()
	kinds := []struct {
		name      string
		cred      credstore.Credential
		principal string
	}{
		{"unbound key", credstore.Credential{ID: unbound, UserID: unbound, IsAdmin: true}, unbound},
		{"OAuth client", credstore.Credential{ID: "jwt:" + uuid.NewString(), UserID: "oauth_client:ci-2459", IsAdmin: true}, "oauth_client:ci-2459"},
		{"bootstrap admin", credstore.Credential{ID: "jwt:" + uuid.NewString(), UserID: "bootstrap-admin", IsAdmin: true}, "bootstrap-admin"},
	}
	for _, kind := range kinds {
		t.Run(kind.name, func(t *testing.T) {
			s := setup(t)
			allowSelfApproval(t, s, false)
			kind.cred.TenantID = s.tenant

			id := createDraftPolicy(t, s, "Edited By "+kind.name, "1.0", nil)
			if code, body := editAs(t, s, kind.cred, id, editBody("Edited By "+kind.name, []string{s.ctrlID.String()})); code != http.StatusOK {
				t.Fatalf("edit as %s: status %d body=%s", kind.name, code, body)
			}
			var editors []string
			if err := s.admin.QueryRow(context.Background(),
				`SELECT draft_editors FROM policies WHERE id = $1`, id).Scan(&editors); err != nil {
				t.Fatalf("read draft_editors: %v", err)
			}
			if len(editors) != 2 || editors[1] != kind.principal {
				t.Fatalf("draft_editors = %v, want [creator, %s]", editors, kind.principal)
			}

			s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)
			// A second person who never touched the draft is refused: the
			// credential's holder could be them.
			raw := s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.approverBear, http.StatusForbidden)
			if !strings.Contains(string(raw), "author is unknown") {
				t.Fatalf("403 body = %s", raw)
			}

			allowSelfApproval(t, s, true)
			if !approvedFlag(t, s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.approverBear, http.StatusOK)) {
				t.Fatal("approval of a draft with an unknown author is not flagged self_approved")
			}
		})
	}
}

func TestUpdateDraft_StateBeforeLinks(t *testing.T) {
	s := setup(t)
	bad := []string{uuid.NewString()}

	// Missing policy, bad link: 404, not 422.
	s.do(t, http.MethodPatch, "/v1/policies/"+uuid.NewString(), editBody("Missing", bad), s.adminBear, http.StatusNotFound)

	// Submitted policy, bad link: 409, not 422.
	id := createDraftPolicy(t, s, "Submitted Then Edited", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+id, editBody("Submitted Then Edited", bad), s.adminBear, http.StatusConflict)

	// A draft with a bad link is still 422.
	draft := createDraftPolicy(t, s, "Draft Bad Link", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+draft, editBody("Draft Bad Link", bad), s.adminBear, http.StatusUnprocessableEntity)
}

func TestCreate_PredecessorBeforeLinks(t *testing.T) {
	s := setup(t)
	create := func(predecessor string) []byte {
		body, _ := json.Marshal(map[string]any{
			"title":              "Next Version",
			"version":            "2.0",
			"body_md":            "# Next Version",
			"owner_role":         "tenant_admin",
			"approver_role":      "security_lead",
			"linked_control_ids": []string{uuid.NewString()},
			"source_attribution": "tenant_authored",
			"predecessor_id":     predecessor,
		})
		return body
	}
	// Unknown predecessor, bad link: 400, not 422.
	s.do(t, http.MethodPost, "/v1/policies", create(uuid.NewString()), s.adminBear, http.StatusBadRequest)
	// Unpublished predecessor, bad link: 409, not 422.
	draft := createDraftPolicy(t, s, "Still A Draft", "1.0", nil)
	s.do(t, http.MethodPost, "/v1/policies", create(draft), s.adminBear, http.StatusConflict)
	// Predecessor that already has a successor, bad link: 409, not 422.
	published := createAndPublishPolicy(t, s, "Published", "1.0", nil)
	good, _ := json.Marshal(map[string]any{
		"title":              "Successor",
		"version":            "2.0",
		"body_md":            "# Successor",
		"owner_role":         "tenant_admin",
		"approver_role":      "security_lead",
		"linked_control_ids": []string{s.ctrlID.String()},
		"source_attribution": "tenant_authored",
		"predecessor_id":     published,
	})
	s.do(t, http.MethodPost, "/v1/policies", good, s.adminBear, http.StatusCreated)
	s.do(t, http.MethodPost, "/v1/policies", create(published), s.adminBear, http.StatusConflict)
}

func TestLinkedControls_RetiredControlDoesNotCount(t *testing.T) {
	s := setup(t)
	id := createDraftPolicy(t, s, "Links A Retired Control", "1.0", nil)
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE controls SET lifecycle_state = 'retired' WHERE id = $1`, s.ctrlID); err != nil {
		t.Fatalf("retire control: %v", err)
	}
	raw := s.do(t, http.MethodPatch, "/v1/policies/"+id, editBody("Links A Retired Control", []string{s.ctrlID.String()}), s.adminBear, http.StatusUnprocessableEntity)
	if !strings.Contains(string(raw), "does not exist") {
		t.Fatalf("422 body = %s", raw)
	}
}
