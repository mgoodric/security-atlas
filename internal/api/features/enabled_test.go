// Pure-Go unit tests for the slice 660 non-admin enabled-modules read.
// No Postgres: a nil-pool featureflag.Store returns Seed defaults, which
// is enough to assert the wire shape, the non-admin posture, and the
// fail-on-missing-credential branch. The RLS / per-tenant-override
// behavior is covered by the integration test.

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

type enabledBody struct {
	Modules map[string]bool `json:"modules"`
}

func newEnabledRouter(withCred bool, isAdmin bool) http.Handler {
	// nil pool -> Store.Get returns Seed defaults (oscal.export +
	// board.reporting both OFF).
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
	r.Get("/v1/features/enabled", h.Enabled)
	return r
}

func TestEnabledReturnsSeedDefaultsForNonAdmin(t *testing.T) {
	t.Parallel()
	r := newEnabledRouter(true, false) // non-admin credential
	req := httptest.NewRequest(http.MethodGet, "/v1/features/enabled", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var body enabledBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	// The response must carry exactly the gating keys, all OFF (Seed default).
	for _, key := range featureflag.GatingKeys {
		on, ok := body.Modules[key]
		if !ok {
			t.Errorf("response missing gating key %q", key)
			continue
		}
		if on {
			t.Errorf("gating key %q reported ON; Seed default is OFF", key)
		}
	}
	if len(body.Modules) != len(featureflag.GatingKeys) {
		t.Errorf("response has %d modules; want exactly the %d gating keys",
			len(body.Modules), len(featureflag.GatingKeys))
	}
}

func TestEnabledRequiresCredential(t *testing.T) {
	t.Parallel()
	r := newEnabledRouter(false, false) // no credential injected
	req := httptest.NewRequest(http.MethodGet, "/v1/features/enabled", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401 when no credential", rec.Code)
	}
}

func TestEnabledAdminCredentialAlsoAllowed(t *testing.T) {
	t.Parallel()
	// The read is non-admin; an admin credential is ALSO permitted (it is
	// authed). This guards against accidentally requiring admin.
	r := newEnabledRouter(true, true)
	req := httptest.NewRequest(http.MethodGet, "/v1/features/enabled", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; admin should also reach the non-admin read", rec.Code)
	}
}
