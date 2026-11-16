package authz_test

// #2447: rows the #2428 matrix did not pin. Each request runs through
// authzmw with the real OPA bundle and a resolver standing in for
// user_roles, then a handler applying the route's gate from routes2428:
//   - a user who is admin in tenant A and viewer in tenant B, acting in B,
//     is refused every gate;
//   - an API key bound to a user (#2402 D2) gets its owner's live roles, so
//     a key owned by a tenant admin passes every gate whatever its label
//     says. That is today's behaviour; #2431 is to cap it at the label;
//   - an unbound key labelled viewer is refused every gate.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

const (
	tenantA2447 = "00000000-0000-4000-8000-00000000a447"
	tenantB2447 = "00000000-0000-4000-8000-00000000b447"
	user2447    = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2447"
)

// tenantResolver returns the live roles of (tenant, user).
type tenantResolver map[[2]string][]authz.Role

func (m tenantResolver) RolesFor(_ context.Context, tenant, user string) ([]authz.Role, error) {
	return m[[2]string{tenant, user}], nil
}

// gateStatus2447 sends one request as cred through authzmw and a handler
// applying the route's routes2428 gate: 204 when both admit it, else 403.
func gateStatus2447(t *testing.T, e *authz.Engine, cred credstore.Credential, rt route2428) int {
	t.Helper()
	h := authzmw.Middleware(e, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := authctx.CredentialFromContext(r.Context())
		if !ok || !rt.gate.fn(c) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(rt.method, rt.path, nil)
	r = r.WithContext(authctx.WithCredential(r.Context(), cred))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestTenantGates2447_AdminInAViewerInB(t *testing.T) {
	t.Parallel()
	e, err := authz.NewEngine(context.Background(), tenantResolver{
		{tenantA2447, user2447}: {authz.RoleAdmin},
		{tenantB2447, user2447}: {authz.RoleViewer},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// jwtmw puts the current tenant's role claim in OwnerRoles.
	inB := credstore.Credential{ID: "jwt:b", TenantID: tenantB2447, UserID: user2447, OwnerRoles: []string{"viewer"}}
	inA := credstore.Credential{ID: "jwt:a", TenantID: tenantA2447, UserID: user2447, OwnerRoles: []string{"admin"}}
	for _, rt := range routes2428 {
		if got := gateStatus2447(t, e, inB, rt); got != http.StatusForbidden {
			t.Errorf("admin in A, viewer in B, tenant-B token: %s %s = %d, want 403", rt.method, rt.path, got)
		}
		// The same person in A passes, so the refusal above is B's role.
		if got := gateStatus2447(t, e, inA, rt); got != http.StatusNoContent {
			t.Errorf("admin in A, tenant-A token: %s %s = %d, want 204", rt.method, rt.path, got)
		}
	}
}

func TestTenantGates2447_APIKeyRows(t *testing.T) {
	t.Parallel()
	e, err := authz.NewEngine(context.Background(), tenantResolver{
		{tenantA2447, user2447}: {authz.RoleAdmin},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Bound: UserID is the owner, not the key id. The viewer label derives
	// nothing; the owner's live admin role is what OPA and the gate see.
	bound := credstore.Credential{ID: "key_bound", TenantID: tenantA2447, UserID: user2447, OwnerRoles: []string{"viewer"}}
	// Unbound: UserID is the key id, so there are no user_roles to read
	// and the label is the whole of its roles.
	unbound := credstore.Credential{ID: "key_unbound", TenantID: tenantA2447, UserID: "key_unbound", OwnerRoles: []string{"viewer"}}
	for _, rt := range routes2428 {
		if got := gateStatus2447(t, e, bound, rt); got != http.StatusNoContent {
			t.Errorf("key bound to a tenant admin (#2402 D2, until #2431): %s %s = %d, want 204", rt.method, rt.path, got)
		}
		if got := gateStatus2447(t, e, unbound, rt); got != http.StatusForbidden {
			t.Errorf("unbound viewer key: %s %s = %d, want 403", rt.method, rt.path, got)
		}
	}
}
