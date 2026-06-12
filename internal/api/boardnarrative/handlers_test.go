// Slice 440 — pure-Go unit tests for the board-narrative AI handler GUARD
// branches: the role gate (AC-11), the missing-tenant / missing-credential
// 401, the nil-service 503, and the bad-request 400. These reach NO DB (the
// guards fire before any store call), so they run without Postgres — the
// slice-353 Q-2 fast-loop convention. The happy-path DB behavior is proven in
// internal/boardnarrative/integration_test.go (RLS + cross-tenant + the four
// guardrails + the approval guard).
package boardnarrative

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const testTenant = "11111111-1111-1111-1111-111111111111"

func reqWith(t *testing.T, method, path, body, tenant string, cred *credstore.Credential) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := r.Context()
	if tenant != "" {
		var err error
		ctx, err = tenancy.WithTenant(ctx, tenant)
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
	}
	if cred != nil {
		ctx = authctx.WithCredential(ctx, *cred)
	}
	return r.WithContext(ctx)
}

func run(h http.HandlerFunc, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func approverCred() *credstore.Credential {
	return &credstore.Credential{ID: "key_grc", TenantID: testTenant, IsApprover: true}
}

func TestGenerate_MissingTenantOrCred(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := run(h.Generate, reqWith(t, "POST", "/x", "{}", "", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no-tenant: got %d, want 401", w.Code)
	}
	w = run(h.Generate, reqWith(t, "POST", "/x", "{}", testTenant, nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no-cred: got %d, want 401", w.Code)
	}
}

func TestGenerate_RoleGate(t *testing.T) {
	t.Parallel()
	h := New(nil)
	cred := &credstore.Credential{ID: "key_viewer", TenantID: testTenant}
	w := run(h.Generate, reqWith(t, "POST", "/x", "{}", testTenant, cred))
	if w.Code != http.StatusForbidden {
		t.Errorf("viewer generate: got %d, want 403", w.Code)
	}
	w = run(h.Approve, reqWith(t, "POST", "/x", "{}", testTenant, cred))
	if w.Code != http.StatusForbidden {
		t.Errorf("viewer approve: got %d, want 403", w.Code)
	}
}

func TestGenerate_NilServiceUnavailable(t *testing.T) {
	t.Parallel()
	h := New(nil) // role passes (approver) but service is nil -> 503
	w := run(h.Generate, reqWith(t, "POST", "/x", `{"period_end":"2026-05-31"}`, testTenant, approverCred()))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil service generate: got %d, want 503", w.Code)
	}
	w = run(h.Approve, reqWith(t, "POST", "/x", `{"record_id":"x"}`, testTenant, approverCred()))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil service approve: got %d, want 503", w.Code)
	}
}

// Note: the missing-period_end 400 and bad-uuid 400 branches sit AFTER the
// nil-service 503 guard in the handler, so they cannot be reached with a nil
// service. They are exercised end-to-end in the integration tier where a real
// service is wired. The role + auth + nil-service guards above are the
// DB-free branches worth a fast unit test.
