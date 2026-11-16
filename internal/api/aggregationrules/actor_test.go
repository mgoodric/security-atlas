package aggregationrules

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

// TestWrites_NoCredentialIs401 covers #2375: a rule create, activate or
// deactivate with a tenant but no credential, or a credential with no id,
// is refused before the store is reached, rather than recorded under a
// "system" actor. The handler has no store, so reaching it would panic.
func TestWrites_NoCredentialIs401(t *testing.T) {
	t.Parallel()
	tenant := "11111111-1111-1111-1111-111111111111"
	creds := map[string]*credstore.Credential{
		"no credential": nil,
		"no id":         {TenantID: tenant, IsAdmin: true},
	}
	id := uuid.NewString()
	routes := []struct{ name, method, path string }{
		{"create", http.MethodPost, "/v1/aggregation-rules"},
		{"activate", http.MethodPatch, "/v1/aggregation-rules/" + id + "/activate"},
		{"deactivate", http.MethodPatch, "/v1/aggregation-rules/" + id + "/deactivate"},
	}
	h := New(nil)
	router := chi.NewRouter()
	router.Post("/v1/aggregation-rules", h.Create)
	router.Patch("/v1/aggregation-rules/{id}/activate", h.Activate)
	router.Patch("/v1/aggregation-rules/{id}/deactivate", h.Deactivate)
	for credName, cred := range creds {
		for _, rt := range routes {
			t.Run(credName+"/"+rt.name, func(t *testing.T) {
				t.Parallel()
				ctx, err := tenancy.WithTenant(context.Background(), tenant)
				if err != nil {
					t.Fatalf("WithTenant: %v", err)
				}
				if cred != nil {
					ctx = authctx.WithCredential(ctx, *cred)
				}
				r := httptest.NewRequest(rt.method, rt.path, strings.NewReader(validRuleBody)).WithContext(ctx)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

// validRuleBody passes validation, so a 401 on create can only come from
// the missing actor.
const validRuleBody = `{"rule_id":"r","target_theme":"t","min_risks":2,"min_teams":1,` +
	`"window_days":30,"parent_level":"org","severity_function":"max","title_template":"x"}`

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
