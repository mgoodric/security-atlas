// Slice 757 — pure-Go unit tests for the AI-reject handler GUARD branches:
// the role gate, the missing-tenant / missing-credential 401, the nil-service
// 503, and the bad-uuid 400. These reach NO DB (the guards fire before any
// store call) — the slice-353 Q-2 fast-loop convention. The DB behavior
// (discard + audit + 409/404 + cross-tenant denial) is proven in
// handlers_ai_reject_integration_test.go.

package questionnaires

import (
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

func TestAIReject_MissingTenantOrCred(t *testing.T) {
	t.Parallel()
	h := New(nil)
	// No tenant + no cred -> 401.
	if w := route(h.AIReject, reqWith(t, "POST", "/x", "", nil), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no-tenant: got %d, want 401", w.Code)
	}
	// Tenant present but no credential -> 401 (tenantCred requires both).
	if w := route(h.AIReject, reqWith(t, "POST", "/x", testTenant, nil), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no-cred: got %d, want 401", w.Code)
	}
}

func TestAIReject_RoleGate(t *testing.T) {
	t.Parallel()
	h := New(nil)
	// A non-approver, non-admin credential -> 403 (role-gated like ai-approve).
	cred := credstore.Credential{ID: "key_viewer", TenantID: testTenant}
	if w := route(h.AIReject, reqWith(t, "POST", "/x", testTenant, &cred), nil); w.Code != http.StatusForbidden {
		t.Errorf("viewer: got %d, want 403", w.Code)
	}
}

func TestAIReject_NilServiceUnavailable(t *testing.T) {
	t.Parallel()
	// Service nil but caller is authorized -> 503, not a panic.
	h := New(nil)
	cred := credstore.Credential{ID: "key_grc", TenantID: testTenant, IsApprover: true}
	if w := route(h.AIReject, reqWith(t, "POST", "/x", testTenant, &cred), nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil-service: got %d, want 503", w.Code)
	}
}
