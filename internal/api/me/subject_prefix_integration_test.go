//go:build integration

// #2055 — /v1/me under the subject the real login path mints.
//
// Local login, PKCE and the device grant all issue Subject = "user:<uuid>",
// and jwtmw bridges that verbatim into cred.UserID. The slice-108 suite mints
// bare-UUID subjects, which is why the raw uuid.Parse in buildProfile /
// PatchMe passed there and failed in production (a signed-in person rendered
// as "API key"). These tests use the prefixed subject, and pin that an
// API-key bearer still gets the credential profile.

package me_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// testServerForLocalLogin mirrors testServerForUser with the Subject shape
// internal/api/auth's local-login mint emits.
func testServerForLocalLogin(t *testing.T, app *pgxpool.Pool, tenantID, userID string) testEnv {
	t.Helper()
	return testServerForSubject(t, app, tenantID, "user:"+userID)
}

func testServerForSubject(t *testing.T, app *pgxpool.Pool, tenantID, subject string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	claims := testjwt.ViewerFor(uuid.MustParse(tenantID))
	claims.Subject = subject
	bearer := srv.IssueTestJWT(t, claims)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

func TestGetMe_LocalLoginSubjectResolvesUser(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "pat@example.com", "Pat Example")
	env := testServerForLocalLogin(t, app, tenantID, userID)

	resp, body := do(t, env, http.MethodGet, "/v1/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/me: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	if body["user_id"] != userID {
		t.Errorf("user_id = %v; want %s", body["user_id"], userID)
	}
	if body["display_name"] != "Pat Example" {
		t.Errorf("display_name = %v; want Pat Example", body["display_name"])
	}
	if body["email"] != "pat@example.com" {
		t.Errorf("email = %v; want pat@example.com", body["email"])
	}
}

func TestPatchMe_LocalLoginSubjectUpdatesUser(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "pat@example.com", "Pat Example")
	env := testServerForLocalLogin(t, app, tenantID, userID)

	resp, body := do(t, env, http.MethodPatch, "/v1/me", map[string]any{"display_name": "Pat Renamed"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /v1/me: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	if body["display_name"] != "Pat Renamed" {
		t.Errorf("display_name = %v; want Pat Renamed", body["display_name"])
	}
	var stored string
	if err := admin.QueryRow(context.Background(),
		`SELECT display_name FROM users WHERE id = $1`, userID).Scan(&stored); err != nil {
		t.Fatalf("read back users row: %v", err)
	}
	if stored != "Pat Renamed" {
		t.Errorf("users.display_name = %q; want Pat Renamed", stored)
	}
}

// Notification preferences are what Settings' notifications panel reads; the
// same raw parse made them 404 for a local-login session.
func TestGetPreferences_LocalLoginSubjectResolvesUser(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "pat@example.com", "Pat Example")
	env := testServerForLocalLogin(t, app, tenantID, userID)

	resp, body := do(t, env, http.MethodGet, "/v1/me/preferences", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/me/preferences: status %d, want 200; body=%v", resp.StatusCode, body)
	}
}

func TestGetMe_APIKeyKeepsCredentialProfile(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, _ := seedTenantAndUser(t, admin, "pat@example.com", "Pat Example")
	// Since slice 326 a credential reaches HTTP only as an atlas JWT; these
	// are the Subjects that name no users row.
	for _, tc := range []struct{ name, subject string }{
		{"API key (own credential id)", uuid.NewString()},
		{"bootstrap admin", "bootstrap-admin"},
		{"OAuth client (machine token)", "oauth_client:ci-runner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testServerForSubject(t, app, tenantID, tc.subject)

			resp, body := do(t, env, http.MethodGet, "/v1/me", nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /v1/me: status %d, want 200; body=%v", resp.StatusCode, body)
			}
			if name, _ := body["display_name"].(string); !strings.HasPrefix(name, "API key") {
				t.Errorf("display_name = %q; want the credential profile", name)
			}
			if body["email"] != "" || body["idp_subject"] != "" {
				t.Errorf("email/idp_subject = %v/%v; want empty for a credential", body["email"], body["idp_subject"])
			}
			if body["user_id"] != tc.subject {
				t.Errorf("user_id = %v; want the credential's own id %s", body["user_id"], tc.subject)
			}

			resp, body = do(t, env, http.MethodPatch, "/v1/me", map[string]any{"display_name": "nope"})
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("PATCH /v1/me: status %d, want 404; body=%v", resp.StatusCode, body)
			}
		})
	}
}

// #2216: /v1/me roles under the "user:<uuid>" subject. user_roles stores the
// bare uuid, so the unstripped subject matched no rows and every password or
// OIDC sign-in reported "roles": []. Both sign-ins mint the same subject
// shape; the OIDC case is a users row carrying an idp_subject.
func TestGetMe_UserPrefixedSubjectReturnsRoles(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	cases := []struct {
		name, email, idpSubject, role string
	}{
		{"password sign-in", "pw@example.com", "", "admin"},
		{"oidc sign-in", "oidc@example.com", "oidc|abc123", "grc_engineer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, userID := seedTenantAndUser(t, admin, tc.email, tc.name)
			if tc.idpSubject != "" {
				if _, err := admin.Exec(ctx, `UPDATE users SET idp_subject = $1 WHERE id = $2`, tc.idpSubject, userID); err != nil {
					t.Fatalf("set idp_subject: %v", err)
				}
			}
			if _, err := admin.Exec(ctx,
				`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test')`,
				tenantID, userID, tc.role); err != nil {
				t.Fatalf("seed user_roles: %v", err)
			}
			t.Cleanup(func() {
				if _, err := admin.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1`, tenantID); err != nil {
					t.Logf("cleanup user_roles: %v", err)
				}
			})
			env := testServerForLocalLogin(t, app, tenantID, userID)

			resp, body := do(t, env, http.MethodGet, "/v1/me", nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /v1/me: status %d, want 200; body=%v", resp.StatusCode, body)
			}
			roles, _ := body["roles"].([]any)
			if len(roles) != 1 || roles[0] != tc.role {
				t.Fatalf("roles = %v; want [%s]", body["roles"], tc.role)
			}
		})
	}
}
