// Package oidc is the security-atlas OIDC Relying Party.
//
// We are never an IdP. The platform initiates the OAuth 2.0 + OIDC code flow
// with PKCE against a per-tenant IdP config, validates the ID token, and
// upserts a user keyed on (idp_issuer, idp_subject).
//
// State + PKCE protection lives in short-lived cookies scoped to /auth/oidc;
// the callback handler verifies that state cookie matches the query param
// (CSRF guard) before exchanging the code.
//
// Library: github.com/coreos/go-oidc/v3 + golang.org/x/oauth2.
package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	coreos "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const (
	// StateCookie holds the per-flow nonce. The callback verifies state
	// cookie == state query param; mismatch = CSRF.
	StateCookie = "atlas_oidc_state"

	// VerifierCookie holds the PKCE code_verifier. The callback passes it
	// in the code-exchange request as the proof that the original /login
	// initiator and this callback recipient are the same client.
	VerifierCookie = "atlas_oidc_verifier"

	// IdpCookie names which IdP config (by `name`) this flow runs against.
	// The callback uses this to look up the right IdP for the exchange.
	IdpCookie = "atlas_oidc_idp"

	// NonceCookie holds the per-flow OIDC `nonce` (slice 365). The
	// callback verifies the ID token's `nonce` claim against this cookie
	// value to defend against ID-token replay per OIDC Core §3.1.2.1 +
	// RFC 9700 §4.5.3. Additive to state + PKCE — covers a distinct
	// attack class (ID-token replay; state covers CSRF; PKCE covers
	// code-interception).
	NonceCookie = "atlas_oidc_nonce"

	// FlowCookieMaxAge is how long state/verifier cookies live. 10 minutes
	// is generous for a user to authenticate but short enough that an
	// abandoned tab does not leave persistent verifier material around.
	FlowCookieMaxAge = 10 * time.Minute
)

// IdpConfig is one OIDC IdP relationship — what we received at provisioning
// time. The platform may carry multiple per tenant.
type IdpConfig struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	Name                string
	IssuerURL           string
	ClientID            string
	ClientSecret        string
	RedirectURL         string
	AllowedEmailDomains []string
}

// IdpResolver is the per-request lookup the Authenticator needs: given a
// tenant + IdP name, return the config. The platform plugs in a DB-backed
// resolver in cmd/atlas; tests use a fake.
type IdpResolver interface {
	ResolveIdp(ctx context.Context, tenantID uuid.UUID, name string) (IdpConfig, error)
}

// ErrUnknownIdp is the sentinel for "no such IdP configured."
var ErrUnknownIdp = errors.New("oidc: unknown IdP")

// ErrStateMismatch is the CSRF guard's sentinel. The callback returns 400
// when this fires.
var ErrStateMismatch = errors.New("oidc: state mismatch (CSRF guard)")

// ErrNonceMismatch is the ID-token-replay guard's sentinel (slice 365).
// Fires when either the per-flow nonce cookie is missing on callback or
// the ID token's `nonce` claim does not match the cookie value. Kept
// distinct from ErrStateMismatch so audit-log review can tell a CSRF
// attempt apart from an ID-token replay attempt — operationally and
// forensically different signals.
var ErrNonceMismatch = errors.New("oidc: nonce mismatch (ID-token replay guard)")

// Authenticator drives the RP-side OIDC flow.
type Authenticator struct {
	resolver IdpResolver
	mu       sync.Mutex
	cache    map[string]*coreos.Provider // keyed by issuer URL
}

// New constructs an Authenticator over a per-tenant IdP resolver.
func New(resolver IdpResolver) *Authenticator {
	return &Authenticator{
		resolver: resolver,
		cache:    map[string]*coreos.Provider{},
	}
}

// LoginInput captures the per-flow inputs at /auth/oidc/login.
type LoginInput struct {
	TenantID uuid.UUID
	IdpName  string
}

// LoginResult is what the login handler returns to its caller: the URL to
// redirect the user to, plus the cookies to set on the response.
type LoginResult struct {
	AuthURL string
	Cookies []*http.Cookie
}

// BeginLogin generates state + PKCE, looks up the IdP, and returns the
// authorize URL + cookies to set. The handler issues a 302 to AuthURL.
func (a *Authenticator) BeginLogin(ctx context.Context, in LoginInput, secureCookies bool) (LoginResult, error) {
	cfg, err := a.resolver.ResolveIdp(ctx, in.TenantID, in.IdpName)
	if err != nil {
		return LoginResult{}, err
	}
	provider, err := a.provider(ctx, cfg.IssuerURL)
	if err != nil {
		return LoginResult{}, err
	}
	verifier := oauth2.GenerateVerifier()
	state, err := randomState()
	if err != nil {
		return LoginResult{}, err
	}
	// Slice 365 — per-flow OIDC nonce. 16-byte crypto/rand via the same
	// randomState() helper that backs state (P0-365-5: must use
	// crypto/rand). The same value is persisted in the NonceCookie and
	// sent on the authorize URL via coreos.Nonce(). The callback then
	// verifies the ID token's `nonce` claim matches the cookie.
	nonce, err := randomState()
	if err != nil {
		return LoginResult{}, err
	}
	oa := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{coreos.ScopeOpenID, "email", "profile"},
	}
	authURL := oa.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		coreos.Nonce(nonce),
	)

	cookies := []*http.Cookie{
		flowCookie(StateCookie, state, secureCookies),
		flowCookie(VerifierCookie, verifier, secureCookies),
		flowCookie(IdpCookie, in.IdpName, secureCookies),
		flowCookie(NonceCookie, nonce, secureCookies),
	}
	return LoginResult{AuthURL: authURL, Cookies: cookies}, nil
}

// CallbackResult is what the callback handler returns once the code has been
// exchanged and the ID token verified. The caller upserts the user and
// establishes a session.
type CallbackResult struct {
	TenantID    uuid.UUID
	Issuer      string
	Subject     string
	Email       string
	DisplayName string
	// IDPConfigID is the id of the oidc_idp_configs row this login flowed
	// through (slice 733). It scopes the slice-509 group-to-role derivation so a
	// tenant with multiple IdP configs maps each IdP's groups independently
	// (509 AC-6): the resolver matches mappings with idp_config_id IS NOT
	// DISTINCT FROM this value. uuid.Nil when the resolver supplied no id (e.g.
	// the local-mode resolver), in which case derivation matches the
	// NULL-source (IdP-agnostic) mappings.
	IDPConfigID uuid.UUID
	// Groups is the validated `groups` claim from the verified ID token (slice
	// 509). It is populated ONLY from a token that passed signature + issuer +
	// audience + nonce verification above, so it is a trusted group set the
	// group-to-role resolver may safely consume (P0-509-2: never derive roles
	// from an unvalidated token). Empty when the IdP emits no groups claim. The
	// caller passes it (with the login's idp_config_id) to grouprole.Resolver
	// to derive the user's group-mapped roles after the user is upserted.
	Groups []string
}

// HandleCallback verifies state, exchanges code, validates ID token, and
// returns the canonical user identifiers. Returns ErrStateMismatch on CSRF
// failure (400) and an opaque error on any other failure (502 / 401 — let
// the handler decide).
func (a *Authenticator) HandleCallback(ctx context.Context, r *http.Request, tenantID uuid.UUID) (CallbackResult, error) {
	stateCookie, err := r.Cookie(StateCookie)
	if err != nil {
		return CallbackResult{}, ErrStateMismatch
	}
	verifierCookie, err := r.Cookie(VerifierCookie)
	if err != nil {
		return CallbackResult{}, ErrStateMismatch
	}
	idpCookie, err := r.Cookie(IdpCookie)
	if err != nil {
		return CallbackResult{}, ErrStateMismatch
	}
	// Slice 365 — nonce cookie absence is treated as a replay attempt:
	// every legitimate flow set NonceCookie in BeginLogin. Distinguished
	// from ErrStateMismatch so audit-log review can tell a CSRF attempt
	// apart from an ID-token replay attempt (P0-365-4: no bypass path).
	nonceCookie, err := r.Cookie(NonceCookie)
	if err != nil {
		return CallbackResult{}, ErrNonceMismatch
	}
	if nonceCookie.Value == "" {
		return CallbackResult{}, ErrNonceMismatch
	}

	queryState := r.URL.Query().Get("state")
	queryCode := r.URL.Query().Get("code")
	if queryState == "" || queryCode == "" {
		return CallbackResult{}, ErrStateMismatch
	}
	if stateCookie.Value != queryState {
		return CallbackResult{}, ErrStateMismatch
	}

	cfg, err := a.resolver.ResolveIdp(ctx, tenantID, idpCookie.Value)
	if err != nil {
		return CallbackResult{}, err
	}
	provider, err := a.provider(ctx, cfg.IssuerURL)
	if err != nil {
		return CallbackResult{}, err
	}
	oa := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{coreos.ScopeOpenID, "email", "profile"},
	}
	tok, err := oa.Exchange(ctx, queryCode, oauth2.VerifierOption(verifierCookie.Value))
	if err != nil {
		return CallbackResult{}, fmt.Errorf("oidc: code exchange: %w", err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return CallbackResult{}, errors.New("oidc: no id_token in token response")
	}
	verifier := provider.Verifier(&coreos.Config{ClientID: cfg.ClientID})
	idTok, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("oidc: id_token verify: %w", err)
	}
	// Slice 365 — manual nonce-claim verification. go-oidc v3.18.0's
	// verifier.Verify explicitly does NOT check nonce
	// (verify.go:189 — "Verify does NOT do nonce validation, which is
	// the callers responsibility"); the library exposes the claim as
	// idTok.Nonce after parse but leaves the comparison to the RP. We
	// compare against the cookie value the RP persisted in BeginLogin.
	// Constant-time comparison would over-engineer this — the cookie
	// value is HttpOnly and the claim is a single equality on a
	// non-secret session-scoped identifier; the worst outcome of a
	// non-constant-time compare here is a side-channel that leaks
	// nothing useful (the attacker already controls the supplied nonce
	// in the forged ID token they're trying to replay). P0-365-3: do
	// NOT log either side of the comparison.
	if idTok.Nonce != nonceCookie.Value {
		return CallbackResult{}, ErrNonceMismatch
	}
	var claims struct {
		Email             string   `json:"email"`
		Name              string   `json:"name"`
		PreferredUsername string   `json:"preferred_username"`
		Groups            []string `json:"groups"`
	}
	if err := idTok.Claims(&claims); err != nil {
		return CallbackResult{}, fmt.Errorf("oidc: id_token claims: %w", err)
	}
	if claims.Email == "" {
		return CallbackResult{}, errors.New("oidc: id_token missing email")
	}
	if len(cfg.AllowedEmailDomains) > 0 {
		ok := false
		for _, d := range cfg.AllowedEmailDomains {
			if strings.HasSuffix(strings.ToLower(claims.Email), "@"+strings.ToLower(d)) {
				ok = true
				break
			}
		}
		if !ok {
			return CallbackResult{}, fmt.Errorf("oidc: email domain not in allowlist")
		}
	}
	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	if name == "" {
		name = claims.Email
	}
	return CallbackResult{
		TenantID:    tenantID,
		Issuer:      idTok.Issuer,
		Subject:     idTok.Subject,
		Email:       claims.Email,
		DisplayName: name,
		IDPConfigID: cfg.ID,
		Groups:      claims.Groups,
	}, nil
}

// ClearFlowCookies sets MaxAge=-1 on state/verifier/idp/nonce cookies.
// The login success handler calls this after establishing the session.
// Slice 365 added NonceCookie to the list.
func ClearFlowCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{StateCookie, VerifierCookie, IdpCookie, NonceCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/auth/oidc",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

// --- helpers ---

func (a *Authenticator) provider(ctx context.Context, issuer string) (*coreos.Provider, error) {
	a.mu.Lock()
	if p, ok := a.cache[issuer]; ok {
		a.mu.Unlock()
		return p, nil
	}
	a.mu.Unlock()
	p, err := coreos.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover %s: %w", issuer, err)
	}
	a.mu.Lock()
	a.cache[issuer] = p
	a.mu.Unlock()
	return p, nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oidc: random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func flowCookie(name, value string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/auth/oidc",
		MaxAge:   int(FlowCookieMaxAge / time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}
