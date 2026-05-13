//go:build integration

// Integration tests for slice 053: risk theme tagging + manual aggregation
// API + org_units CRUD. Real Postgres only — RLS cannot be tested against a
// fake DB.
//
// Run with:
//
//	go test -tags=integration -race ./internal/risk/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (postgres /
// admin role for fixture seeding).

package risk_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness helpers -----

func slice053AppDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func slice053AdminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func slice053OpenPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	// Register cleanup via t.Cleanup so it runs in LIFO order with
	// other Cleanup callbacks — tenant deletes (registered later by
	// freshTenant) run BEFORE the pool closes.
	t.Cleanup(func() { pool.Close() })
	return pool
}

func slice053FreshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM risk_aggregations WHERE tenant_id = $1`,
			`DELETE FROM risk_control_links WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
			`DELETE FROM org_units WHERE tenant_id = $1`,
			`DELETE FROM org_themes WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func slice053CtxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// seedRisk inserts a risk directly via admin pool with the given (L,I) on a
// nist_800_30 methodology so it's eligible for aggregation.
func seedAggregableRisk(t *testing.T, admin *pgxpool.Pool, tenant string, title string, likelihood, impact int, themes []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if themes == nil {
		themes = []string{}
	}
	score := map[string]int{"likelihood": likelihood, "impact": impact}
	scoreB, _ := json.Marshal(score)
	_, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, category, methodology, inherent_score,
			treatment, treatment_owner, residual_score, accepter,
			instrument_reference, level, themes
		) VALUES (
			$1, $2, $3, 'confidentiality', 'nist_800_30', $4,
			'avoid', '', '{}', '', '', 'team', $5
		)`, id, tenant, title, scoreB, themes)
	if err != nil {
		t.Fatalf("seed aggregable risk: %v", err)
	}
	return id
}

// seedTenantTheme inserts a tenant-private theme.
func seedTenantTheme(t *testing.T, admin *pgxpool.Pool, tenant string, name, desc string) {
	t.Helper()
	_, err := admin.Exec(context.Background(), `
		INSERT INTO org_themes (id, tenant_id, theme_name, description)
		VALUES ($1, $2, $3, $4)`, uuid.New(), tenant, name, desc)
	if err != nil {
		t.Fatalf("seed tenant theme: %v", err)
	}
}

// ================================================================
// AC-10 (run first — most important per brief): cross-tenant child denial.
// ================================================================

func TestAggregate_CrossTenantChildDenial_AC10(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))

	tenantA := slice053FreshTenant(t, admin)
	tenantB := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)

	// Seed a child risk under tenant B.
	foreignChild := seedAggregableRisk(t, admin, tenantB, "tenant-B risk", 3, 3, nil)
	// Seed a local child under tenant A.
	localChild := seedAggregableRisk(t, admin, tenantA, "tenant-A risk", 4, 4, nil)

	// As tenant A, try to aggregate including tenant B's child.
	ctx := slice053CtxFor(t, tenantA)
	_, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Cross-tenant attempt",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: risk.SeverityFunctionMax,
		ChildRiskIDs:     []uuid.UUID{localChild, foreignChild},
	})
	if !errors.Is(err, risk.ErrChildrenNotFound) {
		t.Fatalf("expected ErrChildrenNotFound, got %v", err)
	}

	// Sanity: aggregating with only the local child succeeds.
	res, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Local only",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: risk.SeverityFunctionMax,
		ChildRiskIDs:     []uuid.UUID{localChild},
	})
	if err != nil {
		t.Fatalf("local-only aggregate: %v", err)
	}
	if res.Severity != 16 {
		t.Fatalf("local-only severity: got %d, want 16", res.Severity)
	}
}

// ================================================================
// AC-1..AC-3: theme assignment + GET /v1/themes
// ================================================================

func TestAssignThemes_DefaultVocab_AC1(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	riskID := seedAggregableRisk(t, admin, tenant, "needs themes", 3, 3, nil)
	updated, err := store.AssignThemes(ctx, riskID, []string{"ownership", "access-control"})
	if err != nil {
		t.Fatalf("AssignThemes: %v", err)
	}
	if len(updated.Themes) != 2 {
		t.Fatalf("expected 2 themes, got %d (%v)", len(updated.Themes), updated.Themes)
	}
	// Themes stored in canonical (sorted) order.
	if updated.Themes[0] != "access-control" || updated.Themes[1] != "ownership" {
		t.Fatalf("themes not in canonical order: %v", updated.Themes)
	}
}

func TestAssignThemes_RejectUnknown_AC1_AC25(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	riskID := seedAggregableRisk(t, admin, tenant, "rejects unknown", 2, 2, nil)
	_, err := store.AssignThemes(ctx, riskID, []string{"ownership", "made-up-theme"})
	if !errors.Is(err, risk.ErrUnknownTheme) {
		t.Fatalf("expected ErrUnknownTheme, got %v", err)
	}
}

func TestAssignThemes_TenantPrivateAccepted_AC1(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	seedTenantTheme(t, admin, tenant, "org-private:fintech", "tenant-private theme")
	riskID := seedAggregableRisk(t, admin, tenant, "tenant-private theme", 2, 2, nil)
	updated, err := store.AssignThemes(ctx, riskID, []string{"org-private:fintech"})
	if err != nil {
		t.Fatalf("AssignThemes tenant-private: %v", err)
	}
	if len(updated.Themes) != 1 || updated.Themes[0] != "org-private:fintech" {
		t.Fatalf("tenant-private theme not stored: %v", updated.Themes)
	}
}

func TestListVisibleThemes_DefaultsPlusTenantPrivate_AC3(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	seedTenantTheme(t, admin, tenant, "org-private:zeta", "tenant-private")
	themes, err := store.ListVisibleThemes(ctx)
	if err != nil {
		t.Fatalf("ListVisibleThemes: %v", err)
	}
	// 10 defaults + 1 tenant-private = 11.
	if len(themes) != 11 {
		t.Fatalf("expected 11 themes (10 defaults + 1 tenant), got %d: %+v", len(themes), themes)
	}
	// Alphabetical: "access-control" should be first, "org-private:zeta" last
	// (because lexicographically `o` < `s` < `t` < `v` and `z` is the latest letter).
	if themes[0].Name != "access-control" {
		t.Fatalf("first theme alpha: got %q, want access-control", themes[0].Name)
	}
	// Verify source tagging.
	var defaultCount, tenantCount int
	for _, th := range themes {
		switch th.Source {
		case risk.ThemeSourceDefault:
			defaultCount++
		case risk.ThemeSourceTenant:
			tenantCount++
		}
	}
	if defaultCount != 10 || tenantCount != 1 {
		t.Fatalf("source counts: defaults=%d tenant=%d, want 10/1", defaultCount, tenantCount)
	}
}

// ================================================================
// AC-2: DELETE theme is idempotent
// ================================================================

func TestRemoveTheme_Idempotent_AC2(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	riskID := seedAggregableRisk(t, admin, tenant, "idempotent delete", 2, 2, []string{"ownership", "tech-debt"})
	current, err := store.GetRiskThemes(ctx, riskID)
	if err != nil {
		t.Fatalf("GetRiskThemes: %v", err)
	}
	if len(current) != 2 {
		t.Fatalf("seed themes: got %d, want 2", len(current))
	}
	// Remove "ownership" by re-assigning with the rest.
	updated, err := store.AssignThemes(ctx, riskID, []string{"tech-debt"})
	if err != nil {
		t.Fatalf("AssignThemes after remove: %v", err)
	}
	if len(updated.Themes) != 1 || updated.Themes[0] != "tech-debt" {
		t.Fatalf("after remove: %v", updated.Themes)
	}
	// Remove again — no error (idempotent).
	updated2, err := store.AssignThemes(ctx, riskID, []string{"tech-debt"})
	if err != nil {
		t.Fatalf("idempotent re-remove: %v", err)
	}
	if len(updated2.Themes) != 1 {
		t.Fatalf("after idempotent re-remove: %v", updated2.Themes)
	}
}

// ================================================================
// AC-4: org_units CRUD + cycle detection
// ================================================================

func TestOrgUnit_CRUD_HappyPath_AC4(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	// Create root.
	root, err := store.CreateOrgUnit(ctx, risk.OrgUnitInput{
		Name:  "Engineering",
		Level: dbx.RiskLevelOrg,
	})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	// Create child of root.
	child, err := store.CreateOrgUnit(ctx, risk.OrgUnitInput{
		Name:     "AppSec",
		Level:    dbx.RiskLevelTeam,
		ParentID: &root.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != root.ID {
		t.Fatalf("child parent_id mismatch")
	}

	// Get returns same.
	got, err := store.GetOrgUnit(ctx, child.ID)
	if err != nil || got.Name != "AppSec" {
		t.Fatalf("Get round-trip: %v / %+v", err, got)
	}

	// List returns both.
	all, err := store.ListOrgUnits(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("List: %v / count %d", err, len(all))
	}

	// Update — rename.
	updated, err := store.UpdateOrgUnit(ctx, child.ID, risk.OrgUnitInput{
		Name:     "Application Security",
		Level:    dbx.RiskLevelTeam,
		ParentID: &root.ID,
	})
	if err != nil || updated.Name != "Application Security" {
		t.Fatalf("Update: %v / %+v", err, updated)
	}

	// Delete child.
	if err := store.DeleteOrgUnit(ctx, child.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.GetOrgUnit(ctx, child.ID); !errors.Is(err, risk.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestOrgUnit_CycleDetection_AC4_AC24(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	// A -> B -> C chain (A is root, B's parent is A, C's parent is B).
	a, _ := store.CreateOrgUnit(ctx, risk.OrgUnitInput{Name: "A", Level: dbx.RiskLevelCompany})
	b, _ := store.CreateOrgUnit(ctx, risk.OrgUnitInput{Name: "B", Level: dbx.RiskLevelOrg, ParentID: &a.ID})
	c, _ := store.CreateOrgUnit(ctx, risk.OrgUnitInput{Name: "C", Level: dbx.RiskLevelTeam, ParentID: &b.ID})

	// Self-parent on A — must reject.
	_, err := store.UpdateOrgUnit(ctx, a.ID, risk.OrgUnitInput{
		Name: "A", Level: dbx.RiskLevelCompany, ParentID: &a.ID,
	})
	if !errors.Is(err, risk.ErrCycleDetected) {
		t.Fatalf("self-parent should be cycle, got %v", err)
	}

	// Set A's parent = C (would form C->B->A->C loop).
	_, err = store.UpdateOrgUnit(ctx, a.ID, risk.OrgUnitInput{
		Name: "A", Level: dbx.RiskLevelCompany, ParentID: &c.ID,
	})
	if !errors.Is(err, risk.ErrCycleDetected) {
		t.Fatalf("A.parent = C should be cycle, got %v", err)
	}

	// Set A's parent = B (B->A->B loop).
	_, err = store.UpdateOrgUnit(ctx, a.ID, risk.OrgUnitInput{
		Name: "A", Level: dbx.RiskLevelCompany, ParentID: &b.ID,
	})
	if !errors.Is(err, risk.ErrCycleDetected) {
		t.Fatalf("A.parent = B should be cycle, got %v", err)
	}
}

func TestOrgUnit_CrossTenantParent_AC9(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenantA := slice053FreshTenant(t, admin)
	tenantB := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)

	// Create a unit in tenant B.
	ctxB := slice053CtxFor(t, tenantB)
	bUnit, _ := store.CreateOrgUnit(ctxB, risk.OrgUnitInput{Name: "B-root", Level: dbx.RiskLevelOrg})

	// As tenant A, try to use B's id as a parent — must return ErrNotFound.
	ctxA := slice053CtxFor(t, tenantA)
	_, err := store.CreateOrgUnit(ctxA, risk.OrgUnitInput{
		Name: "A-child", Level: dbx.RiskLevelTeam, ParentID: &bUnit.ID,
	})
	if !errors.Is(err, risk.ErrNotFound) {
		t.Fatalf("cross-tenant parent should be 404 (ErrNotFound), got %v", err)
	}
}

// ================================================================
// AC-5..AC-8 + AC-22: end-to-end manual aggregation
// ================================================================

func TestAggregate_EndToEnd_AC5_AC6_AC8_AC22(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	// Two org_units. Three child risks tagged with `ownership`, spread across
	// the two units. Severities 15 (3*5), 12 (4*3), 9 (3*3).
	_, _ = store.CreateOrgUnit(ctx, risk.OrgUnitInput{Name: "Cloud", Level: dbx.RiskLevelTeam})
	parentUnit, _ := store.CreateOrgUnit(ctx, risk.OrgUnitInput{Name: "AppSec", Level: dbx.RiskLevelTeam})

	r1 := seedAggregableRisk(t, admin, tenant, "Orphaned S3 bucket", 3, 5, []string{"ownership"})
	r2 := seedAggregableRisk(t, admin, tenant, "Unowned IAM role", 4, 3, []string{"ownership"})
	r3 := seedAggregableRisk(t, admin, tenant, "Unowned service account", 3, 3, []string{"ownership"})

	// Aggregate via max — expect severity = 15, grid (4, 4) since ceil(sqrt(15))=4.
	parentOrgUnit := parentUnit.ID
	res, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Cross-team ownership pattern",
		ParentLevel:      dbx.RiskLevelOrg,
		ParentOrgUnitID:  &parentOrgUnit,
		SeverityFunction: risk.SeverityFunctionMax,
		ChildRiskIDs:     []uuid.UUID{r1, r2, r3},
	})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if res.Severity != 15 {
		t.Fatalf("max severity: got %d, want 15", res.Severity)
	}
	if res.ChildCount != 3 {
		t.Fatalf("child count: got %d, want 3", res.ChildCount)
	}
	if len(res.LinkedChildren) != 3 {
		t.Fatalf("linked children: got %d, want 3", len(res.LinkedChildren))
	}
	// Parent must have themes union'd from children.
	if len(res.Parent.Themes) != 1 || res.Parent.Themes[0] != "ownership" {
		t.Fatalf("parent themes union: got %v, want [ownership]", res.Parent.Themes)
	}
	parentID := res.Parent.ID

	// AC-8: delete one child (the highest-severity one). Parent severity
	// must drop from 15 to 12 (next max), parent row stays alive.
	if err := store.Delete(ctx, r1); err != nil {
		t.Fatalf("delete child: %v", err)
	}

	live, err := store.LiveAggregation(ctx, parentID)
	if err != nil {
		t.Fatalf("LiveAggregation: %v", err)
	}
	if live.Severity != 12 {
		t.Fatalf("live severity after child close: got %d, want 12", live.Severity)
	}
	if live.ChildCount != 2 {
		t.Fatalf("live child count: got %d, want 2", live.ChildCount)
	}
	// Parent still alive.
	parent, err := store.Get(ctx, parentID)
	if err != nil {
		t.Fatalf("Get parent after child close: %v", err)
	}
	if parent.ID != parentID {
		t.Fatalf("parent id mismatch after child close")
	}
}

func TestAggregate_WeightedMax(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	r1 := seedAggregableRisk(t, admin, tenant, "WM 1", 3, 5, nil) // 15
	r2 := seedAggregableRisk(t, admin, tenant, "WM 2", 4, 3, nil) // 12
	r3 := seedAggregableRisk(t, admin, tenant, "WM 3", 3, 3, nil) // 9

	res, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Weighted max test",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: risk.SeverityFunctionWeightedMax,
		ChildRiskIDs:     []uuid.UUID{r1, r2, r3},
	})
	if err != nil {
		t.Fatalf("Aggregate weighted_max: %v", err)
	}
	// 15 * (1 + log10(3)) ≈ 22.16, ceil = 23.
	if res.Severity != 23 {
		t.Fatalf("weighted_max severity: got %d, want 23", res.Severity)
	}
}

func TestAggregate_Sum_Capped(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	r1 := seedAggregableRisk(t, admin, tenant, "Sum 1", 5, 4, nil) // 20
	r2 := seedAggregableRisk(t, admin, tenant, "Sum 2", 3, 3, nil) // 9

	res, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Sum capped",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: risk.SeverityFunctionSum,
		ChildRiskIDs:     []uuid.UUID{r1, r2},
	})
	if err != nil {
		t.Fatalf("Aggregate sum: %v", err)
	}
	if res.Severity != 25 {
		t.Fatalf("sum severity capped: got %d, want 25", res.Severity)
	}
}

// ================================================================
// AC-7: idempotency on (parent_title, child_set)
// ================================================================

func TestAggregate_Idempotent_AC7(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	r1 := seedAggregableRisk(t, admin, tenant, "Idemp 1", 3, 3, nil)
	r2 := seedAggregableRisk(t, admin, tenant, "Idemp 2", 3, 3, nil)

	first, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Idempotent",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: risk.SeverityFunctionMax,
		ChildRiskIDs:     []uuid.UUID{r1, r2},
	})
	if err != nil {
		t.Fatalf("first Aggregate: %v", err)
	}
	// Re-request with reversed child order — must return the SAME parent id.
	second, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Idempotent",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: risk.SeverityFunctionMax,
		ChildRiskIDs:     []uuid.UUID{r2, r1},
	})
	if err != nil {
		t.Fatalf("second Aggregate: %v", err)
	}
	if first.Parent.ID != second.Parent.ID {
		t.Fatalf("idempotency failed: first=%s second=%s", first.Parent.ID, second.Parent.ID)
	}
	if second.AggregationKey != first.AggregationKey {
		t.Fatalf("aggregation_key mismatch: %s vs %s", first.AggregationKey, second.AggregationKey)
	}
}

// ================================================================
// Mixed-methodology rejection (slice 053 v1 constraint)
// ================================================================

func TestAggregate_MixedMethodology_Rejected(t *testing.T) {
	admin := slice053OpenPool(t, slice053AdminDSN(t))
	app := slice053OpenPool(t, slice053AppDSN(t))
	tenant := slice053FreshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := slice053CtxFor(t, tenant)

	// Eligible child (nist_800_30).
	r1 := seedAggregableRisk(t, admin, tenant, "eligible", 3, 3, nil)
	// Ineligible child (FAIR LEF/LM).
	r2 := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, category, methodology, inherent_score,
			treatment, treatment_owner, residual_score, accepter,
			instrument_reference, level, themes
		) VALUES (
			$1, $2, 'fair risk', 'confidentiality', 'fair',
			'{"lef":1.5,"lm":50000}'::jsonb,
			'avoid', '', '{}', '', '', 'team', '{}'::text[]
		)`, r2, tenant); err != nil {
		t.Fatalf("seed FAIR risk: %v", err)
	}

	_, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Mixed",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: risk.SeverityFunctionMax,
		ChildRiskIDs:     []uuid.UUID{r1, r2},
	})
	if !errors.Is(err, risk.ErrIncompatibleMethodology) {
		t.Fatalf("expected ErrIncompatibleMethodology, got %v", err)
	}
}
