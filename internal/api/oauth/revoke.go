// revoke.go implements the slice-190 `POST /oauth/revoke` endpoint
// per RFC 7009.
//
// Authentication shape (decision D3 in
// docs/audit-log/190-jwt-middleware-r2-decisions.md):
//
//  1. client_credentials via Basic header or form params — the
//     classic RFC 6749 §2.3.1 client authentication. Any registered
//     oauth_clients row can call this to revoke any token (the
//     credential proves the caller is a trusted machine identity).
//  2. Self-revocation via JWT bearer — the caller presents a valid
//     JWT in `Authorization: Bearer <jwt>` and is authorised to
//     revoke ONLY tokens whose `sub` claim matches their own. This
//     is the user logout path; users do not hold client_secret.
//
// Response semantics (P0-190-4 — RFC 7009 §2.2):
//
//   - 200 on successful revocation.
//   - 200 for unknown / already-revoked / malformed tokens (silent —
//     no information disclosure).
//   - 400 on a missing `token` form param.
//   - 401 on a failed AUTHENTICATION of the caller. Auth failure is
//     distinct from token unknown-ness.
//
// Anti-criteria honored:
//
//   - P0-190-4: returns 200 for unknown tokens.
//   - P0-190-6: signature verification goes through
//     tokensign.Signer.Verify — no direct keystore access.
package oauth

import (
	"net/http"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// RevocationEndpoint owns the dependencies for `POST /oauth/revoke`.
type RevocationEndpoint struct {
	signer  *tokensign.Signer
	revoked *revocation.Store
	clients *oauthclient.Store
	issuer  string
}

// RevocationEndpointConfig is the constructor parameter bag.
type RevocationEndpointConfig struct {
	Issuer string
}

// NewRevocationEndpoint builds a RevocationEndpoint. signer +
// revoked + clients must be non-nil for production wiring; the
// constructor panics on missing required deps because this is wired
// once at process startup.
func NewRevocationEndpoint(signer *tokensign.Signer, revoked *revocation.Store, clients *oauthclient.Store, cfg RevocationEndpointConfig) *RevocationEndpoint {
	if signer == nil {
		panic("oauth: NewRevocationEndpoint: signer is nil")
	}
	if revoked == nil {
		panic("oauth: NewRevocationEndpoint: revoked store is nil")
	}
	if cfg.Issuer == "" {
		panic("oauth: NewRevocationEndpoint: Issuer is empty")
	}
	return &RevocationEndpoint{
		signer:  signer,
		revoked: revoked,
		clients: clients,
		issuer:  cfg.Issuer,
	}
}

// ServeHTTP implements `POST /oauth/revoke` per RFC 7009.
func (e *RevocationEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if !isFormContentType(ct) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"Content-Type must be application/x-www-form-urlencoded")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}

	tokenIn := r.FormValue("token")
	if tokenIn == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}

	// Verify the supplied token ONCE. authenticate may also need to
	// peek at the target's sub for the self-revoke path; we share
	// the verify result to avoid double-work on the hot path.
	// Verify failure does NOT short-circuit yet — RFC 7009 §2.2
	// requires a silent 200 for invalid tokens, but only AFTER the
	// caller authenticates successfully.
	targetClaims, targetErr := e.signer.Verify(r.Context(), tokenIn)

	// AUTHENTICATE the revoker. Two paths:
	//   (a) client_credentials via Basic header OR form params.
	//   (b) self-revocation: caller's Authorization Bearer JWT has
	//       the same `sub` as the supplied (verified) token. Only
	//       tried when (a) was not attempted.
	revokedBy, authOK := e.authenticate(r, targetClaims, targetErr)
	if !authOK {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client",
			"client authentication failed")
		return
	}

	if targetErr != nil {
		// Invalid signature → silent 200. Per RFC 7009 §2.2 the
		// caller authenticated but the token is garbage; we don't
		// disclose that fact.
		write200(w)
		return
	}

	// Compute exp for the revocation row. We use the token's own
	// exp claim — that's the natural sweeper boundary.
	exp := time.Unix(targetClaims.ExpiresAt, 0)
	if targetClaims.ExpiresAt <= 0 {
		// Defensive: a token with no exp is malformed; treat the
		// revocation as a short-window entry so it still gets
		// swept eventually.
		exp = time.Now().Add(time.Hour)
	}

	ipStr, _ := requestIP(r).(string)

	if err := e.revoked.Revoke(r.Context(), targetClaims.ID, exp, revokedBy, ipStr); err != nil {
		// Internal failure — fail closed with a generic 500. The
		// RFC's "silent 200 for unknown" applies to client
		// behaviour; an internal error is different from "token
		// not present".
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to record revocation")
		return
	}

	write200(w)
}

// authenticate decides which revoker identity to use. Returns the
// revoked_by value to record (formatted as `oauth_client:<client_id>`
// or `user:<sub>`) and whether authentication succeeded.
//
// targetClaims + targetErr are the result of the caller's verify
// call on the to-be-revoked token. Sharing them here avoids
// double-verifying on the self-revoke hot path.
func (e *RevocationEndpoint) authenticate(r *http.Request, targetClaims jwt.AtlasClaims, targetErr error) (string, bool) {
	// Path (a): client_credentials. Check Basic header first, then
	// form params. A Basic header declaration is an explicit
	// "I'm a client" intent — when present we do NOT fall through
	// to self-revoke, even if Basic verification fails.
	if clientID, clientSecret, ok := r.BasicAuth(); ok && clientID != "" && clientSecret != "" {
		if e.clients != nil {
			if _, err := e.clients.Verify(r.Context(), clientID, clientSecret); err == nil {
				return "oauth_client:" + clientID, true
			}
		}
		return "", false
	}
	if clientID := r.FormValue("client_id"); clientID != "" {
		clientSecret := r.FormValue("client_secret")
		if clientSecret != "" && e.clients != nil {
			if _, err := e.clients.Verify(r.Context(), clientID, clientSecret); err == nil {
				return "oauth_client:" + clientID, true
			}
		}
		return "", false
	}

	// Path (b): self-revocation. The Authorization Bearer header
	// must carry a valid JWT whose `sub` matches the
	// to-be-revoked token's sub.
	bearer, ok := extractBearerRaw(r)
	if !ok || !strings.HasPrefix(bearer, "eyJ") {
		return "", false
	}
	callerClaims, err := e.signer.Verify(r.Context(), bearer)
	if err != nil {
		return "", false
	}
	if err := jwt.Validate(callerClaims, jwt.ValidationParams{
		ExpectedIssuer:   e.issuer,
		ExpectedAudience: e.issuer,
		Now:              time.Now(),
	}); err != nil {
		return "", false
	}
	if targetErr != nil {
		// Target token is malformed → we cannot determine its sub.
		// The caller authenticated via their own valid bearer, so
		// accept the call; the silent-200 path in ServeHTTP runs
		// next and no revocation row is written. Recording the
		// caller as the would-be revoker keeps the audit semantics
		// consistent if the call ever reaches the Revoke path.
		return "user:" + callerClaims.Subject, true
	}
	if targetClaims.Subject != callerClaims.Subject {
		// Caller is trying to revoke someone else's token without
		// client_credentials. Reject.
		return "", false
	}
	return "user:" + callerClaims.Subject, true
}

// extractBearerRaw pulls the Authorization: Bearer value (raw token,
// no shape filter). Returns ("", false) when absent.
func extractBearerRaw(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", false
	}
	parts := strings.SplitN(strings.TrimSpace(auth), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

// write200 writes the RFC 7009 §2.2 silent-success response. Empty
// body, 200 status.
func write200(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
