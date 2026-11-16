package risks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// TestPatchRisk_NoCredentialIs401 covers #1954: a PATCH with a tenant but
// no credential, or a credential with no id, is refused before the store
// is reached. The handler has no store, so reaching it would panic.
func TestPatchRisk_NoCredentialIs401(t *testing.T) {
	t.Parallel()
	tenant := "11111111-1111-1111-1111-111111111111"
	cases := map[string]*credstore.Credential{
		"no credential": nil,
		"no id":         {TenantID: tenant},
	}
	for name, cred := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			if cred != nil {
				ctx = authctx.WithCredential(ctx, *cred)
			}
			r := httptest.NewRequest(http.MethodPatch, "/v1/risks/"+uuid.NewString(),
				strings.NewReader(`{"level":"org"}`)).WithContext(ctx)
			w := httptest.NewRecorder()
			router := chi.NewRouter()
			router.Patch("/v1/risks/{id}", New(nil).PatchRisk)
			router.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestActorFrom covers the credential id becoming the audit actor.
func TestActorFrom(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPatch, "/", nil)
	r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{ID: "key_abc"}))
	w := httptest.NewRecorder()
	actor, ok := actorFrom(w, r)
	if !ok || actor != "key_abc" {
		t.Fatalf("actorFrom = %q, %v; want key_abc, true", actor, ok)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("actorFrom wrote status %d on success", w.Code)
	}
}
