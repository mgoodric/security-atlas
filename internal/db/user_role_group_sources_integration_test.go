//go:build integration

// #2760 — 20261272000000_user_role_group_sources.sql backfills a source row
// for each existing group-derived role from the latest group_role_audit_log
// entry per source: a source whose latest entry is a grant gets a row, one
// whose latest entry is a revoke does not, and a role with no grant on
// record, or granted by a config that no longer exists, gets none.

package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	userRoleGroupSourcesUp   = "../../migrations/sql/20261272000000_user_role_group_sources.sql"
	userRoleGroupSourcesDown = "../../migrations/sql/20261272000000_user_role_group_sources.down.sql"
)

func TestUserRoleGroupSourcesMigration_Backfill(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(userRoleGroupSourcesUp)
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(userRoleGroupSourcesDown)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Rolled back, so the schema and data are untouched for later tests.
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%q: %v", sql, err)
		}
	}

	exec(string(down))

	tenant := uuid.New()
	cfg := uuid.New()
	gone := uuid.New() // a config that was deleted by SQL
	exec(`INSERT INTO tenants (id, name) VALUES ($1, $2)`, tenant, "urgs-2760-"+tenant.String()[:8])
	exec(`INSERT INTO oidc_idp_configs (id, tenant_id, name, issuer_url, client_id, client_secret_enc, redirect_url)
	      VALUES ($1, $2, 'primary', 'https://idp.example', 'cid', '\x00', 'https://app/cb')`, cfg, tenant)

	const (
		granted  = "u-granted"  // config grant on record
		reGrant  = "u-regrant"  // config grant then revoke, then SCIM grant
		noRecord = "u-norecord" // group-derived, no audit entry at all
		goneCfg  = "u-gonecfg"  // granted by a config that no longer exists
		manual   = "u-manual"   // manual role: never backfilled
	)
	for _, u := range []string{granted, reGrant, noRecord, goneCfg} {
		exec(`INSERT INTO user_roles (tenant_id, user_id, role, origin) VALUES ($1, $2, 'viewer', 'group-derived')`, tenant, u)
	}
	exec(`INSERT INTO user_roles (tenant_id, user_id, role, origin) VALUES ($1, $2, 'viewer', 'manual')`, tenant, manual)

	audit := func(user, change, source string, cfgID any, minutesAgo int) {
		t.Helper()
		exec(`INSERT INTO group_role_audit_log (tenant_id, occurred_at, user_id, role, change, source, idp_config_id)
		      VALUES ($1, now() - make_interval(mins => $2), $3, 'viewer', $4, $5, $6)`,
			tenant, minutesAgo, user, change, source, cfgID)
	}
	audit(granted, "grant", "oidc", cfg, 10)
	audit(reGrant, "grant", "oidc", cfg, 30)
	audit(reGrant, "revoke", "oidc", cfg, 20)
	audit(reGrant, "grant", "scim", nil, 10)
	audit(goneCfg, "grant", "oidc", gone, 10)
	audit(manual, "grant", "oidc", cfg, 10)

	exec(string(up))

	rows, err := tx.Query(ctx,
		`SELECT user_id, COALESCE(idp_config_id::text, 'scim') FROM user_role_group_sources
		  WHERE tenant_id = $1 ORDER BY user_id`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for rows.Next() {
		var u, src string
		if err := rows.Scan(&u, &src); err != nil {
			t.Fatal(err)
		}
		got[u] = append(got[u], src)
	}
	rows.Close()

	want := map[string][]string{
		granted: {cfg.String()},
		reGrant: {"scim"},
	}
	if len(got) != len(want) {
		t.Fatalf("backfilled sources = %v; want %v", got, want)
	}
	for u, srcs := range want {
		if len(got[u]) != len(srcs) || got[u][0] != srcs[0] {
			t.Errorf("%s sources = %v; want %v", u, got[u], srcs)
		}
	}
}
