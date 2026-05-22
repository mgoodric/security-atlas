// Test-fixture helpers exposed by the api package. The file is NOT
// build-tagged and is not name-suffixed `_test.go` because callers in
// sibling packages (internal/api/anchors, internal/api/risks, etc.)
// need to import this symbol from THEIR `_test.go` files — Go's
// `_test.go` files cannot be imported across packages.
//
// Production code MUST NOT call IssueTestJWT: the signature takes
// `testing.TB`, which makes accidental production use a compile error
// when `t` is not in scope.
//
// Slice 197 — this method replaces the legacy
// `IssueBootstrap{Admin,Approver,Owner,...}Credential` surface that
// minted slice-034 opaque bearers. The legacy bearer middleware mount
// is removed in the same slice; tests now authenticate through the
// same slice 190 JWT validator the production binary uses.

package api

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	atlasjwt "github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// testJWTIssuer is the deterministic issuer URL stamped onto every
// helper-minted JWT. The matching value is set as both ExpectedIssuer
// and ExpectedAudience on the lazy-wired jwtmw middleware so a single
// IssueTestJWT call produces a token the same Server validates.
//
// The literal value is irrelevant — it never leaves the test process
// — but it MUST be stable so the validator and the signer agree.
const testJWTIssuer = "https://atlas.test"

// IssueTestJWT mints a JWT for integration tests and lazy-wires the
// slice 190 JWT validator onto the Server on first call. The returned
// string is the compact-serialized JWS suitable for an
// `Authorization: Bearer <token>` header against
// `srv.HTTPHandlerForTests()`.
//
// Slice 197 — this replaces the legacy `IssueBootstrap*Credential`
// fixture surface. The credstore-backed bearer middleware mount is
// removed in this slice; tests authenticate via the same JWT path
// the production binary uses.
//
// First-call behaviour: opens an in-memory keystore via
// `fsstore.Open(t.TempDir())`, wraps it in a `tokensign.Signer`,
// attaches the validator with a deterministic issuer + audience,
// and stamps the signer onto the Server for re-use. Subsequent calls
// reuse the same signer so multiple bearers minted off one Server
// authenticate through one middleware chain.
//
// The revocation.Store passed to the validator is nil — the JWT
// middleware's revocation check short-circuits on a nil store
// (`jwtmw.Middleware` line 150: `if claims.ID != "" && revoked != nil`).
// Integration tests do not exercise revocation; the unit test surface
// in `internal/auth/revocation/integration_test.go` covers it.
//
// `claims` is typically built by the helpers in
// `internal/api/testjwt/` (`AdminFor`, `OwnerFor`, `ApproverFor`,
// `ViewerFor`). The helper stamps Issuer + Audience + IssuedAt +
// ExpiresAt onto the claims if the caller left them zero — typical
// call sites pass only the Atlas-specific fields.
//
// The signature accepts `testing.TB` (not `*testing.T`) so the same
// helper works in benchmarks (`*testing.B`).
func (s *Server) IssueTestJWT(t testing.TB, claims atlasjwt.AtlasClaims) string {
	t.Helper()
	if s.jwtSigner == nil {
		ks, err := fsstore.Open(t.TempDir())
		if err != nil {
			t.Fatalf("IssueTestJWT: fsstore.Open: %v", err)
		}
		signer := tokensign.New(ks)
		// nil revocation store — see method doc + jwtmw.Middleware
		// line 150 (`if claims.ID != "" && revoked != nil`).
		s.AttachJWTValidator(signer, nil, testJWTIssuer, testJWTIssuer)
	}
	return testjwt.MustMint(t, s.jwtSigner, testJWTIssuer, testJWTIssuer, claims)
}
