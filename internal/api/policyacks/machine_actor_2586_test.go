package policyacks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// TestAcknowledge2586_RefusesMachineActors pins that a credential naming no
// users row is refused with 403 before the store is reached (#2586). An
// unbound key with a live label matches an "everyone" policy, so without
// this gate it passed the audience check and failed inside Record. The
// handler has no store and no ingester here: reaching either would panic or
// answer 503, so a 403 proves the refusal comes first.
func TestAcknowledge2586_RefusesMachineActors(t *testing.T) {
	t.Parallel()
	const tenant = "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name string
		cred credstore.Credential
	}{
		{"unbound key with a live label",
			credstore.Credential{ID: "key_2586", UserID: "key_2586", TenantID: tenant, OwnerRoles: []string{"viewer"}, TenantRoles: []string{"viewer"}}},
		{"unbound key, user-prefixed own id",
			credstore.Credential{ID: "key_2586", UserID: "user:key_2586", TenantID: tenant, OwnerRoles: []string{"viewer"}}},
		{"credential with no user id",
			credstore.Credential{ID: "key_2586", TenantID: tenant, IsAdmin: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := New(nil, nil)
			r := httptest.NewRequest(http.MethodPost, "/v1/policies/33333333-3333-3333-3333-333333333333/acknowledge", nil)
			ctx, err := tenancy.WithTenant(authctx.WithCredential(r.Context(), tc.cred), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "33333333-3333-3333-3333-333333333333")
			r = r.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
			w := httptest.NewRecorder()
			h.Acknowledge(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d; want 403 (body=%s)", w.Code, w.Body.String())
			}
		})
	}
}
