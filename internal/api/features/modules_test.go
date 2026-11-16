// Pure-Go unit tests for GET /v1/me/modules (#2170). No Postgres: a
// nil-pool featureflag.Store returns Seed defaults, which is enough to
// assert the wire shape and the credential branch. Per-tenant switches
// under RLS are covered by internal/api's module-gate integration test.

package features_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/features"
	"github.com/mgoodric/security-atlas/internal/featureflag"
)

func newModulesRouter(withCred bool, isAdmin bool) http.Handler {
	h := features.New(featureflag.NewStore(nil))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			if withCred {
				ctx = authctx.WithCredential(ctx, credstore.Credential{
					ID:       "key_test",
					TenantID: "11111111-1111-1111-1111-111111111111",
					IsAdmin:  isAdmin,
					UserID:   "test-user",
				})
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/me/modules", h.Modules)
	return r
}

func getModules(t *testing.T, r http.Handler) features.ModulesResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/modules", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var body features.ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return body
}

func TestModulesReportsSeedDefaults(t *testing.T) {
	t.Parallel()
	// Only optional modules are reported; core modules are always on.
	want := map[string]string{
		"risk":               features.ModuleOn,
		"third_party_risk":   features.ModuleOn,
		"policies":           features.ModuleOn,
		"questionnaires":     features.ModuleOn,
		"personnel_security": features.ModuleOn,
		"exceptions":         features.ModuleOn,
		"board_reporting":    features.ModuleSwitchedOff,
	}
	for _, isAdmin := range []bool{false, true} {
		body := getModules(t, newModulesRouter(true, isAdmin))
		if len(body.Modules) != len(want) {
			t.Fatalf("admin=%v: %d modules; want %d", isAdmin, len(body.Modules), len(want))
		}
		for _, m := range body.Modules {
			if m.State != want[m.Key] {
				t.Errorf("admin=%v: %s state = %q; want %q", isAdmin, m.Key, m.State, want[m.Key])
			}
			if len(m.Pages) == 0 {
				t.Errorf("admin=%v: %s has no pages", isAdmin, m.Key)
			}
			if m.Key == "third_party_risk" {
				if len(m.Features) != 1 || m.Features[0].Key != "vendor_claims" || m.Features[0].State != features.ModuleSwitchedOff {
					t.Errorf("admin=%v: third_party_risk features = %+v; want vendor_claims switched_off", isAdmin, m.Features)
				}
			}
		}
	}
}

func TestModulesRequiresCredential(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	newModulesRouter(false, false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/modules", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401 when no credential", rec.Code)
	}
}
