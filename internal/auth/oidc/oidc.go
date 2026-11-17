// Package oidc is the security-atlas OIDC Relying Party.
//
// We are never an IdP. The platform initiates the OAuth 2.0 + OIDC code flow
// with PKCE against a per-tenant IdP config, validates the ID token, and
// upserts a user keyed on (idp_issuer, idp_subject).
//
// State + PKCE protection lives in short-lived flow cookies; the callback
// handler verifies that state cookie matches the query param (CSRF guard)
// and that the state's HMAC binds the flow's tenant, IdP and nonce (#2177)
// before exchanging the code. With secure cookies the flow cookies carry
// the __Host- prefix, so a sibling subdomain cannot plant them. The state
// also carries its issue time under the MAC, so the server refuses a flow
// older than FlowCookieMaxAge even where the browser kept its cookies
// (#2298).
//
// Library: github.com/coreos/go-oidc/v3 + golang.org/x/oauth2.
package oidc

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
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

	// TenantCookie holds the tenant id /auth/oidc/login resolved (#1968).
	// The callback refuses a tenant that differs from it, so the tenant is
	// bound to the flow: a tenant change between login and callback (a
	// second tenant created on a single-tenant install, or a hand-edited
	// tenant_id) cannot move the login into another tenant.
	TenantCookie = "atlas_oidc_tenant"

	// FlowCookieMaxAge is how long state/verifier cookies live. 10 minutes
	// is generous for a user to authenticate but short enough that an
	// abandoned tab does not leave persistent verifier material around.
	FlowCookieMaxAge = 10 * time.Minute

	// HostPrefix is prepended to every flow cookie name when secure cookies
	// are on (#2177). Browsers accept a __Host- cookie only when it is
	// Secure, has Path=/ and no Domain, so a sibling subdomain cannot set
	// or overwrite one. Plain-HTTP dev (secureCookies=false) cannot use the
	// prefix and keeps the bare names scoped to FlowCookiePath; the state
	// HMAC still binds the flow there.
	HostPrefix = "__Host-"

	// FlowCookiePath scopes the unprefixed flow cookies of a plain-HTTP
	// deployment. __Host- cookies must use Path=/.
	FlowCookiePath = "/auth/oidc"

	// StateKeyEnv names the optional dedicated key for the state HMAC
	// (#2177). When unset, the key is derived from BEARER_HASH_KEY, which
	// every replica already shares. Either way it is never per-process, so
	// a callback that lands on another replica verifies.
	StateKeyEnv = "ATLAS_OIDC_STATE_KEY"

	// StateKeyMinBytes is the shortest state HMAC key accepted.
	StateKeyMinBytes = 32

	// StateClockSkew is the replica clock difference the state issue time
	// tolerates (#2298): a state is refused once it is older than
	// FlowCookieMaxAge plus this, or dated more than this in the future.
	StateClockSkew = 30 * time.Second

	// ProviderCacheTTL bounds how long the RP serves a discovered IdP provider
	// before re-running OIDC discovery. Slice 335's chaos design expected new
	// logins to observe IdP outage/recovery within roughly this window.
	ProviderCacheTTL = 30 * time.Second
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
	// TrustUnverifiedEmailForLink is oidc_idp_configs.trust_unverified_email_for_link
	// (#1944 D3). When true, a sign-in whose ID token omits email_verified
	// may link to a SCIM-provisioned user by email; false (the default)
	// links only on email_verified=true.
	TrustUnverifiedEmailForLink bool
}

// IdpResolver is the per-request lookup the Authenticator needs: given a
// tenant + IdP name, return the config. The platform plugs in a DB-backed
// resolver in cmd/atlas; tests use a fake.
type IdpResolver interface {
	ResolveIdp(ctx context.Context, tenantID uuid.UUID, name string) (IdpConfig, error)
}

// ErrUnknownIdp is the sentinel for "no such IdP configured."
var ErrUnknownIdp = errors.New("oidc: unknown IdP")

// ErrProviderUnavailable is the sentinel for a configured IdP whose discovery
// endpoint cannot be reached or does not answer in time.
var ErrProviderUnavailable = errors.New("oidc: provider unavailable")

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

// ErrTenantMismatch is the flow-binding guard's sentinel (#1968): the
// callback's tenant is not the one /auth/oidc/login bound the flow to, or
// the tenant cookie is missing. The callback returns 400 when it fires.
var ErrTenantMismatch = errors.New("oidc: tenant does not match the login flow")

// ErrStateKeyInvalid is returned by BeginLogin and HandleCallback when the
// Authenticator was built without a state HMAC key of at least
// StateKeyMinBytes. The flow fails closed rather than issuing an unsigned
// state.
var ErrStateKeyInvalid = fmt.Errorf("oidc: state HMAC key must be at least %d bytes", StateKeyMinBytes)

// Authenticator drives the RP-side OIDC flow.
type Authenticator struct {
	resolver         IdpResolver
	stateKey         []byte
	discoveryTimeout time.Duration
	mu               sync.Mutex
	cache            map[string]cachedProvider // keyed by issuer URL
	providerCacheTTL time.Duration
	now              func() time.Time
}

type cachedProvider struct {
	provider     *coreos.Provider
	discoveredAt time.Time
}

const defaultDiscoveryTimeout = 5 * time.Second

// New constructs an Authenticator over a per-tenant IdP resolver. stateKey
// signs the flow state (#2177); it must be at least StateKeyMinBytes and the
// same on every replica (see StateKeyFromEnv). The key is borrowed, not
// copied.
func New(resolver IdpResolver, stateKey []byte) *Authenticator {
	return NewWithDiscoveryTimeout(resolver, stateKey, defaultDiscoveryTimeout)
}

// NewWithDiscoveryTimeout constructs an Authenticator with an explicit OIDC
// discovery timeout. Tests use this to prove the outage path is bounded without
// waiting on the production timeout.
func NewWithDiscoveryTimeout(resolver IdpResolver, stateKey []byte, discoveryTimeout time.Duration) *Authenticator {
	if discoveryTimeout <= 0 {
		discoveryTimeout = defaultDiscoveryTimeout
	}
	return &Authenticator{
		resolver:         resolver,
		stateKey:         stateKey,
		discoveryTimeout: discoveryTimeout,
		cache:            map[string]cachedProvider{},
		providerCacheTTL: ProviderCacheTTL,
		now:              time.Now,
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
	if len(a.stateKey) < StateKeyMinBytes {
		return LoginResult{}, ErrStateKeyInvalid
	}
	cfg, err := a.resolver.ResolveIdp(ctx, in.TenantID, in.IdpName)
	if err != nil {
		return LoginResult{}, err
	}
	provider, err := a.provider(ctx, cfg.IssuerURL)
	if err != nil {
		return LoginResult{}, err
	}
	verifier := oauth2.GenerateVerifier()
	// Slice 365 — per-flow OIDC nonce. 16-byte crypto/rand via the same
	// randomState() helper that backs state (P0-365-5: must use
	// crypto/rand). The same value is persisted in the NonceCookie and
	// sent on the authorize URL via coreos.Nonce(). The callback then
	// verifies the ID token's `nonce` claim matches the cookie.
	nonce, err := randomState()
	if err != nil {
		return LoginResult{}, err
	}
	// #2177: state = rand "." iat "." HMAC(k, rand|iat|tenant|idp|nonce),
	// so the callback can tell a flow it issued from cookies planted by
	// hand. #2298: iat (unix seconds) lets the server expire the flow
	// itself rather than trust the browser to drop the cookies.
	stateRand, err := randomState()
	if err != nil {
		return LoginResult{}, err
	}
	iat := strconv.FormatInt(a.now().Unix(), 10)
	state := stateRand + "." + iat + "." + a.stateMAC(stateRand, iat, in.TenantID.String(), in.IdpName, nonce)
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
		flowCookie(TenantCookie, in.TenantID.String(), secureCookies),
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
	// EmailVerified is the ID token's email_verified claim, nil when the IdP
	// omits it. It gates linking a sign-in to an existing user by email
	// (#1944).
	EmailVerified *bool
	// TrustUnverifiedEmailForLink is the IdP config's opt-out letting an
	// absent EmailVerified link (#1944 D3).
	TrustUnverifiedEmailForLink bool
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
// returns the canonical user identifiers. secureCookies must match the
// value BeginLogin ran with: it selects the __Host- cookie names. Returns
// ErrStateMismatch on CSRF failure (400) and an opaque error on any other
// failure (502 / 401 — let the handler decide).
func (a *Authenticator) HandleCallback(ctx context.Context, r *http.Request, tenantID uuid.UUID, secureCookies bool) (CallbackResult, error) {
	if len(a.stateKey) < StateKeyMinBytes {
		return CallbackResult{}, ErrStateKeyInvalid
	}
	stateCookie, err := r.Cookie(CookieName(StateCookie, secureCookies))
	if err != nil {
		return CallbackResult{}, ErrStateMismatch
	}
	verifierCookie, err := r.Cookie(CookieName(VerifierCookie, secureCookies))
	if err != nil {
		return CallbackResult{}, ErrStateMismatch
	}
	idpCookie, err := r.Cookie(CookieName(IdpCookie, secureCookies))
	if err != nil {
		return CallbackResult{}, ErrStateMismatch
	}
	// Slice 365 — nonce cookie absence is treated as a replay attempt:
	// every legitimate flow set NonceCookie in BeginLogin. Distinguished
	// from ErrStateMismatch so audit-log review can tell a CSRF attempt
	// apart from an ID-token replay attempt (P0-365-4: no bypass path).
	nonceCookie, err := r.Cookie(CookieName(NonceCookie, secureCookies))
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
	// #1968: the tenant is bound to the flow by the cookie BeginLogin set.
	tenantCookie, err := r.Cookie(CookieName(TenantCookie, secureCookies))
	if err != nil {
		return CallbackResult{}, ErrTenantMismatch
	}
	if flowTenant, perr := uuid.Parse(tenantCookie.Value); perr != nil || flowTenant != tenantID {
		return CallbackResult{}, ErrTenantMismatch
	}
	// #2177: the state's HMAC must cover the tenant, IdP and nonce cookies
	// as they arrived. A state the server did not issue, or flow cookies
	// mixed from two flows, fails here before any IdP lookup. #2298: so
	// does a state issued more than FlowCookieMaxAge ago.
	if !a.verifyState(queryState, tenantCookie.Value, idpCookie.Value, nonceCookie.Value) {
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
		EmailVerified     any      `json:"email_verified"`
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
		TenantID:      tenantID,
		Issuer:        idTok.Issuer,
		Subject:       idTok.Subject,
		Email:         claims.Email,
		DisplayName:   name,
		EmailVerified: parseEmailVerified(claims.EmailVerified),
		IDPConfigID:   cfg.ID,
		Groups:        claims.Groups,

		TrustUnverifiedEmailForLink: cfg.TrustUnverifiedEmailForLink,
	}, nil
}

// parseEmailVerified reads the email_verified claim. OIDC Core types it as a
// boolean, but some IdPs send the string "true"/"false". Absent or any other
// shape is nil (unknown), so only an explicit false reads as unverified.
func parseEmailVerified(v any) *bool {
	switch t := v.(type) {
	case bool:
		return &t
	case string:
		b, err := strconv.ParseBool(t)
		if err != nil {
			return nil
		}
		return &b
	}
	return nil
}

// ClearFlowCookies sets MaxAge=-1 on state/verifier/idp/nonce/tenant
// cookies, under the names and path BeginLogin used for the same secure
// setting. The callback calls this after establishing the session and on
// every refusal that ends the flow. Slice 365 added NonceCookie to the
// list, #1968 TenantCookie.
func ClearFlowCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{StateCookie, VerifierCookie, IdpCookie, NonceCookie, TenantCookie} {
		c := flowCookie(name, "", secure)
		c.MaxAge = -1
		http.SetCookie(w, c)
	}
}

// CookieName is the name a flow cookie is set and read under: base with
// the __Host- prefix when secure cookies are on, bare otherwise (#2177).
func CookieName(base string, secure bool) string {
	if secure {
		return HostPrefix + base
	}
	return base
}

// stateKeyInfo is the HKDF info label of the derived state key. v1 was
// HMAC(root, label), which is a bearer.Hash value; v2 (#2298) is HKDF.
const stateKeyInfo = "atlas-oidc-state-key-v2"

// DeriveStateKey derives the 32-byte state HMAC key from a root server
// secret (BEARER_HASH_KEY) with HKDF-SHA256: extract, then expand with
// stateKeyInfo. The extract step keys the final HMAC with a pseudorandom
// key instead of the root, so the derived key is not HMAC(root, x) for any
// x, and no bearer-token hash, HMAC(root, token), can equal it.
func DeriveStateKey(root []byte) []byte {
	k, err := hkdf.Key(sha256.New, root, nil, stateKeyInfo, 32)
	if err != nil {
		// hkdf.Key errors only past 255 hash blocks; 32 bytes is one.
		panic("oidc: hkdf: " + err.Error())
	}
	return k
}

// StateKeyFromEnv returns the state HMAC key: ATLAS_OIDC_STATE_KEY when
// set (at least StateKeyMinBytes), otherwise DeriveStateKey(root). root is
// the BEARER_HASH_KEY the server already refuses to boot without. A set but
// short ATLAS_OIDC_STATE_KEY is an error, never a silent fallback.
func StateKeyFromEnv(root []byte) ([]byte, error) {
	if v := os.Getenv(StateKeyEnv); v != "" {
		if len(v) < StateKeyMinBytes {
			return nil, fmt.Errorf("%s must be at least %d bytes", StateKeyEnv, StateKeyMinBytes)
		}
		return []byte(v), nil
	}
	if len(root) < StateKeyMinBytes {
		return nil, ErrStateKeyInvalid
	}
	return DeriveStateKey(root), nil
}

// --- helpers ---

func (a *Authenticator) provider(ctx context.Context, issuer string) (*coreos.Provider, error) {
	now := a.now()
	a.mu.Lock()
	entry, ok := a.cache[issuer]
	if ok && now.Sub(entry.discoveredAt) < a.providerCacheTTL {
		a.mu.Unlock()
		return entry.provider, nil
	}
	a.mu.Unlock()
	discoveryCtx, cancel := context.WithTimeout(ctx, a.discoveryTimeout)
	defer cancel()
	p, err := coreos.NewProvider(discoveryCtx, issuer)
	if err != nil {
		a.mu.Lock()
		delete(a.cache, issuer)
		a.mu.Unlock()
		return nil, ErrProviderUnavailable
	}
	a.mu.Lock()
	a.cache[issuer] = cachedProvider{
		provider:     p,
		discoveredAt: a.now(),
	}
	a.mu.Unlock()
	return p, nil
}

// stateMAC is HMAC-SHA256 over the length-prefixed state random, issue
// time, tenant, IdP name and nonce, base64url-encoded. Length prefixes keep
// two field lists from encoding to the same bytes. The v2 label (#2298)
// keeps a v1 MAC, which had no issue time, from verifying.
func (a *Authenticator) stateMAC(fields ...string) string {
	m := hmac.New(sha256.New, a.stateKey)
	m.Write([]byte("atlas-oidc-state-v2"))
	var n [8]byte
	for _, f := range fields {
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		m.Write(n[:])
		m.Write([]byte(f))
	}
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// verifyState checks state = rand "." iat "." stateMAC(rand, iat, tenant,
// idp, nonce) in constant time, and only then reads iat: the state must
// have been issued no more than FlowCookieMaxAge ago and not in the
// future, each give or take StateClockSkew (#2298). A two-part state from
// before #2298 has no iat and is refused.
func (a *Authenticator) verifyState(state, tenant, idp, nonce string) bool {
	parts := strings.Split(state, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return false
	}
	stateRand, iat, mac := parts[0], parts[1], parts[2]
	want := a.stateMAC(stateRand, iat, tenant, idp, nonce)
	if !hmac.Equal([]byte(mac), []byte(want)) {
		return false
	}
	issued, err := strconv.ParseInt(iat, 10, 64)
	if err != nil {
		return false
	}
	age := a.now().Sub(time.Unix(issued, 0))
	return age >= -StateClockSkew && age <= FlowCookieMaxAge+StateClockSkew
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oidc: random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func flowCookie(name, value string, secure bool) *http.Cookie {
	path := FlowCookiePath
	if secure {
		path = "/"
	}
	return &http.Cookie{
		Name:     CookieName(name, secure),
		Value:    value,
		Path:     path,
		MaxAge:   int(FlowCookieMaxAge / time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}
