package oauth

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// ExportComputePKCEChallengeS256 exposes computePKCEChallengeS256
// to the external test package without making the function public.
// Slice-189 test seam.
func ExportComputePKCEChallengeS256(verifier string) string {
	return computePKCEChallengeS256(verifier)
}

// ExportConstantTimeEqual exposes constantTimeEqualString to the
// external test package.
func ExportConstantTimeEqual(a, b string) bool {
	return constantTimeEqualString(a, b)
}

// ExportBuildDeviceCodeClaims exposes buildDeviceCodeClaims (slice-191
// device-code redemption claim mapping) to the external test package.
// Slice-314 test seam — the approval-snapshot → JWT-claims projection
// is a load-bearing security function (super_admin copy-not-synthesize,
// tenant + role parsing) that is otherwise only reachable through a
// DB-backed device-code redemption.
func ExportBuildDeviceCodeClaims(issuer string, row *DeviceCodeRow, now time.Time) (jwt.AtlasClaims, error) {
	return buildDeviceCodeClaims(issuer, row, now)
}

// ExportNewDevicePollTracker + ExportDevicePollAllow expose the RFC
// 8628 §3.5 slow_down poll tracker so the per-device_code 5-second
// floor can be asserted deterministically with an injected clock.
func ExportNewDevicePollTracker(now func() time.Time) *devicePollTracker {
	return newDevicePollTracker(now)
}

func ExportDevicePollAllow(p *devicePollTracker, deviceCode string, now time.Time) bool {
	return p.Allow(deviceCode, now)
}

// ExportRequestIP exposes requestIP for the audit-log IP-parsing
// branch coverage (valid host:port, bare host, unparseable, nil).
func ExportRequestIP(r *http.Request) any {
	return requestIP(r)
}

// ExportGenerateDeviceCode + ExportGenerateUserCode drive the RFC
// 8628 §6.1 secret generators through a constructed endpoint with an
// injected entropy source, without needing the DB-backed ServeHTTP
// path (which is gated behind a client lookup).
func ExportGenerateDeviceCode(ep *DeviceAuthorizationEndpoint) (string, error) {
	return ep.generateDeviceCode()
}

func ExportGenerateUserCode(ep *DeviceAuthorizationEndpoint) (string, error) {
	return ep.generateUserCode()
}

// ExportReadRandom exposes the production entropy source so its
// fixed-length contract is asserted.
func ExportReadRandom(n int) ([]byte, error) {
	return readRandom(n)
}

// ExportUserCodeAlphabet re-exports the unambiguous user_code alphabet
// (P0-191-4) so the device-code generator test can assert membership.
const ExportUserCodeAlphabet = UserCodeAlphabet

// ExportDefaultLoginRedirect exposes defaultLoginRedirect so the
// slice-189 "no session → bounce to OIDC login with return-to" URL
// builder is asserted without driving the DB-backed authorize flow.
func ExportDefaultLoginRedirect(returnTo string, tenantID uuid.UUID) string {
	return defaultLoginRedirect(returnTo, tenantID)
}

// ExportBuildAtlasClaimsForUser exposes buildAtlasClaimsForUser
// (slice-189 authorization_code claim mapping) so the user-mode claim
// projection (sub prefix, nil-roles/nil-tenants defensive defaults,
// super_admin copy) is asserted without a DB-backed code redemption.
func ExportBuildAtlasClaimsForUser(issuer string, ac authCodeForExport, roles map[uuid.UUID][]string, now time.Time) jwt.AtlasClaims {
	return buildAtlasClaimsForUser(issuer, oauthcode.AuthCode(ac), roles, now)
}

// authCodeForExport is a type alias-shaped wrapper so the external
// test package can pass an oauthcode.AuthCode without importing it at
// the seam (it imports it anyway, but this keeps the seam explicit).
type authCodeForExport = oauthcode.AuthCode

// ===== slice 456 residual-coverage seams =====
//
// These unexported seams expose the best-effort audit-write methods
// and the rate-limiter internals to the external test package WITHOUT
// changing any production behavior (slice 409 precedent — New*Endpoint
// stays byte-for-byte unchanged). They exist so the slice-456 tests can
// drive the writeAudit / writeAuthCodeAudit failure arms (D3
// best-effort, non-blocking) with a controllable pool, and the
// tokenBucketLimiter overflow / window edge arms with an injected clock.

// ExportTokenEndpointForAudit constructs a TokenEndpoint wired with the
// supplied audit pool so the audit-write seams can drive the
// failure arms. The signer is the only other required dep; clients may
// be nil (the audit methods never touch the client store).
func ExportTokenEndpointForAudit(signer *tokensign.Signer, auditPool *pgxpool.Pool, now func() time.Time) *TokenEndpoint {
	return NewTokenEndpoint(signer, nil, TokenEndpointConfig{
		Issuer:    "https://atlas.example.test",
		AuditPool: auditPool,
		Now:       now,
	})
}

// ExportWriteAudit drives the token-exchange best-effort audit write
// (token.go writeAudit). A failure inside MUST NOT panic or block — the
// seam returns after the method completes regardless of the audit
// outcome (D3).
func ExportWriteAudit(t *TokenEndpoint, r *http.Request, subjectClaims jwt.AtlasClaims, targetTenant uuid.UUID) {
	t.writeAudit(r, subjectClaims, targetTenant)
}

// ExportWriteAuthCodeAudit drives the authorization_code best-effort
// audit write (pkce.go writeAuthCodeAudit).
func ExportWriteAuthCodeAudit(t *TokenEndpoint, r interface{ Context() context.Context }, ac oauthcode.AuthCode) {
	t.writeAuthCodeAudit(r, ac)
}

// ExportNewTokenBucketLimiter exposes the per-client token-bucket
// limiter with an injected clock so the overflow-cap and window-edge
// arms are assertable deterministically.
func ExportNewTokenBucketLimiter(rate int, now func() time.Time) *ExportTokenBucketLimiter {
	return newTokenBucketLimiter(rate, now)
}

// ExportLimiterAllow consumes a token from the bucket.
func ExportLimiterAllow(l *ExportTokenBucketLimiter, key string) bool {
	return l.Allow(key)
}

// ExportLimiterWindowSeconds returns the Retry-After window.
func ExportLimiterWindowSeconds(l *ExportTokenBucketLimiter) int {
	return l.WindowSeconds()
}

// ExportTokenBucketLimiter aliases the unexported limiter so the
// external test package can hold a typed handle.
type ExportTokenBucketLimiter = tokenBucketLimiter
