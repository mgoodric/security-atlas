// Package jwtmw is the chi HTTP middleware that gates `/v1/*` on a
// valid OAuth-AS-issued JWT.
//
// Slice 190 ships this as the PRIMARY auth path for `/v1/*`. Slice
// 034's bearer-token middleware continues to serve requests whose
// Authorization header carries an OPAQUE bearer (the slice-034
// `atlas_*` shape). The two paths coexist during the migration
// window; slice 191 retires the legacy bearer middleware.
//
// Coexistence resolution order (decision D3 in
// docs/audit-log/190-jwt-middleware-r2-decisions.md):
//
//  1. If the Authorization header value starts with `Bearer eyJ`
//     (the JWT compact-serialization prefix) OR the configured cookie
//     is present, run the JWT validation path. On success: set
//     context + tenant GUC + delegate. On failure: 401 + no fall-
//     through (P0-190-1 — falling through to legacy would be an auth
//     bypass).
//  2. Otherwise, leave the request untouched and let the downstream
//     legacy bearer middleware (or exempt-path bypass) handle it.
//
// Validation pipeline (P0-190-2 — order is load-bearing):
//
//  1. Parse + signature verification via slice 187's
//     tokensign.Signer.Verify.
//  2. Claim validation via slice 187's jwt.Validate (iss, aud, exp,
//     nbf, tenant-in-available_tenants).
//  3. Revocation check via slice 190's revocation.Store.IsRevoked.
//  4. Tenant GUC application via slice 033's tenancy.WithTenant
//     (skipped when CurrentTenantID is uuid.Nil — machine tokens).
//  5. Claims attached to context for downstream handlers.
//
// 401 responses always include the RFC 6750 §3
// `WWW-Authenticate: Bearer realm="atlas", error="invalid_token"`
// header (P0-190-11).
//
// Anti-criteria honored:
//
//   - P0-190-2: signature verification BEFORE revocation check.
//   - P0-190-3: tenant GUC is set from the VERIFIED claim only —
//     request headers are never read for tenant override.
//   - P0-190-9: the middleware operates only on `/v1/*` (its mount
//     site is the chi route group, not the global root router).
//   - P0-190-10: every signature verification goes through the
//     keystore-backed tokensign.Signer; no direct key access.
package jwtmw

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultCookieName matches slice 189's `atlas_session` cookie. The
// frontend OAuth completion flow stores the freshly minted JWT in
// this cookie; the middleware reads it back on every subsequent
// request.
const DefaultCookieName = "atlas_session"

// realm is the WWW-Authenticate `realm=` value advertised on 401
// responses per RFC 6750 §3.
const realm = "atlas"

// JWTPrefix is the literal byte prefix every JWS compact
// serialization begins with: the base64url of `{"alg":` is `eyJ`.
// We use this as the SHAPE check to distinguish JWT-bearing
// Authorization headers from opaque legacy bearer tokens (which
// begin with `atlas_`). Decision D3 leans on this prefix.
const JWTPrefix = "eyJ"

// ctxKey carries the verified JWT claims through the request
// context. Private type means external packages cannot collide on
// the key.
type ctxKey struct{}

// Options bundles the configuration knobs that vary across
// deployments. CookieName defaults to DefaultCookieName when zero.
type Options struct {
	// ExpectedIssuer + ExpectedAudience are passed to jwt.Validate.
	// Both must be non-empty in production wiring.
	ExpectedIssuer   string
	ExpectedAudience string

	// CookieName is the HTTP cookie the middleware reads as an
	// alternative to the Authorization header. Defaults to
	// DefaultCookieName.
	CookieName string

	// Now is the clock; tests inject a pinned clock. Defaults to
	// time.Now.
	Now func() int64
}

// Middleware returns a chi-compatible middleware that gates the
// request on a valid OAuth-AS-issued JWT.
//
// signer + revoked + opts must be non-nil at production wiring. The
// constructor accepts nil opts.Now (defaults to time.Now). The
// constructor does NOT panic — failures surface as runtime 500s so
// a misconfigured deployment doesn't crash the binary at startup;
// the unit-test surface verifies the failure mode.
func Middleware(signer *tokensign.Signer, revoked *revocation.Store, opts Options) func(http.Handler) http.Handler {
	cookieName := opts.CookieName
	if cookieName == "" {
		cookieName = DefaultCookieName
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, present := extractJWT(r, cookieName)
			if !present {
				// No JWT and no cookie → pass through. The
				// downstream chain (legacy bearer middleware or
				// exempt-path bypass) handles authentication.
				next.ServeHTTP(w, r)
				return
			}

			// P0-190-2: signature verification FIRST. tokensign.Verify
			// rejects any token whose signature does not match a
			// verification key in the keystore; the result is the
			// parsed AtlasClaims.
			claims, err := signer.Verify(r.Context(), tok)
			if err != nil {
				write401(w)
				return
			}

			// Claim validation: iss, aud, exp, nbf, tenant-scope.
			// Slice 187's jwt.Validate enumerates the failure modes.
			if err := jwt.Validate(claims, jwt.ValidationParams{
				ExpectedIssuer:   opts.ExpectedIssuer,
				ExpectedAudience: opts.ExpectedAudience,
				Now:              nowTime(opts.Now),
			}); err != nil {
				write401(w)
				return
			}

			// Revocation check. PK lookup on jti — single index probe.
			if claims.ID != "" && revoked != nil {
				isRevoked, rerr := revoked.IsRevoked(r.Context(), claims.ID)
				if rerr != nil {
					// Defensive: a DB error on the revocation check
					// fails closed. Operators see the error in logs;
					// the client sees a 401.
					write401(w)
					return
				}
				if isRevoked {
					write401(w)
					return
				}
			}

			// P0-190-3: tenant GUC is set from the VERIFIED claim, not
			// from request headers. Machine tokens carry uuid.Nil and
			// skip the GUC step (the request context simply lacks a
			// tenant — downstream handlers that require a tenant fail
			// loudly via tenancy.ErrNoTenant).
			ctx := r.Context()
			if claims.CurrentTenantID != uuid.Nil {
				tctx, terr := tenancy.WithTenant(ctx, claims.CurrentTenantID.String())
				if terr != nil {
					// A verified JWT whose CurrentTenantID is not a
					// valid UUID string is impossible — the signer
					// stamps a real uuid.UUID. If we get here, the
					// signer's invariants are broken. Fail closed.
					write401(w)
					return
				}
				ctx = tctx
			}

			// Attach claims to context for downstream handlers.
			claimsCopy := claims
			ctx = context.WithValue(ctx, ctxKey{}, &claimsCopy)

			// Synthesize a credstore.Credential from the verified
			// JWT so downstream chi middleware that reads
			// authctx.CredentialFromContext (tenancymw, authzmw)
			// continues to work unchanged. The synthesized
			// credential is in-memory only — it is NEVER persisted
			// and the bearer-token paths cannot resolve it. Roles
			// for the credential are the JWT claim's per-tenant
			// role list for the CURRENT tenant; super_admin maps to
			// IsAdmin. OwnerRoles are taken verbatim from the JWT
			// claim's role list so the slice 011 owner-role check
			// continues to gate manual attestations.
			cred := credstore.Credential{
				ID:         "jwt:" + claims.ID,
				TenantID:   claims.CurrentTenantID.String(),
				UserID:     claims.Subject,
				IsAdmin:    claims.SuperAdmin,
				IsApprover: claims.SuperAdmin,
				OwnerRoles: claims.Roles[claims.CurrentTenantID],
				IssuedAt:   time.Unix(claims.IssuedAt, 0),
				LastUsedAt: nowTime(opts.Now),
			}
			ctx = authctx.WithCredential(ctx, cred)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext returns the verified AtlasClaims set by Middleware, or
// nil when the request did not pass through the middleware (e.g.,
// exempt routes). Downstream handlers should treat a nil return as
// "no JWT context" rather than as an error — the legacy bearer path
// uses authctx.CredentialFromContext for its own per-request data.
func FromContext(ctx context.Context) *jwt.AtlasClaims {
	v, _ := ctx.Value(ctxKey{}).(*jwt.AtlasClaims)
	return v
}

// WithClaimsForTest injects an AtlasClaims into ctx so tests can
// exercise handlers that read FromContext without standing up the
// full middleware chain. Test-only by convention — production code
// MUST NOT call this; the middleware is the only legitimate setter.
//
// Added in slice 192 to support the /v1/me/tenants integration test
// harness. The helper is package-public because the consumer
// (internal/api/me) lives in a different package.
func WithClaimsForTest(ctx context.Context, c *jwt.AtlasClaims) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// extractJWT looks for a JWT in (a) the Authorization header with a
// "Bearer " prefix where the value starts with the JWT shape `eyJ`,
// then (b) the configured cookie.
//
// Decision D1: header wins when both are present. The
// Authorization header is an explicit client signal; a passive
// cookie should not preempt it.
//
// Decision D3 shape filter: only headers that START WITH `Bearer eyJ`
// are accepted as JWT candidates. A `Bearer atlas_*` header is left
// untouched so the legacy bearer middleware can pick it up. A
// `Bearer eyJ*` header that fails verification returns 401 — NO
// fall-through (P0-190-1 risk: falling through to legacy after a
// JWT-shaped failure would be an auth bypass).
func extractJWT(r *http.Request, cookieName string) (string, bool) {
	auth := r.Header.Get("Authorization")
	if auth != "" {
		parts := strings.SplitN(strings.TrimSpace(auth), " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			tok := strings.TrimSpace(parts[1])
			if isJWTShape(tok) {
				return tok, true
			}
		}
	}
	if cookieName != "" {
		if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
			if isJWTShape(c.Value) {
				return c.Value, true
			}
		}
	}
	return "", false
}

// isJWTShape reports whether tok matches the JWT compact-
// serialization shape: starts with `eyJ` and has exactly two dot
// separators. Cheap pre-filter so non-JWT bearers don't enter the
// signature-verification path.
func isJWTShape(tok string) bool {
	return strings.HasPrefix(tok, JWTPrefix) && strings.Count(tok, ".") == 2
}

// write401 writes the RFC 6750 §3 401 response.
func write401(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`", error="invalid_token"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
}

// nowTime adapts the int64 Unix-seconds clock the Options carries
// (matches the tokensign + jwt packages' clock shape) to the
// time.Time the slice 187 validator expects.
func nowTime(now func() int64) (t time.Time) {
	if now == nil {
		return time.Now()
	}
	return time.Unix(now(), 0)
}
