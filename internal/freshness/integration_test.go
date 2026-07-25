//go:build integration

// Integration tests for slice 016: evidence freshness read model. Real
// Postgres only — RLS, the UPSERTed evidence_freshness read-model table, and
// the "stale flagged but never deleted" property (AC-2 / AC-6) are only
// meaningful against a real database. The DB is never mocked.
//
// Run with: go test -tags=integration -race ./internal/freshness/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); the harness seeds
//                       controls + evidence outside the GUC.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//                       freshness.Store runs against this so RLS is enforced.

package freshness_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// freshTenant cleans every slice-016 + dependency table for the tenant after
// the test. evidence_freshness is dropped first (it FKs to controls). Pure
// tenant-scoped DELETE in FK order, so it delegates to dbtest.SeedTenant
// (slice 435 / 742 drain).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"evidence_freshness",
		"evidence_records",
		"controls",
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

// seedControl inserts one control row with the given freshness class ("" for
// the default-null case).
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, freshnessClass string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	var fc *string
	if freshnessClass != "" {
		fc = &freshnessClass
	}
	bundleID := "test-bundle-016fr-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 016 freshness test control', 'AAA', 'automated',
		        $3, $4, '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID, fc); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedEvidence inserts one evidence_records row with the given observed_at.
// The append-only ledger — the freshness Store reads these, never writes.
func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, observedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	controlRef := ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $5, $6)
	`, id, tenant, ctrlID, observedAt, "hash-016fr-"+id.String()[:8], controlRef); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

func findByControl(rows []freshness.ControlFreshness, ctrlID uuid.UUID) (freshness.ControlFreshness, bool) {
	for _, r := range rows {
		if r.ControlID == ctrlID {
			return r, true
		}
	}
	return freshness.ControlFreshness{}, false
}

func countEvidenceRecords(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_records WHERE tenant_id = $1 AND control_id = $2`,
		tenant, ctrlID).Scan(&n); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	return n
}

// ===== tracer bullet: Refresh derives valid_until from freshest observed_at =====

func TestRefresh_DerivesValidUntilFromFreshestObservedAt(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// A `weekly` control (30d max-age, canvas §2.3) with evidence observed
	// 5 days ago — well inside the window, so fresh.
	ctrlID := seedControl(t, admin, tenant, "weekly")
	observed := time.Now().UTC().Add(-5 * 24 * time.Hour)
	seedEvidence(t, admin, tenant, ctrlID, observed)

	store := freshness.NewStore(app)
	n, err := store.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if n != 1 {
		t.Fatalf("Refresh wrote %d rows, want 1", n)
	}

	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	cf, ok := findByControl(rows, ctrlID)
	if !ok {
		t.Fatalf("control %s not in freshness read model", ctrlID)
	}
	if cf.FreshnessClass != "weekly" {
		t.Errorf("FreshnessClass = %q, want weekly", cf.FreshnessClass)
	}
	if cf.LatestObservedAt == nil {
		t.Fatal("LatestObservedAt is nil, want the seeded observed_at")
	}
	if cf.ValidUntil == nil {
		t.Fatal("ValidUntil is nil, want observed_at + 30d")
	}
	// weekly = 30d max-age. valid_until should be observed + 30d.
	wantValidUntil := observed.Add(30 * 24 * time.Hour)
	if diff := cf.ValidUntil.Sub(wantValidUntil); diff > time.Second || diff < -time.Second {
		t.Errorf("ValidUntil = %v, want ~%v (observed + 30d)", cf.ValidUntil, wantValidUntil)
	}
	if cf.IsStale {
		t.Error("IsStale = true, want false — evidence is 5d old inside a 30d window")
	}
	if cf.EvidenceCount != 1 {
		t.Errorf("EvidenceCount = %d, want 1", cf.EvidenceCount)
	}
}

// ===== AC-2 / P0-2: per-control freshness_class respected — a realtime
// control with day-old evidence is stale; a monthly control with the same
// evidence is fresh =====

func TestRefresh_RespectsPerControlFreshnessClass(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	observed := time.Now().UTC().Add(-2 * 24 * time.Hour) // 2 days ago

	// realtime = 24h max-age — 2-day-old evidence is STALE.
	realtimeCtrl := seedControl(t, admin, tenant, "realtime")
	seedEvidence(t, admin, tenant, realtimeCtrl, observed)

	// monthly = 90d max-age — the SAME 2-day-old evidence is FRESH.
	monthlyCtrl := seedControl(t, admin, tenant, "monthly")
	seedEvidence(t, admin, tenant, monthlyCtrl, observed)

	store := freshness.NewStore(app)
	if _, err := store.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	rt, ok := findByControl(rows, realtimeCtrl)
	if !ok {
		t.Fatal("realtime control missing from read model")
	}
	if !rt.IsStale {
		t.Error("realtime control IsStale = false, want true — 2d-old evidence past a 24h window")
	}

	mo, ok := findByControl(rows, monthlyCtrl)
	if !ok {
		t.Fatal("monthly control missing from read model")
	}
	if mo.IsStale {
		t.Error("monthly control IsStale = true, want false — 2d-old evidence inside a 90d window")
	}
}

// ===== AC-2 / AC-6: a stale record is FLAGGED is_stale=true in the read
// model but is NEVER deleted from the evidence ledger =====

func TestRefresh_StaleRecordFlaggedButNeverDeleted(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// A daily control (7d max-age) with evidence observed 100 days ago —
	// far past the window. Stale.
	ctrlID := seedControl(t, admin, tenant, "daily")
	seedEvidence(t, admin, tenant, ctrlID, time.Now().UTC().Add(-100*24*time.Hour))

	beforeCount := countEvidenceRecords(t, admin, tenant, ctrlID)
	if beforeCount != 1 {
		t.Fatalf("seeded evidence count = %d, want 1", beforeCount)
	}

	store := freshness.NewStore(app)
	if _, err := store.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	cf, ok := findByControl(rows, ctrlID)
	if !ok {
		t.Fatal("control missing from read model")
	}
	if !cf.IsStale {
		t.Error("IsStale = false, want true — evidence is 100d old in a 7d window")
	}

	// AC-6: the evidence record itself must still be in the ledger,
	// queryable for audit replay. The refresh flags; it never deletes.
	afterCount := countEvidenceRecords(t, admin, tenant, ctrlID)
	if afterCount != beforeCount {
		t.Errorf("evidence record count after Refresh = %d, want %d — refresh must NEVER delete from the ledger",
			afterCount, beforeCount)
	}
}

// ===== a control with NO evidence is is_stale=true and has no valid_until =====

func TestRefresh_NoEvidenceControlIsStale(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	ctrlID := seedControl(t, admin, tenant, "monthly")
	// No evidence seeded.

	store := freshness.NewStore(app)
	if _, err := store.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	cf, ok := findByControl(rows, ctrlID)
	if !ok {
		t.Fatal("no-evidence control missing from read model — it must still appear")
	}
	if !cf.IsStale {
		t.Error("IsStale = false, want true — a control with no evidence is not fresh")
	}
	if cf.ValidUntil != nil {
		t.Errorf("ValidUntil = %v, want nil — no observed_at to derive a horizon from", cf.ValidUntil)
	}
	if cf.EvidenceCount != 0 {
		t.Errorf("EvidenceCount = %d, want 0", cf.EvidenceCount)
	}
}

// ===== Refresh is idempotent: running it twice UPSERTs, not duplicates =====

func TestRefresh_IsIdempotent(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	ctrlID := seedControl(t, admin, tenant, "weekly")
	seedEvidence(t, admin, tenant, ctrlID, time.Now().UTC().Add(-3*24*time.Hour))

	store := freshness.NewStore(app)
	if _, err := store.Refresh(ctx); err != nil {
		t.Fatalf("Refresh #1: %v", err)
	}
	if _, err := store.Refresh(ctx); err != nil {
		t.Fatalf("Refresh #2: %v", err)
	}

	// Two refreshes -> still exactly one row per control (UPSERT, not append).
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_freshness WHERE tenant_id = $1 AND control_id = $2`,
		tenant, ctrlID).Scan(&n); err != nil {
		t.Fatalf("count freshness rows: %v", err)
	}
	if n != 1 {
		t.Errorf("evidence_freshness row count = %d after two refreshes, want 1 — UPSERT must not duplicate", n)
	}
}

// ===== cross-tenant RLS isolation: tenant A's freshness Store never sees
// tenant B's rows =====

func TestRefresh_CrossTenantIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	ctrlA := seedControl(t, admin, tenantA, "weekly")
	seedEvidence(t, admin, tenantA, ctrlA, time.Now().UTC().Add(-1*24*time.Hour))
	ctrlB := seedControl(t, admin, tenantB, "weekly")
	seedEvidence(t, admin, tenantB, ctrlB, time.Now().UTC().Add(-1*24*time.Hour))

	store := freshness.NewStore(app)

	// Refresh + list as tenant A.
	ctxA := ctxFor(t, tenantA)
	if _, err := store.Refresh(ctxA); err != nil {
		t.Fatalf("Refresh A: %v", err)
	}
	rowsA, err := store.List(ctxA)
	if err != nil {
		t.Fatalf("List A: %v", err)
	}
	if _, ok := findByControl(rowsA, ctrlB); ok {
		t.Error("tenant A's freshness List returned tenant B's control — RLS isolation breach")
	}
	if _, ok := findByControl(rowsA, ctrlA); !ok {
		t.Error("tenant A's freshness List missing tenant A's own control")
	}
}
