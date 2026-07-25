//go:build integration

// Integration tests for slice 028: AuditPeriod + freezing primitive. Real
// Postgres only -- RLS cannot be tested against a fake DB, and the hash
// idempotence contract (AC-7) is only meaningful against a real ledger.
//
// Run with: go test -tags=integration -race ./internal/audit/period/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); used by the harness
//                       to seed controls + evidence outside the tenant GUC.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//                       period.Store runs against this so RLS is enforced.

package period_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 435 / 742: the pool/DSN/tenant-seed/tenant-context boilerplate this
// file used to re-derive (appDSN / adminDSN / openPool / freshTenant / inline
// tenancy.WithTenant) now lives in the shared internal/dbtest harness.
// dbtest.NewAppPool opens the RLS-enforcing atlas_app pool (the default — the
// period.Store / audit.Store and every RLS-bound assertion run through it);
// dbtest.NewMigratePool opens the privileged BYPASSRLS pool used ONLY for
// cross-tenant fixture seeding, the append-only audit_period_audit_log /
// sample_audit_log / evidence_records cleanup, the AC-7 admin rollback, and the
// information_schema anti-criterion probe — none of which the app role can
// perform. dbtest.WithTenantCtx tags the tenant GUC context. seedFrameworkVersion
// / seedControl / seedEvidence remain suite-local (FK-parent + ledger fixtures
// dbtest does not provide); they route through the migrate pool exactly as before.

// freshTenant returns a fresh tenant id and registers cleanup of the slice-028 +
// slice-026 tables (children before parents, with the populations FK detach
// kept as a leading statement) via the privileged migrate pool. The
// freshTenant body retains the populations.audit_period_id detach UPDATE that
// dbtest.SeedTenant's table-DELETE loop does not express, so the cleanup
// ordering is byte-for-byte preserved.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`UPDATE populations SET audit_period_id = NULL, frozen_at = NULL WHERE tenant_id = $1`,
			`DELETE FROM sample_audit_log WHERE tenant_id = $1`,
			`DELETE FROM sample_annotations WHERE tenant_id = $1`,
			`DELETE FROM sample_evidence WHERE tenant_id = $1`,
			`DELETE FROM samples WHERE tenant_id = $1`,
			`DELETE FROM populations WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedFrameworkVersion seeds a global-catalog framework + framework_version
// (tenant_id IS NULL) so the test's audit_periods row can FK to it. Global
// rows are visible to every tenant by RLS.
func seedFrameworkVersion(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	fwID := uuid.New()
	versionID := uuid.New()
	slug := fmt.Sprintf("slice028-%s", uuid.NewString()[:8])
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, 'Slice 028 test framework', $2, 'test')
	`, fwID, slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')
	`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	return versionID
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'slice 028 test control', 'AAA', 'automated', 'test-bundle-028')
	`, ctrlID, tenant); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedEvidence inserts n evidence_records rows with observed_at evenly
// spaced one hour apart starting at windowStart.
func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, n int, windowStart time.Time) []uuid.UUID {
	t.Helper()
	out := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		out[i] = id
		observed := windowStart.Add(time.Duration(i) * time.Hour)
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO evidence_records (
				id, tenant_id, control_id, observed_at, ingested_at,
				provenance, result, payload, hash, control_ref
			)
			VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $5, 'test')
		`, id, tenant, ctrlID, observed, fmt.Sprintf("hash-028-%03d", i)); err != nil {
			t.Fatalf("seed evidence %d: %v", i, err)
		}
	}
	return out
}

// ===== AC-1: POST /v1/audit-periods creates open period =====

func TestCreate_ReturnsOpenPeriod(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	p, err := store.Create(ctx, period.CreateInput{
		Name:               "SOC 2 2026 Q2",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_test_028_ac1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Status != period.StatusOpen {
		t.Fatalf("AC-1: expected status=open, got %q", p.Status)
	}
	if p.FrozenAt != nil {
		t.Fatalf("AC-1: expected frozen_at=NULL on create, got %v", *p.FrozenAt)
	}
	if len(p.FrozenHash) != 0 {
		t.Fatalf("AC-1: expected frozen_hash=NULL on create, got %x", p.FrozenHash)
	}
	if p.Name != "SOC 2 2026 Q2" {
		t.Fatalf("AC-1: expected name=\"SOC 2 2026 Q2\", got %q", p.Name)
	}
	if p.FrameworkVersionID != fwID {
		t.Fatalf("AC-1: expected framework_version_id=%v, got %v", fwID, p.FrameworkVersionID)
	}
	if p.CreatedBy != "key_test_028_ac1" {
		t.Fatalf("AC-1: expected created_by=key_test_028_ac1, got %q", p.CreatedBy)
	}
}

// ===== AC-2: POST /v1/audit-periods/:id/freeze stamps frozen_at + hash =====

func TestFreeze_SetsFrozenAtAndHash(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	_ = seedEvidence(t, admin, tenant, ctrlID, 5, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	p, err := store.Create(ctx, period.CreateInput{
		Name:               "AC-2 period",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_test_028_ac2",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	frozen, err := store.Freeze(ctx, p.ID, "key_test_028_ac2", time.Now().UTC())
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if frozen.Status != period.StatusFrozen {
		t.Fatalf("AC-2: expected status=frozen, got %q", frozen.Status)
	}
	if frozen.FrozenAt == nil {
		t.Fatalf("AC-2: expected frozen_at to be set")
	}
	if len(frozen.FrozenHash) != 32 {
		t.Fatalf("AC-2: expected 32-byte sha256 hash, got %d bytes", len(frozen.FrozenHash))
	}
	if frozen.FrozenBy != "key_test_028_ac2" {
		t.Fatalf("AC-2: expected frozen_by=key_test_028_ac2, got %q", frozen.FrozenBy)
	}
}

// ===== AC-3: control-state query honors frozen_at horizon =====

func TestControlState_HonorsFrozenAtHorizon(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	// 24 hourly records starting at windowStart.
	_ = seedEvidence(t, admin, tenant, ctrlID, 24, windowStart)

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	p, err := store.Create(ctx, period.CreateInput{
		Name:               "AC-3 period",
		FrameworkVersionID: fwID,
		PeriodStart:        windowStart,
		PeriodEnd:          windowStart.Add(48 * time.Hour),
		CreatedBy:          "key_test_028_ac3",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Freeze at hour 10 -- records 0..10 (11 records inclusive) are
	// visible, 11..23 (13 records) are NOT.
	frozenAt := windowStart.Add(10 * time.Hour)
	if _, err := store.Freeze(ctx, p.ID, "key_test_028_ac3", frozenAt); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	obs, err := store.ControlState(ctx, p.ID, ctrlID)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(obs) != 11 {
		t.Fatalf("AC-3: expected 11 observations <= frozen_at, got %d", len(obs))
	}
	for _, o := range obs {
		if o.ObservedAt.After(frozenAt) {
			t.Fatalf("AC-3: observation at %v exceeds frozen_at %v",
				o.ObservedAt, frozenAt)
		}
	}
}

// ===== AC-4: population attached to frozen period excludes post-freeze records =====

func TestAttachPopulation_FrozenPeriodExcludesPostFreezeEvidence(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenant, ctrlID, 30, windowStart)

	periodStore := period.NewStore(app)
	auditStore := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	p, err := periodStore.Create(ctx, period.CreateInput{
		Name:               "AC-4 period",
		FrameworkVersionID: fwID,
		PeriodStart:        windowStart,
		PeriodEnd:          windowStart.Add(72 * time.Hour),
		CreatedBy:          "key_test_028_ac4",
	})
	if err != nil {
		t.Fatalf("Create period: %v", err)
	}

	// Slice 026 population spanning the entire period window. row_count
	// at creation reflects the live ledger; once we attach + freeze, the
	// next DrawSample honors populations.frozen_at.
	pop, err := auditStore.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(72 * time.Hour),
		CreatedBy:       "key_test_028_ac4",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	if pop.RowCount != 30 {
		t.Fatalf("AC-4 baseline: expected RowCount=30 pre-freeze, got %d", pop.RowCount)
	}

	// Attach BEFORE freeze. Population.frozen_at remains NULL.
	if err := periodStore.AttachPopulation(ctx, p.ID, pop.ID, "key_test_028_ac4"); err != nil {
		t.Fatalf("AttachPopulation: %v", err)
	}

	// Freeze at hour 10 (records 0..10 visible -> 11 records).
	frozenAt := windowStart.Add(10 * time.Hour)
	if _, err := periodStore.Freeze(ctx, p.ID, "key_test_028_ac4", frozenAt); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	// DrawSample after freeze: the populations.frozen_at stamp now caps
	// the universe at 11 records.
	s, err := auditStore.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: pop.ID,
		N:            100, // ask for more than available
		Seed:         "ac4-seed",
		CreatedBy:    "key_test_028_ac4",
	})
	if err != nil {
		t.Fatalf("DrawSample: %v", err)
	}
	if len(s.EvidenceRecordIDs) != 11 {
		t.Fatalf("AC-4: after freezing at hour 10, expected 11 records, got %d",
			len(s.EvidenceRecordIDs))
	}
}

// ===== AC-5: live evaluation unaffected — new evidence after freeze is reachable via live path =====
//
// "Live evaluation" is whatever read path does NOT join audit_periods. We
// prove this property by hitting the slice-026 query path with a fresh
// (non-period-attached) population AFTER a freeze and asserting it sees
// post-freeze evidence. The period's frozen state does not bleed into the
// live state.

func TestLiveEvaluation_UnaffectedByFreeze(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenant, ctrlID, 20, windowStart)

	periodStore := period.NewStore(app)
	auditStore := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	p, err := periodStore.Create(ctx, period.CreateInput{
		Name:               "AC-5 period",
		FrameworkVersionID: fwID,
		PeriodStart:        windowStart,
		PeriodEnd:          windowStart.Add(48 * time.Hour),
		CreatedBy:          "key_test_028_ac5",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := periodStore.Freeze(ctx, p.ID, "key_test_028_ac5",
		windowStart.Add(5*time.Hour)); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	// Live population (NOT attached to the period). Spans the full
	// window. Slice-026 query path uses COALESCE(frozen_at, 'infinity')
	// = live, so it sees all 20 records.
	livePop, err := auditStore.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(48 * time.Hour),
		CreatedBy:       "key_test_028_ac5",
	})
	if err != nil {
		t.Fatalf("CreatePopulation (live): %v", err)
	}
	if livePop.RowCount != 20 {
		t.Fatalf("AC-5: live evaluation row count unchanged by freeze; expected 20 got %d",
			livePop.RowCount)
	}
	if livePop.FrozenAt != nil {
		t.Fatalf("AC-5: live population.frozen_at must be NULL, got %v", *livePop.FrozenAt)
	}
}

// ===== AC-6: re-freezing a frozen period rejected =====

func TestFreeze_RejectsRefreeze(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	p, err := store.Create(ctx, period.CreateInput{
		Name:               "AC-6 period",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_test_028_ac6",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Freeze(ctx, p.ID, "key_test_028_ac6", time.Now().UTC()); err != nil {
		t.Fatalf("first Freeze: %v", err)
	}
	_, err = store.Freeze(ctx, p.ID, "key_test_028_ac6", time.Now().UTC())
	if !errors.Is(err, period.ErrAlreadyFrozen) {
		t.Fatalf("AC-6: expected ErrAlreadyFrozen, got %v", err)
	}

	// Audit log should contain the freeze_rejected_already_frozen row.
	logRows, err := store.ListLog(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListLog: %v", err)
	}
	var rejectedSeen bool
	for _, lr := range logRows {
		if lr.Action == "freeze_rejected_already_frozen" {
			rejectedSeen = true
			break
		}
	}
	if !rejectedSeen {
		t.Fatalf("AC-6: expected freeze_rejected_already_frozen audit log entry, got %d rows", len(logRows))
	}
}

// ===== AC-7: re-hash of unchanged content produces identical bytes =====
//
// AC-7 reads: "freezing the same content twice produces the same hash."
// "Twice" requires a way to re-run the freeze under unchanged conditions.
// Production users cannot re-freeze (AC-6 forbids it). The test uses a
// privileged admin SQL rollback to flip status back to 'open' (simulating
// a hypothetical disaster-recovery flow), then re-freezes and asserts the
// resulting hash bytes are byte-identical.

func TestFreeze_HashIsIdempotentForUnchangedContent(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenant, ctrlID, 7, windowStart)

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	p, err := store.Create(ctx, period.CreateInput{
		Name:               "AC-7 period",
		FrameworkVersionID: fwID,
		PeriodStart:        windowStart,
		PeriodEnd:          windowStart.Add(72 * time.Hour),
		CreatedBy:          "key_test_028_ac7",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	frozenAt := windowStart.Add(10 * time.Hour)
	first, err := store.Freeze(ctx, p.ID, "key_test_028_ac7", frozenAt)
	if err != nil {
		t.Fatalf("first Freeze: %v", err)
	}
	hash1 := append([]byte(nil), first.FrozenHash...)

	// Admin-privileged rollback to 'open' so we can re-freeze (otherwise
	// AC-6 prevents it). This simulates a recovery flow; ordinary users
	// cannot reach this state.
	if _, err := admin.Exec(context.Background(),
		`UPDATE audit_periods
		 SET status      = 'open',
		     frozen_at   = NULL,
		     frozen_hash = NULL,
		     frozen_by   = NULL,
		     updated_at  = now()
		 WHERE id = $1`, p.ID); err != nil {
		t.Fatalf("admin rollback: %v", err)
	}

	// Re-freeze at the SAME wall-clock and content. AC-7 holds because
	// frozen_at is NOT in the hash input set (ADR 0003) -- but to make
	// the assertion airtight we pin the same wall-clock here too.
	second, err := store.Freeze(ctx, p.ID, "key_test_028_ac7", frozenAt)
	if err != nil {
		t.Fatalf("second Freeze: %v", err)
	}
	hash2 := second.FrozenHash

	if hex.EncodeToString(hash1) != hex.EncodeToString(hash2) {
		t.Fatalf("AC-7: re-hash of unchanged content differs:\n  first  = %x\n  second = %x",
			hash1, hash2)
	}

	// And the hash actually has nonzero distinguishing bytes (not the
	// all-zero "empty input" sentinel).
	allZero := true
	for _, b := range hash1 {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatalf("AC-7: freeze hash is all-zero; expected sha256 with content")
	}
}

// ===== RLS: cross-tenant invisibility =====

func TestPeriod_CrossTenantInvisible(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)

	store := period.NewStore(app)
	ctxA := dbtest.WithTenantCtx(t, tenantA)
	ctxB := dbtest.WithTenantCtx(t, tenantB)

	pA, err := store.Create(ctxA, period.CreateInput{
		Name:               "tenant A period",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_a",
	})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	if _, err := store.Get(ctxB, pA.ID); !errors.Is(err, period.ErrNotFound) {
		t.Fatalf("RLS: tenant B saw tenant A's period: %v", err)
	}
}

// ===== Anti-criterion ISC-A1: no evidence snapshot table introduced =====
//
// This is a structural assertion best done at the file layer (ship-gate
// also greps the migrations dir). At the test layer we assert there is no
// public.evidence_records_snapshot table -- if a future slice introduces
// one without removing it before this test runs, the test fails.

func TestAntiCriterion_NoEvidenceSnapshotTable(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	row := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public'
		   AND table_name LIKE 'evidence%snapshot%'`)
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	if n != 0 {
		t.Fatalf("anti-criterion P0: found %d evidence_*snapshot* tables; expected 0", n)
	}
}

// ===== Audit log: period_created + period_frozen rows recorded =====

func TestAuditLog_RecordsCreateAndFreeze(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	p, err := store.Create(ctx, period.CreateInput{
		Name:               "audit-log period",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_test_028_log",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Freeze(ctx, p.ID, "key_test_028_log", time.Now().UTC()); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	logRows, err := store.ListLog(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListLog: %v", err)
	}
	if len(logRows) < 2 {
		t.Fatalf("expected >=2 audit log rows (created + frozen), got %d", len(logRows))
	}
	var created, frozen bool
	for _, lr := range logRows {
		switch lr.Action {
		case "period_created":
			created = true
		case "period_frozen":
			frozen = true
		}
	}
	if !created || !frozen {
		t.Fatalf("expected period_created (%v) AND period_frozen (%v)", created, frozen)
	}
}
