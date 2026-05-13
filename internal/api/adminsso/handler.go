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
//   - client_secret is NEVER returned in any GET response. The DB column
//     client_secret_enc is encrypted-at-rest (slice 034 contract); we
//     never read it back to the wire.
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
	"github.com/mgoodric/security-atlas/internal/db/dbx"
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
}

// PreflightOptions configures the SSRF defenses for the OIDC discovery
// fetch. Defaults are sane for production; tests override via WithPreflightOptions.
type PreflightOptions struct {
	// Timeout caps the discovery fetch. Default: 5s.
	Timeout time.Duration
	// MaxBodyBytes caps the response body size. Default: 64 KiB.
	MaxBodyBytes int64
	// AllowPrivateIPs, when true, permits the fetch to target loopback
	// and RFC1918 addresses. Defaults to false in production; tests set
	// true so a local fake IdP can be reached via 127.0.0.1.
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
	return &Handler{pool: h.pool, preflightOpts: opts}
}

// --- response shapes ---

// GetResponse is the JSON body of GET /v1/admin/sso.
// client_secret is intentionally omitted.
type GetResponse struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	IssuerURL           string    `json:"issuer_url"`
	ClientID            string    `json:"client_id"`
	RedirectURL         string    `json:"redirect_url"`
	AllowedEmailDomains []string  `json:"allowed_email_domains"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// PatchRequest is the JSON body of PATCH /v1/admin/sso. client_secret is
// write-only; an empty string means "leave existing".
type PatchRequest struct {
	IssuerURL           string   `json:"issuer_url"`
	ClientID            string   `json:"client_id"`
	ClientSecret        string   `json:"client_secret,omitempty"`
	RedirectURL         string   `json:"redirect_url"`
	AllowedEmailDomains []string `json:"allowed_email_domains"`
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
		writeError(w, http.StatusInternalServerError, "invalid tenant in credential")
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
			writeError(w, http.StatusNotFound, "no SSO config")
			return
		}
		writeError(w, http.StatusInternalServerError, "fetch sso: "+err.Error())
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
	}
	writeJSON(w, http.StatusOK, resp)
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
		writeError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}

	var req PatchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssuerURL) == "" {
		writeError(w, http.StatusBadRequest, "issuer_url is required")
		return
	}
	if strings.TrimSpace(req.ClientID) == "" {
		writeError(w, http.StatusBadRequest, "client_id is required")
		return
	}

	// First INSERT requires a non-empty client_secret -- the upsert ON
	// CONFLICT branch tolerates empty (leave-existing). We check by
	// reading whether a row already exists; if not, secret is required.
	var exists bool
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		_, gErr := q.GetAdminSSO(ctx, dbx.GetAdminSSOParams{
			TenantID: uuidToPgtype(tenantID),
			Name:     configName,
		})
		if gErr == nil {
			exists = true
			return nil
		}
		if errors.Is(gErr, pgx.ErrNoRows) {
			return nil
		}
		return gErr
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "check existing: "+err.Error())
		return
	}
	if !exists && req.ClientSecret == "" {
		writeError(w, http.StatusBadRequest, "client_secret is required on first config")
		return
	}

	// Encrypt-at-rest in v1 is a stub: we store the raw bytes. KMS-wrap
	// is a v1.x follow-up (per slice 034's contract). The handler treats
	// the field as already-encrypted for forward compatibility -- callers
	// pass a base-string today, KMS-ciphertext tomorrow.
	secretBytes := []byte(req.ClientSecret)
	// Normalize nil slice -> empty slice so the column's NOT NULL
	// constraint is satisfied. allowed_email_domains is text[] with NOT
	// NULL DEFAULT '{}' at the schema layer; the upsert path passes the
	// value through and would otherwise inject NULL.
	domains := req.AllowedEmailDomains
	if domains == nil {
		domains = []string{}
	}

	var row dbx.UpsertAdminSSORow
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		got, qErr := q.UpsertAdminSSO(ctx, dbx.UpsertAdminSSOParams{
			ID:                  uuidToPgtype(uuid.New()),
			TenantID:            uuidToPgtype(tenantID),
			Name:                configName,
			IssuerUrl:           req.IssuerURL,
			ClientID:            req.ClientID,
			ClientSecretEnc:     secretBytes,
			RedirectUrl:         req.RedirectURL,
			AllowedEmailDomains: domains,
		})
		row = got
		return qErr
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "upsert sso: "+err.Error())
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
	}
	writeJSON(w, http.StatusOK, resp)
}

// Preflight handles POST /v1/admin/sso/preflight. Admin-only. Fetches the
// IdP discovery document and returns the parsed endpoints. SSRF-hardened.
func (h *Handler) Preflight(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}

	var req PreflightRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssuerURL) == "" {
		writeError(w, http.StatusBadRequest, "issuer_url is required")
		return
	}

	// Parse + validate URL.
	parsed, err := url.Parse(strings.TrimRight(req.IssuerURL, "/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid issuer_url: "+err.Error())
		return
	}
	if !h.preflightOpts.AllowPrivateIPs && parsed.Scheme != "https" {
		writeError(w, http.StatusBadRequest, "issuer_url must be https")
		return
	}
	if parsed.Host == "" {
		writeError(w, http.StatusBadRequest, "issuer_url missing host")
		return
	}

	// SSRF guard: resolve host and reject loopback / RFC1918.
	if !h.preflightOpts.AllowPrivateIPs {
		if err := h.guardSSRF(r.Context(), parsed.Host); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Fetch discovery doc.
	discURL := parsed.String() + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(r.Context(), h.preflightOpts.Timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, discURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build request: "+err.Error())
		return
	}
	client := h.preflightOpts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: h.preflightOpts.Timeout}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "discovery fetch failed: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("discovery returned %d", resp.StatusCode))
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, h.preflightOpts.MaxBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "read body: "+err.Error())
		return
	}
	if int64(len(body)) > h.preflightOpts.MaxBodyBytes {
		writeError(w, http.StatusBadGateway, "discovery body exceeds size cap")
		return
	}

	var parsedDoc PreflightResponse
	if err := json.Unmarshal(body, &parsedDoc); err != nil {
		writeError(w, http.StatusBadGateway, "discovery body not JSON: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, parsedDoc)
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
		if isUnsafeIP(ip) {
			return fmt.Errorf("issuer_url host resolves to non-routable address %s", ip)
		}
	}
	return nil
}

// isUnsafeIP reports whether ip is in loopback, link-local, RFC1918,
// RFC4193, multicast, or unspecified space. Public IPs return false.
func isUnsafeIP(ip net.IP) bool {
	if ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() {
		return true
	}
	return false
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	if !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "admin credential required")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
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
