//go:build integration

// security-atlas#1739 — a control bundle uploaded with an OAuth client's
// client_credentials token lands in the tenant the client is bound to and
// is visible there; a client bound to no tenant is refused instead of
// writing into the all-zeros tenant.
//
// The whole path is real: oauthclient.Store over Postgres, the token
// endpoint at POST /oauth/token, jwtmw + tenancymw on the full router,
// the upload handler, and the list handler read back as a user of the
// tenant.
package controls_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const oauthTenantIssuer = "https://atlas.test/1739"

type oauthTenantSetup struct {
	server  *httptest.Server
	clients *oauthclient.Store
	signer  *tokensign.Signer
	admin   *pgxpool.Pool
}

// setupOAuthTenantHTTP wires the full router with the OAuth token endpoint
// and the JWT validator sharing one signer, so a client_credentials token
// minted at /oauth/token authenticates /v1/*.
func setupOAuthTenantHTTP(t *testing.T) oauthTenantSetup {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	reg := bootRegistry(t, admin)
	seedSCFAnchor(t, admin, "IAC-37", "IAC")

	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	clients := oauthclient.New(app)

	srv := api.New(api.Config{RotationGrace: time.Hour, SchemaRegistry: reg})
	srv.AttachDB(app)
	srv.AttachJWTValidator(signer, nil, oauthTenantIssuer, oauthTenantIssuer)
	oh := oauth.New(ks, oauth.Config{Issuer: oauthTenantIssuer})
	oh.AttachTokenEndpoint(oauth.NewTokenEndpoint(signer, clients, oauth.TokenEndpointConfig{
		Issuer:        oauthTenantIssuer,
		RatePerMinute: 600,
	}))
	srv.AttachOAuthHandler(oh)

	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests nil")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return oauthTenantSetup{server: ts, clients: clients, signer: signer, admin: admin}
}

// seedBoundTenant inserts a tenants row (oauth_clients.bound_tenant_id is a
// foreign key to it) and cleans up the tenant's controls, its clients and
// the row itself.
func seedBoundTenant(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `INSERT INTO tenants (id, name) VALUES ($1, $2)`,
		id, "oauth-1739-"+id.String()[:8]); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM controls WHERE tenant_id = $1`,
			`DELETE FROM oauth_clients WHERE bound_tenant_id = $1`,
			`DELETE FROM tenants WHERE id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, id); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return id
}

func clientCredentialsToken(t *testing.T, s oauthTenantSetup, clientID, secret string) string {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeClientCredentials)
	form.Set("client_id", clientID)
	form.Set("client_secret", secret)
	resp, err := http.Post(s.server.URL+"/oauth/token", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("POST /oauth/token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/oauth/token: status %d body %s", resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		t.Fatalf("/oauth/token: no access_token in %s (%v)", raw, err)
	}
	return out.AccessToken
}

func uploadManualBundle(t *testing.T, s oauthTenantSetup, token, bundleID string) (int, []byte) {
	t.Helper()
	yaml := `bundle_schema_version: "1"
bundle_id: ` + bundleID + `
title: "Quarterly access review (1739)"
implementation_type: manual_attested
scf_anchor_id: IAC-37
owner_role: ` + ownerRole + `
freshness_class: quarterly
manual_evidence_schema:
  type: object
  required: [reviewer]
  properties:
    reviewer:
      type: string
      minLength: 1
`
	body, _ := json.Marshal(map[string]string{"manifest_yaml": yaml})
	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, s.server.URL+"/v1/controls:upload-bundle", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func countControlsByBundle(t *testing.T, admin *pgxpool.Pool, bundleID string, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM controls WHERE bundle_id = $1 AND tenant_id = $2`, bundleID, tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count controls: %v", err)
	}
	return n
}

func TestOAuthClientUpload_LandsInBoundTenantAndIsVisible(t *testing.T) {
	s := setupOAuthTenantHTTP(t)
	tenant := seedBoundTenant(t, s.admin)
	ctx := context.Background()

	client, secret, err := s.clients.IssueForTenant(ctx, "it-1739-bound-"+uuid.NewString()[:8], tenant)
	if err != nil {
		t.Fatalf("IssueForTenant: %v", err)
	}
	token := clientCredentialsToken(t, s, client.ClientID, secret)

	claims, err := s.signer.Verify(ctx, token)
	if err != nil {
		t.Fatalf("verify minted token: %v", err)
	}
	if claims.CurrentTenantID != tenant {
		t.Fatalf("token current_tenant_id = %s, want the bound tenant %s", claims.CurrentTenantID, tenant)
	}

	bundleID := "it_1739_bound_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	code, raw := uploadManualBundle(t, s, token, bundleID)
	if code != http.StatusCreated {
		t.Fatalf("upload: status %d body %s", code, raw)
	}

	if n := countControlsByBundle(t, s.admin, bundleID, tenant); n != 1 {
		t.Errorf("controls in the bound tenant = %d, want 1", n)
	}
	if n := countControlsByBundle(t, s.admin, bundleID, uuid.Nil); n != 0 {
		t.Errorf("controls in the all-zeros tenant = %d, want 0", n)
	}

	// The tenant's user sees the control through the API.
	userToken := testjwt.MustMint(t, s.signer, oauthTenantIssuer, oauthTenantIssuer, testjwt.AdminFor(tenant))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.server.URL+"/v1/controls?limit=500", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/controls: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	listRaw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/controls: status %d body %s", resp.StatusCode, listRaw)
	}
	if !bytes.Contains(listRaw, []byte(bundleID)) {
		t.Errorf("GET /v1/controls as the tenant's user does not list bundle %s: %s", bundleID, listRaw)
	}
}

func TestOAuthClientUpload_UnboundClientIsRefused(t *testing.T) {
	s := setupOAuthTenantHTTP(t)
	ctx := context.Background()

	client, secret, err := s.clients.Issue(ctx, "it-1739-unbound-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.admin.Exec(context.Background(), `DELETE FROM oauth_clients WHERE id = $1`, client.ID)
	})
	token := clientCredentialsToken(t, s, client.ClientID, secret)

	bundleID := "it_1739_unbound_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	code, raw := uploadManualBundle(t, s, token, bundleID)
	if code != http.StatusForbidden {
		t.Fatalf("upload with an unbound client: status %d body %s, want 403", code, raw)
	}
	var body map[string]string
	if err := json.Unmarshal(raw, &body); err != nil || body["error"] != "tenant_unbound" {
		t.Errorf("refusal body = %s, want error tenant_unbound", raw)
	}
	if !strings.Contains(body["message"], "bind-client") {
		t.Errorf("refusal message %q does not name atlas-cli oauth bind-client", body["message"])
	}
	if body["message"] != tenancymw.UnboundWriteMessage {
		t.Errorf("message = %q, want tenancymw.UnboundWriteMessage", body["message"])
	}

	var n int
	if err := s.admin.QueryRow(ctx, `SELECT count(*) FROM controls WHERE bundle_id = $1`, bundleID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("a refused upload persisted %d control row(s)", n)
	}
}
