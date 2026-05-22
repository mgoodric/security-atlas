// Slice 201 — env-gated POST /v1/test/issue-jwt endpoint that mints a
// JWT for the Playwright e2e harness.
//
// This is the runtime analog of `Server.IssueTestJWT(t, claims)` from
// `internal/api/server_testing.go` (slice 197). Where IssueTestJWT is a
// compile-time-gated helper that takes `testing.TB` and is unusable
// from production code, this endpoint is callable from any HTTP client
// — gated AT REQUEST TIME on `ATLAS_TEST_MODE=1`. The Playwright
// global-setup module (`web/e2e/global-setup.ts`) POSTs to this route
// once per test invocation, then seeds the returned JWT into
// `process.env.TEST_BEARER` for downstream specs.
//
// Slice 197 retired the slice 034 opaque-bearer middleware; the
// Playwright fixtures still set `sa_session_token` as a cookie, but the
// Next.js BFF (`web/lib/api/bff.ts`) forwards that cookie value as
// `Authorization: Bearer <value>` to the atlas Go server, where the
// slice 190 jwtmw middleware shape-checks for the `eyJ` JWT prefix.
// So the fixture continues to work as-is — only the VALUE of the
// cookie changes (from a static "test-bearer-e2e" to a freshly minted
// JWT).
//
// P0-201-2 (production safety):
//
//   - The handler refuses with 404 when `ATLAS_TEST_MODE != "1"`. A
//     404 is indistinguishable from "route not mounted" — defense in
//     depth so a misconfigured production binary that somehow keeps
//     the route mounted still returns a uniform Not-Found response.
//   - The route is conditionally mounted in `httpserver.go` ONLY when
//     `ATLAS_TEST_MODE=1` AT BOOT TIME, so the typical production
//     binary never sees the route at all.
//
// P0-201-3: the JWT is minted at request time and lives only in the
// response body. It is never persisted, never logged, never baked into
// an image layer.
//
// P0-201-4: the handler signs through `s.jwtSigner` — the SAME
// `tokensign.Signer` instance the slice 190 middleware verifies
// against, wired by `cmd/atlas/main.go` from the slice 187
// `fsstore.Open` keystore. There is no parallel test-only signing
// surface, no separate test keystore, no weakened constraints.

package api

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
)

// testModeEnvVar is the environment variable that gates the
// /v1/test/issue-jwt endpoint. Set to "1" by the Playwright CI workflow
// + the local self-host bundle's test-mode profile. Unset (or any value
// other than "1") makes the handler refuse with 404.
const testModeEnvVar = "ATLAS_TEST_MODE"

// issueTestJWTRequest is the JSON body accepted by the handler. All
// fields are optional except TenantID (without a tenant the JWT cannot
// satisfy the slice-190 tenant-scope invariant in jwt.Validate).
//
// Defaults applied when fields are zero:
//
//   - UserID: empty → subject = "test-admin:<tenant>" synthetic.
//     A caller wanting `/v1/me` to resolve to a real users row supplies
//     the row's UUID here.
//   - Roles: nil → ["admin"]. The jwtmw bridge stamps these onto the
//     synthesized credential's OwnerRoles.
//   - SuperAdmin: defaults true. The jwtmw bridge maps SuperAdmin into
//     both IsAdmin AND IsApprover on the credential — the typical
//     Playwright spec needs admin-gated routes (settings, dashboards,
//     admin-bootstrap).
type issueTestJWTRequest struct {
	TenantID   string   `json:"tenant_id"`
	UserID     string   `json:"user_id,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	SuperAdmin *bool    `json:"super_admin,omitempty"`
}

// issueTestJWTResponse is the JSON body returned on success. The
// Playwright global-setup module reads `token` and writes it into
// `process.env.TEST_BEARER`.
type issueTestJWTResponse struct {
	Token string `json:"token"`
}

// handleIssueTestJWT serves POST /v1/test/issue-jwt. See the package
// comment block at the top of this file for the full design rationale.
//
// Failure modes:
//
//   - 404 — ATLAS_TEST_MODE != "1" (production-safe default).
//   - 404 — s.jwtSigner == nil (no OAuth keystore wired; the route is
//     effectively absent).
//   - 400 — invalid JSON body OR missing/invalid tenant_id.
//   - 500 — signing failure (e.g., keystore inaccessible mid-request).
func (s *Server) handleIssueTestJWT(w http.ResponseWriter, r *http.Request) {
	// P0-201-2: per-request env-gate check. Belt-and-suspenders on top
	// of the mount-time gate in httpserver.go — a hypothetical bug that
	// leaves the route mounted in production still returns 404 here.
	if os.Getenv(testModeEnvVar) != "1" {
		http.NotFound(w, r)
		return
	}
	if s.jwtSigner == nil {
		// No keystore wired → behave as if the route were absent.
		http.NotFound(w, r)
		return
	}

	var body issueTestJWTRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	tenant, err := uuid.Parse(body.TenantID)
	if err != nil {
		http.Error(w, `{"error":"tenant_id must be a UUID"}`, http.StatusBadRequest)
		return
	}

	// Apply defaults.
	subject := body.UserID
	if subject == "" {
		subject = "test-admin:" + tenant.String()
	}
	roles := body.Roles
	if roles == nil {
		roles = []string{"admin"}
	}
	superAdmin := true
	if body.SuperAdmin != nil {
		superAdmin = *body.SuperAdmin
	}

	// Build claims. The shape mirrors testjwt.AdminFor / OwnerFor —
	// the per-tenant role list goes under Roles[tenant], the SuperAdmin
	// flag drives IsAdmin + IsApprover at jwtmw bridge time, and the
	// Subject carries through to cred.UserID (which /v1/me parses as a
	// UUID to resolve the real users row).
	now := time.Now().UTC()
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    s.jwtIssuer,
			Audience:  []string{s.jwtAudience},
			IssuedAt:  now.Unix(),
			ExpiresAt: now.Add(testjwt.DefaultExpiry).Unix(),
		},
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
		Roles:            map[uuid.UUID][]string{tenant: roles},
		SuperAdmin:       superAdmin,
	}

	token, err := s.jwtSigner.Sign(r.Context(), claims)
	if err != nil {
		http.Error(w, `{"error":"sign failure"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(issueTestJWTResponse{Token: token})
}

// testModeEnabled reports whether the env-gate is set. Used by
// httpserver.go to decide whether to mount the /v1/test/issue-jwt
// route at boot time.
func testModeEnabled() bool {
	return os.Getenv(testModeEnvVar) == "1"
}
