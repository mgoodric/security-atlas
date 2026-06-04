//go:build integration

// Integration tests for the slice 205 demo seed.
// Requires Postgres reachable via DATABASE_URL (BYPASSRLS pool).
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/demoseed/...
//
// Coverage maps to slice 205 acceptance criteria:
//
//	AC-1  every primitive populated (row-count probe)
//	AC-3  > 10-row guard refuses to overwrite populated tenants
//	AC-4  idempotent on --tenant-slug
//	AC-5  scale knob (0.5x produces ~half the floors)
//	AC-6  every primitive listed in canvas §02-primitives has >= 1 row
//	AC-9  every audit-log row carries demo_seed_v = "205"
//	AC-10 1 of 3 audit periods is frozen
//	AC-18 cross-tenant isolation — a second tenant created during the
//	      same test run sees ZERO demo rows.
//	AC-19 idempotency: re-running on the same slug produces no new rows.
//	AC-20 refusal: pre-populating a tenant past the >10-row guard
//	      blocks the seeder from running.
//
// Test fixtures use only neutral `demo-*` slugs (P0-A7).

package demoseed_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

var adminPool *pgxpool.Pool

func TestMain(m *testing.M) {
	adminURL := os.Getenv("DATABASE_URL")
	if adminURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL not set; skipping demoseed integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", err)
		os.Exit(1)
	}
	adminPool = p
	code := m.Run()
	p.Close()
	os.Exit(code)
}

// cleanupTenant tears down the named tenant via the seeder's own
// Teardown method so the cleanup respects the slice-205 forensic
// mark. Used in t.Cleanup blocks.
func cleanupTenant(t *testing.T, slug string) {
	t.Helper()
	t.Cleanup(func() {
		seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
		if err != nil {
			t.Logf("cleanup NewSeeder: %v", err)
			return
		}
		ctx := context.Background()
		if err := seeder.Teardown(ctx, slug, uuid.Nil, uuid.Nil); err != nil {
			// Best-effort cleanup; teardown errors for non-seeded
			// tenants are expected when a test deliberately created
			// a manual tenant.
			t.Logf("cleanup teardown %s: %v", slug, err)
		}
		// Belt and suspenders: drop the tenant row by slug if it
		// still exists (covers manually-seeded test tenants that
		// teardown refuses to touch).
		_, _ = adminPool.Exec(ctx, `DELETE FROM tenants WHERE slug = $1`, slug)
	})
}

// rowCount returns the count of rows in `table` for the named tenant.
func rowCount(t *testing.T, table string, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	err := adminPool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1`, table),
		tenantID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestApply_Happy verifies one end-to-end seed lands every primitive
// and the row counts hit at least the per-primitive floor (AC-1, AC-5,
// AC-6).
func TestApply_Happy(t *testing.T) {
	const slug = "demo-it-happy"
	cleanupTenant(t, slug)

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	res, err := seeder.Apply(context.Background(), demoseed.ApplyInput{
		Slug:          slug,
		ActorUserID:   uuid.Nil,
		ActorTenantID: uuid.Nil,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Idempotent {
		t.Fatal("expected first apply to write rows; got idempotent")
	}

	// AC-5 row-count floors (scale=1.0).
	checks := []struct {
		Name string
		Want int
		Got  int
	}{
		{"controls", 50, res.Controls},
		{"risks", 20, res.Risks},
		{"evidence", 200, res.Evidence},
		{"policies", 5, res.Policies},
		{"vendors", 10, res.Vendors},
		{"audit_periods", 3, res.AuditPeriods},
		{"walkthroughs", 5, res.Walkthroughs},
		{"exceptions", 10, res.Exceptions},
		{"board_briefs", 2, res.BoardBriefs},
		{"framework_scopes", 3, res.FrameworkScopes},
	}
	for _, c := range checks {
		if c.Got < c.Want {
			t.Errorf("%s: got %d; want >= %d", c.Name, c.Got, c.Want)
		}
	}

	// AC-6: every primitive has at least one row when probed in the DB.
	for _, table := range []string{
		"controls", "risks", "evidence_records", "policies", "vendors",
		"audit_periods", "walkthroughs", "exceptions",
		"board_briefs", "board_packs", "framework_scopes",
		"populations", "samples", "sample_evidence",
		"risk_control_links", "me_audit_log",
	} {
		n := rowCount(t, table, res.TenantID)
		if n == 0 {
			t.Errorf("%s: row count 0 for tenant %s; AC-6 violation", table, res.TenantID)
		}
	}

	// D3 — at least 8 evidence kinds used (target was 8-12).
	if len(res.EvidenceKindsUsed) < 8 {
		t.Errorf("evidence_kinds used: got %d; want >= 8", len(res.EvidenceKindsUsed))
	}

	// AC-10: exactly one audit_period is frozen.
	var frozen int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_periods WHERE tenant_id = $1 AND status = 'frozen'`,
		res.TenantID,
	).Scan(&frozen); err != nil {
		t.Fatalf("count frozen periods: %v", err)
	}
	if frozen != 1 {
		t.Errorf("frozen audit_periods: got %d; want 1 (AC-10)", frozen)
	}

	// AC-9: every audit-log row written by us carries demo_seed_v.
	var unmarked int
	if err := adminPool.QueryRow(context.Background(), `
		SELECT count(*) FROM me_audit_log
		WHERE tenant_id = $1
		  AND NOT (after ? 'demo_seed_v')
	`, res.TenantID).Scan(&unmarked); err != nil {
		t.Fatalf("count unmarked audit rows: %v", err)
	}
	if unmarked != 0 {
		t.Errorf("audit-log rows without demo_seed_v: got %d; want 0 (AC-9)", unmarked)
	}

	// AC-12 / P0-A2: the printed password is at least 16 chars + not empty.
	if len(res.PlaintextPasswd) < 16 {
		t.Errorf("password length: got %d; want >= 16", len(res.PlaintextPasswd))
	}
}

// TestApply_Idempotent verifies AC-4 / AC-19: re-running with the
// same slug produces no new rows and reports Idempotent=true.
func TestApply_Idempotent(t *testing.T) {
	const slug = "demo-it-idempot"
	cleanupTenant(t, slug)

	seeder, _ := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	res1, err := seeder.Apply(context.Background(), demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	preCount := rowCount(t, "controls", res1.TenantID)

	res2, err := seeder.Apply(context.Background(), demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if !res2.Idempotent {
		t.Errorf("second apply: Idempotent = false; want true")
	}
	postCount := rowCount(t, "controls", res1.TenantID)
	if preCount != postCount {
		t.Errorf("controls count drifted across re-apply: pre=%d post=%d (AC-19)", preCount, postCount)
	}
}

// TestApply_RefusesPopulated verifies AC-3 / AC-20: a tenant that
// already has > 10 rows in any of (controls/risks/evidence) is refused.
func TestApply_RefusesPopulated(t *testing.T) {
	const slug = "demo-it-populated"
	cleanupTenant(t, slug)

	ctx := context.Background()
	// Pre-create the tenant + plant 11 control rows under it. The
	// seeder must refuse to run against this.
	tenantID := uuid.New()
	if _, err := adminPool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`,
		tenantID, "Pre-populated", slug,
	); err != nil {
		t.Fatalf("seed pretenant: %v", err)
	}
	for i := 0; i < 11; i++ {
		if _, err := adminPool.Exec(ctx,
			`INSERT INTO controls
			 (id, tenant_id, title, control_family, implementation_type, lifecycle_state, bundle_id)
			 VALUES (gen_random_uuid(), $1, $2, 'demo', 'automated', 'active', $3)`,
			tenantID, fmt.Sprintf("Pre-control %d", i), fmt.Sprintf("demo-pre-%d", i),
		); err != nil {
			t.Fatalf("seed precontrol: %v", err)
		}
	}

	seeder, _ := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	_, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: slug})
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	// The error message should mention the >10-row guard or the
	// "does not carry the slice-205 mark" (the manually-created
	// tenant trips the no-mark branch first since it has no audit
	// rows; either branch is correct AC-3 behavior).
	if !strings.Contains(err.Error(), "rows in controls/risks/evidence_records") &&
		!strings.Contains(err.Error(), "does not carry the slice-205") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestApply_CrossTenantIsolation verifies AC-18: a second tenant
// created during the same test run sees ZERO rows from the demo seed.
func TestApply_CrossTenantIsolation(t *testing.T) {
	const slugA = "demo-it-iso-a"
	const slugB = "demo-it-iso-b"
	cleanupTenant(t, slugA)
	cleanupTenant(t, slugB)

	seederA, _ := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	resA, err := seederA.Apply(context.Background(), demoseed.ApplyInput{Slug: slugA})
	if err != nil {
		t.Fatalf("apply A: %v", err)
	}

	// Tenant B: a manually-created tenant (no demo seed).
	ctx := context.Background()
	tenantB := uuid.New()
	if _, err := adminPool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`,
		tenantB, "Tenant B", slugB,
	); err != nil {
		t.Fatalf("seed tenant B: %v", err)
	}

	// Probe every domain table for tenant B; assert zero.
	for _, table := range []string{
		"controls", "risks", "evidence_records", "policies",
		"vendors", "audit_periods", "walkthroughs", "exceptions",
	} {
		n := rowCount(t, table, tenantB)
		if n != 0 {
			t.Errorf("tenant B saw %d rows in %s; AC-18 violation (tenant A id=%s, B id=%s)",
				n, table, resA.TenantID, tenantB)
		}
	}
}

// TestApply_Scale verifies AC-5: a scale knob below 1.0 produces fewer
// rows.
func TestApply_Scale(t *testing.T) {
	const slug = "demo-it-scale"
	cleanupTenant(t, slug)

	seeder, err := demoseed.NewSeeder(adminPool, 0.5)
	if err != nil {
		t.Fatalf("NewSeeder 0.5x: %v", err)
	}
	res, err := seeder.Apply(context.Background(), demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("Apply 0.5x: %v", err)
	}
	if res.Controls >= 50 {
		t.Errorf("0.5x scale produced %d controls; expected fewer than 50", res.Controls)
	}
	if res.Controls < 1 {
		t.Errorf("0.5x scale produced %d controls; AC-6 violation (at least one row required)", res.Controls)
	}
}

// allSeededTables enumerates every tenant-scoped table the seeder
// writes into via Apply. Seed → Teardown must return each to its
// pre-seed count for that tenant (slice 463 AC-1). super_admin_audit_log
// is intentionally excluded: it is PLATFORM-GLOBAL (no tenant_id; slice
// 142 D1) and Apply's platform-level "a demo seed happened" record is
// deliberately retained across teardown (threat-model R — repudiation).
var allSeededTables = []string{
	"controls", "risks", "risk_control_links",
	"evidence_records", "policies", "vendors",
	"audit_periods", "populations", "samples", "sample_evidence",
	"walkthroughs", "exceptions",
	"board_briefs", "board_packs", "framework_scopes",
	"frameworks", "framework_versions",
	"scope_cells", "scope_dimensions",
	"users", "local_credentials", "user_roles", "me_audit_log",
	"tenants", // probed by id, not tenant_id — handled specially below
}

// tenantStateSnapshot captures the per-table row count for one tenant.
type tenantStateSnapshot map[string]int

func snapshotTenantState(t *testing.T, tenantID uuid.UUID) tenantStateSnapshot {
	t.Helper()
	snap := make(tenantStateSnapshot, len(allSeededTables))
	for _, tbl := range allSeededTables {
		var n int
		var q string
		if tbl == "tenants" {
			q = `SELECT count(*) FROM tenants WHERE id = $1`
		} else {
			q = fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1`, tbl)
		}
		if err := adminPool.QueryRow(context.Background(), q, tenantID).Scan(&n); err != nil {
			t.Fatalf("snapshot %s: %v", tbl, err)
		}
		snap[tbl] = n
	}
	return snap
}

// TestSeedTeardown_RoundTrip is the slice-463 regression: Seeder.Teardown
// must be the exact inverse of Seeder.Apply for every tenant-scoped table
// it wrote. The original slice-205 teardown swept the six primitives +
// their child tables but omitted the tenant-scoped fallback `frameworks`
// + `framework_versions` rows that Apply writes when the global SCF
// catalog is absent — leaving orphans on every teardown against a
// catalog-less DB (the integration DB, and any install without the
// bundled catalog). This test pins the full round-trip.
//
//	AC-1  Seed → Teardown returns every seeded table to its pre-seed count.
//	AC-3  FK ordering honored (no ON DELETE RESTRICT violation — the run
//	      simply succeeds without error).
//	AC-4  Teardown is keyed on tenant_id / id; the global catalog
//	      (tenant_id IS NULL) is untouched, asserted explicitly below.
func TestSeedTeardown_RoundTrip(t *testing.T) {
	const slug = "demo-it-roundtrip"
	cleanupTenant(t, slug)

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	ctx := context.Background()

	// Record the global SCF-catalog framework_versions count (tenant_id
	// IS NULL) before we touch anything; teardown must never change it
	// (AC-4 — no cross-boundary delete).
	globalCatalogCount := func() int {
		var n int
		if err := adminPool.QueryRow(ctx,
			`SELECT count(*) FROM framework_versions WHERE tenant_id IS NULL`,
		).Scan(&n); err != nil {
			t.Fatalf("global catalog count: %v", err)
		}
		return n
	}
	globalBefore := globalCatalogCount()

	res, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Idempotent {
		t.Fatal("expected first apply to write rows; got idempotent")
	}
	tenantID := res.TenantID

	// Sanity: the seed actually populated the tables (otherwise the
	// round-trip would pass vacuously).
	postSeed := snapshotTenantState(t, tenantID)
	for _, tbl := range []string{"controls", "risks", "frameworks", "framework_versions", "tenants"} {
		if postSeed[tbl] == 0 {
			t.Fatalf("post-seed %s = 0; seed did not populate it (test would pass vacuously)", tbl)
		}
	}

	// Teardown must succeed (AC-3: no FK-RESTRICT violation).
	if err := seeder.Teardown(ctx, slug, uuid.Nil, uuid.Nil); err != nil {
		t.Fatalf("Teardown: %v (AC-3 FK-ordering or AC-1 leak)", err)
	}

	// AC-1: every seeded table is back to zero rows for this tenant.
	postTeardown := snapshotTenantState(t, tenantID)
	for _, tbl := range allSeededTables {
		if postTeardown[tbl] != 0 {
			t.Errorf("AC-1 leak: %s has %d rows for tenant %s after teardown; Teardown is not the inverse of Seed",
				tbl, postTeardown[tbl], tenantID)
		}
	}

	// AC-4: the global SCF catalog (tenant_id IS NULL) is untouched.
	if globalAfter := globalCatalogCount(); globalAfter != globalBefore {
		t.Errorf("AC-4 violation: global framework_versions catalog count changed %d → %d across teardown",
			globalBefore, globalAfter)
	}
}

// TestSeedTeardown_Idempotent verifies slice-463 AC-2: Teardown is
// idempotent. A second Teardown against an already-torn-down tenant, and
// a Teardown against a never-seeded slug, both return an error WITHOUT
// leaving partial state or panicking (the seeder refuses non-seeded
// tenants by design — that refusal is the idempotent no-op, not a wedge).
func TestSeedTeardown_Idempotent(t *testing.T) {
	const slug = "demo-it-td-idem"
	cleanupTenant(t, slug)

	seeder, _ := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	ctx := context.Background()

	res, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// First teardown: succeeds, removes everything.
	if err := seeder.Teardown(ctx, slug, uuid.Nil, uuid.Nil); err != nil {
		t.Fatalf("first Teardown: %v", err)
	}

	// Second teardown: the tenant no longer exists, so Teardown returns a
	// "not found" error. The contract is no-error-state-change: assert the
	// error is the benign not-found, and that no tenant row resurrected.
	err = seeder.Teardown(ctx, slug, uuid.Nil, uuid.Nil)
	if err == nil {
		t.Error("second Teardown on a removed tenant: expected not-found error; got nil")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("second Teardown: unexpected error: %v (want not-found)", err)
	}

	// Teardown against a never-seeded slug also errors benignly and leaves
	// no state.
	if err := seeder.Teardown(ctx, "demo-it-never-seeded", uuid.Nil, uuid.Nil); err == nil {
		t.Error("Teardown on never-seeded slug: expected not-found error; got nil")
	}

	// No tenant row resurrected for the original id.
	var n int
	if err := adminPool.QueryRow(ctx,
		`SELECT count(*) FROM tenants WHERE id = $1`, res.TenantID).Scan(&n); err != nil {
		t.Fatalf("post double-teardown tenant count: %v", err)
	}
	if n != 0 {
		t.Errorf("tenant row count after double teardown = %d; want 0 (AC-2)", n)
	}
}

// TestApply_RejectsInvalidSlug verifies the input validator catches
// non-canonical slugs before any DB work.
func TestApply_RejectsInvalidSlug(t *testing.T) {
	seeder, _ := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	for _, bad := range []string{
		"",           // empty
		"Demo-UPPER", // upper-case
		"-leading",   // leading hyphen
		"demo with space",
	} {
		_, err := seeder.Apply(context.Background(), demoseed.ApplyInput{Slug: bad})
		if err == nil {
			t.Errorf("slug %q: expected error; got nil", bad)
		}
	}
}
