//go:build integration

// Integration tests for the slice 509 group-to-role resolver. These are the
// security proofs — each maps to an AC / P0 from docs/issues/509-*.md:
//
//   - AC-2:           OIDC-claim-derived and SCIM-Group-derived roles flow
//     through the SAME resolver (identical mapping logic; the
//     only difference is the source label + idp_config scoping).
//   - AC-3/P0-509-1:  an unmapped group contributes no role (fail-closed); a
//     user in multiple mapped groups gets the union.
//   - AC-4:           a manual admin-assigned role survives re-derivation.
//   - AC-5/P0-509-3:  the last-admin guard blocks a re-derivation that would
//     remove the tenant's final admin.
//   - AC-6:           multi-IdP — a tenant with two IdP configs maps each IdP's
//     groups independently.
//   - AC-7:           every group-derived role change writes an append-only
//     audit row capturing the triggering group + source.
//   - P0-509-4:       a mapping to a non-existent role is rejected.
//   - RLS:            tenant-A groups never derive tenant-B roles.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) + DATABASE_URL (atlas_migrate,
// BYPASSRLS, for seeding + cleanup).
package grouprole_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	if appURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping grouprole integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, appURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New app: %v\n", err)
		os.Exit(1)
	}
	appPool = p
	if adminURL := os.Getenv("DATABASE_URL"); adminURL != "" {
		a, aerr := pgxpool.New(ctx, adminURL)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", aerr)
			os.Exit(1)
		}
		adminPool = a
	}
	code := m.Run()
	p.Close()
	if adminPool != nil {
		adminPool.Close()
	}
	os.Exit(code)
}

func requireAdminPool(t *testing.T) {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL (atlas_migrate) not set; skipping test that needs BYPASSRLS")
	}
}

func seedTenant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	requireAdminPool(t)
	id := uuid.New()
	// Tenant names carry a unique-lower-name index; suffix with the id so
	// re-runs (and parallel cases sharing a base name) never collide.
	uniqueName := name + "-" + id.String()[:8]
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, uniqueName); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, table := range []string{
			"group_role_audit_log", "oidc_idp_group_mappings", "user_roles",
			"oidc_idp_configs", "users",
		} {
			_, _ = adminPool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE tenant_id = $1`, table), id)
		}
		_, _ = adminPool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

func tenantCtx(t *testing.T, tenantID uuid.UUID) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("withTenant: %v", err)
	}
	return ctx
}

// seedMapping inserts a (group -> role) mapping via BYPASSRLS. idpConfig may be
// uuid.Nil (SCIM / IdP-agnostic source).
func seedMapping(t *testing.T, tenantID uuid.UUID, idpConfig uuid.UUID, group, role string) {
	t.Helper()
	var cfg any
	if idpConfig != uuid.Nil {
		cfg = idpConfig
	}
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO oidc_idp_group_mappings (tenant_id, idp_config_id, group_ref, role)
		 VALUES ($1, $2, $3, $4)`, tenantID, cfg, group, role); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
}

// seedIDPConfig inserts a minimal oidc_idp_configs row and returns its id.
func seedIDPConfig(t *testing.T, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO oidc_idp_configs (id, tenant_id, name, issuer_url, client_id, client_secret_enc, redirect_url)
		 VALUES ($1, $2, $3, 'https://idp.example', 'cid', '\x00', 'https://app/cb')`,
		id, tenantID, name); err != nil {
		t.Fatalf("seed idp config: %v", err)
	}
	return id
}

// grantManualRole inserts a manual (origin='manual') user_roles row.
func grantManualRole(t *testing.T, tenantID uuid.UUID, userID, role string) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by, origin)
		 VALUES ($1, $2, $3, 'admin:test', 'manual')
		 ON CONFLICT (tenant_id, user_id, role) DO NOTHING`,
		tenantID, userID, role); err != nil {
		t.Fatalf("grant manual role: %v", err)
	}
}

// rolesFor returns the user's roles with their origins, sorted.
func rolesFor(t *testing.T, tenantID uuid.UUID, userID string) map[string]string {
	t.Helper()
	rows, err := adminPool.Query(context.Background(),
		`SELECT role, origin FROM user_roles WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID)
	if err != nil {
		t.Fatalf("rolesFor: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var role, origin string
		if err := rows.Scan(&role, &origin); err != nil {
			t.Fatalf("scan role: %v", err)
		}
		out[role] = origin
	}
	return out
}

func auditCount(t *testing.T, tenantID uuid.UUID, userID, change string) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM group_role_audit_log
		  WHERE tenant_id = $1 AND user_id = $2 AND change = $3`,
		tenantID, userID, change).Scan(&n); err != nil {
		t.Fatalf("auditCount: %v", err)
	}
	return n
}

// --- AC-3 / P0-509-1: fail-closed + union ---

func TestDerive_UnmappedGroupFailsClosed_AndUnion(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-failclosed")
	seedMapping(t, tenant, uuid.Nil, "SecurityTeam", "grc_engineer")
	seedMapping(t, tenant, uuid.Nil, "Auditors", "auditor")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	user := uuid.New().String()

	// User in two MAPPED groups + one UNMAPPED group.
	res, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user,
		Groups: []string{"SecurityTeam", "Auditors", "Marketing"}, // Marketing unmapped
		Source: grouprole.SourceSCIM,
	})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	// Union of the two mapped groups; Marketing contributes nothing.
	got := rolesFor(t, tenant, user)
	if got["grc_engineer"] != "group-derived" || got["auditor"] != "group-derived" {
		t.Fatalf("expected union grc_engineer+auditor group-derived, got %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("unmapped group leaked a role: %v", got)
	}
	if len(res.Granted) != 2 {
		t.Fatalf("granted = %v; want 2", res.Granted)
	}
}

// --- AC-4: manual roles survive re-derivation ---

func TestDerive_ManualRoleSurvivesRederivation(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-manual")
	seedMapping(t, tenant, uuid.Nil, "Eng", "control_owner")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	user := uuid.New().String()

	// User holds 'viewer' MANUALLY.
	grantManualRole(t, tenant, user, "viewer")

	// First derivation grants control_owner (group-derived).
	if _, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user, Groups: []string{"Eng"}, Source: grouprole.SourceOIDC,
	}); err != nil {
		t.Fatalf("derive 1: %v", err)
	}

	// Re-derivation with NO groups (user removed from all groups): the
	// group-derived control_owner revokes, but the manual viewer SURVIVES.
	if _, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user, Groups: nil, Source: grouprole.SourceOIDC,
	}); err != nil {
		t.Fatalf("derive 2: %v", err)
	}
	got := rolesFor(t, tenant, user)
	if got["viewer"] != "manual" {
		t.Fatalf("manual viewer did not survive: %v", got)
	}
	if _, stillDerived := got["control_owner"]; stillDerived {
		t.Fatalf("group-derived control_owner should have been revoked: %v", got)
	}
}

// --- AC-5 / P0-509-3: last-admin guard ---

func TestDerive_LastAdminGuardBlocksRevoke(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-lastadmin")
	seedMapping(t, tenant, uuid.Nil, "Admins", "admin")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	user := uuid.New().String()

	// Group-derive admin for the sole user.
	if _, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user, Groups: []string{"Admins"}, Source: grouprole.SourceSCIM,
	}); err != nil {
		t.Fatalf("derive 1: %v", err)
	}
	if rolesFor(t, tenant, user)["admin"] != "group-derived" {
		t.Fatal("admin not group-derived after first derive")
	}

	// Now the Admins group is removed (unmapped membership). Re-derivation would
	// revoke the ONLY admin — the guard must suppress it.
	res, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user, Groups: nil, Source: grouprole.SourceSCIM,
	})
	if err != nil {
		t.Fatalf("derive 2: %v", err)
	}
	if got := rolesFor(t, tenant, user); got["admin"] != "group-derived" {
		t.Fatalf("last admin was stranded! roles=%v", got)
	}
	if len(res.SuppressedRevokes) != 1 || res.SuppressedRevokes[0] != "admin" {
		t.Fatalf("expected suppressed admin revoke, got %v", res.SuppressedRevokes)
	}
}

func TestDerive_AdminRevokeAllowedWhenSecondAdminExists(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-twoadmins")
	seedMapping(t, tenant, uuid.Nil, "Admins", "admin")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	userA := uuid.New().String()
	userB := uuid.New().String()

	// userB holds admin manually (a second admin).
	grantManualRole(t, tenant, userB, "admin")
	// userA group-derives admin.
	if _, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: userA, Groups: []string{"Admins"}, Source: grouprole.SourceSCIM,
	}); err != nil {
		t.Fatalf("derive 1: %v", err)
	}

	// userA loses the group: revoke is allowed because userB remains admin.
	res, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: userA, Groups: nil, Source: grouprole.SourceSCIM,
	})
	if err != nil {
		t.Fatalf("derive 2: %v", err)
	}
	if _, stillAdmin := rolesFor(t, tenant, userA)["admin"]; stillAdmin {
		t.Fatalf("userA admin should have been revoked (second admin exists)")
	}
	if len(res.Revoked) != 1 || res.Revoked[0] != "admin" {
		t.Fatalf("expected admin revoke, got %v", res.Revoked)
	}
}

// --- AC-6: multi-IdP independence ---

func TestDerive_MultiIDPIndependent(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-multiidp")
	cfgA := seedIDPConfig(t, tenant, "okta")
	cfgB := seedIDPConfig(t, tenant, "entra")
	// SAME group name "Engineering" maps to DIFFERENT roles per IdP config.
	seedMapping(t, tenant, cfgA, "Engineering", "grc_engineer")
	seedMapping(t, tenant, cfgB, "Engineering", "viewer")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	user := uuid.New().String()

	// Login via cfgA → grc_engineer ONLY (cfgB's mapping must not apply).
	res, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user, IDPConfigID: cfgA, Groups: []string{"Engineering"}, Source: grouprole.SourceOIDC,
	})
	if err != nil {
		t.Fatalf("derive cfgA: %v", err)
	}
	if len(res.ResolvedRoles) != 1 || res.ResolvedRoles[0] != "grc_engineer" {
		t.Fatalf("cfgA resolved = %v; want [grc_engineer]", res.ResolvedRoles)
	}
	got := rolesFor(t, tenant, user)
	if _, leaked := got["viewer"]; leaked {
		t.Fatalf("cfgB mapping leaked into cfgA derivation: %v", got)
	}
}

// --- AC-7: audit rows ---

func TestDerive_WritesAuditRows(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-audit")
	seedMapping(t, tenant, uuid.Nil, "SecTeam", "auditor")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)
	user := uuid.New().String()

	if _, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user, Groups: []string{"SecTeam"}, Source: grouprole.SourceSCIM,
	}); err != nil {
		t.Fatalf("derive grant: %v", err)
	}
	if auditCount(t, tenant, user, "grant") != 1 {
		t.Fatalf("expected 1 grant audit row")
	}
	// Verify the triggering group + source were captured.
	var group, source string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT triggering_group, source FROM group_role_audit_log
		  WHERE tenant_id=$1 AND user_id=$2 AND change='grant'`,
		tenant, user).Scan(&group, &source); err != nil {
		t.Fatalf("audit detail: %v", err)
	}
	if group != "SecTeam" || source != "scim" {
		t.Fatalf("audit captured group=%q source=%q; want SecTeam/scim", group, source)
	}

	if _, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user, Groups: nil, Source: grouprole.SourceSCIM,
	}); err != nil {
		t.Fatalf("derive revoke: %v", err)
	}
	if auditCount(t, tenant, user, "revoke") != 1 {
		t.Fatalf("expected 1 revoke audit row")
	}
}

// --- AC-2: OIDC + SCIM go through the SAME resolver with identical mapping ---

func TestDerive_BothSourcesIdenticalMapping(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-bothsrc")
	// SCIM-source mapping (idp_config NULL) maps "X"->viewer.
	seedMapping(t, tenant, uuid.Nil, "X", "viewer")
	r := grouprole.NewResolver(appPool)
	ctx := tenantCtx(t, tenant)

	scimUser := uuid.New().String()
	if _, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: scimUser, Groups: []string{"X"}, Source: grouprole.SourceSCIM,
	}); err != nil {
		t.Fatalf("scim derive: %v", err)
	}
	// OIDC-source with idp_config NULL resolves the SAME mapping (the resolver
	// logic is shared; only the source label + idp scoping differ).
	oidcUser := uuid.New().String()
	if _, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: oidcUser, Groups: []string{"X"}, Source: grouprole.SourceOIDC,
	}); err != nil {
		t.Fatalf("oidc derive: %v", err)
	}
	if rolesFor(t, tenant, scimUser)["viewer"] != "group-derived" ||
		rolesFor(t, tenant, oidcUser)["viewer"] != "group-derived" {
		t.Fatal("both sources must derive viewer via the same mapping")
	}
}

// --- RLS: tenant-A groups never derive tenant-B roles ---

func TestDerive_CrossTenantIsolation(t *testing.T) {
	requireAdminPool(t)
	tenantA := seedTenant(t, "gr-rls-A")
	tenantB := seedTenant(t, "gr-rls-B")
	// Mapping exists ONLY in tenant A.
	seedMapping(t, tenantA, uuid.Nil, "Shared", "admin")
	r := grouprole.NewResolver(appPool)
	user := uuid.New().String()

	// Derive under tenant B's context with the SAME group name. Tenant B has no
	// mapping → fail-closed → no role. RLS confines the lookup to tenant B.
	res, err := r.Derive(tenantCtx(t, tenantB), grouprole.DeriveInput{
		UserID: user, Groups: []string{"Shared"}, Source: grouprole.SourceSCIM,
	})
	if err != nil {
		t.Fatalf("derive tenantB: %v", err)
	}
	if len(res.ResolvedRoles) != 0 {
		t.Fatalf("tenant-A mapping leaked into tenant-B derivation: %v", res.ResolvedRoles)
	}
	if len(rolesFor(t, tenantB, user)) != 0 {
		t.Fatal("tenant B user must have no roles (cross-tenant isolation)")
	}
}

// --- P0-509-4: mapping to a non-existent role is rejected ---

func TestValidateMappingRole_RejectsUnknown(t *testing.T) {
	t.Parallel()
	if err := grouprole.ValidateMappingRole("superuser"); err == nil {
		t.Fatal("expected unknown role rejected (P0-509-4)")
	}
	for _, role := range []string{"admin", "grc_engineer", "control_owner", "auditor", "viewer"} {
		if err := grouprole.ValidateMappingRole(role); err != nil {
			t.Fatalf("canonical role %q rejected: %v", role, err)
		}
	}
}
