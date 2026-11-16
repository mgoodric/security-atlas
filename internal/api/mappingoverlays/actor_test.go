package mappingoverlays

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

// TestWrites_CredentialWithoutIDIs401 covers #2375: an overlay write whose
// credential has no id is refused before the store is reached, rather than
// recorded under a "system" actor. The handler has no store, so reaching it
// would panic. A request with no credential at all is already a 403 from
// requireOverlayWrite.
func TestWrites_CredentialWithoutIDIs401(t *testing.T) {
	t.Parallel()
	tenant := "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name, method, path, body string
	}{
		{"create", http.MethodPost, "/v1/mapping-overlays", `{}`},
		{"retire", http.MethodPost, "/v1/mapping-overlays/" + uuid.NewString() + "/retire", `{"reason":"x"}`},
		{"import", http.MethodPost, "/v1/mapping-overlays/import?format=csv", "a,b\n"},
	}
	h := New(nil)
	router := chi.NewRouter()
	router.Post("/v1/mapping-overlays", h.Create)
	router.Post("/v1/mapping-overlays/{id}/retire", h.Retire)
	router.Post("/v1/mapping-overlays/import", h.Import)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			ctx = authctx.WithCredential(ctx, credstore.Credential{TenantID: tenant, IsAdmin: true})
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)).WithContext(ctx)
			w := httptest.NewRecorder()
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
	r := httptest.NewRequest(http.MethodPost, "/", nil)
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
