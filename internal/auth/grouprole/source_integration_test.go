//go:build integration

// #2760: group-derived roles are scoped by source. A reconcile through one
// source (an OIDC config, or SCIM) diffs only the roles that source derived,
// so it never revokes a role another source derived; a role two sources
// derive stays until both drop it. RevokeConfigRoles removes one config's
// derivations ahead of the config delete.
package grouprole_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func derive(t *testing.T, ctx context.Context, r *grouprole.Resolver, user string, cfg uuid.UUID, src grouprole.Source, groups ...string) grouprole.DeriveResult {
	t.Helper()
	res, err := r.Derive(ctx, grouprole.DeriveInput{UserID: user, IDPConfigID: cfg, Groups: groups, Source: src})
	if err != nil {
		t.Fatalf("derive %s via %v: %v", user, cfg, err)
	}
	return res
}

func sourceCount(t *testing.T, tenant uuid.UUID, user, role string) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM user_role_group_sources WHERE tenant_id = $1 AND user_id = $2 AND role = $3`,
		tenant, user, role).Scan(&n); err != nil {
		t.Fatalf("sourceCount: %v", err)
	}
	return n
}

func TestDerive_OtherSourceDoesNotRevoke_2760(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-2760-other")
	cfg := seedIDPConfig(t, tenant, "okta")
	seedMapping(t, tenant, cfg, "Engineering", "grc_engineer")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	user := uuid.New().String()

	derive(t, ctx, r, user, cfg, grouprole.SourceOIDC, "Engineering")
	// A SCIM reconcile with no groups: SCIM never derived grc_engineer, so
	// it has nothing to revoke. Before #2760 this revoked it.
	res := derive(t, ctx, r, user, uuid.Nil, grouprole.SourceSCIM)
	if len(res.Revoked) != 0 {
		t.Fatalf("SCIM reconcile revoked %v; want nothing", res.Revoked)
	}
	if got := rolesFor(t, tenant, user); got["grc_engineer"] != "group-derived" {
		t.Fatalf("roles after SCIM reconcile = %v; want grc_engineer kept", got)
	}

	// The config's own reconcile with no groups does revoke it.
	res = derive(t, ctx, r, user, cfg, grouprole.SourceOIDC)
	if len(res.Revoked) != 1 || res.Revoked[0] != "grc_engineer" {
		t.Fatalf("config reconcile revoked %v; want [grc_engineer]", res.Revoked)
	}
	if got := rolesFor(t, tenant, user); len(got) != 0 {
		t.Fatalf("roles after config reconcile = %v; want none", got)
	}
}

func TestDerive_SharedRoleStaysUntilEverySourceDrops_2760(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-2760-shared")
	cfg := seedIDPConfig(t, tenant, "okta")
	seedMapping(t, tenant, cfg, "Engineering", "viewer")
	seedMapping(t, tenant, uuid.Nil, "Everyone", "viewer")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	user := uuid.New().String()

	derive(t, ctx, r, user, cfg, grouprole.SourceOIDC, "Engineering")
	// SCIM also derives viewer: a second source on the same role is
	// recorded, and is a grant in that source's audit trail.
	res := derive(t, ctx, r, user, uuid.Nil, grouprole.SourceSCIM, "Everyone")
	if len(res.Granted) != 1 || res.Granted[0] != "viewer" {
		t.Fatalf("SCIM granted %v; want [viewer]", res.Granted)
	}
	if n := sourceCount(t, tenant, user, "viewer"); n != 2 {
		t.Fatalf("viewer sources = %d; want 2", n)
	}

	derive(t, ctx, r, user, uuid.Nil, grouprole.SourceSCIM)
	if got := rolesFor(t, tenant, user); got["viewer"] != "group-derived" {
		t.Fatalf("roles after SCIM drops viewer = %v; want viewer kept via the config", got)
	}
	derive(t, ctx, r, user, cfg, grouprole.SourceOIDC)
	if got := rolesFor(t, tenant, user); len(got) != 0 {
		t.Fatalf("roles after both sources drop viewer = %v; want none", got)
	}
}

func TestDerive_LastAdminGuardCountsOtherSource_2760(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-2760-admin")
	cfg := seedIDPConfig(t, tenant, "okta")
	seedMapping(t, tenant, cfg, "Admins", "admin")
	seedMapping(t, tenant, uuid.Nil, "Admins", "admin")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	user := seedMember(t, tenant, "active")

	derive(t, ctx, r, user, cfg, grouprole.SourceOIDC, "Admins")
	derive(t, ctx, r, user, uuid.Nil, grouprole.SourceSCIM, "Admins")
	// The tenant's only admin; SCIM drops admin, but the config still
	// derives it, so the user stays admin and the revoke is not suppressed.
	res := derive(t, ctx, r, user, uuid.Nil, grouprole.SourceSCIM)
	if len(res.SuppressedRevokes) != 0 || len(res.Revoked) != 1 {
		t.Fatalf("SCIM revoke = %+v; want admin revoked for SCIM, not suppressed", res)
	}
	if got := rolesFor(t, tenant, user); got["admin"] != "group-derived" {
		t.Fatalf("roles = %v; want admin kept via the config", got)
	}
	// Now the config is the last source of the last admin: suppressed.
	res = derive(t, ctx, r, user, cfg, grouprole.SourceOIDC)
	if len(res.SuppressedRevokes) != 1 {
		t.Fatalf("config revoke = %+v; want suppressed by the last-admin guard", res)
	}
}

func TestRevokeConfigRoles_2760(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-2760-revoke")
	cfg := seedIDPConfig(t, tenant, "okta")
	other := seedIDPConfig(t, tenant, "entra")
	seedMapping(t, tenant, cfg, "Engineering", "grc_engineer")
	seedMapping(t, tenant, cfg, "Engineering", "viewer")
	seedMapping(t, tenant, other, "Engineering", "viewer")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)

	only := uuid.New().String()   // the config is the only source
	second := uuid.New().String() // a second config also derives viewer
	manual := uuid.New().String() // holds viewer manually too
	derive(t, ctx, r, only, cfg, grouprole.SourceOIDC, "Engineering")
	derive(t, ctx, r, second, cfg, grouprole.SourceOIDC, "Engineering")
	derive(t, ctx, r, second, other, grouprole.SourceOIDC, "Engineering")
	grantManualRole(t, tenant, manual, "viewer")
	derive(t, ctx, r, manual, cfg, grouprole.SourceOIDC, "Engineering")

	var preview grouprole.ConfigDeletePreview
	var rev grouprole.ConfigRevocation
	inTenantTx(t, ctx, func(q *dbx.Queries) {
		var err error
		preview, err = grouprole.PreviewConfigDelete(ctx, q, tenant, cfg)
		if err != nil {
			t.Fatal(err)
		}
		rev, err = grouprole.RevokeConfigRoles(ctx, q, tenant, cfg)
		if err != nil {
			t.Fatal(err)
		}
	})
	// only: grc_engineer + viewer; second: grc_engineer; manual: grc_engineer.
	if preview.MappingCount != 2 || preview.DerivedRoleCount != 4 {
		t.Errorf("preview = %+v; want 2 mappings, 4 roles", preview)
	}
	if rev.Derivations != 6 || rev.RevokedRoles != 4 || rev.AffectedUsers != 3 {
		t.Errorf("revocation = %+v; want 6 derivations, 4 roles, 3 users", rev)
	}
	if got := rolesFor(t, tenant, only); len(got) != 0 {
		t.Errorf("only-source user roles = %v; want none", got)
	}
	if got := rolesFor(t, tenant, second); len(got) != 1 || got["viewer"] != "group-derived" {
		t.Errorf("second-source user roles = %v; want viewer via the other config", got)
	}
	if got := rolesFor(t, tenant, manual); len(got) != 1 || got["viewer"] != "manual" {
		t.Errorf("manual user roles = %v; want the manual viewer only", got)
	}
	var audited int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM group_role_audit_log
		  WHERE tenant_id = $1 AND change = 'revoke' AND idp_config_id = $2
		    AND detail->>'reason' = 'idp_config_delete'`, tenant, cfg).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 6 {
		t.Errorf("config-delete revoke audit rows = %d; want 6", audited)
	}
}

func inTenantTx(t *testing.T, ctx context.Context, fn func(*dbx.Queries)) {
	t.Helper()
	tx, err := appPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatal(err)
	}
	fn(dbx.New(tx))
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
