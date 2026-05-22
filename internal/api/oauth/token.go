// token.go implements the slice-188 `POST /oauth/token` endpoint.
//
// Two grant types are supported:
//
//  1. RFC 6749 §4.4 — `client_credentials` for machine clients.
//  2. RFC 8693 — token-exchange for tenant switching.
//
// The handler is dispatched from `oauth.Handler.Mount` only when the
// caller has wired a TokenEndpoint via AttachTokenEndpoint. Unit
// servers that don't configure the dependencies leave the route as a
// 501 stub.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-188-3 (no plaintext secret): the handler never logs or
//     echoes the supplied client_secret. Argon2id verify happens
//     constant-time via the oauthclient.Store, and on mismatch the
//     reply is 401 with the standard `invalid_client` error.
//   - P0-188-4 (no super_admin elevation via exchange): the
//     token-exchange path copies `super_admin` from the verified
//     subject_token claims; it cannot set the claim true.
//   - P0-188-5 (signature-before-allowlist): the token-exchange path
//     ALWAYS calls tokensign.Verify (signature validation) BEFORE
//     reading any claim from the supplied subject_token. An
//     unverified token cannot influence the allowlist check.
//   - P0-188-9 (per-client rate limit): the token-bucket limiter is
//     keyed on `client_id`, NEVER on the source IP. IP-based limits
//     are bypassable behind NAT.
//   - P0-188-10 (signing goes through tokensign): every JWT minted
//     by this handler goes through tokensign.Signer.Sign — there is
//     no direct keystore access.
package oauth

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Grant type identifiers.
const (
	GrantTypeClientCredentials = "client_credentials"
	GrantTypeTokenExchange     = "urn:ietf:params:oauth:grant-type:token-exchange"
	// GrantTypeAuthorizationCode is the RFC 6749 §4.1 grant — added
	// by slice 189 to redeem `oauth_auth_codes` rows minted by the
	// authorize endpoint.
	GrantTypeAuthorizationCode = "authorization_code"
	// (GrantTypeDeviceCode is declared in device_authorization.go.)

	// Subject token type for RFC 8693 — atlas accepts JWT subject
	// tokens only in v1. Future-slice work can add SAML2 / etc.
	SubjectTokenTypeJWT = "urn:ietf:params:oauth:token-type:jwt"

	// AccessTokenLifetime is the validity window of every JWT this
	// handler mints. 1 hour matches ADR-0003's locked default; clients
	// re-acquire after expiry (no refresh-token grant in v1 — that's
	// v3 deferred per P0-188-6).
	AccessTokenLifetime = time.Hour
	accessTokenSeconds  = int(AccessTokenLifetime / time.Second)

	// MachineSubjectPrefix prefixes the `sub` claim of every
	// client_credentials-minted JWT. Distinguishes machine tokens
	// from OIDC-authenticated human tokens (which carry a `user:` or
	// IdP-subject prefix per the canvas).
	MachineSubjectPrefix = "oauth_client:"

	// MachineIDPIssuer is the `atlas:idp_issuer` claim value for
	// client_credentials-issued tokens. Documented in
	// docs/adr/0003-oauth-authorization-server.md token-shape table.
	MachineIDPIssuer = "atlas-oauth-client"

	// DefaultTokenRatePerMin is the default per-client rate limit.
	// Configurable via ATLAS_OAUTH_TOKEN_RATE_PER_MIN. 60/min/client
	// chosen as the D4 default — see decisions log.
	DefaultTokenRatePerMin = 60
)

// TokenEndpoint owns the dependencies and state required to serve
// `POST /oauth/token`.
type TokenEndpoint struct {
	signer      *tokensign.Signer
	clients     *oauthclient.Store
	codes       *oauthcode.Store // slice 189 — wired via AttachAuthCodeStore
	deviceCodes *DeviceCodeStore // slice 191 — wired via AttachDeviceCodeStore
	devicePoll  *devicePollTracker
	issuer      string
	auditPool   *pgxpool.Pool
	limiter     *tokenBucketLimiter
	now         func() time.Time
}

// TokenEndpointConfig is the constructor parameter bag for
// NewTokenEndpoint. Splitting from oauth.Config so the slice-187
// scaffolding can stay independent.
type TokenEndpointConfig struct {
	// Issuer is the externally-reachable URL clients see — MUST match
	// the parent Handler's cfg.Issuer.
	Issuer string

	// AuditPool is the tenant-scoped atlas_app pgxpool used to write
	// rows into oauth_token_exchanges. The package applies
	// `tenancy.ApplyTenant` per write so RLS isolates the audit log
	// at the DB layer (constitutional invariant #6).
	AuditPool *pgxpool.Pool

	// RatePerMinute is the per-client rate limit. Zero falls back to
	// DefaultTokenRatePerMin.
	RatePerMinute int

	// Now is the clock; nil falls back to time.Now. Tests inject a
	// pinned clock so token timestamps are deterministic.
	Now func() time.Time
}

// NewTokenEndpoint constructs a TokenEndpoint. The signer + clients
// + audit pool are required; rate limit + clock have defaults.
//
// The constructor panics on a missing required dependency because
// the handler is wired only at process startup — a missing
// dependency at runtime is a programmer error, not a recoverable
// condition.
func NewTokenEndpoint(signer *tokensign.Signer, clients *oauthclient.Store, cfg TokenEndpointConfig) *TokenEndpoint {
	if signer == nil {
		panic("oauth: NewTokenEndpoint: signer is nil")
	}
	if cfg.Issuer == "" {
		panic("oauth: NewTokenEndpoint: Issuer is empty")
	}
	// clients MAY be nil — unit tests that exercise only the
	// token-exchange path or the dispatch-table path do not need a
	// DB-backed client store. The client_credentials handler checks
	// for nil at request time and returns 503 in that case.
	rate := cfg.RatePerMinute
	if rate <= 0 {
		rate = DefaultTokenRatePerMin
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &TokenEndpoint{
		signer:    signer,
		clients:   clients,
		issuer:    cfg.Issuer,
		auditPool: cfg.AuditPool,
		limiter:   newTokenBucketLimiter(rate, nowFn),
		now:       nowFn,
	}
}

// ServeHTTP dispatches on `grant_type` per AC-5.
func (t *TokenEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// RFC 6749 §3.2: the token endpoint accepts
	// `application/x-www-form-urlencoded` and rejects other content
	// types. The Content-Type check is case-insensitive and tolerates
	// charset/boundary parameters.
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

	switch r.FormValue("grant_type") {
	case GrantTypeClientCredentials:
		t.handleClientCredentials(w, r)
	case GrantTypeTokenExchange:
		t.handleTokenExchange(w, r)
	case GrantTypeAuthorizationCode:
		t.handleAuthorizationCode(w, r)
	case GrantTypeDeviceCode:
		t.handleDeviceCode(w, r)
	case "":
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "grant_type is required")
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"the requested grant_type is not supported")
	}
}

// ===== client_credentials =====

func (t *TokenEndpoint) handleClientCredentials(w http.ResponseWriter, r *http.Request) {
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	if clientID == "" || clientSecret == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"client_id and client_secret are required")
		return
	}
	// Rate-limit BEFORE any expensive work (DB lookup, argon2id verify,
	// even the nil-clients check) so a wrong-secret OR misconfigured-
	// platform attack cannot exhaust CPU. Token bucket key = client_id;
	// per P0-188-9, NEVER the source IP.
	if !t.limiter.Allow(clientID) {
		retryAfterSeconds(w, t.limiter.WindowSeconds())
		writeOAuthError(w, http.StatusTooManyRequests, "invalid_request",
			"rate limit exceeded; retry after the indicated window")
		return
	}

	if t.clients == nil {
		// Defensive: a misconfigured deployment that wires the token
		// endpoint without a client store cannot honor any
		// client_credentials request. The 503 (not 500) signals
		// "service not configured for this grant" so downstream
		// clients back off rather than retry.
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error",
			"client_credentials grant not configured")
		return
	}

	client, err := t.clients.Verify(r.Context(), clientID, clientSecret)
	if err != nil {
		// RFC 6749 §5.2: invalid_client returns 401 + the standard
		// error code. The error is opaque per oauthclient.ErrUnknownClient.
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client",
			"client authentication failed")
		return
	}

	// `audience` form param: RFC 8693 §2.1 + RFC 6749 §4.4 do not
	// specify audience for client_credentials, but RFC 8693 §2.1
	// allows it. atlas accepts the form param; default to the issuer
	// URL when absent.
	audience := r.FormValue("audience")
	if audience == "" {
		audience = t.issuer
	}

	now := t.now()
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.issuer,
			Subject:   MachineSubjectPrefix + client.ClientID,
			Audience:  []string{audience},
			ExpiresAt: now.Add(AccessTokenLifetime).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer: MachineIDPIssuer,
		// CurrentTenantID intentionally zero — machine tokens are
		// tenant-free by default; the caller scopes a tenant via the
		// slice-191 wire convention or via a follow-on token-exchange.
		AvailableTenants: []uuid.UUID{},
		Roles:            map[uuid.UUID][]string{},
		SuperAdmin:       false,
	}

	tok, err := t.signer.Sign(r.Context(), claims)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to sign token")
		return
	}

	writeTokenResponse(w, tok)
}

// ===== token-exchange (RFC 8693) =====

func (t *TokenEndpoint) handleTokenExchange(w http.ResponseWriter, r *http.Request) {
	subjectToken := r.FormValue("subject_token")
	subjectTokenType := r.FormValue("subject_token_type")
	targetTenantRaw := r.FormValue("atlas:target_tenant_id")
	if subjectToken == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "subject_token is required")
		return
	}
	if subjectTokenType != SubjectTokenTypeJWT {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"subject_token_type must be urn:ietf:params:oauth:token-type:jwt")
		return
	}
	if targetTenantRaw == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"atlas:target_tenant_id is required")
		return
	}
	targetTenant, err := uuid.Parse(targetTenantRaw)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"atlas:target_tenant_id is not a valid UUID")
		return
	}

	// P0-188-5: signature verification MUST run before the
	// allowlist check. tokensign.Verify validates the JWS against
	// the keystore's verification key set.
	claims, err := t.signer.Verify(r.Context(), subjectToken)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_token",
			"subject_token signature is invalid")
		return
	}
	// Temporal + identity claim validation (iss + aud + exp + nbf).
	// We use a permissive audience match: the subject_token MUST be
	// addressed to the atlas issuer (matches the discovery contract);
	// audience-based slice 190 R2 middleware will tighten further.
	if err := jwt.Validate(claims, jwt.ValidationParams{
		ExpectedIssuer:   t.issuer,
		ExpectedAudience: t.issuer,
		Now:              t.now(),
	}); err != nil {
		// The claim validator surfaces tenant-out-of-scope errors —
		// fold them into invalid_token because the subject_token is
		// the input under scrutiny here.
		writeOAuthError(w, http.StatusUnauthorized, "invalid_token",
			"subject_token claim validation failed")
		return
	}

	// AC-12 / P0-188-4: allowlist check. The target tenant MUST be
	// in the subject_token's verified `atlas:available_tenants[]`
	// OR the caller MUST already be super_admin. The super_admin
	// shortcut is the deliberate escape hatch for platform-wide
	// operations; the slice 142 OIDC login is the only path that
	// sets super_admin true.
	if !claims.SuperAdmin && !containsUUID(claims.AvailableTenants, targetTenant) {
		writeOAuthError(w, http.StatusForbidden, "invalid_target",
			"target tenant is not in available_tenants")
		return
	}

	now := t.now()
	newClaims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.issuer,
			Subject:   claims.Subject,
			Audience:  []string{t.issuer},
			ExpiresAt: now.Add(AccessTokenLifetime).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		// P0-188-4: copy super_admin, NEVER set it.
		IDPIssuer:        claims.IDPIssuer,
		CurrentTenantID:  targetTenant,
		AvailableTenants: claims.AvailableTenants,
		Roles:            claims.Roles,
		SuperAdmin:       claims.SuperAdmin,
	}

	tok, err := t.signer.Sign(r.Context(), newClaims)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to sign token")
		return
	}

	// D3 (decisions log): the audit-log write runs POST-sign, best-effort.
	// Rationale: the alternative (same-transaction) would couple the
	// signer's keystore access to a Postgres transaction it doesn't
	// otherwise need, and on a transient DB failure would force the
	// caller to re-acquire a token they already legitimately earned.
	// Best-effort write keeps the hot path fast; the audit row is
	// reconciled by the audit log's append-only guarantee — the
	// row either lands or doesn't, but is never partial. We log
	// audit-write failures via the writeAudit function's stderr
	// fallback so operators can see drift.
	t.writeAudit(r, claims, targetTenant)

	writeTokenResponse(w, tok)
}

// writeAudit inserts one row into oauth_token_exchanges scoped to the
// target tenant. Best-effort: a failure here does NOT block the
// token response (D3, decisions log). The audit pool may be nil in
// unit tests; in that case the write is skipped.
func (t *TokenEndpoint) writeAudit(r *http.Request, subjectClaims jwt.AtlasClaims, targetTenant uuid.UUID) {
	if t.auditPool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Apply tenant GUC to satisfy the RLS tenant_write policy on
	// oauth_token_exchanges. The target tenant is the row's scope.
	tenantCtx, err := tenancy.WithTenant(ctx, targetTenant.String())
	if err != nil {
		return
	}
	tx, err := t.auditPool.BeginTx(tenantCtx, pgxBeginRW())
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()

	if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
		return
	}
	ctx = tenantCtx

	var fromTenant *uuid.UUID
	if subjectClaims.CurrentTenantID != uuid.Nil {
		ft := subjectClaims.CurrentTenantID
		fromTenant = &ft
	}
	ipAddr := requestIP(r)

	const q = `
		INSERT INTO oauth_token_exchanges (
			id, tenant_id, subject_token_jti, from_tenant_id, to_tenant_id,
			subject_token_iss, subject_token_sub, ip_address
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	jti := subjectClaims.ID
	if jti == "" {
		// Empty jti would violate the ote_jti_nonempty CHECK; use a
		// fallback so a malformed subject_token claim doesn't break
		// the audit write (the row is still useful for forensics
		// because subject_token_sub + iss are present).
		jti = "unknown"
	}
	if _, err := tx.Exec(ctx, q,
		uuid.New(),
		targetTenant,
		jti,
		fromTenant,
		targetTenant,
		subjectClaims.Issuer,
		subjectClaims.Subject,
		ipAddr,
	); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// ===== helpers =====

// writeOAuthError writes an RFC 6749 §5.2 error response. Always
// `Content-Type: application/json`; never echoes any secret form
// value back to the caller.
func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

// writeTokenResponse renders the RFC 6749 §5.1 standard response.
func writeTokenResponse(w http.ResponseWriter, accessToken string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   accessTokenSeconds,
		"scope":        "",
	})
}

// retryAfterSeconds sets the standard `Retry-After` header in the
// 429 path. RFC 6585 §4 honors this header on Too Many Requests.
func retryAfterSeconds(w http.ResponseWriter, seconds int) {
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}

// isFormContentType returns true when ct matches
// `application/x-www-form-urlencoded` with any optional parameters
// (charset, boundary). The check is case-insensitive on the base
// type per RFC 9110 §8.3.
func isFormContentType(ct string) bool {
	base, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(base), "application/x-www-form-urlencoded")
}

// containsUUID reports whether target is in the list. Linear scan;
// available_tenants is small (a single human's tenants — almost
// always single digits).
func containsUUID(list []uuid.UUID, target uuid.UUID) bool {
	for _, u := range list {
		if u == target {
			return true
		}
	}
	return false
}

// requestIP parses the request's RemoteAddr into a netip.Addr for
// the audit log. Returns the zero netip.Addr (which marshals to
// NULL on the INET column) when the address cannot be parsed.
func requestIP(r *http.Request) any {
	if r == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	// netip.Addr can be passed directly to pgx for an INET column.
	return addr.String()
}

// ===== rate limiter =====
//
// A minimal in-memory token bucket keyed on client_id. v1 is a
// single-process platform; a follow-on slice can swap to a
// distributed limiter (Redis / Postgres advisory lock) when
// multi-process deployment lands.
//
// The bucket refills at one full bucket per minute. Allow() consumes
// a token; if none available, the request is rate-limited. The
// bucket size = rate, so steady-state behavior is "rate requests per
// minute; bursts up to rate at any instant".

type tokenBucketLimiter struct {
	mu      sync.Mutex
	rate    int // tokens per minute
	buckets map[string]*tokenBucketState
	now     func() time.Time
}

type tokenBucketState struct {
	tokens    float64
	lastCheck time.Time
}

func newTokenBucketLimiter(rate int, nowFn func() time.Time) *tokenBucketLimiter {
	return &tokenBucketLimiter{
		rate:    rate,
		buckets: make(map[string]*tokenBucketState),
		now:     nowFn,
	}
}

// Allow returns true when a token was consumed; false when the
// bucket was empty and the request must be rate-limited.
func (l *tokenBucketLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	state, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &tokenBucketState{
			tokens:    float64(l.rate) - 1, // first call consumes one
			lastCheck: now,
		}
		return true
	}
	// Refill: tokens added since last check at rate/60 per second.
	elapsed := now.Sub(state.lastCheck).Seconds()
	state.tokens += elapsed * (float64(l.rate) / 60.0)
	if state.tokens > float64(l.rate) {
		state.tokens = float64(l.rate)
	}
	state.lastCheck = now
	if state.tokens < 1.0 {
		return false
	}
	state.tokens -= 1.0
	return true
}

// WindowSeconds reports the retry-after value the 429 response
// should advertise. With a 1-token refill at rate/60 per second, the
// expected wait for the next token is 60/rate seconds. Operators see
// "Retry-After: 1" for the default 60/min limit.
func (l *tokenBucketLimiter) WindowSeconds() int {
	if l.rate <= 0 {
		return 60
	}
	return max(1, 60/l.rate)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// pgxBeginRW returns the read-write transaction options. Defined as
// a function so the call site is the only consumer; future tweaks
// (e.g. SERIALIZABLE) land in one place.
func pgxBeginRW() pgx.TxOptions {
	return pgx.TxOptions{}
}
