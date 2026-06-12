// Package scim is the HTTP surface for the inbound SCIM 2.0 provisioning
// endpoints (/scim/v2/*), slice 508.
//
// The auth model is the load-bearing security property (P0-508-2). SCIM
// endpoints are mounted OUTSIDE the /v1 JWT/authz/tenancy chain and are
// authenticated SOLELY by this package's Middleware, which validates a
// per-tenant, SCIM-scoped, revocable bearer credential (internal/scim
// CredentialStore). A SCIM token:
//
//   - cannot reach any /v1 platform handler (those require a JWT/api-key
//     credstore.Credential this package never produces);
//   - cannot mint an atlas human session (there is no session-issue path here);
//   - resolves to exactly one tenant, set on the request context so every
//     downstream provisioning query runs under that tenant's RLS (P0-508-4).
package scim

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/scim"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type ctxKey struct{}

// credentialContext carries the authenticated SCIM credential identity +
// tenant down to the handlers.
type credentialContext struct {
	credentialID uuid.UUID
	tenantID     string
}

// withSCIMCredential attaches the SCIM credential identity to ctx.
func withSCIMCredential(ctx context.Context, c credentialContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// scimCredentialFromContext returns the attached SCIM credential identity.
func scimCredentialFromContext(ctx context.Context) (credentialContext, bool) {
	v, ok := ctx.Value(ctxKey{}).(credentialContext)
	return v, ok
}

// authenticator is the minimal surface Middleware needs from the SCIM
// credential store. Satisfied by *scim.CredentialStore.
type authenticator interface {
	Authenticate(ctx context.Context, token string) (scim.Credential, error)
}

// Middleware authenticates the inbound SCIM bearer and, on success, sets the
// tenant GUC context + the SCIM credential identity. On failure it returns an
// RFC 7644-shaped 401 SCIM error. The discovery endpoints
// (ServiceProviderConfig / ResourceTypes / Schemas) are AUTHENTICATED too —
// RFC 7644 §4 allows them to be anonymous, but we require the bearer so an
// unauthenticated probe cannot fingerprint the deployment; an IdP always has
// the token before it probes.
func Middleware(store authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := extractBearer(r)
			if !ok {
				writeUnauthorized(w)
				return
			}
			cred, err := store.Authenticate(r.Context(), token)
			if err != nil {
				// All auth failures collapse to 401 — no oracle distinguishing
				// "revoked" from "never existed" from "wrong tenant".
				writeUnauthorized(w)
				return
			}
			tenantID := cred.TenantID.String()
			ctx, terr := tenancy.WithTenant(r.Context(), tenantID)
			if terr != nil {
				writeUnauthorized(w)
				return
			}
			ctx = withSCIMCredential(ctx, credentialContext{
				credentialID: cred.ID,
				tenantID:     tenantID,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearer(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", false
	}
	parts := strings.SplitN(strings.TrimSpace(auth), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	tok := strings.TrimSpace(parts[1])
	return tok, tok != ""
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="scim"`)
	writeSCIMError(w, http.StatusUnauthorized, "", "authorization must be a valid SCIM `Bearer <token>`")
}
