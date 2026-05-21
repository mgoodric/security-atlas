// authorize.go implements the slice-189 `GET /oauth/authorize`
// endpoint — the browser entry point of the OAuth Authorization Code
// + PKCE flow (RFC 6749 §4.1 + RFC 7636).
//
// The handler:
//
//  1. Validates RFC 6749 §4.1.1 query parameters (response_type,
//     client_id, redirect_uri, scope, state, code_challenge,
//     code_challenge_method).
//  2. Rejects PKCE `plain` — only `S256` is accepted (P0-189-1).
//  3. Validates the redirect_uri against the oauth_client_redirect_uris
//     registry (P0-189-2 — open-redirect prevention).
//  4. Requires an active slice-034 atlas_session cookie. When missing,
//     redirects to the OIDC login entry point with a return-to so the
//     authorize flow resumes after login.
//  5. Generates a 32-byte random base64url code, inserts into
//     oauth_auth_codes with the user's identity snapshot + PKCE
//     challenge + 60s TTL, redirects 302 to
//     `<redirect_uri>?code=<code>&state=<state>`.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-189-1: code_challenge_method=plain is rejected at the
//     application layer AND at the DB CHECK constraint.
//   - P0-189-2: redirect_uri is validated against the registry BEFORE
//     the browser sees any redirect. An unregistered URI never
//     receives an issued code.
//   - P0-189-3: codes are platform-global, one-shot — see the
//     oauthcode.Store contract.
//   - P0-189-4: response_type=token (Implicit grant) is rejected.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
)

// AuthCodeByteLen is the entropy of the random authorization code.
// 32 bytes = 256 bits; brute-force is computationally infeasible.
// base64url-encoded length is 43 characters (no padding).
const AuthCodeByteLen = 32

// SessionResolver loads a slice-034 session by its cookie value under
// a known tenant. The authorize handler does NOT depend on the full
// sessions.Store API — only this read primitive — so tests can mock
// without the pgxpool.
type SessionResolver interface {
	Read(ctx context.Context, tenantID uuid.UUID, id string) (sessions.Session, error)
}

// UserResolver loads the user identity claims (roles + tenant scope)
// the JWT will carry. Decoupled into an interface so tests can stub
// the user-identity lookup.
type UserResolver interface {
	ResolveForOAuth(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID) (UserIdentity, error)
}

// UserIdentity is the snapshot the authorize handler captures into
// oauth_auth_codes and the redemption path uses to mint the JWT.
type UserIdentity struct {
	UserID           uuid.UUID
	CurrentTenantID  uuid.UUID
	AvailableTenants []uuid.UUID
	Roles            map[uuid.UUID][]string
	SuperAdmin       bool
}

// AuthorizeEndpoint owns the dependencies for GET /oauth/authorize.
type AuthorizeEndpoint struct {
	codes         *oauthcode.Store
	clients       *oauthclient.Store
	sessions      SessionResolver
	users         UserResolver
	issuer        string
	codeTTL       time.Duration
	now           func() time.Time
	randRead      func([]byte) (int, error)
	loginRedirect func(returnTo string, tenantID uuid.UUID) string
}

// AuthorizeEndpointConfig is the constructor parameter bag.
type AuthorizeEndpointConfig struct {
	Codes    *oauthcode.Store
	Clients  *oauthclient.Store
	Sessions SessionResolver
	Users    UserResolver
	Issuer   string
	// CodeTTL — zero falls back to oauthcode.DefaultTTL.
	CodeTTL time.Duration
	// Now / RandRead — clock + entropy hooks for tests.
	Now      func() time.Time
	RandRead func([]byte) (int, error)
	// LoginRedirect builds the URL the handler 302-redirects to when
	// the user lacks an active session. Default points at
	// `/auth/oidc/login?tenant_id=<id>` with an opaque return-to param.
	LoginRedirect func(returnTo string, tenantID uuid.UUID) string
}

// NewAuthorizeEndpoint constructs an AuthorizeEndpoint.
//
// Panics on missing required dependency — process startup is the only
// caller, and a missing dependency at runtime is a programmer error.
func NewAuthorizeEndpoint(cfg AuthorizeEndpointConfig) *AuthorizeEndpoint {
	if cfg.Codes == nil {
		panic("oauth: NewAuthorizeEndpoint: Codes is nil")
	}
	if cfg.Clients == nil {
		panic("oauth: NewAuthorizeEndpoint: Clients is nil")
	}
	if cfg.Sessions == nil {
		panic("oauth: NewAuthorizeEndpoint: Sessions is nil")
	}
	if cfg.Users == nil {
		panic("oauth: NewAuthorizeEndpoint: Users is nil")
	}
	if cfg.Issuer == "" {
		panic("oauth: NewAuthorizeEndpoint: Issuer is empty")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	randRead := cfg.RandRead
	if randRead == nil {
		randRead = rand.Read
	}
	loginRedirect := cfg.LoginRedirect
	if loginRedirect == nil {
		loginRedirect = defaultLoginRedirect
	}
	ttl := cfg.CodeTTL
	if ttl <= 0 {
		ttl = oauthcode.DefaultTTL
	}
	return &AuthorizeEndpoint{
		codes:         cfg.Codes,
		clients:       cfg.Clients,
		sessions:      cfg.Sessions,
		users:         cfg.Users,
		issuer:        cfg.Issuer,
		codeTTL:       ttl,
		now:           now,
		randRead:      randRead,
		loginRedirect: loginRedirect,
	}
}

// defaultLoginRedirect builds `/auth/oidc/login?tenant_id=<id>` and
// embeds the original URL as `return_to` so the OIDC callback path
// can resume the authorize flow. Slice 190 will formalize the
// resume-token; v1 uses query-string preservation.
func defaultLoginRedirect(returnTo string, tenantID uuid.UUID) string {
	u := &url.URL{Path: "/auth/oidc/login"}
	q := u.Query()
	q.Set("tenant_id", tenantID.String())
	q.Set("return_to", returnTo)
	u.RawQuery = q.Encode()
	return u.String()
}

// ServeHTTP is the chi-mounted handler. Mounted via direct
// `root.Get(PathAuthorize, ...)` per the established parallel-batch
// convention (chi rejects a second Mount("/")).
func (a *AuthorizeEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// AC-15 / P0-189-4: only response_type=code is supported. The
	// Implicit grant (response_type=token) is explicitly rejected.
	responseType := q.Get("response_type")
	if responseType != "code" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_response_type",
			"only response_type=code is supported")
		return
	}

	clientID := q.Get("client_id")
	if clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is required")
		return
	}
	state := q.Get("state")
	// state is RECOMMENDED by RFC 6749 §4.1.1 but not strictly
	// required. We pass it through whether present or not; an absent
	// state means the redirect URL has no `&state=` segment.

	codeChallenge := q.Get("code_challenge")
	if codeChallenge == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"code_challenge is required (PKCE mandatory)")
		return
	}
	codeChallengeMethod := q.Get("code_challenge_method")
	if codeChallengeMethod == "" {
		// PKCE-required (D3 + P0-189-1): default to S256 ONLY when the
		// client passes `code_challenge` without specifying the method.
		// Per RFC 7636 §4.3 the default is `plain` — we deviate
		// deliberately to fail-secure.
		codeChallengeMethod = oauthcode.PKCEMethodS256
	}
	if codeChallengeMethod != oauthcode.PKCEMethodS256 {
		// AC-16 / P0-189-1: `plain` is explicitly rejected.
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"only code_challenge_method=S256 is supported")
		return
	}

	tenantIDStr := q.Get("tenant_id")
	if tenantIDStr == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"tenant_id is required")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"tenant_id is not a valid UUID")
		return
	}

	// AC-17: validate client_id against oauth_clients. We use the
	// Verify path with a dummy secret to detect "unknown" via the
	// ErrUnknownClient sentinel, but that conflates "wrong secret"
	// with "unknown client" — the authorize flow doesn't have a
	// secret. Instead we issue a dedicated "exists?" check by
	// attempting Verify with an empty secret, which the oauthclient
	// package rejects with ErrUnknownClient when (a) the client_id is
	// unknown OR (b) the supplied secret is empty. To preserve the
	// "no secret oracle" guarantee, the authorize handler does NOT
	// distinguish between "client unknown" and "client exists" via
	// the same error class — instead it uses the redirect URI
	// registry as the proxy: if the client_id has no registered URIs,
	// it's effectively not configured for browser flows.
	//
	// Pragmatic check: the redirect URI registry IS the
	// authorize-mode source of truth. A client_id with no registered
	// redirect URI cannot use the authorize endpoint, period.
	registered, err := a.codes.IsRedirectURIRegistered(r.Context(), clientID, redirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"registry lookup failed")
		return
	}
	if !registered {
		// AC-18 / P0-189-2: unregistered URI rejection BEFORE any
		// browser redirect. Whether the client_id is unknown OR
		// registered-but-without-this-URI, the response is the same:
		// no code issued.
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"redirect_uri is not registered for this client")
		return
	}

	// AC-20: require an active session. Read the atlas_session cookie
	// + look up the row under the supplied tenant. Missing / expired
	// → redirect to OIDC login with return-to.
	c, cerr := r.Cookie(sessions.CookieName)
	if cerr != nil || c.Value == "" {
		http.Redirect(w, r, a.loginRedirect(r.URL.String(), tenantID), http.StatusFound)
		return
	}
	sess, serr := a.sessions.Read(r.Context(), tenantID, c.Value)
	if serr != nil {
		// Treat any failure to resolve the session as "no session" —
		// redirect to login. The session store distinguishes
		// ErrNotFound / ErrRevoked / ErrExpired but the authorize
		// flow's response is the same in all three cases.
		http.Redirect(w, r, a.loginRedirect(r.URL.String(), tenantID), http.StatusFound)
		return
	}

	// AC-22: capture the user's identity snapshot — current tenant,
	// available tenants, roles, super_admin. This is what the JWT
	// carries at redemption.
	identity, ierr := a.users.ResolveForOAuth(r.Context(), sess.UserID, sess.TenantID)
	if ierr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"identity resolution failed")
		return
	}

	// Marshal roles as JSONB. Empty map → "{}".
	var rolesJSON []byte
	if len(identity.Roles) == 0 {
		rolesJSON = []byte("{}")
	} else {
		// JSONB requires string keys; serialise the UUID keys as
		// strings.
		rolesAsStringKey := make(map[string][]string, len(identity.Roles))
		for k, v := range identity.Roles {
			rolesAsStringKey[k.String()] = v
		}
		b, merr := json.Marshal(rolesAsStringKey)
		if merr != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error",
				"roles serialization failed")
			return
		}
		rolesJSON = b
	}

	// AC-21: 32-byte base64url random code.
	code, gerr := a.generateCode()
	if gerr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"code generation failed")
		return
	}

	if _, ierr := a.codes.Insert(r.Context(), oauthcode.InsertParams{
		Code:                code,
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: oauthcode.PKCEMethodS256,
		UserID:              identity.UserID,
		IDPIssuer:           sess.IdpIssuer,
		IDPSubject:          sess.IdpSubject,
		CurrentTenantID:     identity.CurrentTenantID,
		AvailableTenants:    identity.AvailableTenants,
		RolesJSON:           rolesJSON,
		SuperAdmin:          identity.SuperAdmin,
		TTL:                 a.codeTTL,
	}); ierr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"code persistence failed")
		return
	}

	// AC-23: 302 redirect to redirect_uri?code=<code>&state=<state>.
	// Build the redirect URL via url.Parse so we honor existing query
	// strings on the registered URI (rare, but RFC 6749 §3.1.2
	// permits them).
	target, perr := url.Parse(redirectURI)
	if perr != nil {
		// Should not happen — the URI was registered, so it parsed
		// once. Defense in depth.
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"redirect_uri unparseable")
		return
	}
	tq := target.Query()
	tq.Set("code", code)
	if state != "" {
		tq.Set("state", state)
	}
	target.RawQuery = tq.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// generateCode produces a 32-byte random URL-safe base64 (no padding)
// authorization code.
func (a *AuthorizeEndpoint) generateCode() (string, error) {
	buf := make([]byte, AuthCodeByteLen)
	if _, err := a.randRead(buf); err != nil {
		return "", fmt.Errorf("oauth: code rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ---- Token endpoint extension: authorization_code grant ----

// AttachAuthCodeStore wires the oauthcode.Store into the
// TokenEndpoint so the `grant_type=authorization_code` dispatch can
// redeem codes. Called from cmd/atlas/main.go at startup AFTER
// NewTokenEndpoint.
func (t *TokenEndpoint) AttachAuthCodeStore(codes *oauthcode.Store) {
	t.codes = codes
}

// handleAuthorizationCode redeems an oauth_auth_codes row, validates
// PKCE + redirect_uri, and mints a JWT.
func (t *TokenEndpoint) handleAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	if t.codes == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error",
			"authorization_code grant not configured")
		return
	}
	code := r.FormValue("code")
	verifier := r.FormValue("code_verifier")
	redirectURI := r.FormValue("redirect_uri")
	clientID := r.FormValue("client_id")
	if code == "" || verifier == "" || redirectURI == "" || clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"code, code_verifier, redirect_uri, client_id are required")
		return
	}

	// AC-30 / P0-189-3: one-shot consume. ConsumeOnce returns the row
	// with consumed_at set OR an error.
	ac, err := t.codes.ConsumeOnce(r.Context(), code)
	if err != nil {
		// AC-25 / AC-26 / AC-27: collapse to invalid_grant per RFC
		// 6749 §5.2 — no oracle.
		switch {
		case errors.Is(err, oauthcode.ErrNotFound),
			errors.Is(err, oauthcode.ErrAlreadyConsumed),
			errors.Is(err, oauthcode.ErrExpired):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"authorization code is invalid")
			return
		default:
			writeOAuthError(w, http.StatusInternalServerError, "server_error",
				"code redemption failed")
			return
		}
	}

	// AC-28: redirect_uri must match the value used at authorize.
	if ac.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
			"authorization code is invalid")
		return
	}
	// client_id must match.
	if ac.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
			"authorization code is invalid")
		return
	}

	// AC-29 / P0-189-1: PKCE S256 verification. Compute the expected
	// challenge from the supplied verifier and constant-time compare.
	expected := computePKCEChallengeS256(verifier)
	if !constantTimeEqualString(expected, ac.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
			"authorization code is invalid")
		return
	}

	// Deserialize roles JSONB. Empty map fallback on missing/empty.
	var roles map[uuid.UUID][]string
	if len(ac.Roles) > 0 {
		var raw map[string][]string
		if err := json.Unmarshal(ac.Roles, &raw); err == nil {
			roles = make(map[uuid.UUID][]string, len(raw))
			for k, v := range raw {
				if uid, perr := uuid.Parse(k); perr == nil {
					roles[uid] = v
				}
			}
		}
	}

	// AC-31: mint JWT via tokensign.Sign.
	now := t.now()
	claims := buildAtlasClaimsForUser(t.issuer, ac, roles, now)
	tok, signErr := t.signer.Sign(r.Context(), claims)
	if signErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to sign token")
		return
	}

	// D2: write audit row to oauth_token_exchanges. Best-effort
	// post-sign (same discipline as the token-exchange handler).
	t.writeAuthCodeAudit(r, ac)

	writeTokenResponse(w, tok)
}

// PKCE primitives + audit writer live in pkce.go.
