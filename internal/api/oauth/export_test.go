package oauth

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
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
