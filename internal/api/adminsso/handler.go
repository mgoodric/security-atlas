// Package adminsso is the HTTP surface for /v1/admin/sso (slice 062).
//
// Two routes:
//
//	GET   /v1/admin/sso              -- returns the tenant's primary IdP config
//	                                    (sans client_secret); 404 when unset
//	PATCH /v1/admin/sso              -- upserts the primary IdP config
//	POST  /v1/admin/sso/preflight    -- server-side fetch of the IdP
//	                                    .well-known/openid-configuration
//	                                    (no state change)
//
// All three require an admin credential (cred.IsAdmin) -- the slice 035
// OPA RBAC middleware also gates the path, this handler does
// defense-in-depth.
//
// Anti-criteria honored (P0):
//
//   - client_secret is NEVER returned in any GET response. PATCH seals it
//     with idpconfig.SecretCrypter (AES-256-GCM bound to tenant + name,
//     #2034) before it reaches client_secret_enc; we never read it back to
//     the wire.
//   - PATCH treats an empty client_secret field as "leave existing" -- the
//     UI cannot accidentally clear the secret by submitting an empty form.
//   - Preflight is SSRF-hardened: scheme must be https, host must not
//     resolve to a loopback or RFC1918 address, content-length cap of
//     64 KiB, 5-second timeout.
//
// Constitutional invariants honored:
//
//   - Invariant 6 (RLS): all DB access goes through the tenancy-applied
//     transaction; oidc_idp_configs.tenant_id RLS policies fire.
//   - Slice 033 D1: no tenant_id in any request body. The handler reads
//     tenant from the credential, never from the wire.
//
// v1 single-config model: the handler surfaces one IdP per tenant by
// convention, keyed by name='primary'. Multi-IdP support is a v2
// conversation; the underlying table already supports many rows per
// tenant.
package adminsso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/idpconfig"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/netguard"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// configName is the single v1 IdP name surfaced by the admin API. Multi-IdP
// support uses additional names; v1 hardcodes "primary".
const configName = "primary"

// Handler owns the SSO admin routes. The pgx pool is the tenancy-aware
// connection pool; every request opens a transaction, applies the tenant
// GUC, and runs the query through sqlc-generated code.
type Handler struct {
	pool          *pgxpool.Pool
	preflightOpts PreflightOptions
	// crypter seals the client secret before it is stored (#2034). nil
	// means the deployment set no ATLAS_OIDC_SECRET_KEY: PATCH then refuses
	// a new secret rather than store one the sign-in path cannot read.
	crypter *idpconfig.SecretCrypter
	// insertAudit writes the PATCH audit row (#2609). Tests replace it to
	// prove a failed audit write rolls the change back.
	insertAudit func(context.Context, *dbx.Queries, dbx.InsertMeAuditLogParams) error
}

// PreflightOptions configures the SSRF defenses for the OIDC discovery
// fetch. Defaults are sane for production; tests override via WithPreflightOptions.
type PreflightOptions struct {
	// Timeout caps the discovery fetch. Default: 5s.
	Timeout time.Duration
	// MaxBodyBytes caps the response body size. Default: 64 KiB.
	MaxBodyBytes int64
	// AllowPrivateIPs, when true, permits the fetch to target loopback
	// and private (RFC1918 / ULA / CGNAT) addresses; link-local stays
	// denied. Off by default; the deployment sets it through
	// oidc.AllowPrivateIssuersEnv (#2534), and tests set it so a local
	// fake IdP can be reached via 127.0.0.1.
	AllowPrivateIPs bool
	// HTTPClient overrides the default transport so tests can supply a
	// roundtripper that records calls.
	HTTPClient *http.Client
	// LookupHost overrides net.DefaultResolver.LookupIP for SSRF tests.
	LookupHost func(ctx context.Context, host string) ([]net.IP, error)
}

// New constructs a Handler with default preflight options.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{
		pool: pool,
		preflightOpts: PreflightOptions{
			Timeout:      5 * time.Second,
			MaxBodyBytes: 64 * 1024,
		},
	}
}

// WithPreflightOptions returns a copy of h with the supplied preflight
// options. Used by tests to widen SSRF defenses for a local fake IdP.
func (h *Handler) WithPreflightOptions(opts PreflightOptions) *Handler {
	if opts.Timeout == 0 {
		opts.Timeout = h.preflightOpts.Timeout
	}
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = h.preflightOpts.MaxBodyBytes
	}
	c := *h
	c.preflightOpts = opts
	return &c
}

// WithSecretCrypter returns a copy of h that seals client secrets with c.
func (h *Handler) WithSecretCrypter(c *idpconfig.SecretCrypter) *Handler {
	cp := *h
	cp.crypter = c
	return &cp
}

// --- response shapes ---

// GetResponse is the JSON body of GET /v1/admin/sso.
// client_secret is intentionally omitted.
type GetResponse struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	IssuerURL           string   `json:"issuer_url"`
	ClientID            string   `json:"client_id"`
	RedirectURL         string   `json:"redirect_url"`
	AllowedEmailDomains []string `json:"allowed_email_domains"`
	// TrustUnverifiedEmailForLink lets a sign-in whose ID token omits
	// email_verified link to a SCIM-provisioned user by email (#1944 D3).
	TrustUnverifiedEmailForLink bool      `json:"trust_unverified_email_for_link"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

// PatchRequest is the JSON body of PATCH /v1/admin/sso. client_secret is
// write-only; an empty string means "leave existing".
type PatchRequest struct {
	IssuerURL           string   `json:"issuer_url"`
	ClientID            string   `json:"client_id"`
	ClientSecret        string   `json:"client_secret,omitempty"`
	RedirectURL         string   `json:"redirect_url"`
	AllowedEmailDomains []string `json:"allowed_email_domains"`
	// TrustUnverifiedEmailForLink: like every field here, an omitted value
	// is written as its zero value, false, which is the strict default.
	TrustUnverifiedEmailForLink bool `json:"trust_unverified_email_for_link"`
}

// PreflightRequest is the JSON body of POST /v1/admin/sso/preflight.
type PreflightRequest struct {
	IssuerURL string `json:"issuer_url"`
}

// PreflightResponse echoes the relevant endpoints from the IdP discovery
// document.
type PreflightResponse struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKsURI               string `json:"jwks_uri"`
}

// --- handlers ---

// Get handles GET /v1/admin/sso. Admin-only.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}

	var row dbx.GetAdminSSORow
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		got, qErr := q.GetAdminSSO(ctx, dbx.GetAdminSSOParams{
			TenantID: uuidToPgtype(tenantID),
			Name:     configName,
		})
		row = got
		return qErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpresp.WriteError(w, http.StatusNotFound, "no SSO config")
			return
		}
		httperr.WriteInternal(w, r, "fetch sso", err)
		return
	}

	resp := GetResponse{
		ID:                  uuidFromPgtype(row.ID).String(),
		Name:                row.Name,
		IssuerURL:           row.IssuerUrl,
		ClientID:            row.ClientID,
		RedirectURL:         row.RedirectUrl,
		AllowedEmailDomains: row.AllowedEmailDomains,
		CreatedAt:           row.CreatedAt.Time,
		UpdatedAt:           row.UpdatedAt.Time,

		TrustUnverifiedEmailForLink: row.TrustUnverifiedEmailForLink,
	}
	httpresp.WriteJSON(w, http.StatusOK, resp)
}

// Patch handles PATCH /v1/admin/sso. Admin-only. Upserts the primary
// IdP config; an empty client_secret leaves the existing secret intact.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}

	var req PatchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssuerURL) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "issuer_url is required")
		return
	}
	if strings.TrimSpace(req.ClientID) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "client_id is required")
		return
	}
	// The sign-in path refuses the same issuers (#2034), so a config that
	// saves here is one the resolver will use.
	if err := idpconfig.ValidateIssuerURL(req.IssuerURL, h.preflightOpts.AllowPrivateIPs); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// #2034: the secret is sealed before it is stored; an empty value is
	// the upsert's leave-existing sentinel and is passed through as such.
	secretBytes := []byte{}
	if req.ClientSecret != "" {
		if h.crypter == nil {
			httpresp.WriteError(w, http.StatusServiceUnavailable,
				"client secret encryption is not configured on this deployment (set "+idpconfig.SecretKeyEnv+")")
			return
		}
		sealed, sErr := h.crypter.Seal(tenantID, configName, req.ClientSecret)
		if sErr != nil {
			httpresp.WriteError(w, http.StatusInternalServerError, "seal client secret failed")
			return
		}
		secretBytes = sealed
	}
	// Normalize nil slice -> empty slice so the column's NOT NULL
	// constraint is satisfied. allowed_email_domains is text[] with NOT
	// NULL DEFAULT '{}' at the schema layer; the upsert path passes the
	// value through and would otherwise inject NULL.
	domains := req.AllowedEmailDomains
	if domains == nil {
		domains = []string{}
	}

	// #2609: the change and its audit row commit together or not at all.
	// The lock serialises SSO writes in the tenant, so the before value is
	// the state this write replaces, also while the tenant has no row yet.
	userID, principal := actor(r)
	var row dbx.UpsertAdminSSORow
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		if lErr := q.LockAdminSSO(ctx, uuidToPgtype(tenantID)); lErr != nil {
			return fmt.Errorf("lock sso: %w", lErr)
		}
		before := ssoAuditState{AllowedEmailDomains: []string{}}
		cur, gErr := q.GetAdminSSOForUpdate(ctx, dbx.GetAdminSSOForUpdateParams{
			TenantID: uuidToPgtype(tenantID),
			Name:     configName,
		})
		switch {
		case gErr == nil:
			before = ssoAuditState{
				Configured:                  true,
				IssuerURL:                   cur.IssuerUrl,
				ClientID:                    cur.ClientID,
				RedirectURL:                 cur.RedirectUrl,
				AllowedEmailDomains:         cur.AllowedEmailDomains,
				TrustUnverifiedEmailForLink: cur.TrustUnverifiedEmailForLink,
			}
		case errors.Is(gErr, pgx.ErrNoRows):
			// First INSERT requires a non-empty client_secret -- the
			// upsert ON CONFLICT branch tolerates empty (leave-existing).
			if req.ClientSecret == "" {
				return errSecretRequired
			}
		default:
			return gErr
		}

		got, qErr := q.UpsertAdminSSO(ctx, dbx.UpsertAdminSSOParams{
			ID:                  uuidToPgtype(uuid.New()),
			TenantID:            uuidToPgtype(tenantID),
			Name:                configName,
			IssuerUrl:           req.IssuerURL,
			ClientID:            req.ClientID,
			ClientSecretEnc:     secretBytes,
			RedirectUrl:         req.RedirectURL,
			AllowedEmailDomains: domains,

			TrustUnverifiedEmailForLink: req.TrustUnverifiedEmailForLink,
		})
		if qErr != nil {
			return qErr
		}
		row = got
		after := ssoAuditState{
			Configured:                  true,
			IssuerURL:                   got.IssuerUrl,
			ClientID:                    got.ClientID,
			RedirectURL:                 got.RedirectUrl,
			AllowedEmailDomains:         got.AllowedEmailDomains,
			TrustUnverifiedEmailForLink: got.TrustUnverifiedEmailForLink,
		}
		return h.auditSSOChange(ctx, q, tenantID, userID, principal, before, after, req.ClientSecret != "")
	})
	if errors.Is(err, errSecretRequired) {
		httpresp.WriteError(w, http.StatusBadRequest, "client_secret is required on first config")
		return
	}
	if err != nil {
		httperr.WriteInternal(w, r, "upsert sso", err)
		return
	}

	resp := GetResponse{
		ID:                  uuidFromPgtype(row.ID).String(),
		Name:                row.Name,
		IssuerURL:           row.IssuerUrl,
		ClientID:            row.ClientID,
		RedirectURL:         row.RedirectUrl,
		AllowedEmailDomains: row.AllowedEmailDomains,
		CreatedAt:           row.CreatedAt.Time,
		UpdatedAt:           row.UpdatedAt.Time,

		TrustUnverifiedEmailForLink: row.TrustUnverifiedEmailForLink,
	}
	httpresp.WriteJSON(w, http.StatusOK, resp)
}

// Preflight handles POST /v1/admin/sso/preflight. Admin-only. Fetches the
// IdP discovery document and returns the parsed endpoints. SSRF-hardened.
func (h *Handler) Preflight(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}

	var req PreflightRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssuerURL) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "issuer_url is required")
		return
	}

	// Parse + validate URL.
	parsed, err := url.Parse(strings.TrimRight(req.IssuerURL, "/"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid issuer_url: "+err.Error())
		return
	}
	if !h.preflightOpts.AllowPrivateIPs && parsed.Scheme != "https" {
		httpresp.WriteError(w, http.StatusBadRequest, "issuer_url must be https")
		return
	}
	if parsed.Host == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "issuer_url missing host")
		return
	}

	// SSRF guard: resolve host and reject loopback / RFC1918.
	if !h.preflightOpts.AllowPrivateIPs {
		if err := h.guardSSRF(r.Context(), parsed.Host); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Fetch discovery doc.
	discURL := parsed.String() + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(r.Context(), h.preflightOpts.Timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, discURL, nil)
	if err != nil {
		httperr.WriteInternal(w, r, "build request", err)
		return
	}
	client := h.preflightOpts.HTTPClient
	if client == nil {
		// Re-checks every dialled IP (closing the DNS-rebinding window
		// after guardSSRF), ignores proxies and refuses redirects:
		// discovery documents have no legitimate reason to redirect.
		client = h.guard().NewHTTPClient(h.preflightOpts.Timeout)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		httperr.WriteStatus(w, r, http.StatusBadGateway, "discovery fetch failed", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		httpresp.WriteError(w, http.StatusBadGateway, fmt.Sprintf("discovery returned %d", resp.StatusCode))
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, h.preflightOpts.MaxBodyBytes+1))
	if err != nil {
		httperr.WriteStatus(w, r, http.StatusBadGateway, "read body", err)
		return
	}
	if int64(len(body)) > h.preflightOpts.MaxBodyBytes {
		httpresp.WriteError(w, http.StatusBadGateway, "discovery body exceeds size cap")
		return
	}

	var parsedDoc PreflightResponse
	if err := json.Unmarshal(body, &parsedDoc); err != nil {
		httperr.WriteStatus(w, r, http.StatusBadGateway, "discovery body not JSON", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, parsedDoc)
}

// --- helpers ---

func (h *Handler) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// guardSSRF rejects hostnames whose A/AAAA records resolve to loopback,
// link-local, RFC1918, RFC4193, or other non-routable address space.
// The check uses net.LookupIP which honors /etc/hosts; tests inject a
// LookupHost override to seed a fake IdP at 127.0.0.1.
func (h *Handler) guardSSRF(ctx context.Context, host string) error {
	hostname := host
	if i := strings.IndexByte(host, ':'); i > 0 {
		hostname = host[:i]
	}
	// Reject raw bracketed IPv6 explicitly.
	if strings.HasPrefix(host, "[") {
		return errors.New("issuer_url host must not be a raw IP address")
	}
	// Reject raw IPv4.
	if ip := net.ParseIP(hostname); ip != nil {
		return errors.New("issuer_url host must not be a raw IP address")
	}

	lookup := h.preflightOpts.LookupHost
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		}
	}
	ips, err := lookup(ctx, hostname)
	if err != nil {
		return fmt.Errorf("dns lookup failed for %s: %w", hostname, err)
	}
	for _, ip := range ips {
		if reason := h.guard().DenyReason(ip); reason != "" {
			return fmt.Errorf("issuer_url host resolves to non-routable address %s (%s)", ip, reason)
		}
	}
	return nil
}

// guard is the shared SSRF policy (internal/netguard, #2534) the preflight
// applies. AllowPrivateIPs is the deployment opt-in for a self-hosted IdP
// on a private network (oidc.AllowPrivateIssuersEnv); it permits loopback
// and private ranges but never link-local, so cloud metadata stays denied.
// The sign-in path builds the same policy, so preflight and runtime agree.
func (h *Handler) guard() netguard.Policy {
	return netguard.Policy{
		AllowLoopback: h.preflightOpts.AllowPrivateIPs,
		AllowPrivate:  h.preflightOpts.AllowPrivateIPs,
	}
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	if !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return false
	}
	return true
}

func uuidToPgtype(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidFromPgtype(u pgtype.UUID) uuid.UUID {
	if !u.Valid {
		return uuid.Nil
	}
	return uuid.UUID(u.Bytes)
}
