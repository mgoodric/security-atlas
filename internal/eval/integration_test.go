//go:build integration

// Integration tests for slice 012: control state evaluation engine. Real
// Postgres only — RLS, the append-only control_evaluations ledger, and the
// AC-7 replay-reproducibility property are only meaningful against a real
// database. The DB is never mocked.
//
// Run with: go test -tags=integration -race ./internal/eval/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); the harness seeds
//                       controls + evidence + scope cells outside the GUC.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//                       eval.Store + scope.Store run against this so RLS is
//                       enforced on every read and the single write.

package eval_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// jsonString returns s as a JSON string literal (quoted, escaped) so test
// fixtures can embed a multi-line Rego expression inside a JSON array.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

// freshTenant cleans every slice-012 + dependency table for the tenant after
// the test so reruns do not accumulate. control_evaluations is dropped first
// (it FKs to controls + scope_cells).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM scope_cells WHERE tenant_id = $1`,
			`DELETE FROM scope_dimensions WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// seedControl inserts one control row. implType is the implementation_type
// (automated | manual_attested | ...); freshnessClass may be "" for the
// default. evidenceQueries is the raw evidence_queries JSONB ("[]" for none).
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, implType, freshnessClass, evidenceQueries string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	var fc *string
	if freshnessClass != "" {
		fc = &freshnessClass
	}
	if evidenceQueries == "" {
		evidenceQueries = "[]"
	}
	// bundle_id is computed in Go (not `'prefix' || $1::text`) so no
	// placeholder is referenced twice — a single-placeholder-reused
	// expression trips pgx type inference (SQLSTATE 42P08).
	bundleID := "test-bundle-012-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 012 test control', 'AAA', $3,
		        $4, $5, $6::jsonb, 'true')
	`, ctrlID, tenant, implType, bundleID, fc, evidenceQueries); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedEvidence inserts one evidence_records row with the given result +
// observed_at. The append-only ledger — the engine reads these, never writes.
func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, result string, observedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	// control_ref is the ctrlID string passed as its own param ($7) — not
	// `$3::text` — so no placeholder is referenced twice (avoids 42P08).
	controlRef := ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, $5, '{}'::jsonb, $6, $7)
	`, id, tenant, ctrlID, observedAt, result, "hash-012-"+id.String()[:8], controlRef); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

// seedScopeDimension + seedScopeCell give the tenant a cell universe so the
// applicability_expr evaluation has somewhere to resolve.
func seedScopeCell(t *testing.T, admin *pgxpool.Pool, tenant, label string) uuid.UUID {
	t.Helper()
	cellID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
		VALUES ($1, $2, $3, '{"environment":"prod"}'::jsonb, $4)
	`, cellID, tenant, label, "hash-cell-"+cellID.String()[:8]); err != nil {
		t.Fatalf("seed scope cell: %v", err)
	}
	return cellID
}

func newEngine(app *pgxpool.Pool) *eval.Engine {
	return eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
}

// countEvaluations is a raw read of how many control_evaluations rows exist
// for the tenant — used by the replay test (AC-7).
func countEvaluations(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control_evaluations WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count evaluations: %v", err)
	}
	return n
}

// ===== AC-4: manual_attested control with a fresh attestation -> pass =====

func TestEvaluateControl_ManualAttestedFreshAttestationIsPass(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// A manual_attested control whose freshest attestation evidence record
	// is `pass` and recent. Invariant 9: manual evidence flows through the
	// SAME evaluation path as automated.
	ctrlID := seedControl(t, admin, tenant, "manual_attested", "monthly", "")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-2*24*time.Hour))

	eng := newEngine(app)
	n, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture)
	if err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}
	if n != 1 {
		t.Fatalf("AC-4: expected 1 evaluation row (no scope cells -> whole-tenant), got %d", n)
	}

	states, err := eng.ControlState(ctx, ctrlID, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("AC-4: expected 1 state, got %d", len(states))
	}
	if states[0].Result != "pass" {
		t.Fatalf("AC-4: manual_attested fresh attestation = %q, want pass", states[0].Result)
	}
	if states[0].FreshnessStatus != "fresh" {
		t.Fatalf("AC-4: expected freshness=fresh, got %q", states[0].FreshnessStatus)
	}
}

// ===== AC-5: freshest evidence past freshness_class max age -> stale =====

func TestEvaluateControl_EvidencePastWindowIsStale(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// daily class = 7d window. Freshest evidence is 30 days old -> stale.
	ctrlID := seedControl(t, admin, tenant, "automated", "daily", "")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-30*24*time.Hour))

	eng := newEngine(app)
	if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerScheduled, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}

	states, err := eng.ControlState(ctx, ctrlID, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}
	if states[0].FreshnessStatus != "stale" {
		t.Fatalf("AC-5: 30d-old evidence, daily class = %q, want stale", states[0].FreshnessStatus)
	}
	// AC-5: still queryable. The state row exists and is returned — stale is
	// a flag, not a deletion.
	if states[0].EvidenceCountInWindow != 0 {
		t.Fatalf("AC-5: out-of-window evidence must not count in-window, got %d", states[0].EvidenceCountInWindow)
	}
	// anti-criterion P0-2: the out-of-window pass did NOT drive the result.
	if states[0].Result != "inconclusive" {
		t.Fatalf("AC-5/P0-2: out-of-window pass leaked into result %q, want inconclusive", states[0].Result)
	}
}

// ===== AC-3: evaluation is idempotent =====

func TestEvaluateControl_IdempotentComputedColumns(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	ctrlID := seedControl(t, admin, tenant, "automated", "monthly", "")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*24*time.Hour))
	seedEvidence(t, admin, tenant, ctrlID, "fail", time.Now().UTC().Add(-2*24*time.Hour))

	eng := newEngine(app)

	// Run evaluation twice over the SAME ledger slice.
	if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl run 1: %v", err)
	}
	first, err := eng.ControlState(ctx, ctrlID, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState run 1: %v", err)
	}
	if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl run 2: %v", err)
	}
	second, err := eng.ControlState(ctx, ctrlID, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState run 2: %v", err)
	}

	// AC-3: the COMPUTED columns are identical run-over-run. (id, eval_run_id
	// and evaluated_at differ — that is expected of an append-only ledger;
	// the idempotency property is about the derived state, not row identity.)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("AC-3: expected 1 state each run, got %d / %d", len(first), len(second))
	}
	if first[0].Result != second[0].Result {
		t.Fatalf("AC-3: result not idempotent: %q vs %q", first[0].Result, second[0].Result)
	}
	if first[0].FreshnessStatus != second[0].FreshnessStatus {
		t.Fatalf("AC-3: freshness not idempotent: %q vs %q", first[0].FreshnessStatus, second[0].FreshnessStatus)
	}
	if first[0].EvidenceCountInWindow != second[0].EvidenceCountInWindow {
		t.Fatalf("AC-3: evidence count not idempotent: %d vs %d",
			first[0].EvidenceCountInWindow, second[0].EvidenceCountInWindow)
	}
	// A pass + a fail in-window -> fail (any fail is fail).
	if first[0].Result != "fail" {
		t.Fatalf("AC-3: expected fail (pass+fail in window), got %q", first[0].Result)
	}
}

// ===== AC-7: replay — delete control_evaluations, re-run, identical state =====

func TestReplay_ReproducesIdenticalStateFromLedger(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// Two controls with distinct evidence shapes so the replay has something
	// non-trivial to reproduce.
	c1 := seedControl(t, admin, tenant, "automated", "monthly", "")
	seedEvidence(t, admin, tenant, c1, "pass", time.Now().UTC().Add(-3*24*time.Hour))
	c2 := seedControl(t, admin, tenant, "manual_attested", "daily", "")
	seedEvidence(t, admin, tenant, c2, "fail", time.Now().UTC().Add(-1*24*time.Hour))

	eng := newEngine(app)

	// Pin a fixed EVIDENCE horizon so the replay reads exactly the same
	// ledger slice both times — this is the point-in-time property. The
	// evidence horizon (which evidence the engine reads) is a distinct axis
	// from the evaluation-row read horizon (which control_evaluations rows
	// ControlState returns). We pin the former and read the latest state via
	// FarFuture for the latter.
	evidenceHorizon := time.Now().UTC()

	if _, err := eng.EvaluateAll(ctx, eval.TriggerScheduled, evidenceHorizon); err != nil {
		t.Fatalf("EvaluateAll (initial): %v", err)
	}
	before1, err := eng.ControlState(ctx, c1, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState c1 before: %v", err)
	}
	before2, err := eng.ControlState(ctx, c2, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState c2 before: %v", err)
	}

	// AC-7: delete EVERY control_evaluations row, then Replay.
	if _, err := admin.Exec(ctx, `DELETE FROM control_evaluations WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatalf("delete control_evaluations: %v", err)
	}
	if countEvaluations(t, admin, tenant) != 0 {
		t.Fatalf("AC-7 precondition: expected 0 evaluation rows after delete")
	}

	if _, err := eng.Replay(ctx, evidenceHorizon); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	after1, err := eng.ControlState(ctx, c1, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState c1 after: %v", err)
	}
	after2, err := eng.ControlState(ctx, c2, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState c2 after: %v", err)
	}

	// The derived state must be IDENTICAL — the engine holds no hidden
	// state; everything derives from the immutable ledger.
	assertSameState(t, "AC-7 c1", before1, after1)
	assertSameState(t, "AC-7 c2", before2, after2)
	if after1[0].Result != "pass" {
		t.Fatalf("AC-7: c1 expected pass, got %q", after1[0].Result)
	}
	if after2[0].Result != "fail" {
		t.Fatalf("AC-7: c2 expected fail, got %q", after2[0].Result)
	}
}

func assertSameState(t *testing.T, label string, before, after []eval.State) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("%s: state count changed %d -> %d", label, len(before), len(after))
	}
	for i := range before {
		if before[i].Result != after[i].Result {
			t.Fatalf("%s: result changed %q -> %q", label, before[i].Result, after[i].Result)
		}
		if before[i].FreshnessStatus != after[i].FreshnessStatus {
			t.Fatalf("%s: freshness changed %q -> %q", label, before[i].FreshnessStatus, after[i].FreshnessStatus)
		}
		if before[i].EvidenceCountInWindow != after[i].EvidenceCountInWindow {
			t.Fatalf("%s: evidence count changed %d -> %d", label,
				before[i].EvidenceCountInWindow, after[i].EvidenceCountInWindow)
		}
	}
}

// ===== AC-1: per-scope-cell evaluation — one row per applicable cell =====

func TestEvaluateControl_OneRowPerApplicableScopeCell(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// Two scope cells + a control whose applicability_expr is "true" (matches
	// every cell). The engine writes one evaluation row per applicable cell.
	cellA := seedScopeCell(t, admin, tenant, "prod-us")
	cellB := seedScopeCell(t, admin, tenant, "prod-eu")
	ctrlID := seedControl(t, admin, tenant, "automated", "monthly", "")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*24*time.Hour))

	eng := newEngine(app)
	n, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture)
	if err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}
	if n != 2 {
		t.Fatalf("AC-1: expected 2 evaluation rows (2 applicable cells), got %d", n)
	}

	states, err := eng.ControlState(ctx, ctrlID, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("AC-1: expected state for 2 cells, got %d", len(states))
	}
	seen := map[uuid.UUID]bool{}
	for _, st := range states {
		if st.ScopeCellID == nil {
			t.Fatalf("AC-1: expected per-cell state, got NULL cell")
		}
		seen[*st.ScopeCellID] = true
		if st.Result != "pass" {
			t.Fatalf("AC-1: cell %s result = %q, want pass", st.ScopeCellID, st.Result)
		}
	}
	if !seen[cellA] || !seen[cellB] {
		t.Fatalf("AC-1: expected state for both cells %s and %s", cellA, cellB)
	}
}

// ===== AC-1: ?scope= filter narrows the returned cells =====

func TestControlState_ScopeFilterNarrowsCells(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	cellA := seedScopeCell(t, admin, tenant, "prod-us")
	_ = seedScopeCell(t, admin, tenant, "prod-eu")
	ctrlID := seedControl(t, admin, tenant, "automated", "monthly", "")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*24*time.Hour))

	eng := newEngine(app)
	if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}

	// A scope predicate that selects only cellA's label dimension. Both cells
	// share environment=prod, but the filter narrows by the cell-id allowlist
	// the predicate resolves to. Here we filter to "true" (all) first to
	// confirm 2, then narrow.
	all, err := eng.ControlState(ctx, ctrlID, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState (no filter): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 cells unfiltered, got %d", len(all))
	}

	// Narrow with an eq predicate on environment — both cells have
	// environment=prod, so this still returns 2. The point of the test is
	// that the filter path runs without error and respects the allowlist;
	// a predicate matching nothing returns 0.
	none, err := eng.ControlState(ctx, ctrlID, `{"op":"eq","dim":"environment","value":"does-not-exist"}`, eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState (no-match filter): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("AC-1: scope filter matching nothing should return 0 states, got %d", len(none))
	}
	_ = cellA
}

// ===== AC-6: effectiveness — rolling 30-day pass rate =====

func TestEffectiveness_RollingPassRate(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	ctrlID := seedControl(t, admin, tenant, "automated", "monthly", "")

	eng := newEngine(app)

	// Three evaluation runs: pass, fail, pass. Each EvaluateControl appends a
	// row. We control the result by swapping the evidence between runs.
	passEv := seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*24*time.Hour))
	if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl run 1: %v", err)
	}
	// Add a fail record so the next run rolls up to fail.
	seedEvidence(t, admin, tenant, ctrlID, "fail", time.Now().UTC().Add(-1*time.Hour))
	if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl run 2: %v", err)
	}
	// Remove the fail record (the ledger is append-only in production, but
	// the test harness uses the admin role to construct a 3rd distinct run).
	if _, err := admin.Exec(ctx, `DELETE FROM evidence_records WHERE tenant_id = $1 AND result = 'fail'`, tenant); err != nil {
		t.Fatalf("delete fail evidence: %v", err)
	}
	if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl run 3: %v", err)
	}
	_ = passEv

	eff, err := eng.Effectiveness(ctx, ctrlID)
	if err != nil {
		t.Fatalf("Effectiveness: %v", err)
	}
	// 3 evaluations: pass, fail, pass -> 2/3.
	if eff.TotalCount != 3 {
		t.Fatalf("AC-6: expected 3 evaluations in window, got %d", eff.TotalCount)
	}
	if eff.PassCount != 2 {
		t.Fatalf("AC-6: expected 2 passing evaluations, got %d", eff.PassCount)
	}
	want := 2.0 / 3.0
	if eff.PassRate < want-0.001 || eff.PassRate > want+0.001 {
		t.Fatalf("AC-6: pass rate = %f, want ~%f", eff.PassRate, want)
	}
}

// ===== unknown control id -> ErrControlNotFound =====

func TestControlState_UnknownControlIsNotFound(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	_, err := newEngine(app).ControlState(ctx, uuid.New(), "", eval.FarFuture)
	if err == nil {
		t.Fatalf("expected ErrControlNotFound for unknown control id")
	}
}

// ===== Rego evidence query path — bundle-declared query drives the result ==

func TestEvaluateControl_RegoEvidenceQueryDrivesResult(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// A control whose bundle declares a Rego evidence query: "pass iff there
	// is at least one record and they all passed". The engine runs the query
	// instead of the per-record rollup.
	regoExpr := `package evidence.query
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 0
	every r in input.records { r.result == "pass" }
}`
	queriesJSON := `[{"id":"all-pass","language":"rego","expression":` + jsonString(regoExpr) + `}]`
	ctrlID := seedControl(t, admin, tenant, "automated", "monthly", queriesJSON)
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*24*time.Hour))

	eng := newEngine(app)
	if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}
	states, err := eng.ControlState(ctx, ctrlID, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(states) != 1 || states[0].Result != "pass" {
		t.Fatalf("rego query path: expected pass, got %+v", states)
	}
}
