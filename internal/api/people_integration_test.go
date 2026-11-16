//go:build integration

// #2085 — GET /v1/people end to end through the production router (JWT,
// tenancy, OPA) on real Postgres under RLS. A viewer and an auditor read
// their tenant's people; no response carries an email; tenant B's users
// and keys never appear for tenant A, including for a super-admin.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/people"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func seedPeopleUser(t *testing.T, migrate *pgxpool.Pool, tenant, email, displayName, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, $4, $5)`,
		id, tenant, email, displayName, status); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// seedPeopleKey inserts an API key owned by issuedBy and minted by
// mintedBy; either may be nil.
func seedPeopleKey(t *testing.T, migrate *pgxpool.Pool, tenant string, issuedBy, mintedBy *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	hash := sha256.Sum256([]byte(id.String()))
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO api_keys (id, tenant_id, token_hash, last4, issued_by, minted_by) VALUES ($1, $2, $3, 'zz99', $4, $5)`,
		id, tenant, hash[:], issuedBy, mintedBy); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	return id
}

func TestPeople2085_TenantScopedNoEmail(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := dbtest.SeedTenant(t, migrate, "api_keys", "users")
	tenantB := dbtest.SeedTenant(t, migrate, "api_keys", "users")

	alice := seedPeopleUser(t, migrate, tenantA, "alice-2085@example.invalid", "Alice 2085", "active")
	noName := seedPeopleUser(t, migrate, tenantA, "nameless-2085@example.invalid", "", "active")
	former := seedPeopleUser(t, migrate, tenantA, "former-2085@example.invalid", "Former 2085", "disabled")
	// The owner is issued_by; minted_by is attribution and never stands in
	// for a missing owner.
	keyA := seedPeopleKey(t, migrate, tenantA, &alice, &former)
	ownerless := seedPeopleKey(t, migrate, tenantA, nil, &alice)
	bobB := seedPeopleUser(t, migrate, tenantB, "bob-2085@example.invalid", "Bob Other Tenant", "active")
	keyB := seedPeopleKey(t, migrate, tenantB, &bobB, &bobB)

	tid := uuid.MustParse(tenantA)
	// AdminFor carries SuperAdmin=true: the super-admin case.
	superClaims := testjwt.AdminFor(tid)
	viewerClaims := testjwt.ViewerFor(tid)
	auditorClaims := testjwt.OwnerFor(tid, []string{"auditor"})
	auditorClaims.Subject = "test-auditor:" + tid.String()
	engine, err := authz.NewEngine(context.Background(), noteSubjectRoles{
		superClaims.Subject:   {authz.RoleAdmin},
		viewerClaims.Subject:  {authz.RoleViewer},
		auditorClaims.Subject: {authz.RoleAuditor},
	})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	// Mint before building the handler: it captures the test signer.
	callers := []struct {
		name   string
		bearer string
	}{
		{"viewer", srv.IssueTestJWT(t, viewerClaims)},
		{"auditor", srv.IssueTestJWT(t, auditorClaims)},
		{"super-admin", srv.IssueTestJWT(t, superClaims)},
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	for _, c := range callers {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/v1/people", nil)
			req.Header.Set("Authorization", "Bearer "+c.bearer)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET /v1/people: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /v1/people: status %d body=%s", resp.StatusCode, raw)
			}
			body := string(raw)
			for _, leak := range []string{"@example.invalid", `"email"`, "zz99", "last4", "token"} {
				if strings.Contains(body, leak) {
					t.Errorf("response carries %q: %s", leak, body)
				}
			}
			for _, other := range []string{bobB.String(), keyB.String(), "Bob Other Tenant"} {
				if strings.Contains(body, other) {
					t.Errorf("tenant B's %q visible to tenant A's %s", other, c.name)
				}
			}

			var got people.Response
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode: %v; body=%s", err, body)
			}
			users := map[string]people.User{}
			for _, u := range got.Users {
				users[u.ID] = u
			}
			if u := users[alice.String()]; u.DisplayName != "Alice 2085" || u.Status != "active" {
				t.Errorf("alice = %+v", u)
			}
			if u := users[noName.String()]; u.DisplayName != "nameless-2085" {
				t.Errorf("nameless user display_name = %q, want the email local-part", u.DisplayName)
			}
			if u := users[former.String()]; u.Status != "disabled" {
				t.Errorf("former user = %+v", u)
			}
			keys := map[string]people.APIKey{}
			for _, k := range got.APIKeys {
				keys[k.ID] = k
			}
			if k, ok := keys[keyA.String()]; !ok {
				t.Errorf("tenant A's key %s missing from %s", keyA, body)
			} else if k.OwnerID != alice.String() {
				t.Errorf("key owner = %q, want issued_by %s (not minted_by)", k.OwnerID, alice)
			}
			if k, ok := keys[ownerless.String()]; !ok {
				t.Errorf("ownerless key %s missing from %s", ownerless, body)
			} else if k.OwnerID != "" {
				t.Errorf("ownerless key owner = %q, want none (no minted_by fallback)", k.OwnerID)
			}
		})
	}
}
