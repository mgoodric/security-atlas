package idpconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrConfigUnusable wraps every refusal of a saved config that exists but
// cannot be used: a non-https issuer, a missing client or redirect, or a
// client secret that does not decrypt. The sign-in fails; nothing falls back
// to another config or tenant. Its text never carries the secret.
var ErrConfigUnusable = errors.New("oidc: IdP config unusable")

// Resolver is the production oidc.IdpResolver (#2034). It returns the
// oidc_idp_configs row named name in tenantID, read under that tenant's RLS
// context, so one tenant's config can never answer for another.
type Resolver struct {
	pool    *pgxpool.Pool
	crypter *SecretCrypter
	// allowLoopbackHTTP admits an http:// issuer on a loopback host. It is
	// the dev-only opt-in carried by oidc.AllowPrivateIssuersEnv; every
	// other issuer must be https.
	allowLoopbackHTTP bool
}

var _ oidc.IdpResolver = (*Resolver)(nil)

// NewResolver builds a Resolver over the RLS app pool. A nil crypter makes
// every config unusable, since no client secret can be read.
func NewResolver(pool *pgxpool.Pool, crypter *SecretCrypter, allowLoopbackHTTP bool) *Resolver {
	return &Resolver{pool: pool, crypter: crypter, allowLoopbackHTTP: allowLoopbackHTTP}
}

// ResolveIdp implements oidc.IdpResolver. No row is oidc.ErrUnknownIdp; a row
// that fails validation is ErrConfigUnusable. ctx must carry tenantID as its
// tenancy context (the auth handlers set it); a mismatch is refused.
func (r *Resolver) ResolveIdp(ctx context.Context, tenantID uuid.UUID, name string) (oidc.IdpConfig, error) {
	if tenantID == uuid.Nil || name == "" {
		return oidc.IdpConfig{}, oidc.ErrUnknownIdp
	}
	ctxTenant, err := tenancy.TenantFromContext(ctx)
	if err != nil || ctxTenant != tenantID.String() {
		return oidc.IdpConfig{}, oidc.ErrUnknownIdp
	}

	row, err := r.load(ctx, tenantID, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return oidc.IdpConfig{}, oidc.ErrUnknownIdp
	}
	if err != nil {
		return oidc.IdpConfig{}, fmt.Errorf("oidc: load IdP config: %w", err)
	}
	// RLS already scopes the read; this holds even if a policy regressed.
	if !row.TenantID.Valid || uuid.UUID(row.TenantID.Bytes) != tenantID {
		return oidc.IdpConfig{}, oidc.ErrUnknownIdp
	}

	cfgID := uuid.UUID(row.ID.Bytes)
	if err := ValidateIssuerURL(row.IssuerUrl, r.allowLoopbackHTTP); err != nil {
		return oidc.IdpConfig{}, r.unusable(ctx, tenantID, cfgID, err.Error())
	}
	if strings.TrimSpace(row.ClientID) == "" {
		return oidc.IdpConfig{}, r.unusable(ctx, tenantID, cfgID, "client_id is empty")
	}
	if strings.TrimSpace(row.RedirectUrl) == "" {
		return oidc.IdpConfig{}, r.unusable(ctx, tenantID, cfgID, "redirect_url is empty")
	}
	if r.crypter == nil {
		return oidc.IdpConfig{}, r.unusable(ctx, tenantID, cfgID, ErrSecretKeyUnconfigured.Error())
	}
	secret, err := r.crypter.Open(tenantID, row.Name, row.ClientSecretEnc)
	if err != nil {
		return oidc.IdpConfig{}, r.unusable(ctx, tenantID, cfgID, err.Error())
	}

	return oidc.IdpConfig{
		ID:                  cfgID,
		TenantID:            tenantID,
		Name:                row.Name,
		IssuerURL:           row.IssuerUrl,
		ClientID:            row.ClientID,
		ClientSecret:        secret,
		RedirectURL:         row.RedirectUrl,
		AllowedEmailDomains: row.AllowedEmailDomains,

		TrustUnverifiedEmailForLink: row.TrustUnverifiedEmailForLink,
	}, nil
}

func (r *Resolver) load(ctx context.Context, tenantID uuid.UUID, name string) (dbx.OidcIdpConfig, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return dbx.OidcIdpConfig{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return dbx.OidcIdpConfig{}, err
	}
	return dbx.New(tx).GetOidcIdpConfigByName(ctx, dbx.GetOidcIdpConfigByNameParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		Name:     name,
	})
}

// unusable logs why a saved config was refused (the reason never holds the
// secret) and returns ErrConfigUnusable with it.
func (r *Resolver) unusable(ctx context.Context, tenantID, cfgID uuid.UUID, reason string) error {
	slog.WarnContext(ctx, "oidc sign-in refused: IdP config unusable",
		"tenant_id", tenantID, "idp_config_id", cfgID, "reason", reason)
	return fmt.Errorf("%w: %s", ErrConfigUnusable, reason)
}

// ValidateIssuerURL accepts an absolute https URL with a host and no
// userinfo, query or fragment. With allowLoopbackHTTP, an http URL whose host
// is loopback is also accepted, for a dev IdP on the same machine.
func ValidateIssuerURL(raw string, allowLoopbackHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return errors.New("issuer_url must be an absolute URL with a host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("issuer_url must not carry userinfo, a query or a fragment")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if allowLoopbackHTTP && isLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return errors.New("issuer_url must be https")
}

// LocalModeEnv is the dev-only switch that turns OIDC sign-in off (#2034):
// "true" wires a resolver that knows no IdP, so saved SSO settings are not
// used and only /auth/local/login signs in. Unset or "false" is the
// production default, the DB-backed Resolver.
const LocalModeEnv = "ATLAS_OIDC_LOCAL_MODE"

// LocalModeFromEnv reads LocalModeEnv. Any value other than "", "true" or
// "false" is an error, so a typo never changes which resolver runs.
func LocalModeFromEnv() (bool, error) {
	switch v := os.Getenv(LocalModeEnv); v {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be \"true\" or \"false\", got %q", LocalModeEnv, v)
	}
}

// LocalModeResolver knows no IdP: every lookup is oidc.ErrUnknownIdp. It is
// what LocalModeEnv=true wires.
type LocalModeResolver struct{}

// ResolveIdp implements oidc.IdpResolver.
func (LocalModeResolver) ResolveIdp(context.Context, uuid.UUID, string) (oidc.IdpConfig, error) {
	return oidc.IdpConfig{}, oidc.ErrUnknownIdp
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
