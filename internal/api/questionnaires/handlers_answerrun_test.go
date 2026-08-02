// Slice 756 — pure-Go unit tests for the batch answer-run handler GUARD
// branches: the missing-tenant / missing-credential 401, the grc_engineer
// role gate 403, the nil-service 503, and the bad-uuid 400. These reach NO
// DB (the guards fire before any store call), so they run without Postgres —
// the slice-353 Q-2 fast-loop convention. The happy-path run semantics
// (mixed outcomes, cancel, two-tenant isolation, zero-approved invariant)
// are proven in internal/questionnaire/answerrun_integration_test.go.

package questionnaires

import (
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
)

const testRunID = "33333333-3333-3333-3333-333333333333"

// answerRunHandler builds a Handler whose answerRuns service is non-nil but
// backed by a nil store — safe for guard branches that return before any
// service call (the uuid-parse 400 fires before Start/Get/Cancel).
func answerRunHandler() *Handler {
	return NewWithAIAndAnswerRuns(nil, nil, nil, questionnaire.NewAnswerRunService(nil, nil))
}

func TestStartAnswerRun_MissingTenantOrCred(t *testing.T) {
	t.Parallel()
	h := answerRunHandler()
	// No tenant + no cred -> 401.
	if w := route(h.StartAnswerRun, reqWith(t, "POST", "/x", "", nil), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no-tenant: got %d, want 401", w.Code)
	}
	// Tenant present but no credential -> 401 (tenantCred requires both).
	if w := route(h.StartAnswerRun, reqWith(t, "POST", "/x", testTenant, nil), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no-cred: got %d, want 401", w.Code)
	}
}

func TestStartAnswerRun_RoleGate(t *testing.T) {
	t.Parallel()
	h := answerRunHandler()
	// A non-approver, non-admin credential -> 403.
	viewer := credstore.Credential{ID: "key_viewer", TenantID: testTenant}
	if w := route(h.StartAnswerRun, reqWith(t, "POST", "/x", testTenant, &viewer), nil); w.Code != http.StatusForbidden {
		t.Errorf("viewer: got %d, want 403", w.Code)
	}
}

func TestStartAnswerRun_NilServiceUnavailable(t *testing.T) {
	t.Parallel()
	// Service nil but caller is authorized -> 503, not a panic.
	h := New(nil) // New leaves answerRuns nil
	cred := credstore.Credential{ID: "key_grc", TenantID: testTenant, IsApprover: true}
	if w := route(h.StartAnswerRun, reqWith(t, "POST", "/x", testTenant, &cred), nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil-service: got %d, want 503", w.Code)
	}
}

func TestStartAnswerRun_BadQuestionnaireID(t *testing.T) {
	t.Parallel()
	h := answerRunHandler()
	cred := credstore.Credential{ID: "key_grc", TenantID: testTenant, IsApprover: true}
	w := route(h.StartAnswerRun, reqWith(t, "POST", "/x", testTenant, &cred),
		map[string]string{"id": "not-a-uuid"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad-id: got %d, want 400", w.Code)
	}
}

func TestGetAnswerRun_GuardBranches(t *testing.T) {
	t.Parallel()
	// Missing tenant -> 401.
	h := answerRunHandler()
	if w := route(h.GetAnswerRun, reqWith(t, "GET", "/x", "", nil), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no-tenant get: got %d, want 401", w.Code)
	}
	// Any authenticated credential may read status (no role gate on GET), but
	// a nil service -> 503.
	viewer := credstore.Credential{ID: "key_viewer", TenantID: testTenant}
	nilSvc := New(nil)
	if w := route(nilSvc.GetAnswerRun, reqWith(t, "GET", "/x", testTenant, &viewer), nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil-service get: got %d, want 503", w.Code)
	}
	// Bad runId -> 400 before any store call.
	if w := route(h.GetAnswerRun, reqWith(t, "GET", "/x", testTenant, &viewer),
		map[string]string{"id": testRunID, "runId": "not-a-uuid"}); w.Code != http.StatusBadRequest {
		t.Errorf("bad-runId get: got %d, want 400", w.Code)
	}
}

func TestCancelAnswerRun_GuardBranches(t *testing.T) {
	t.Parallel()
	h := answerRunHandler()
	cred := credstore.Credential{ID: "key_grc", TenantID: testTenant, IsAdmin: true}

	// Missing tenant -> 401.
	if w := route(h.CancelAnswerRun, reqWith(t, "POST", "/x", "", nil), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no-tenant cancel: got %d, want 401", w.Code)
	}
	// Non-privileged -> 403.
	viewer := credstore.Credential{ID: "key_viewer", TenantID: testTenant}
	if w := route(h.CancelAnswerRun, reqWith(t, "POST", "/x", testTenant, &viewer), nil); w.Code != http.StatusForbidden {
		t.Errorf("viewer cancel: got %d, want 403", w.Code)
	}
	// Authorized but nil service -> 503.
	nilSvc := New(nil)
	if w := route(nilSvc.CancelAnswerRun, reqWith(t, "POST", "/x", testTenant, &cred), nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil-service cancel: got %d, want 503", w.Code)
	}
	// Bad runId -> 400 before any store call.
	if w := route(h.CancelAnswerRun, reqWith(t, "POST", "/x", testTenant, &cred),
		map[string]string{"id": testRunID, "runId": "not-a-uuid"}); w.Code != http.StatusBadRequest {
		t.Errorf("bad-runId cancel: got %d, want 400", w.Code)
	}
}
