// #2653 — pure-Go tests for the answer PATCH's citation refusals that fire
// before any DB call: a malformed citation, and one the caller may not read
// (the real embedded OPA engine decides). The absent, foreign and spoofed-
// title cases need the tenant's records and live in integration_test.go.

package questionnaires

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func newEngine(t *testing.T) *authz.Engine {
	t.Helper()
	engine, err := authz.NewEngine(context.Background(), nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

// patchAnswer runs UpsertAnswer for cred with the given citations.
func patchAnswer(t *testing.T, h *Handler, cred credstore.Credential, citations []any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"answer_value": "yes", "citations": citations})
	r := httptest.NewRequest(http.MethodPatch, "/v1/questionnaires/q/answers/a", strings.NewReader(string(body)))
	ctx, err := tenancy.WithTenant(r.Context(), testTenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	r = r.WithContext(authctx.WithCredential(ctx, cred))
	return route(h.UpsertAnswer, r, map[string]string{"id": "q", "qid": uuid.NewString()})
}

var viewerKey = credstore.Credential{ID: "key_viewer", UserID: "key_viewer", TenantID: testTenant, OwnerRoles: []string{"viewer"}}

func TestUpsertAnswer_ViewerCitingEvidenceRefused(t *testing.T) {
	t.Parallel()
	h := New(nil).WithAuthz(newEngine(t))
	w := patchAnswer(t, h, viewerKey, []any{
		map[string]any{"id": uuid.NewString(), "type": "evidence", "title": "anything"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("viewer citing evidence = %d, want 400; body=%s", w.Code, w.Body)
	}
	// The same refusal as a malformed citation: nothing tells an
	// unreadable record from an invalid one.
	malformed := patchAnswer(t, h, viewerKey, []any{map[string]any{"id": "ev-01", "type": "evidence"}})
	if malformed.Code != http.StatusBadRequest || malformed.Body.String() != w.Body.String() {
		t.Errorf("malformed = %d %s; want the same 400 as unreadable %s", malformed.Code, malformed.Body, w.Body)
	}
	if !strings.Contains(w.Body.String(), invalidCitationMsg) {
		t.Errorf("body %s does not carry the citation refusal", w.Body)
	}
}

// TestViewerAnswerPatchDeniedByMiddleware pins why the viewer case above is
// a handler test: OPA refuses a viewer's answer PATCH before the handler
// runs (docs/audit-log/2653-questionnaire-citation-validation-decisions.md D2).
func TestViewerAnswerPatchDeniedByMiddleware(t *testing.T) {
	t.Parallel()
	engine := newEngine(t)
	r := httptest.NewRequest(http.MethodPatch, "/v1/questionnaires/"+uuid.NewString()+"/answers/"+uuid.NewString(), nil)
	r = r.WithContext(authctx.WithCredential(r.Context(), viewerKey))
	d, err := engine.Decide(r.Context(), authz.BuildInput(r, nil))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Error("OPA admits a viewer's answer PATCH; the citation read check is now reachable through the router, so test it there")
	}
}

func TestCitationsReadable(t *testing.T) {
	t.Parallel()
	h := New(nil).WithAuthz(newEngine(t))
	admin := credstore.Credential{ID: "key_admin", UserID: "key_admin", TenantID: testTenant, OwnerRoles: []string{"admin"}}
	control := questionnaire.Citation{ID: uuid.NewString(), Type: questionnaire.CitationControls}
	evidence := questionnaire.Citation{ID: uuid.NewString(), Type: questionnaire.CitationEvidence}
	cases := []struct {
		name  string
		cred  credstore.Credential
		cites []questionnaire.Citation
		want  bool
	}{
		{"viewer, no citations", viewerKey, nil, true},
		{"viewer, control", viewerKey, []questionnaire.Citation{control}, true},
		{"viewer, evidence", viewerKey, []questionnaire.Citation{evidence}, false},
		{"viewer, control then evidence", viewerKey, []questionnaire.Citation{control, evidence}, false},
		{"admin, evidence", admin, []questionnaire.Citation{control, evidence}, true},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPatch, "/v1/questionnaires/q/answers/a", nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), tc.cred))
		if got := h.citationsReadable(r, tc.cites); got != tc.want {
			t.Errorf("%s: readable = %v, want %v", tc.name, got, tc.want)
		}
	}
	// No engine (unit-test servers): every citation passes.
	r := httptest.NewRequest(http.MethodPatch, "/x", nil)
	if !New(nil).citationsReadable(r, []questionnaire.Citation{evidence}) {
		t.Error("nil engine refused a citation")
	}
}
