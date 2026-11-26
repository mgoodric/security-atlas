//go:build integration

// Integration tests for the #2034 DB-backed IdP resolver against real
// Postgres with RLS: the saved row is what resolves, one tenant's row never
// answers for another, and a row that cannot be used is refused rather than
// replaced by anything else.
//
// Run via: go test -tags=integration ./internal/auth/idpconfig/...
// (needs DATABASE_URL = BYPASSRLS migrate role, DATABASE_URL_APP = atlas_app).
package idpconfig_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/idpconfig"
	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/netguard"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// devIssuerPolicy is the private-issuer opt-in (oidc.AllowPrivateIssuersEnv),
// so a fake IdP on 127.0.0.1 can be saved and used.
var devIssuerPolicy = netguard.Policy{AllowLoopback: true, AllowPrivate: true}

type fixture struct {
	app, admin *pgxpool.Pool
	crypter    *idpconfig.SecretCrypter
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	appURL, adminURL := os.Getenv("DATABASE_URL_APP"), os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		t.Skip("DATABASE_URL_APP or DATABASE_URL not set; skipping #2034 resolver integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	app, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(func() { app.Close(); admin.Close() })
	c, err := idpconfig.NewSecretCrypter(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return fixture{app: app, admin: admin, crypter: c}
}

type row struct {
	issuer, clientID, redirect string
	secret                     []byte
	domains                    []string
	trust                      bool
}

func (f fixture) insert(t *testing.T, tenant uuid.UUID, name string, r row) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if r.domains == nil {
		r.domains = []string{}
	}
	if _, err := f.admin.Exec(context.Background(),
		`INSERT INTO oidc_idp_configs (id, tenant_id, name, issuer_url, client_id, client_secret_enc,
		                               redirect_url, allowed_email_domains, trust_unverified_email_for_link)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, tenant, name, r.issuer, r.clientID, r.secret, r.redirect, r.domains, r.trust,
	); err != nil {
		t.Fatalf("insert oidc_idp_configs: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.admin.Exec(context.Background(), `DELETE FROM oidc_idp_configs WHERE id = $1`, id)
	})
	return id
}

func (f fixture) sealed(t *testing.T, tenant uuid.UUID, name, secret string) []byte {
	t.Helper()
	b, err := f.crypter.Seal(tenant, name, secret)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tenantCtx(t *testing.T, tenant uuid.UUID) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant.String())
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func goodRow(f fixture, t *testing.T, tenant uuid.UUID) row {
	return row{
		issuer:   "https://idp-" + tenant.String()[:8] + ".example.com",
		clientID: "client-" + tenant.String()[:8],
		redirect: "https://atlas.example.com/auth/oidc/callback",
		secret:   f.sealed(t, tenant, "primary", "secret-"+tenant.String()),
		domains:  []string{"example.com"},
		trust:    true,
	}
}

// The saved row is what the Authenticator gets: issuer, client, the
// decrypted secret, domains and the trust flag.
func TestResolver_ReturnsSavedConfig(t *testing.T) {
	f := newFixture(t)
	tenant := uuid.New()
	want := goodRow(f, t, tenant)
	id := f.insert(t, tenant, "primary", want)

	got, err := idpconfig.NewResolver(f.app, f.crypter, netguard.Policy{}).ResolveIdp(tenantCtx(t, tenant), tenant, "primary")
	if err != nil {
		t.Fatalf("ResolveIdp: %v", err)
	}
	if got.ID != id || got.TenantID != tenant || got.Name != "primary" ||
		got.IssuerURL != want.issuer || got.ClientID != want.clientID || got.RedirectURL != want.redirect ||
		got.ClientSecret != "secret-"+tenant.String() || !got.TrustUnverifiedEmailForLink ||
		len(got.AllowedEmailDomains) != 1 || got.AllowedEmailDomains[0] != "example.com" {
		t.Errorf("ResolveIdp = %+v, want the saved row", got)
	}
}

// Tenant B's config never answers for tenant A: not by asking for A with
// B's IdP name, not with B's tenant id under A's RLS context, and not with
// B's sealed secret copied into A's row.
func TestResolver_CrossTenantRefused(t *testing.T) {
	f := newFixture(t)
	a, b := uuid.New(), uuid.New()
	f.insert(t, b, "okta", goodRow(f, t, b))
	r := idpconfig.NewResolver(f.app, f.crypter, netguard.Policy{})

	if _, err := r.ResolveIdp(tenantCtx(t, a), a, "okta"); !errors.Is(err, oidc.ErrUnknownIdp) {
		t.Errorf("tenant A asking for B's IdP name: err = %v, want ErrUnknownIdp", err)
	}
	if _, err := r.ResolveIdp(tenantCtx(t, a), b, "okta"); !errors.Is(err, oidc.ErrUnknownIdp) {
		t.Errorf("B's tenant id under A's context: err = %v, want ErrUnknownIdp", err)
	}
	if _, err := r.ResolveIdp(context.Background(), b, "okta"); !errors.Is(err, oidc.ErrUnknownIdp) {
		t.Errorf("no tenant context: err = %v, want ErrUnknownIdp", err)
	}

	copied := goodRow(f, t, a)
	copied.secret = f.sealed(t, b, "primary", "b-secret")
	f.insert(t, a, "primary", copied)
	if _, err := r.ResolveIdp(tenantCtx(t, a), a, "primary"); !errors.Is(err, idpconfig.ErrConfigUnusable) {
		t.Errorf("B's sealed secret in A's row: err = %v, want ErrConfigUnusable", err)
	}
}

func TestResolver_UnknownIdpRefused(t *testing.T) {
	f := newFixture(t)
	tenant := uuid.New()
	f.insert(t, tenant, "primary", goodRow(f, t, tenant))
	r := idpconfig.NewResolver(f.app, f.crypter, netguard.Policy{})
	for _, name := range []string{"secondary", "", "PRIMARY", "primary "} {
		if _, err := r.ResolveIdp(tenantCtx(t, tenant), tenant, name); !errors.Is(err, oidc.ErrUnknownIdp) {
			t.Errorf("name %q: err = %v, want ErrUnknownIdp", name, err)
		}
	}
}

// A saved row that cannot be used is refused, and the refusal never carries
// the secret.
func TestResolver_UnusableConfigRefused(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name    string
		mutate  func(*row, uuid.UUID)
		crypter bool
		dev     bool
	}{
		{name: "pre-#2034 plaintext secret", crypter: true, mutate: func(r *row, _ uuid.UUID) { r.secret = []byte("plaintext-secret") }},
		{name: "no secret key on the deployment", crypter: false, mutate: func(*row, uuid.UUID) {}},
		{name: "http issuer", crypter: true, mutate: func(r *row, _ uuid.UUID) { r.issuer = "http://idp.example.com" }},
		{name: "http loopback issuer without the dev opt-in", crypter: true, mutate: func(r *row, _ uuid.UUID) { r.issuer = "http://127.0.0.1:9" }},
		{name: "http non-loopback issuer with the dev opt-in", crypter: true, dev: true, mutate: func(r *row, _ uuid.UUID) { r.issuer = "http://idp.example.com" }},
		{name: "empty client id", crypter: true, mutate: func(r *row, _ uuid.UUID) { r.clientID = " " }},
		{name: "empty redirect", crypter: true, mutate: func(r *row, _ uuid.UUID) { r.redirect = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			r := goodRow(f, t, tenant)
			tc.mutate(&r, tenant)
			f.insert(t, tenant, "primary", r)
			var c *idpconfig.SecretCrypter
			if tc.crypter {
				c = f.crypter
			}
			_, err := idpconfig.NewResolver(f.app, c, netguard.Policy{AllowLoopback: tc.dev, AllowPrivate: tc.dev}).ResolveIdp(tenantCtx(t, tenant), tenant, "primary")
			if !errors.Is(err, idpconfig.ErrConfigUnusable) {
				t.Fatalf("err = %v, want ErrConfigUnusable", err)
			}
			if strings.Contains(err.Error(), "secret-"+tenant.String()) || strings.Contains(err.Error(), "plaintext-secret") {
				t.Errorf("refusal carries the secret: %v", err)
			}
		})
	}
}

// The dev opt-in admits an http issuer on loopback only.
func TestResolver_LoopbackHTTPWithDevOptIn(t *testing.T) {
	f := newFixture(t)
	tenant := uuid.New()
	r := goodRow(f, t, tenant)
	r.issuer = "http://127.0.0.1:9"
	f.insert(t, tenant, "primary", r)
	if _, err := idpconfig.NewResolver(f.app, f.crypter, devIssuerPolicy).ResolveIdp(tenantCtx(t, tenant), tenant, "primary"); err != nil {
		t.Errorf("loopback http issuer with the dev opt-in: %v", err)
	}
}

// Local mode ignores every saved row.
func TestLocalModeResolver_IgnoresSavedConfig(t *testing.T) {
	f := newFixture(t)
	tenant := uuid.New()
	f.insert(t, tenant, "primary", goodRow(f, t, tenant))
	if _, err := (idpconfig.LocalModeResolver{}).ResolveIdp(tenantCtx(t, tenant), tenant, "primary"); !errors.Is(err, oidc.ErrUnknownIdp) {
		t.Errorf("err = %v, want ErrUnknownIdp", err)
	}
}
