package tenancymw_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

// security-atlas#1739: a credential that resolves to the all-zeros tenant
// (an unbound OAuth client's client_credentials token, bridged by jwtmw as
// uuid.Nil) may read, but every write is refused before it reaches a
// handler, so nothing lands in a tenant no user can see.
func TestMiddleware_AllZerosTenant_WritesRefused(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			called := false
			h := tenancymw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			cred := credstore.Credential{ID: "jwt:x", TenantID: uuid.Nil.String(), UserID: "oauth_client:abc"}
			req := httptest.NewRequest(method, "/v1/controls/bundles", nil)
			req = req.WithContext(authctx.WithCredential(req.Context(), cred))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if called {
				t.Fatal("handler ran for a write under the all-zeros tenant")
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
			}
			if body["error"] != "tenant_unbound" {
				t.Errorf("error = %q, want tenant_unbound", body["error"])
			}
			if !strings.Contains(body["message"], "atlas-cli oauth bind-client") {
				t.Errorf("message %q does not name how to bind the client", body["message"])
			}
		})
	}
}

func TestMiddleware_AllZerosTenant_ReadsPassThrough(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			called := false
			h := tenancymw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			cred := credstore.Credential{ID: "jwt:x", TenantID: uuid.Nil.String()}
			req := httptest.NewRequest(method, "/v1/controls", nil)
			req = req.WithContext(authctx.WithCredential(req.Context(), cred))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if !called || rec.Code != http.StatusOK {
				t.Fatalf("called=%v status=%d, want the read to pass through", called, rec.Code)
			}
		})
	}
}

func TestMiddleware_BoundTenant_WritesPassThrough(t *testing.T) {
	t.Parallel()
	called := false
	h := tenancymw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	cred := credstore.Credential{ID: "jwt:x", TenantID: uuid.NewString()}
	req := httptest.NewRequest(http.MethodPost, "/v1/controls/bundles", nil)
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called || rec.Code != http.StatusCreated {
		t.Fatalf("called=%v status=%d, want the write to reach the handler", called, rec.Code)
	}
}
