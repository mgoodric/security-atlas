//go:build integration

// #2239 — a real-login auditor ("user:<uuid>" subject, the shape /login, PKCE
// and the device grant mint) reaches an assigned period through the real
// roles resolver, and an auditor who loses the auditor role, or is disabled,
// after the assignment gets nothing from the leftover auditor_assignments
// row: the auditor routes are refused, so the row grants no period.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func assignPeriod2239(t *testing.T, admin *pgxpool.Pool, tenant string, user, period uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`INSERT INTO auditor_assignments (tenant_id, user_id, audit_period_id, granted_by) VALUES ($1, $2, $3, 'test-2239')`,
		tenant, user.String(), period); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM auditor_assignments WHERE tenant_id = $1 AND user_id = $2`, tenant, user.String())
	})
}

func TestPeriodAuditors2239_RealLoginAndLeftoverAssignment(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin)
	tid := uuid.MustParse(tenant)
	period := seedNotePeriod(t, admin, tenant)

	active := seedPeriodUser(t, admin, tenant, "active", "auditor")
	roleRevoked := seedPeriodUser(t, admin, tenant, "active", "auditor")
	adminDisabled := seedPeriodUser(t, admin, tenant, "active", "auditor")
	scimDisabled := seedPeriodUser(t, admin, tenant, "active", "auditor")
	for _, u := range []uuid.UUID{active, roleRevoked, adminDisabled, scimDisabled} {
		assignPeriod2239(t, admin, tenant, u, period)
	}

	// The real resolvers, as production wires them.
	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	engine = engine.WithAttrsResolver(auditor.NewDBAttrsResolver(app))
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))

	// Each token is minted while the user is still an assigned auditor, so
	// its role claim says auditor for the rest of its life.
	bearerFor := func(u uuid.UUID) periodAuditorsCall {
		c := testjwt.OwnerFor(tid, []string{"auditor"})
		c.Subject = "user:" + u.String()
		return periodAuditorsCall{t: t, bearer: srv.IssueTestJWT(t, c)}
	}
	calls := map[uuid.UUID]periodAuditorsCall{}
	for _, u := range []uuid.UUID{active, roleRevoked, adminDisabled, scimDisabled} {
		calls[u] = bearerFor(u)
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	for u, c := range calls {
		c.url = ts.URL
		calls[u] = c
	}

	t.Run("a real-login auditor reaches the assigned period", func(t *testing.T) {
		if got := mePeriodIDs(t, calls[active]); len(got) != 1 || got[0] != period.String() {
			t.Fatalf("periods = %v; want only %s", got, period)
		}
	})

	// After the assignment: the admin API's role revoke deletes the
	// user_roles rows; revoke with remove_membership also disables the
	// user; a SCIM deactivation flips users.status and keeps the rows.
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, roleRevoked.String()); err != nil {
		t.Fatalf("revoke role: %v", err)
	}
	if _, err := admin.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, adminDisabled.String()); err != nil {
		t.Fatalf("revoke role: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = ANY($1)`, []uuid.UUID{adminDisabled, scimDisabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	for name, u := range map[string]uuid.UUID{
		"auditor role revoked":          roleRevoked,
		"revoked and disabled by admin": adminDisabled,
		"disabled through SCIM":         scimDisabled,
	} {
		t.Run(name+": the leftover assignment grants nothing", func(t *testing.T) {
			c := calls[u]
			if s, b := c.do(http.MethodGet, "/v1/me/audit-periods", nil); s != http.StatusForbidden {
				t.Errorf("GET /v1/me/audit-periods: %d %v; want 403", s, b)
			}
			if s, b := c.do(http.MethodGet, "/v1/me/audit-period", nil); s != http.StatusForbidden {
				t.Errorf("GET /v1/me/audit-period: %d %v; want 403", s, b)
			}
			if s, b := postAuditNote(t, ts.URL, c.bearer, period); s != http.StatusForbidden {
				t.Errorf("note in the assigned period: %d %s; want 403", s, b)
			}
		})
	}
}
