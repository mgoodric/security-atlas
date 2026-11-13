// Package tenancymw provides the chi HTTP middleware that lifts the
// authenticated credential's tenant id onto the request context so that
// every downstream handler — and every database transaction it opens —
// runs under the right `app.current_tenant` GUC.
//
// This is the application-side half of constitutional invariant 6
// (CLAUDE.md): "Tenant isolation is enforced at the database layer via
// PostgreSQL Row-Level Security on every tenant-scoped table. Not
// application code. RLS denies on missing context." The middleware is
// the SETTER. The RLS policies under `migrations/sql/` are the ENFORCER.
// Without the setter, a handler that "forgets" `WHERE tenant_id = ?`
// returns zero rows (no default-allow). With the setter, a handler that
// forgets returns the right rows for the right tenant.
//
// Behaviour:
//
//   - If the request context already carries an authenticated credential
//     (slice-014 `authctx.WithCredential` ran during the bearer-auth
//     middleware), the middleware derives a new context with
//     `app.current_tenant` set to that credential's tenant id and serves
//     the inner handler with it.
//
//   - If no credential is in context (the request hit a bearer-exempt
//     prefix like `/auth/*`), the middleware is a no-op. The handler is
//     responsible for calling `tenancy.WithTenant` itself with whatever
//     request-supplied tenant id is appropriate. Those handlers
//     (e.g. `/auth/local/login`, `/auth/oidc/login`) are the only places
//     where a request body legitimately establishes the tenant — every
//     other handler MUST inherit from this middleware.
//
//   - If the credential resolves to no tenant (empty or the all-zeros
//     tenant), a write (any method but GET/HEAD/OPTIONS) is refused with
//     403 `tenant_unbound` (security-atlas#1739). Before that refusal an
//     unbound OAuth client's writes landed in the all-zeros tenant, where
//     no user could see them.
//
//   - If the credential carries a malformed tenant id, the middleware
//     fails the request with 500. A malformed tenant id at this layer
//     means the credstore returned a credential with bad data — that is
//     a server-side bug, not a client error.
//
// The middleware is mounted in `internal/api/httpserver.go` immediately
// AFTER the bearer-auth middleware, so the credential is guaranteed to
// be in context (or guaranteed absent on an exempt path) by the time we
// run.
package tenancymw

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Middleware returns a chi-compatible middleware that injects the
// authenticated credential's tenant id into the request context. It is
// the single source of `app.current_tenant` for every bearer-auth'd
// request path. Callers must register it AFTER bearer-auth and BEFORE
// any handler that opens a database transaction.
//
// Returning `func(http.Handler) http.Handler` (rather than a method on
// a type) keeps the registration site one line:
//
//	root.Use(tenancymw.Middleware)
//
// matching the existing slice-034 middleware-attach pattern.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cred, ok := authctx.CredentialFromContext(r.Context())
		if !ok {
			// Exempt path or pre-auth boundary. Pass through.
			next.ServeHTTP(w, r)
			return
		}
		ctx, err := tenancy.WithTenant(r.Context(), cred.TenantID)
		if err != nil {
			// A credential with a malformed tenant id should be
			// impossible: the credstore / apikeystore both validate it
			// at issuance. If we see one here, the data store has
			// drifted. Fail closed.
			log.Printf("tenancymw: credential %s has invalid tenant id: %v", cred.ID, err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if isUnboundTenant(cred.TenantID) && !isSafeMethod(r.Method) {
			refuseUnboundWrite(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UnboundWriteMessage is the body of the refusal a write gets when its
// credential resolves to no tenant. It names the fix, because the usual
// cause is an OAuth client issued before clients were bound to tenants.
const UnboundWriteMessage = "this credential is not bound to a tenant, so it cannot write. " +
	"Bind its OAuth client to a tenant: atlas-cli oauth bind-client <client_id> --tenant <tenant-id> " +
	"(or issue a new client with atlas-cli oauth issue-client <name> --tenant <tenant-id>)"

// isUnboundTenant reports whether a credential's tenant id is the
// all-zeros tenant (security-atlas#1739). A client_credentials token from
// an unbound OAuth client carries uuid.Nil; jwtmw bridges it as
// "00000000-0000-0000-0000-000000000000". Writes under it would land in a
// tenant no user can see. The id has already parsed (tenancy.WithTenant
// rejects anything that is not a UUID, including "").
func isUnboundTenant(tenantID string) bool {
	return uuid.MustParse(tenantID) == uuid.Nil
}

// isSafeMethod reports the RFC 9110 §9.2.1 safe methods. Reads under the
// all-zeros tenant return nothing and are left alone; only writes are
// refused.
func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func refuseUnboundWrite(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "tenant_unbound",
		"message": UnboundWriteMessage,
	})
}
