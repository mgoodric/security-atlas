//go:build integration

// #2760: DELETE /v1/admin/sso removes the tenant's IdP config, the mappings
// scoped to it and the group-derived roles only it derived, in one
// transaction with one me_audit_log row (actor, config id, mapping count)
// and a group_role_audit_log revoke row per derivation. A user with a
// second source keeps that source's roles; a delete that would revoke the
// last admin is refused and changes nothing.
package adminsso_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/adminsso"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// migratePool is the BYPASSRLS pool for seeding and reading across RLS.
func migratePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; the delete tests seed tenants as the migrate role")
	}
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

type deleteFixture struct {
	admin   *pgxpool.Pool
	tenant  uuid.UUID
	actor   uuid.UUID
	primary uuid.UUID
	router  http.Handler
}

func newDeleteFixture(t *testing.T, actorIsManualAdmin bool) deleteFixture {
	t.Helper()
	admin := migratePool(t)
	ctx := context.Background()
	f := deleteFixture{admin: admin, tenant: uuid.New(), actor: uuid.New(), primary: uuid.New()}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %q: %v", sql, err)
		}
	}
	exec(`INSERT INTO tenants (id, name) VALUES ($1, $2)`, f.tenant, "sso-2760-"+f.tenant.String()[:8])
	t.Cleanup(func() {
		for _, table := range []string{
			"me_audit_log", "group_role_audit_log", "oidc_idp_group_mappings",
			"user_roles", "oidc_idp_configs", "users",
		} {
			_, _ = admin.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE tenant_id = $1`, table), f.tenant)
		}
		_, _ = admin.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, f.tenant)
	})
	exec(`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'SSO Admin', 'active')`,
		f.actor, f.tenant, "sso-2760-"+f.actor.String()[:8]+"@example.com")
	if actorIsManualAdmin {
		exec(`INSERT INTO user_roles (tenant_id, user_id, role, origin) VALUES ($1, $2, 'admin', 'manual')`,
			f.tenant, f.actor.String())
	}
	exec(`INSERT INTO oidc_idp_configs (id, tenant_id, name, issuer_url, client_id, client_secret_enc, redirect_url)
	      VALUES ($1, $2, 'primary', 'https://idp.example', 'cid', '\x00', 'https://app/cb')`, f.primary, f.tenant)

	h := adminsso.New(appPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "jwt:sso-delete",
				TenantID: f.tenant.String(),
				IsAdmin:  true,
				UserID:   "user:" + f.actor.String(),
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/sso", h.Get)
	r.Delete("/v1/admin/sso", h.Delete)
	f.router = r
	return f
}

func (f deleteFixture) seedConfig(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.admin.Exec(context.Background(),
		`INSERT INTO oidc_idp_configs (id, tenant_id, name, issuer_url, client_id, client_secret_enc, redirect_url)
		 VALUES ($1, $2, $3, 'https://idp2.example', 'cid', '\x00', 'https://app/cb')`, id, f.tenant, name); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f deleteFixture) seedMapping(t *testing.T, cfg uuid.UUID, group, role string) {
	t.Helper()
	var c any
	if cfg != uuid.Nil {
		c = cfg
	}
	if _, err := f.admin.Exec(context.Background(),
		`INSERT INTO oidc_idp_group_mappings (tenant_id, idp_config_id, group_ref, role) VALUES ($1, $2, $3, $4)`,
		f.tenant, c, group, role); err != nil {
		t.Fatal(err)
	}
}

func (f deleteFixture) seedMember(t *testing.T) string {
	t.Helper()
	id := uuid.New()
	if _, err := f.admin.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'Member', 'active')`,
		id, f.tenant, "sso-2760-m-"+id.String()[:8]+"@example.com"); err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func (f deleteFixture) derive(t *testing.T, user string, cfg uuid.UUID, src grouprole.Source, groups ...string) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), f.tenant.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grouprole.NewResolver(appPool).Derive(ctx, grouprole.DeriveInput{
		UserID: user, IDPConfigID: cfg, Groups: groups, Source: src,
	}); err != nil {
		t.Fatalf("derive: %v", err)
	}
}

func (f deleteFixture) roles(t *testing.T, user string) map[string]string {
	t.Helper()
	rows, err := f.admin.Query(context.Background(),
		`SELECT role, origin FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, f.tenant, user)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var role, origin string
		if err := rows.Scan(&role, &origin); err != nil {
			t.Fatal(err)
		}
		out[role] = origin
	}
	return out
}

func (f deleteFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func (f deleteFixture) do(t *testing.T, method string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(method, "/v1/admin/sso", nil))
	return rec
}

func TestDeleteSSO_RevokesSoleSourceRolesAndAudits_2760(t *testing.T) {
	f := newDeleteFixture(t, true)
	second := f.seedConfig(t, "entra")
	f.seedMapping(t, f.primary, "Engineering", "grc_engineer")
	f.seedMapping(t, f.primary, "Engineering", "viewer")
	f.seedMapping(t, uuid.Nil, "Everyone", "viewer")
	f.seedMapping(t, second, "Engineering", "auditor")

	onlyPrimary := f.seedMember(t) // signs in only through the primary config
	withSCIM := f.seedMember(t)    // SCIM also derives viewer
	withSecond := f.seedMember(t)  // a second config derives auditor
	f.derive(t, onlyPrimary, f.primary, grouprole.SourceOIDC, "Engineering")
	f.derive(t, withSCIM, f.primary, grouprole.SourceOIDC, "Engineering")
	f.derive(t, withSCIM, uuid.Nil, grouprole.SourceSCIM, "Everyone")
	f.derive(t, withSecond, f.primary, grouprole.SourceOIDC, "Engineering")
	f.derive(t, withSecond, second, grouprole.SourceOIDC, "Engineering")

	// GET carries the counts the confirm dialog shows.
	rec := f.do(t, http.MethodGet)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	var got adminsso.GetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// onlyPrimary: grc_engineer + viewer; withSCIM: grc_engineer;
	// withSecond: grc_engineer + viewer.
	if got.MappingCount != 2 || got.DerivedRoleCount != 5 {
		t.Fatalf("GET counts = %d mappings, %d roles; want 2, 5", got.MappingCount, got.DerivedRoleCount)
	}

	rec = f.do(t, http.MethodDelete)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	var del adminsso.DeleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &del); err != nil {
		t.Fatal(err)
	}
	want := adminsso.DeleteResponse{ConfigID: f.primary.String(), MappingCount: 2, RevokedRoleCount: 5, AffectedUserCount: 3}
	if del != want {
		t.Fatalf("DELETE body = %+v; want %+v", del, want)
	}

	if r := f.roles(t, onlyPrimary); len(r) != 0 {
		t.Errorf("only-primary user roles = %v; want none", r)
	}
	if r := f.roles(t, withSCIM); len(r) != 1 || r["viewer"] != "group-derived" {
		t.Errorf("SCIM user roles = %v; want viewer via SCIM", r)
	}
	if r := f.roles(t, withSecond); len(r) != 1 || r["auditor"] != "group-derived" {
		t.Errorf("second-config user roles = %v; want auditor via the second config", r)
	}
	if n := f.count(t, `SELECT count(*) FROM oidc_idp_configs WHERE tenant_id = $1 AND id = $2`, f.tenant, f.primary); n != 0 {
		t.Errorf("primary config still present")
	}
	if n := f.count(t, `SELECT count(*) FROM oidc_idp_group_mappings WHERE tenant_id = $1`, f.tenant); n != 2 {
		t.Errorf("mappings left = %d; want the SCIM and second-config mappings (2)", n)
	}

	// One me_audit_log row naming the actor, the config and the counts.
	var userID uuid.UUID
	var principal *string
	var before, after map[string]any
	if err := f.admin.QueryRow(context.Background(),
		`SELECT user_id, actor_principal, before, after FROM me_audit_log WHERE tenant_id = $1 AND action = $2`,
		f.tenant, adminsso.AuditActionSSODelete).Scan(&userID, &principal, &before, &after); err != nil {
		t.Fatalf("read delete audit row: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = $2`, f.tenant, adminsso.AuditActionSSODelete); n != 1 {
		t.Errorf("delete audit rows = %d; want 1", n)
	}
	if userID != f.actor {
		t.Errorf("audit user_id = %s; want %s", userID, f.actor)
	}
	if principal == nil || *principal == "" {
		t.Errorf("audit actor_principal empty")
	}
	if after["config_id"] != f.primary.String() || after["mapping_count"] != float64(2) ||
		after["revoked_role_count"] != float64(5) || after["configured"] != false {
		t.Errorf("audit after = %v", after)
	}
	if before["config_id"] != f.primary.String() || before["issuer_url"] != "https://idp.example" {
		t.Errorf("audit before = %v", before)
	}

	// One revoke row per derivation the config held: 2 + 2 + 2.
	if n := f.count(t, `SELECT count(*) FROM group_role_audit_log
	        WHERE tenant_id = $1 AND change = 'revoke' AND idp_config_id = $2
	          AND detail->>'reason' = 'idp_config_delete'`, f.tenant, f.primary); n != 6 {
		t.Errorf("revoke audit rows = %d; want 6", n)
	}

	// Reconciling through the surviving sources keeps their roles.
	f.derive(t, withSCIM, uuid.Nil, grouprole.SourceSCIM, "Everyone")
	f.derive(t, withSecond, second, grouprole.SourceOIDC, "Engineering")
	if r := f.roles(t, withSCIM); r["viewer"] != "group-derived" {
		t.Errorf("SCIM user after reconcile = %v", r)
	}
	if r := f.roles(t, withSecond); r["auditor"] != "group-derived" {
		t.Errorf("second-config user after reconcile = %v", r)
	}

	if rec := f.do(t, http.MethodDelete); rec.Code != http.StatusNotFound {
		t.Errorf("second DELETE = %d; want 404", rec.Code)
	}
}

func TestDeleteSSO_RefusesToRevokeLastAdmin_2760(t *testing.T) {
	f := newDeleteFixture(t, false)
	f.seedMapping(t, f.primary, "Admins", "admin")
	onlyAdmin := f.seedMember(t)
	f.derive(t, onlyAdmin, f.primary, grouprole.SourceOIDC, "Admins")

	rec := f.do(t, http.MethodDelete)
	if rec.Code != http.StatusConflict {
		t.Fatalf("DELETE = %d %s; want 409", rec.Code, rec.Body)
	}
	if r := f.roles(t, onlyAdmin); r["admin"] != "group-derived" {
		t.Errorf("admin after refused delete = %v; want kept", r)
	}
	if n := f.count(t, `SELECT count(*) FROM oidc_idp_configs WHERE tenant_id = $1`, f.tenant); n != 1 {
		t.Errorf("config rows = %d; want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM me_audit_log WHERE tenant_id = $1`, f.tenant); n != 0 {
		t.Errorf("audit rows after refused delete = %d; want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM group_role_audit_log WHERE tenant_id = $1 AND change = 'revoke'`, f.tenant); n != 0 {
		t.Errorf("revoke audit rows after refused delete = %d; want 0", n)
	}
}

func TestDeleteSSO_RejectsNonAdmin_2760(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, false, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/admin/sso", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE as non-admin = %d; want 403", rec.Code)
	}
}
