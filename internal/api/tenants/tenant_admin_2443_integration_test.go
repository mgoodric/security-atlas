//go:build integration

// #2443: PATCH /v1/tenants/{id} is open to an organisation's own admin
// (#2503), allow_self_approval included. The gate is authz.IsTenantAdmin
// on the live roles authzmw resolves, so these tests set
// credstore.Credential.TenantRoles the way authzmw does, with no admin
// credential.

package tenants_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/tenants"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// newRoleRouter mounts the handler for a user session in tenantID whose
// live roles are liveRoles and whose JWT role claim is claimRoles. Neither
// carries an admin credential.
func newRoleRouter(t *testing.T, tenantID uuid.UUID, liveRoles, claimRoles []string) http.Handler {
	t.Helper()
	h := tenants.New(appPool)
	userID := uuid.New()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:          "jwt_test_tenants_2443",
				TenantID:    tenantID.String(),
				UserID:      "user:" + userID.String(),
				TenantRoles: liveRoles,
			})
			ctx = jwtmw.WithClaimsForTest(ctx, &jwt.AtlasClaims{
				CurrentTenantID: tenantID,
				Roles:           map[uuid.UUID][]string{tenantID: claimRoles},
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Patch("/v1/tenants/{id}", h.PatchTenant)
	return r
}

func readTenant(t *testing.T, id uuid.UUID) (name string, allow bool) {
	t.Helper()
	if err := adminPool.QueryRow(context.Background(),
		`SELECT name, allow_self_approval FROM tenants WHERE id = $1`, id).Scan(&name, &allow); err != nil {
		t.Fatalf("read tenant: %v", err)
	}
	return name, allow
}

func TestPatchTenant2443_TenantAdminAllowed(t *testing.T) {
	tenantID := seedTenant(t, "Org Admin Co "+uuid.NewString()[:8])
	h := newRoleRouter(t, tenantID, []string{"admin"}, []string{"admin"})
	path := "/v1/tenants/" + tenantID.String()

	res, body := patch(t, h, path, map[string]any{"allow_self_approval": true})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("tenant admin allow_self_approval: status %d (want 200) body=%v", res.StatusCode, body)
	}
	newName := "Org Admin Renamed " + uuid.NewString()[:8]
	res, body = patch(t, h, path, map[string]any{"name": newName})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("tenant admin rename: status %d (want 200) body=%v", res.StatusCode, body)
	}
	if name, allow := readTenant(t, tenantID); !allow || name != newName {
		t.Fatalf("after tenant admin PATCH: name=%q allow=%v, want %q and true", name, allow, newName)
	}
}

func TestPatchTenant2443_NonAdminForbidden(t *testing.T) {
	for _, role := range []string{"grc_engineer", "control_owner", "auditor", "viewer"} {
		t.Run(role, func(t *testing.T) {
			tenantID := seedTenant(t, "Not Admin Co "+uuid.NewString()[:8])
			h := newRoleRouter(t, tenantID, []string{role}, []string{role})
			res, body := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"allow_self_approval": true})
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("%s: status %d (want 403) body=%v", role, res.StatusCode, body)
			}
			if _, allow := readTenant(t, tenantID); allow {
				t.Fatalf("%s turned self-approval on", role)
			}
		})
	}
}

// A JWT admin role claim with no live admin role is refused: the claim
// outlives a revoke (#2381).
func TestPatchTenant2443_StaleAdminClaimForbidden(t *testing.T) {
	tenantID := seedTenant(t, "Revoked Admin Co "+uuid.NewString()[:8])
	h := newRoleRouter(t, tenantID, nil, []string{"admin"})
	res, body := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"allow_self_approval": true})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("stale admin claim: status %d (want 403) body=%v", res.StatusCode, body)
	}
}

func TestPatchTenant2443_TenantAdminCannotPatchAnotherTenant(t *testing.T) {
	own := seedTenant(t, "Own Org "+uuid.NewString()[:8])
	otherName := "Other Org " + uuid.NewString()[:8]
	other := seedTenant(t, otherName)
	h := newRoleRouter(t, own, []string{"admin"}, []string{"admin"})

	res, body := patch(t, h, "/v1/tenants/"+other.String(), map[string]any{"allow_self_approval": true, "name": "Hijacked"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-tenant: status %d (want 403) body=%v", res.StatusCode, body)
	}
	if name, allow := readTenant(t, other); allow || name != otherName {
		t.Fatalf("other tenant changed: name=%q allow=%v", name, allow)
	}
}
