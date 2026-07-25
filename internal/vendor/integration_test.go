//go:build integration

// Integration tests for slice 024 — vendor lite. Real Postgres only;
// RLS cannot be tested against a fake DB (memory rule: never mock the DB).
// Test layout mirrors internal/scope/integration_test.go.

package vendor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

// freshTenant returns a brand-new tenant id and registers cleanup. Each test
// owns its own tenant so RLS guarantees isolation and tests can run in any
// order. The cleanup is a pure FK-ordered tenant-scoped DELETE returning a
// string, so it delegates to dbtest.SeedTenant (slice 435 / 742 drain batch 22).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"vendor_scope_cells",
		"vendors",
		"scope_cells",
		"scope_dimensions",
	)
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func ptr[T any](v T) *T { return &v }

func parseDate(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return v
}

// AC-1: Create with the full lite payload, including DPA + cadence.
func TestCreateVendor_FullPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	in := vendor.CreateVendorInput{
		Name:           "Datadog",
		Domain:         ptr("datadoghq.com"),
		Criticality:    vendor.CriticalityHigh,
		ContractStart:  ptr(parseDate(t, "2025-01-01")),
		ContractEnd:    ptr(parseDate(t, "2026-01-01")),
		DPASigned:      true,
		DPASignedAt:    ptr(parseDate(t, "2025-01-15")),
		ReviewCadence:  vendor.CadenceAnnual,
		LastReviewDate: ptr(parseDate(t, "2026-01-10")),
		OwnerUser:      "alice@example.com",
		LinkedSOWURI:   ptr("s3://contracts/datadog-2025.pdf"),
		Notes:          "Observability vendor",
	}
	got, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "Datadog" || got.Criticality != vendor.CriticalityHigh {
		t.Fatalf("create round-trip mismatch: %+v", got)
	}
	if got.Domain == nil || *got.Domain != "datadoghq.com" {
		t.Fatalf("domain lowercased: got %v", got.Domain)
	}
	if !got.DPASigned || got.DPASignedAt == nil {
		t.Fatalf("DPA fields not preserved: %+v", got)
	}
	if got.LinkedSOWURI == nil || *got.LinkedSOWURI != "s3://contracts/datadog-2025.pdf" {
		t.Fatalf("SOW URI not preserved: %v", got.LinkedSOWURI)
	}
}

// AC-1 negative: dpa_signed=true without dpa_signed_at must be rejected.
func TestCreateVendor_RejectsDPAWithoutDate(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	_, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "Bad",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
		DPASigned:     true,
		// DPASignedAt intentionally omitted
	})
	if !errors.Is(err, vendor.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput; got %v", err)
	}
}

// AC-1 negative: invalid criticality is rejected at the Go layer (before the
// enum check at the DB).
func TestCreateVendor_RejectsBadCriticality(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	_, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "Bad",
		Criticality:   vendor.Criticality("ultra-critical"),
		ReviewCadence: vendor.CadenceAnnual,
	})
	if !errors.Is(err, vendor.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput; got %v", err)
	}
}

// AC-2: List filter by criticality returns only matching rows.
func TestListVendors_FilterByCriticality(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	seed := []vendor.CreateVendorInput{
		{Name: "Critical-1", Criticality: vendor.CriticalityHigh, ReviewCadence: vendor.CadenceAnnual},
		{Name: "Critical-2", Criticality: vendor.CriticalityHigh, ReviewCadence: vendor.CadenceAnnual},
		{Name: "Mid-1", Criticality: vendor.CriticalityMedium, ReviewCadence: vendor.CadenceAnnual},
		{Name: "Low-1", Criticality: vendor.CriticalityLow, ReviewCadence: vendor.CadenceAnnual},
	}
	for _, s := range seed {
		if _, err := store.Create(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", s.Name, err)
		}
	}

	all, err := store.List(ctx, vendor.ListFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("list all: want 4 rows, got %d", len(all))
	}

	high := vendor.CriticalityHigh
	highOnly, err := store.List(ctx, vendor.ListFilter{Criticality: &high})
	if err != nil {
		t.Fatalf("list high: %v", err)
	}
	if len(highOnly) != 2 {
		t.Fatalf("list high: want 2 rows, got %d (%v)", len(highOnly), highOnly)
	}
	for _, v := range highOnly {
		if v.Criticality != vendor.CriticalityHigh {
			t.Fatalf("non-high row leaked: %+v", v)
		}
	}
}

// AC-2 + AC-4: List filter for review-overdue. Insert one vendor reviewed
// recently, one reviewed long ago, and one never reviewed. With cadence=
// quarterly and cutoff well past the old review, the "long ago" and the
// "never reviewed" rows must surface as overdue.
func TestListVendors_FilterByOverdue(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	if _, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:           "Recent",
		Criticality:    vendor.CriticalityMedium,
		ReviewCadence:  vendor.CadenceQuarterly,
		LastReviewDate: ptr(parseDate(t, "2026-04-01")),
	}); err != nil {
		t.Fatalf("seed recent: %v", err)
	}
	if _, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:           "Stale",
		Criticality:    vendor.CriticalityMedium,
		ReviewCadence:  vendor.CadenceQuarterly,
		LastReviewDate: ptr(parseDate(t, "2025-01-01")),
	}); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if _, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "NeverReviewed",
		Criticality:   vendor.CriticalityMedium,
		ReviewCadence: vendor.CadenceQuarterly,
	}); err != nil {
		t.Fatalf("seed never: %v", err)
	}

	cutoff := parseDate(t, "2026-05-11")
	overdue, err := store.List(ctx, vendor.ListFilter{OverdueOnly: true, Cutoff: cutoff})
	if err != nil {
		t.Fatalf("list overdue: %v", err)
	}
	if len(overdue) != 2 {
		names := make([]string, 0, len(overdue))
		for _, v := range overdue {
			names = append(names, v.Name)
		}
		t.Fatalf("want 2 overdue rows, got %d: %v", len(overdue), names)
	}
}

// AC-3: Burndown returns review-on-time fraction per criticality band.
func TestBurndown_ReviewOnTimeFraction(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	// Five high-criticality vendors: 3 on time, 2 overdue.
	for _, name := range []string{"on-1", "on-2", "on-3"} {
		if _, err := store.Create(ctx, vendor.CreateVendorInput{
			Name:           name,
			Criticality:    vendor.CriticalityHigh,
			ReviewCadence:  vendor.CadenceAnnual,
			LastReviewDate: ptr(parseDate(t, "2026-04-01")),
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	for _, name := range []string{"od-1", "od-2"} {
		if _, err := store.Create(ctx, vendor.CreateVendorInput{
			Name:           name,
			Criticality:    vendor.CriticalityHigh,
			ReviewCadence:  vendor.CadenceAnnual,
			LastReviewDate: ptr(parseDate(t, "2024-01-01")), // > 1y old
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	high := vendor.CriticalityHigh
	bd, err := store.Burndown(ctx, parseDate(t, "2026-05-11"), &high)
	if err != nil {
		t.Fatalf("Burndown: %v", err)
	}
	if len(bd.Bands) != 1 {
		t.Fatalf("want one band; got %d", len(bd.Bands))
	}
	band := bd.Bands[0]
	if band.Total != 5 || band.Overdue != 2 {
		t.Fatalf("totals wrong: %+v", band)
	}
	wantFraction := 3.0 / 5.0
	if band.OnTimeFraction != wantFraction {
		t.Fatalf("OnTimeFraction = %v; want %v", band.OnTimeFraction, wantFraction)
	}
}

// AC-3 edge case: Burndown returns 1.0 on empty tenant.
func TestBurndown_EmptyTenantOnTime(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	bd, err := store.Burndown(ctx, time.Now(), nil)
	if err != nil {
		t.Fatalf("Burndown: %v", err)
	}
	if bd.Total.Total != 0 || bd.Total.OnTimeFraction != 1.0 {
		t.Fatalf("empty: want OnTimeFraction=1.0 total=0; got %+v", bd.Total)
	}
}

// AC-6: Vendors are scope-tagged. Bind two cells; List returns them.
func TestCreateVendor_AttachesScopeCells(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	// Insert two scope cells directly via admin tenant context (we don't
	// need slice 017's store here; just rows in scope_cells that satisfy
	// RLS for this tenant).
	cellA, cellB := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{cellA, cellB} {
		_, err := admin.Exec(context.Background(),
			`INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
			 VALUES ($1, $2, $3, '{"environment":"prod"}'::jsonb, $4)`,
			id, tenant, "label-"+id.String()[:6], "h-"+id.String()[:6])
		if err != nil {
			t.Fatalf("seed cell: %v", err)
		}
	}

	store := vendor.NewStore(app)
	v, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "ScopedVendor",
		Criticality:   vendor.CriticalityMedium,
		ReviewCadence: vendor.CadenceAnnual,
		ScopeCellIDs:  []uuid.UUID{cellA, cellB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(v.ScopeCellIDs) != 2 {
		t.Fatalf("want 2 attached cells; got %d", len(v.ScopeCellIDs))
	}
}

// Update re-binds the cell set: remove A, add C.
func TestUpdateVendor_RebindsScopeCells(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	cellA, cellB, cellC := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{cellA, cellB, cellC} {
		_, err := admin.Exec(context.Background(),
			`INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
			 VALUES ($1, $2, $3, '{"environment":"prod"}'::jsonb, $4)`,
			id, tenant, "label-"+id.String()[:6], "h-"+id.String()[:6])
		if err != nil {
			t.Fatalf("seed cell: %v", err)
		}
	}

	store := vendor.NewStore(app)
	v, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "Reshape",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
		ScopeCellIDs:  []uuid.UUID{cellA, cellB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Re-bind to {B, C}: A removed, C added.
	updated, err := store.Update(ctx, v.ID, vendor.CreateVendorInput{
		Name:          "Reshape",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
		ScopeCellIDs:  []uuid.UUID{cellB, cellC},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.ScopeCellIDs) != 2 {
		t.Fatalf("want 2 cells after re-bind; got %d", len(updated.ScopeCellIDs))
	}
	set := map[uuid.UUID]bool{}
	for _, id := range updated.ScopeCellIDs {
		set[id] = true
	}
	if set[cellA] {
		t.Fatalf("cellA should have been unbound")
	}
	if !set[cellB] || !set[cellC] {
		t.Fatalf("want cellB+cellC; got %v", updated.ScopeCellIDs)
	}
}

// Domain natural-key dedup: same lowercased domain within a tenant rejected.
func TestCreateVendor_RejectsDuplicateDomain(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	first := vendor.CreateVendorInput{
		Name:          "First",
		Domain:        ptr("Example.com"),
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	}
	if _, err := store.Create(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	second := vendor.CreateVendorInput{
		Name:          "Second",
		Domain:        ptr("example.com"), // same domain, different case
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	}
	_, err := store.Create(ctx, second)
	if !errors.Is(err, vendor.ErrDuplicateDomain) {
		t.Fatalf("want ErrDuplicateDomain; got %v", err)
	}
}

// NULL domain rows must NOT collide — partial unique index excludes them.
func TestCreateVendor_NullDomainsCoexist(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	for _, name := range []string{"a", "b", "c"} {
		if _, err := store.Create(ctx, vendor.CreateVendorInput{
			Name:          "no-domain-" + name,
			Criticality:   vendor.CriticalityLow,
			ReviewCadence: vendor.CadenceAnnual,
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
}

// RLS: tenant B cannot read tenant A's vendors.
func TestRLS_OtherTenantCannotSeeVendors(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := vendor.NewStore(app)

	ctxA := tenantCtx(t, tenantA)
	if _, err := store.Create(ctxA, vendor.CreateVendorInput{
		Name:          "Secret",
		Criticality:   vendor.CriticalityHigh,
		ReviewCadence: vendor.CadenceAnnual,
	}); err != nil {
		t.Fatalf("create A: %v", err)
	}

	ctxB := tenantCtx(t, tenantB)
	rows, err := store.List(ctxB, vendor.ListFilter{})
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("tenant B saw %d vendors from tenant A; RLS bypassed", len(rows))
	}
}

// Round-trip: update preserves fields and bumps updated_at.
func TestUpdateVendor_RoundTrip(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	v, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "ToUpdate",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	createdAt := v.UpdatedAt

	// Tiny sleep so updated_at is strictly greater.
	time.Sleep(20 * time.Millisecond)

	updated, err := store.Update(ctx, v.ID, vendor.CreateVendorInput{
		Name:           "ToUpdate-revised",
		Criticality:    vendor.CriticalityHigh,
		ReviewCadence:  vendor.CadenceQuarterly,
		LastReviewDate: ptr(parseDate(t, "2026-04-15")),
		Notes:          "now critical",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "ToUpdate-revised" || updated.Criticality != vendor.CriticalityHigh {
		t.Fatalf("update did not propagate: %+v", updated)
	}
	if !updated.UpdatedAt.After(createdAt) {
		t.Fatalf("updated_at not bumped: %v <= %v", updated.UpdatedAt, createdAt)
	}
}

// Update on a missing vendor returns ErrVendorNotFound.
func TestUpdateVendor_NotFound(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := vendor.NewStore(app)
	ctx := tenantCtx(t, tenant)

	_, err := store.Update(ctx, uuid.New(), vendor.CreateVendorInput{
		Name:          "Ghost",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if !errors.Is(err, vendor.ErrVendorNotFound) {
		t.Fatalf("want ErrVendorNotFound; got %v", err)
	}
}
