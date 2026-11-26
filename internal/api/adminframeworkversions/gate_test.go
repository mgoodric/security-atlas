package adminframeworkversions_test

// Pure-Go gate tests for #1757: every framework-version write route changes
// global catalog rows, so it requires the verified JWT's super_admin claim. A
// tenant admin (per-tenant admin API key, or a JWT without super_admin) is
// refused 403 before the store is touched — the handler is built over a nil
// store, so reaching it would panic.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminframeworkversions"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

type caller int

const (
	noCredential caller = iota
	tenantAdminAPIKey
	tenantAdminJWT
	superAdminJWT
)

func gateRouter(c caller) http.Handler {
	h := adminframeworkversions.New(nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			tenant := uuid.New()
			subject := "user:" + uuid.NewString()
			switch c {
			case tenantAdminAPIKey:
				ctx = authctx.WithCredential(ctx, credstore.Credential{
					ID: "key_tenant_admin", TenantID: tenant.String(), IsAdmin: true, UserID: subject,
				})
			case tenantAdminJWT, superAdminJWT:
				super := c == superAdminJWT
				ctx = jwtmw.WithClaimsForTest(ctx, &jwt.AtlasClaims{
					RegisteredClaims: jwt.RegisteredClaims{Subject: subject},
					CurrentTenantID:  tenant,
					Roles:            map[uuid.UUID][]string{tenant: {"admin"}},
					SuperAdmin:       super,
				})
				// IsAdmin is forced true for the tenant-admin JWT so the test
				// proves the gate reads the super_admin claim, not IsAdmin.
				ctx = authctx.WithCredential(ctx, credstore.Credential{
					ID: "jwt:test", TenantID: tenant.String(), IsAdmin: true, UserID: subject,
				})
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/v1/admin/framework-versions/{id}/promote", h.Promote)
	r.Post("/v1/admin/framework-versions/{id}/revert", h.Revert)
	r.Post("/v1/admin/framework-versions/migrations:suggest", h.Suggest)
	r.Post("/v1/admin/framework-versions/migrations/{id}/decision", h.Decide)
	return r
}

// writeRoutes are the global-catalog write routes. The id path segments are
// deliberately invalid so a caller who passes the gate stops at the 400
// id/body validation, before the (nil) store.
var writeRoutes = []struct{ path, body string }{
	{"/v1/admin/framework-versions/not-a-uuid/promote", `{}`},
	{"/v1/admin/framework-versions/not-a-uuid/revert", `{}`},
	{"/v1/admin/framework-versions/migrations:suggest", `{"from_version_id":"x","to_version_id":"y"}`},
	{"/v1/admin/framework-versions/migrations/not-a-uuid/decision", `{}`},
}

func TestGlobalCatalogWrites_SuperAdminGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		caller caller
		want   int
	}{
		{"no credential", noCredential, http.StatusUnauthorized},
		{"tenant admin API key", tenantAdminAPIKey, http.StatusForbidden},
		{"tenant admin JWT without super_admin", tenantAdminJWT, http.StatusForbidden},
		{"super_admin JWT passes the gate", superAdminJWT, http.StatusBadRequest},
	}
	for _, tc := range cases {
		router := gateRouter(tc.caller)
		for _, rt := range writeRoutes {
			t.Run(tc.name+" "+rt.path, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(http.MethodPost, rt.path, strings.NewReader(rt.body))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != tc.want {
					t.Fatalf("status = %d; want %d (body=%s)", rec.Code, tc.want, rec.Body.String())
				}
			})
		}
	}
}
