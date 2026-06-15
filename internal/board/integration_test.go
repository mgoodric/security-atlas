//go:build integration

// Integration tests for slice 283 (coverage lift of internal/board) covering
// the slice-031 monthly board brief Store + Generator and the slice-032
// quarterly board pack PackStore + PackGenerator against a real Postgres.
//
// Load-bearing functions exercised here (none mockable; the constitutional
// invariants — RLS isolation (#6), append-only ledger (#2), draft-only
// mutation gates on `board_packs` — are only meaningful against a real DB):
//
//   - board.Store.{Insert,Get,List,ListFrameworks,ListRisksAsOf}
//   - board.Store.inTx + storedBriefFromRow + pgUUID + pgTimestamptz + pgDate
//   - board.Generator.{Generate,assemble} (with the WithClock override)
//   - board.PackStore.{Insert,Get,List,ListFrameworks,ListRisksAsOf,
//     ListFailingEvaluations,UpdateSection,Publish}
//   - board.PackStore.inTx + storedPackFromRow + applySectionEdit chain
//   - board.PackGenerator.{Generate,assemble} including the vendor-burndown
//     reader path (slice 273) — both nil-reader and real-reader branches
//
// Run with:
//
//	go test -tags=integration -race ./internal/board/...
//
// Required env:
//
//	DATABASE_URL     - migration role DSN (BYPASSRLS); seeds frameworks /
//	                   controls / risks / control_evaluations outside the GUC.
//	DATABASE_URL_APP - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//	                   board Stores run against this so RLS is actually enforced.

package board_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ---------------- harness ----------------

// freshTenant returns a tenant UUID and registers cleanup that wipes every
// board-related row + dependencies so reruns do not accumulate. The order
// matters: board_briefs / board_packs FK nothing else, but the rest of the
// graph (control_evaluations -> controls -> frameworks; risks; etc.) must
// drop FK-children first. Pure tenant-scoped DELETE in FK order, so it
// delegates to dbtest.SeedTenant (slice 435 / 742 drain).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"board_packs",
		"board_briefs",
		"control_evaluations",
		"risks",
		"controls",
		"frameworks",
	)
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// seedFramework inserts one tenant-private framework with the given slug.
// The board Stores read `frameworks` with `tenant_id IS NULL OR tenant_id = $1`,
// so a tenant-private framework is sufficient to drive the per-framework
// posture row.
func seedFramework(t *testing.T, admin *pgxpool.Pool, tenant, slug, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		VALUES ($1, $2, $3, $4, 'slice-283 test issuer')
	`, id, tenant, slug, name); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	return id
}

// seedRisk inserts one open risk with the given residual + inherent scores
// (raw JSONB strings — the Generator extracts the severity scalar in Go).
// updatedDaysAgo controls the age-since-touch the ranking uses.
func seedRisk(t *testing.T, admin *pgxpool.Pool, tenant, title string, residualJSON, inherentJSON string, updatedDaysAgo int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	updated := time.Now().UTC().Add(-time.Duration(updatedDaysAgo) * 24 * time.Hour)
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, treatment,
			inherent_score, residual_score, updated_at, created_at
		)
		VALUES (
			$1, $2, $3, 'slice 283 test risk', 'operational'::risk_category, 'mitigate'::risk_treatment,
			$4::jsonb, $5::jsonb, $6, $6
		)
	`, id, tenant, title, inherentJSON, residualJSON, updated); err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	return id
}

// seedControl inserts one control row — sufficient to attach
// control_evaluations to. NOT-NULL bundle_id present per slice 009.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	bundleID := "legacy_283_" + id.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'slice 283 test control', 'IAC', 'automated', $3)
	`, id, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// seedFailingEvaluation appends one failing control_evaluations row pinned
// to a specific `evaluated_at`. The pack's open-findings read pulls the
// DISTINCT-ON-latest failing evaluation per (control, scope_cell) bounded
// by period_end — so a row stamped at `evaluatedAt <= period_end` will be
// surfaced.
func seedFailingEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, evaluatedAt time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations
		   (id, tenant_id, control_id, scope_cell_id, eval_run_id,
		    evaluated_at, result, freshness_status, evidence_count_in_window, trigger)
		VALUES ($1, $2, $3, NULL, $4, $5, 'fail', 'stale', 0, 'manual')
	`, uuid.New(), tenant, ctrlID, uuid.New(), evaluatedAt); err != nil {
		t.Fatalf("seed failing evaluation: %v", err)
	}
}

// ----- in-test fakes for upstream-read interfaces -----

// fixedFreshness implements board's freshness-lister contract with a static
// roster — the Generator is a PURE READER of this surface, so an in-test
// implementation is sufficient (the real freshness.Store is exercised by its
// own integration test). Lets the assemble path cover its read of the
// freshness slice WITHOUT seeding `evidence_freshness` rows.
type fixedFreshness struct {
	rows []freshness.ControlFreshness
}

func (f fixedFreshness) List(_ context.Context) ([]freshness.ControlFreshness, error) {
	return f.rows, nil
}

// fixedDrift implements board's drift-reporter contract with a static
// report. Same pure-reader rationale as fixedFreshness.
type fixedDrift struct {
	report drift.DriftReport
}

func (f fixedDrift) Report(_ context.Context, _ time.Duration) (drift.DriftReport, error) {
	return f.report, nil
}

// fixedVendorBurndown implements board.VendorBurndownReader with a static
// triple — the slice-273 read surface. The wiring layer adapts
// vendor.Store.Burndown to this contract; an in-test impl exercises the
// PackGenerator's non-nil-reader branch without dragging in internal/vendor.
type fixedVendorBurndown struct {
	out board.VendorBurndownReadout
}

func (f fixedVendorBurndown) ReadHighCriticalityBurndown(_ context.Context, _ time.Time) (board.VendorBurndownReadout, error) {
	return f.out, nil
}

// =========================================================================
// board.Store — brief append + read + tenant scoping
// =========================================================================

// brief.Insert returns the stored row with the frozen content + narrative
// verbatim; brief.Get re-reads byte-identical content (AC-5 round trip).
func TestBoardStore_InsertGetList_RoundTrip(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewStore(app)

	brief := board.Brief{
		PeriodEnd:   "2026-04-30",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Frameworks: []board.FrameworkPosture{
			{Slug: "soc2", Name: "SOC 2", CoveragePct: 92, FreshnessPct: 88, TrendArrow: board.TrendUp, Delta: 3, State: "audit-ready"},
		},
		Drift: board.DriftSummary{WindowDays: 30, Since: "2026-03-31", Through: "2026-04-30", Delta: 3, FlippedOutCount: 0},
		TopRisks: []board.RiskAging{
			{ID: uuid.NewString(), Title: "Sample risk", Category: "operational", Treatment: "mitigate", ResidualSeverity: 12, AgeDays: 14},
		},
	}
	narrative := "# Test brief\n\nSample narrative.\n"

	stored, err := store.Insert(ctx, brief, narrative, time.Now().UTC())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if stored.ID == uuid.Nil {
		t.Fatal("Insert returned nil id")
	}
	if stored.PeriodEnd != "2026-04-30" {
		t.Errorf("stored.PeriodEnd = %q, want 2026-04-30", stored.PeriodEnd)
	}
	if stored.NarrativeMd != narrative {
		t.Errorf("stored narrative round-trip mismatch")
	}
	if got, want := stored.Content.Frameworks[0].Name, "SOC 2"; got != want {
		t.Errorf("stored.Content.Frameworks[0].Name = %q, want %q", got, want)
	}

	// Get round-trip — frozen content read back verbatim.
	fetched, err := store.Get(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.Content.Frameworks[0].CoveragePct != 92 {
		t.Errorf("Get.Content coverage = %d, want 92", fetched.Content.Frameworks[0].CoveragePct)
	}
	if fetched.NarrativeMd != narrative {
		t.Errorf("Get narrative drift: got %q", fetched.NarrativeMd)
	}

	// List returns the inserted brief.
	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List returned %d rows, want 1", len(rows))
	}
	if rows[0].ID != stored.ID {
		t.Errorf("List[0].ID = %s, want %s", rows[0].ID, stored.ID)
	}
}

// brief.Get with a random UUID returns ErrNotFound (RLS / no-row path).
func TestBoardStore_Get_NotFound(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewStore(app)
	if _, err := store.Get(ctx, uuid.New()); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("Get(unknown) err = %v, want ErrNotFound", err)
	}
}

// brief.Get with a cross-tenant id ALSO returns ErrNotFound — RLS makes the
// foreign row invisible.
func TestBoardStore_Get_CrossTenantInvisible(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctxA := ctxFor(t, tenantA)
	ctxB := ctxFor(t, tenantB)

	store := board.NewStore(app)
	brief := board.Brief{PeriodEnd: "2026-05-31", GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	stored, err := store.Insert(ctxA, brief, "narrative-A", time.Now().UTC())
	if err != nil {
		t.Fatalf("Insert (A): %v", err)
	}
	// Tenant B reads tenant A's brief id -> ErrNotFound.
	if _, err := store.Get(ctxB, stored.ID); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("cross-tenant Get err = %v, want ErrNotFound (RLS isolation)", err)
	}
	// Tenant B's List does NOT include tenant A's brief.
	rows, err := store.List(ctxB)
	if err != nil {
		t.Fatalf("List (B): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("tenant B List returned %d rows, want 0 (RLS isolation)", len(rows))
	}
}

// brief.ListFrameworks honors `tenant_id IS NULL OR tenant_id = $1`.
func TestBoardStore_ListFrameworks(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	seedFramework(t, admin, tenant, "test-soc2-283", "SOC 2 (slice 283)")
	seedFramework(t, admin, tenant, "test-iso-283", "ISO 27001 (slice 283)")

	store := board.NewStore(app)
	fws, err := store.ListFrameworks(ctx)
	if err != nil {
		t.Fatalf("ListFrameworks: %v", err)
	}
	if len(fws) < 2 {
		t.Fatalf("ListFrameworks = %d entries, want >=2", len(fws))
	}
	have := map[string]bool{}
	for _, f := range fws {
		have[f.Slug] = true
	}
	if !have["test-soc2-283"] || !have["test-iso-283"] {
		t.Errorf("expected both seeded framework slugs in result, got %v", have)
	}
}

// brief.ListRisksAsOf returns risks created on or before asOf, oldest first.
func TestBoardStore_ListRisksAsOf(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// Two risks; the inserted-at horizon is now; both fall inside.
	old := seedRisk(t, admin, tenant, "older risk",
		`{"score": 9}`, `{"likelihood": 3, "impact": 3}`, 30)
	recent := seedRisk(t, admin, tenant, "newer risk",
		`{"score": 16}`, `{"likelihood": 4, "impact": 4}`, 5)

	store := board.NewStore(app)
	rows, err := store.ListRisksAsOf(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("ListRisksAsOf: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListRisksAsOf returned %d, want 2 (have %v / %v)", len(rows), old, recent)
	}
	// Ordered oldest-touched first per the SQL.
	if rows[0].Title != "older risk" {
		t.Errorf("rows[0] = %q, want 'older risk'", rows[0].Title)
	}
}

// =========================================================================
// board.Generator — Generate end-to-end with seeded frameworks + risks
// =========================================================================

func TestGenerator_Generate_EndToEnd(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	seedFramework(t, admin, tenant, "test-soc2-gen", "SOC 2 (gen)")
	seedRisk(t, admin, tenant, "Critical CVE backlog",
		`{"score": 20}`, `{"likelihood": 5, "impact": 4}`, 60)

	store := board.NewStore(app)
	gen := board.NewGenerator(store, fixedFreshness{rows: []freshness.ControlFreshness{
		{IsStale: false, EvidenceCount: 3},
		{IsStale: false, EvidenceCount: 1},
		{IsStale: true, EvidenceCount: 2},
	}}, fixedDrift{report: drift.DriftReport{
		SinceDate:    time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		ThroughDate:  time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		Delta:        2,
		FlippedToOut: nil,
	}}).WithClock(func() time.Time {
		return time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	})

	stored, err := gen.Generate(ctx, "2026-04-30")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if stored.PeriodEnd != "2026-04-30" {
		t.Errorf("stored.PeriodEnd = %q, want 2026-04-30", stored.PeriodEnd)
	}
	if len(stored.Content.Frameworks) == 0 {
		t.Fatal("Generate produced zero framework posture rows")
	}
	// Trend arrow up — delta 2 is positive.
	if stored.Content.Frameworks[0].TrendArrow != board.TrendUp {
		t.Errorf("framework trend = %q, want %q", stored.Content.Frameworks[0].TrendArrow, board.TrendUp)
	}
	// Top risk surfaces in the brief.
	if len(stored.Content.TopRisks) == 0 || !strings.Contains(stored.Content.TopRisks[0].Title, "Critical CVE backlog") {
		t.Errorf("top risk not surfaced; got %v", stored.Content.TopRisks)
	}
	// Drift summary populated from the fixed reporter.
	if stored.Content.Drift.WindowDays != 30 {
		t.Errorf("drift window = %d, want 30", stored.Content.Drift.WindowDays)
	}
	if stored.Content.Drift.Delta != 2 {
		t.Errorf("drift delta = %d, want 2", stored.Content.Drift.Delta)
	}
	// Narrative rendered.
	if stored.NarrativeMd == "" {
		t.Error("Generate produced empty narrative")
	}
	if !strings.Contains(stored.NarrativeMd, "SOC 2") {
		t.Errorf("narrative missing framework name; got: %q", stored.NarrativeMd)
	}
}

// Generate rejects a malformed period_end with ErrBadPeriodEnd.
func TestGenerator_Generate_BadPeriodEndRejected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	gen := board.NewGenerator(board.NewStore(app), fixedFreshness{}, fixedDrift{})
	_, err := gen.Generate(ctx, "not-a-date")
	if !errors.Is(err, board.ErrBadPeriodEnd) {
		t.Fatalf("Generate(bad date) err = %v, want ErrBadPeriodEnd", err)
	}
}

// =========================================================================
// board.PackStore — quarterly pack lifecycle round-trip
// =========================================================================

// Pack.Insert appends a draft; Get re-reads it; List returns it.
func TestPackStore_InsertGetList_DraftRoundTrip(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewPackStore(app)
	pack := newSeededDraftPack("2026-06-30")
	stored, err := store.Insert(ctx, pack, "narrative-md", time.Now().UTC())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if stored.Status != board.PackStatusDraft {
		t.Errorf("Insert status = %q, want %q", stored.Status, board.PackStatusDraft)
	}
	if stored.IsPublished() {
		t.Error("draft pack reports IsPublished=true")
	}

	got, err := store.Get(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PeriodEnd != "2026-06-30" {
		t.Errorf("Get.PeriodEnd = %q, want 2026-06-30", got.PeriodEnd)
	}
	if len(got.Content.Sections) != len(board.SectionKeys) {
		t.Errorf("Get.Sections has %d entries, want %d", len(got.Content.Sections), len(board.SectionKeys))
	}

	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List returned %d, want 1", len(rows))
	}
}

// PackStore.Get returns ErrPackNotFound for an unknown id.
func TestPackStore_Get_NotFound(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewPackStore(app)
	if _, err := store.Get(ctx, uuid.New()); !errors.Is(err, board.ErrPackNotFound) {
		t.Fatalf("Get(unknown) err = %v, want ErrPackNotFound", err)
	}
}

// PackStore.UpdateSection mutates a draft section + re-renders the templated
// narrative; the override text round-trips through the JSONB content.
func TestPackStore_UpdateSection_DraftMutation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewPackStore(app)
	stored, err := store.Insert(ctx, newSeededDraftPack("2026-06-30"), "narrative", time.Now().UTC())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	override := "Board ask: approve the Q3 staffing plan."
	approved := true
	updated, err := store.UpdateSection(ctx, stored.ID, board.SectionEdit{
		SectionKey:   board.SectionAsks,
		OverrideText: &override,
		Approved:     &approved,
	})
	if err != nil {
		t.Fatalf("UpdateSection (asks): %v", err)
	}
	asks := updated.Content.Sections[board.SectionAsks]
	if asks.OverrideText != override {
		t.Errorf("override text round-trip failed: %q", asks.OverrideText)
	}
	if !asks.Approved {
		t.Error("approved flag not stored")
	}

	// A second UpdateSection on operational_metrics with structured Inputs.
	phishing := 96
	patches := 4
	incidents := 0
	on := 9
	total := 10
	op, err := store.UpdateSection(ctx, stored.ID, board.SectionEdit{
		SectionKey: board.SectionOperational,
		Inputs: &board.SectionInputs{
			PhishingPassRatePct: &phishing,
			P1PatchMedianDays:   &patches,
			IncidentCount:       &incidents,
			VendorReviewsOnTime: &on,
			VendorReviewsTotal:  &total,
		},
	})
	if err != nil {
		t.Fatalf("UpdateSection (operational): %v", err)
	}
	opData := op.Content.Sections[board.SectionOperational].Data
	if opData.PhishingPassRatePct == nil || *opData.PhishingPassRatePct != 96 {
		t.Errorf("phishing pass rate did not round-trip: %v", opData.PhishingPassRatePct)
	}
	if opData.IncidentCount == nil || *opData.IncidentCount != 0 {
		t.Errorf("incident_count zero round-trip failed: %v", opData.IncidentCount)
	}
}

// PackStore.UpdateSection rejects an unknown section key.
func TestPackStore_UpdateSection_UnknownSection(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewPackStore(app)
	stored, err := store.Insert(ctx, newSeededDraftPack("2026-06-30"), "narrative", time.Now().UTC())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	bogus := "this-is-not-a-real-section"
	override := "x"
	_, err = store.UpdateSection(ctx, stored.ID, board.SectionEdit{
		SectionKey:   bogus,
		OverrideText: &override,
	})
	if !errors.Is(err, board.ErrUnknownSection) {
		t.Fatalf("UpdateSection(unknown) err = %v, want ErrUnknownSection", err)
	}
}

// PackStore.UpdateSection on an unknown pack id returns ErrPackNotFound.
func TestPackStore_UpdateSection_PackNotFound(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewPackStore(app)
	override := "x"
	_, err := store.UpdateSection(ctx, uuid.New(), board.SectionEdit{
		SectionKey:   board.SectionAsks,
		OverrideText: &override,
	})
	if !errors.Is(err, board.ErrPackNotFound) {
		t.Fatalf("UpdateSection(unknown pack) err = %v, want ErrPackNotFound", err)
	}
}

// PackStore.Publish flips status to published when every section is
// approved; re-publish returns ErrPackNotDraft.
func TestPackStore_Publish_Lifecycle(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewPackStore(app)
	stored, err := store.Insert(ctx, newSeededDraftPack("2026-06-30"), "narrative", time.Now().UTC())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Publish before approving every section fails with ErrPackNotReady.
	if _, err := store.Publish(ctx, stored.ID, "alice@example.com"); !errors.Is(err, board.ErrPackNotReady) {
		t.Fatalf("Publish (un-approved) err = %v, want ErrPackNotReady", err)
	}

	// Approve every section in turn.
	approved := true
	for _, key := range board.SectionKeys {
		if _, err := store.UpdateSection(ctx, stored.ID, board.SectionEdit{
			SectionKey: key,
			Approved:   &approved,
		}); err != nil {
			t.Fatalf("UpdateSection(approve %s): %v", key, err)
		}
	}

	pub, err := store.Publish(ctx, stored.ID, "alice@example.com")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if pub.Status != board.PackStatusPublished {
		t.Errorf("Publish.Status = %q, want %q", pub.Status, board.PackStatusPublished)
	}
	if !pub.IsPublished() {
		t.Error("StoredPack.IsPublished returned false after publish")
	}
	if pub.PublishedBy != "alice@example.com" {
		t.Errorf("PublishedBy = %q, want alice@example.com", pub.PublishedBy)
	}
	if pub.PublishedAt.IsZero() {
		t.Error("PublishedAt is zero after publish")
	}

	// Re-publish a published pack -> ErrPackNotDraft.
	if _, err := store.Publish(ctx, stored.ID, "alice@example.com"); !errors.Is(err, board.ErrPackNotDraft) {
		t.Errorf("re-Publish err = %v, want ErrPackNotDraft", err)
	}

	// Update on a published pack -> ErrPackNotDraft.
	override := "x"
	if _, err := store.UpdateSection(ctx, stored.ID, board.SectionEdit{
		SectionKey:   board.SectionAsks,
		OverrideText: &override,
	}); !errors.Is(err, board.ErrPackNotDraft) {
		t.Errorf("UpdateSection (published) err = %v, want ErrPackNotDraft", err)
	}
}

// PackStore.Publish on an unknown id returns ErrPackNotFound.
func TestPackStore_Publish_NotFound(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	store := board.NewPackStore(app)
	if _, err := store.Publish(ctx, uuid.New(), "alice@example.com"); !errors.Is(err, board.ErrPackNotFound) {
		t.Fatalf("Publish(unknown) err = %v, want ErrPackNotFound", err)
	}
}

// PackStore.ListFrameworks / ListRisksAsOf / ListFailingEvaluations cover
// the pack-store reads.
func TestPackStore_ReadSurfaces(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	seedFramework(t, admin, tenant, "test-fw-283-pack", "Test FW (pack)")
	seedRisk(t, admin, tenant, "Pack risk", `{"score": 8}`, `{"likelihood": 2, "impact": 4}`, 10)
	ctrlID := seedControl(t, admin, tenant)
	// Failing evaluation stamped 6h before period_end -> surfaced by the read.
	periodEnd := time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC)
	seedFailingEvaluation(t, admin, tenant, ctrlID, periodEnd.Add(-6*time.Hour))

	store := board.NewPackStore(app)
	fws, err := store.ListFrameworks(ctx)
	if err != nil {
		t.Fatalf("ListFrameworks: %v", err)
	}
	if len(fws) == 0 {
		t.Error("ListFrameworks returned zero results")
	}

	risks, err := store.ListRisksAsOf(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("ListRisksAsOf: %v", err)
	}
	if len(risks) != 1 {
		t.Fatalf("ListRisksAsOf = %d, want 1", len(risks))
	}

	findings, err := store.ListFailingEvaluations(ctx, periodEnd)
	if err != nil {
		t.Fatalf("ListFailingEvaluations: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("ListFailingEvaluations = %d, want 1", len(findings))
	}
	if findings[0].ControlID != ctrlID.String() {
		t.Errorf("finding ControlID = %s, want %s", findings[0].ControlID, ctrlID)
	}
	if findings[0].FreshnessStatus != "stale" {
		t.Errorf("finding FreshnessStatus = %q, want stale", findings[0].FreshnessStatus)
	}
}

// =========================================================================
// board.PackGenerator — Generate end-to-end with seeded inputs +
// fixed freshness/drift readers
// =========================================================================

// PackGenerator.Generate end-to-end with a non-nil vendor-burndown reader.
// Exercises the slice-273 vendor-burndown path; also covers the operator-
// entered sections being seeded with placeholders (decision D3).
func TestPackGenerator_Generate_EndToEnd_WithVendorReader(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	seedFramework(t, admin, tenant, "test-fw-pack-gen", "Test FW (pack gen)")
	seedRisk(t, admin, tenant, "Pack-gen risk", `{"score": 12}`, `{"likelihood": 3, "impact": 4}`, 20)

	store := board.NewPackStore(app)
	gen := board.NewPackGenerator(
		store,
		fixedFreshness{rows: []freshness.ControlFreshness{
			{IsStale: false, EvidenceCount: 5},
			{IsStale: false, EvidenceCount: 1},
		}},
		fixedDrift{report: drift.DriftReport{
			SinceDate:   time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
			ThroughDate: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
			Delta:       1,
		}},
		fixedVendorBurndown{out: board.VendorBurndownReadout{Total: 4, OnTime: 3, PastDue: 1}},
	).WithClock(func() time.Time {
		return time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	})

	stored, err := gen.Generate(ctx, "2026-06-30")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if stored.Status != board.PackStatusDraft {
		t.Errorf("Generate produced status %q, want draft", stored.Status)
	}
	if len(stored.Content.Sections) != len(board.SectionKeys) {
		t.Errorf("Generate produced %d sections, want %d", len(stored.Content.Sections), len(board.SectionKeys))
	}
	// Vendor-burndown section populated from the reader.
	vb := stored.Content.Sections[board.SectionVendorBurndown].Data
	if vb.VendorBurndownTotal != 4 || vb.VendorBurndownOnTime != 3 || vb.VendorBurndownPastDue != 1 {
		t.Errorf("vendor burndown not populated: total=%d on=%d past=%d",
			vb.VendorBurndownTotal, vb.VendorBurndownOnTime, vb.VendorBurndownPastDue)
	}
	if vb.VendorBurndownOnTimePct != 75 {
		t.Errorf("vendor on-time pct = %d, want 75 (3/4)", vb.VendorBurndownOnTimePct)
	}
	// Posture section populated from the framework + freshness fixture.
	posture := stored.Content.Sections[board.SectionPosture].Data
	if len(posture.Frameworks) == 0 {
		t.Error("posture section has no frameworks")
	}
	// Operator-entered sections start with empty data + a placeholder.
	op := stored.Content.Sections[board.SectionOperational]
	if op.Data.PhishingPassRatePct != nil {
		t.Error("operational metrics seeded non-nil — must be empty")
	}
	if !strings.Contains(op.TemplatedText, "operator-entered") {
		t.Errorf("operational placeholder narrative missing 'operator-entered' marker: %q", op.TemplatedText)
	}
}

// PackGenerator.Generate with a nil vendor-burndown reader seeds the
// vendor_burndown section with zero scalars and renders the "no
// high-criticality vendors registered" narrative.
func TestPackGenerator_Generate_NilVendorReader(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	seedFramework(t, admin, tenant, "test-fw-nilreader", "Test FW (nil reader)")
	store := board.NewPackStore(app)
	gen := board.NewPackGenerator(store,
		fixedFreshness{},
		fixedDrift{},
		nil,
	).WithClock(func() time.Time {
		return time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	})

	stored, err := gen.Generate(ctx, "2026-06-30")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	vb := stored.Content.Sections[board.SectionVendorBurndown]
	if vb.Data.VendorBurndownTotal != 0 || vb.Data.VendorBurndownOnTime != 0 || vb.Data.VendorBurndownPastDue != 0 {
		t.Errorf("nil vendor reader did not seed zero scalars: %+v", vb.Data)
	}
	if !strings.Contains(vb.TemplatedText, "No high-criticality vendors are registered") {
		t.Errorf("nil-reader narrative missing 'no high-criticality vendors' line: %q", vb.TemplatedText)
	}
}

// PackGenerator.Generate rejects a malformed period_end with
// ErrPackBadPeriodEnd.
func TestPackGenerator_Generate_BadPeriodEndRejected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	gen := board.NewPackGenerator(board.NewPackStore(app), fixedFreshness{}, fixedDrift{}, nil)
	if _, err := gen.Generate(ctx, "not-a-date"); !errors.Is(err, board.ErrPackBadPeriodEnd) {
		t.Fatalf("Generate(bad date) err = %v, want ErrPackBadPeriodEnd", err)
	}
}

// =========================================================================
// helpers
// =========================================================================

// newSeededDraftPack returns a Pack with every fixed section populated so
// the JSONB shape round-trips cleanly through Insert/Get. Used by the
// PackStore round-trip tests.
func newSeededDraftPack(periodEnd string) board.Pack {
	p := board.Pack{
		PeriodEnd:   periodEnd,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Status:      board.PackStatusDraft,
		Sections:    make(map[string]board.Section, len(board.SectionKeys)),
	}
	for _, key := range board.SectionKeys {
		p.Sections[key] = board.Section{
			Key:           key,
			Title:         key + " title",
			TemplatedText: "templated for " + key,
		}
	}
	return p
}
