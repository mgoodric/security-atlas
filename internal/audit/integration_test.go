//go:build integration

// Integration tests for slice 026: sample-pull primitives. Real Postgres only
// -- RLS cannot be tested against a fake DB and the determinism contract
// (AC-2) is only meaningful against a real id-ordered evidence ledger.
//
// Run with:  go test -tags=integration -race ./internal/audit/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); used by the harness
//                       to seed controls + evidence outside the tenant GUC.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//                       audit.Store runs against this so RLS is enforced.

package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 435 / 742: the pool/DSN/tenant-seed/tenant-context boilerplate this
// file used to re-derive (appDSN / adminDSN / openPool / freshTenant / inline
// tenancy.WithTenant) now lives in the shared internal/dbtest harness.
// dbtest.NewAppPool opens the RLS-enforcing atlas_app pool (the default —
// audit.Store and every RLS-bound assertion run through it); dbtest.NewMigratePool
// opens the privileged BYPASSRLS pool used ONLY for cross-tenant seeding and the
// append-only sample_audit_log / evidence_records cleanup the app role cannot
// DELETE. dbtest.SeedTenant returns a fresh tenant id and registers the
// FK-safe-ordered cleanup through that migrate pool; dbtest.WithTenantCtx tags
// the tenant GUC context. seedControl / seedEvidence remain suite-local
// (FK-parent + ledger fixtures dbtest does not provide); they route through the
// migrate pool exactly as before.

// freshTenant returns a fresh tenant id and registers cleanup of the slice-026
// tables (children before parents) via the privileged migrate pool — the
// sample_audit_log + evidence_records rows are append-only under RLS for the
// app role, so only the migrate role can DELETE them.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"sample_audit_log",
		"sample_annotations",
		"sample_evidence",
		"samples",
		"populations",
		"evidence_records",
		"controls",
	)
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	// bundle_id is NOT NULL since slice 009 -- use a sentinel value
	// rather than seeding a real control bundle row (which slice 009 owns).
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'sample-pull test control', 'AAA', 'automated', 'test-bundle-026')
	`, ctrlID, tenant); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedEvidence inserts n evidence_records rows with observed_at evenly
// spaced inside [windowStart, windowEnd]. ids are server-generated UUIDs,
// returned for the test's reference.
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
		`, id, tenant, ctrlID, observed, fmt.Sprintf("hash-%03d", i)); err != nil {
			t.Fatalf("seed evidence %d: %v", i, err)
		}
	}
	return out
}

// ===== AC-1: POST /v1/populations returns row_count =====

func TestCreatePopulation_ReturnsRowCount(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenant, ctrlID, 25, windowStart)

	store := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(48 * time.Hour),
		CreatedBy:       "key_test_001",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	if pop.RowCount != 25 {
		t.Fatalf("AC-1: expected row_count=25 (full window), got %d", pop.RowCount)
	}
}

// ===== AC-2: same seed -> identical N records =====

func TestDrawSample_IsDeterministic(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Fixed 100-record population (load-bearing per slice spec).
	_ = seedEvidence(t, admin, tenant, ctrlID, 100, windowStart)

	store := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(200 * time.Hour),
		CreatedBy:       "key_test_002",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	if pop.RowCount != 100 {
		t.Fatalf("expected RowCount=100, got %d", pop.RowCount)
	}

	const seed = "soc2-2026q2-deterministic"
	a, err := store.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: pop.ID,
		N:            10,
		Seed:         seed,
		CreatedBy:    "key_test_002",
	})
	if err != nil {
		t.Fatalf("DrawSample first: %v", err)
	}
	b, err := store.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: pop.ID,
		N:            10,
		Seed:         seed,
		CreatedBy:    "key_test_002",
	})
	if err != nil {
		t.Fatalf("DrawSample second: %v", err)
	}
	if len(a.EvidenceRecordIDs) != 10 || len(b.EvidenceRecordIDs) != 10 {
		t.Fatalf("expected both samples len=10, got %d / %d",
			len(a.EvidenceRecordIDs), len(b.EvidenceRecordIDs))
	}
	for i := range a.EvidenceRecordIDs {
		if a.EvidenceRecordIDs[i] != b.EvidenceRecordIDs[i] {
			t.Fatalf("AC-2 determinism: position %d differs %v vs %v",
				i, a.EvidenceRecordIDs[i], b.EvidenceRecordIDs[i])
		}
	}
	// And the two samples have distinct ids (separate rows in DB).
	if a.ID == b.ID {
		t.Fatalf("expected two distinct samples rows, got same id %v", a.ID)
	}
}

// ===== AC-3: Sample row records seed, n, created_by, created_at =====

func TestDrawSample_PersistsSeedAndMetadata(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenant, ctrlID, 20, windowStart)

	store := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(72 * time.Hour),
		CreatedBy:       "key_test_003",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	s, err := store.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: pop.ID,
		N:            5,
		Seed:         "ac3-seed",
		CreatedBy:    "key_auditor_001",
	})
	if err != nil {
		t.Fatalf("DrawSample: %v", err)
	}

	got, err := store.GetSample(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSample: %v", err)
	}
	if got.Seed != "ac3-seed" {
		t.Fatalf("AC-3: expected seed=ac3-seed, got %q", got.Seed)
	}
	if got.N != 5 {
		t.Fatalf("AC-3: expected n=5, got %d", got.N)
	}
	if got.CreatedBy != "key_auditor_001" {
		t.Fatalf("AC-3: expected created_by=key_auditor_001, got %q", got.CreatedBy)
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("AC-3: created_at must be populated")
	}
	if len(got.EvidenceRecordIDs) != 5 {
		t.Fatalf("AC-3: expected 5 evidence ids, got %d", len(got.EvidenceRecordIDs))
	}
}

// ===== AC-4: annotation with result in {passed, failed, not-applicable} =====

func TestAnnotateSample_RecordsResultPerRecord(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenant, ctrlID, 10, windowStart)

	store := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(24 * time.Hour),
		CreatedBy:       "key_test_ac4",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	s, err := store.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: pop.ID,
		N:            3,
		Seed:         "ac4-seed",
		CreatedBy:    "key_test_ac4",
	})
	if err != nil {
		t.Fatalf("DrawSample: %v", err)
	}

	// Annotate all three records with different results.
	results := []string{"passed", "failed", "not-applicable"}
	for i, recID := range s.EvidenceRecordIDs {
		_, err := store.AnnotateSample(ctx, audit.AnnotateSampleInput{
			SampleID:         s.ID,
			EvidenceRecordID: recID,
			Result:           results[i],
			AnnotatedBy:      "key_auditor",
			Notes:            fmt.Sprintf("note-%d", i),
		})
		if err != nil {
			t.Fatalf("AnnotateSample %d: %v", i, err)
		}
	}

	anns, err := store.ListAnnotations(ctx, s.ID)
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}
	if len(anns) != 3 {
		t.Fatalf("AC-4: expected 3 annotations, got %d", len(anns))
	}
	seen := make(map[string]bool, 3)
	for _, a := range anns {
		seen[a.Result] = true
	}
	for _, r := range results {
		if !seen[r] {
			t.Fatalf("AC-4: missing annotation with result=%s", r)
		}
	}

	// Invalid result rejected.
	_, err = store.AnnotateSample(ctx, audit.AnnotateSampleInput{
		SampleID:         s.ID,
		EvidenceRecordID: s.EvidenceRecordIDs[0],
		Result:           "maybe",
		AnnotatedBy:      "key_auditor",
	})
	if !errors.Is(err, audit.ErrInvalidAnnotation) {
		t.Fatalf("AC-4 validation: expected ErrInvalidAnnotation, got %v", err)
	}
}

// ===== AC-5: forward-compat audit-period freezing (no-op until slice 028) =====

func TestPopulation_FrozenAtIsForwardCompat(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	all := seedEvidence(t, admin, tenant, ctrlID, 30, windowStart)

	store := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(48 * time.Hour),
		CreatedBy:       "key_test_ac5",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	if pop.RowCount != 30 {
		t.Fatalf("AC-5 (live): expected 30 records, got %d", pop.RowCount)
	}
	if pop.FrozenAt != nil {
		t.Fatalf("AC-5 forward-compat: frozen_at should be NULL on create, got %v", *pop.FrozenAt)
	}
	_ = all

	// Simulate slice 028 stamping frozen_at to mid-window. Sampling from
	// THIS frozen population should return only records observed at or
	// before frozen_at.
	frozenAt := windowStart.Add(10 * time.Hour) // covers seeds 0..10 inclusive (11 records)
	if _, err := admin.Exec(context.Background(),
		`UPDATE populations SET frozen_at = $1 WHERE id = $2`,
		frozenAt, pop.ID,
	); err != nil {
		t.Fatalf("update frozen_at: %v", err)
	}

	s, err := store.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: pop.ID,
		N:            100, // ask for more than population so we get all of it
		Seed:         "ac5-seed",
		CreatedBy:    "key_test_ac5",
	})
	if err != nil {
		t.Fatalf("DrawSample after freeze: %v", err)
	}
	if len(s.EvidenceRecordIDs) != 11 {
		t.Fatalf("AC-5: after freezing at hour 10, expected 11 records, got %d",
			len(s.EvidenceRecordIDs))
	}
}

// ===== AC-6: every sample pull writes a sample_audit_log row =====

func TestDrawSample_WritesAuditLogWithSeed(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenant, ctrlID, 12, windowStart)

	store := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(24 * time.Hour),
		CreatedBy:       "key_test_ac6",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	s, err := store.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: pop.ID,
		N:            4,
		Seed:         "ac6-replay-seed",
		CreatedBy:    "key_test_ac6",
	})
	if err != nil {
		t.Fatalf("DrawSample: %v", err)
	}

	log, err := store.ListAuditLog(ctx, 20)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if len(log) < 2 {
		t.Fatalf("AC-6: expected >=2 audit rows (population_created + sample_drawn), got %d", len(log))
	}
	var sampleDrawn bool
	for _, row := range log {
		if row.Action == "sample_drawn" &&
			row.SampleID.Valid && uuid.UUID(row.SampleID.Bytes) == s.ID &&
			row.Seed != nil && *row.Seed == "ac6-replay-seed" {
			sampleDrawn = true
			break
		}
	}
	if !sampleDrawn {
		t.Fatalf("AC-6: expected sample_drawn audit row with seed -> sample_id mapping, got %+v", log)
	}
}

// ===== RLS: cross-tenant invisibility =====

func TestPopulation_CrossTenantInvisible(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctrlA := seedControl(t, admin, tenantA)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenantA, ctrlA, 5, windowStart)

	store := audit.NewStore(app)
	ctxA := dbtest.WithTenantCtx(t, tenantA)
	ctxB := dbtest.WithTenantCtx(t, tenantB)

	pop, err := store.CreatePopulation(ctxA, audit.CreatePopulationInput{
		ControlID:       ctrlA,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(24 * time.Hour),
		CreatedBy:       "key_a",
	})
	if err != nil {
		t.Fatalf("CreatePopulation A: %v", err)
	}

	if _, err := store.GetPopulation(ctxB, pop.ID); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("RLS: tenant B should not see tenant A's population, got err=%v", err)
	}
}

// ===== anti-criterion P0: sampling does NOT mutate evidence_records =====

func TestDrawSample_DoesNotMutateLedger(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seeded := seedEvidence(t, admin, tenant, ctrlID, 20, windowStart)

	store := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(72 * time.Hour),
		CreatedBy:       "key_no_mutate",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	if _, err := store.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: pop.ID,
		N:            5,
		Seed:         "no-mutate",
		CreatedBy:    "key_no_mutate",
	}); err != nil {
		t.Fatalf("DrawSample: %v", err)
	}

	// Verify all 20 evidence records still exist with original ids.
	rows, err := admin.Query(context.Background(),
		`SELECT id FROM evidence_records WHERE tenant_id = $1 ORDER BY id`, tenant)
	if err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	defer rows.Close()
	got := make([]uuid.UUID, 0, len(seeded))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, id)
	}
	want := append([]uuid.UUID(nil), seeded...)
	sort.Slice(want, func(i, j int) bool { return less(want[i], want[j]) })
	if len(got) != len(want) {
		t.Fatalf("anti-criterion P0: evidence count changed: %d -> %d", len(want), len(got))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("anti-criterion P0: evidence row mutated at %d: %v vs %v",
				i, want[i], got[i])
		}
	}
}

func less(a, b uuid.UUID) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// ===== scope-predicate reuse: ensures internal/scope.Evaluate is the path =====

func TestCreatePopulation_AppliesScopePredicate(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Seed two scope cells: prod and dev. Seed two evidence records,
	// each pointing at one cell. A predicate {"op":"eq","dim":"environment","value":"prod"}
	// should resolve to exactly one record.
	prodCell := uuid.New()
	devCell := uuid.New()
	for _, row := range []struct {
		id   uuid.UUID
		env  string
		dims string
	}{
		{prodCell, "prod", `{"environment":"prod"}`},
		{devCell, "dev", `{"environment":"dev"}`},
	} {
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
			VALUES ($1, $2, $3, $4::jsonb, $5)
		`, row.id, tenant, row.env, row.dims, fmt.Sprintf("hash-%s", row.env)); err != nil {
			t.Fatalf("seed scope cell %s: %v", row.env, err)
		}
	}
	// Also seed the scope_dimensions row so scope.Evaluate is happy.
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO scope_dimensions (id, tenant_id, name, value_type, is_builtin)
		VALUES ($1, $2, 'environment', 'string', true)
	`, uuid.New(), tenant); err != nil {
		t.Fatalf("seed dimension: %v", err)
	}

	// Seed evidence with scope_id pointing at prod / dev respectively.
	// We need a corresponding scopes row too (evidence_records.scope_id ->
	// scopes(id), not scope_cells(id)). Slice 017 chose to keep slice-002's
	// `scopes` table as the FK target. Mirror prodCell as a scopes row of
	// the same UUID; the audit store evaluates scope.Evaluate against
	// scope_cells but the join is by scope_id only -- when we set the
	// evidence's scope_id == scope_cells.id, our applyScopeFilter matches.
	for _, id := range []uuid.UUID{prodCell, devCell} {
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO scopes (id, tenant_id) VALUES ($1, $2)
		`, id, tenant); err != nil {
			t.Fatalf("seed scope mirror: %v", err)
		}
	}

	// One evidence per cell.
	for i, scopeID := range []uuid.UUID{prodCell, devCell} {
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO evidence_records (
				id, tenant_id, control_id, scope_id, observed_at, ingested_at,
				provenance, result, payload, hash, control_ref
			)
			VALUES ($1, $2, $3, $4, $5, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $6, 'test')
		`, uuid.New(), tenant, ctrlID, scopeID, windowStart.Add(time.Duration(i)*time.Hour),
			fmt.Sprintf("hash-scope-%d", i)); err != nil {
			t.Fatalf("seed evidence %d: %v", i, err)
		}
	}

	store := audit.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	predicate, _ := json.Marshal(map[string]any{
		"op": "eq", "dim": "environment", "value": "prod",
	})
	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       ctrlID,
		ScopePredicate:  predicate,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowStart.Add(72 * time.Hour),
		CreatedBy:       "key_test_scope",
	})
	if err != nil {
		t.Fatalf("CreatePopulation with predicate: %v", err)
	}
	if pop.RowCount != 1 {
		t.Fatalf("scope intersection: expected RowCount=1 (prod only), got %d", pop.RowCount)
	}
}
