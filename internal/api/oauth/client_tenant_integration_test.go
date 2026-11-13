//go:build integration

package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
)

// security-atlas#1739: a client_credentials token from a client bound to a
// tenant acts in that tenant: current_tenant_id is the bound tenant and
// available_tenants lists it (jwt.Validate requires the current tenant to
// be available). An unbound client's token stays tenant-free, which
// TestIntegrationClientCredentialsRoundTrip pins.
func TestIntegrationClientCredentials_BoundClientCarriesTenant(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	srv, signer := newIntegrationServer(t, pool)
	tenant := seedOAuthTenantRow(t)

	store := oauthclient.New(pool)
	client, secret, err := store.IssueForTenant(context.Background(), uniqueName(t, "it-cc-1739"), tenant)
	if err != nil {
		t.Fatalf("IssueForTenant: %v", err)
	}

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeClientCredentials)
	form.Set("client_id", client.ClientID)
	form.Set("client_secret", secret)
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	claims, err := signer.Verify(context.Background(), out.AccessToken)
	if err != nil {
		t.Fatalf("verify minted: %v", err)
	}
	if claims.CurrentTenantID != tenant {
		t.Errorf("current_tenant_id = %s, want bound tenant %s", claims.CurrentTenantID, tenant)
	}
	if len(claims.AvailableTenants) != 1 || claims.AvailableTenants[0] != tenant {
		t.Errorf("available_tenants = %v, want [%s]", claims.AvailableTenants, tenant)
	}
	if claims.SuperAdmin {
		t.Error("super_admin true on a client_credentials token")
	}
	if len(claims.Roles) != 0 {
		t.Errorf("roles = %v, want none on a machine token", claims.Roles)
	}
}

// seedOAuthTenantRow inserts a tenants row through the BYPASSRLS pool
// (oauth_clients.tenant_id is a foreign key to tenants) and removes it,
// with any client bound to it, when the test ends.
func seedOAuthTenantRow(t *testing.T) uuid.UUID {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New admin: %v", err)
	}
	id := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO tenants (id, name) VALUES ($1, $2)`,
		id, "oauth-it-1739-"+id.String()[:8]); err != nil {
		admin.Close()
		t.Fatalf("insert tenant: %v", err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		c := context.Background()
		_, _ = admin.Exec(c, `DELETE FROM oauth_clients WHERE tenant_id = $1`, id)
		_, _ = admin.Exec(c, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}
