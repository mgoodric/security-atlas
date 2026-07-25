// evaluate.go — the post-seed evaluation driver (slice 671).
//
// The demo seed (seeder.go) writes the evidence ledger via direct BYPASSRLS
// INSERTs and DELIBERATELY does NOT write the evaluation read-model tables
// (control_evaluations, evidence_freshness) — those are downstream artifacts
// of the slice-012/016 evaluator (see the LOAD-BEARING comment near
// seeder.go:31). In production, evaluation is driven off the evidence-ingest
// JetStream stream plus the hourly recompute; the demo seed bypasses that
// stream, so nothing ever evaluates the seeded evidence and the demo tenant
// shows STATE / FRESHNESS / LAST OBSERVED = "—" for every control.
//
// EvaluateSeededTenant closes that gap by driving the REAL evaluator for the
// seeded tenant as a SEPARATE stage AFTER the seed write commits. It does not
// fabricate evaluation rows — it runs the same eval.Engine.EvaluateAll +
// freshness.Store.Refresh the production paths run, so demo state is computed
// with production semantics.
//
// LOAD-BEARING — constitutional invariant #2 (ingestion and evaluation are
// separated stages). This driver runs AFTER the seed's BYPASSRLS write
// transaction has committed. It reads the append-only evidence ledger and
// writes ONLY the evaluation read-model tables; it never mutates source
// evidence and never runs inside the seed write tx. Evaluation staying a
// distinct stage is exactly the invariant — a bug here can never corrupt the
// ledger the seed wrote.
//
// LOAD-BEARING — constitutional invariant #6 (RLS tenant isolation). The
// evaluation runs through an app-role (NOSUPERUSER NOBYPASSRLS) Engine bound
// to the seeded tenant via tenancy.WithTenant. The engine + freshness store
// open their own tenant-GUC transactions; RLS scopes every read/write to the
// one seeded tenant. The driver never evaluates across tenants.
package demoseed

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// EvalSummary reports the row counts the post-seed evaluation produced. The
// callers print these so the operator sees that evaluation ran. Both counts
// are honest counts of rows the real evaluator wrote.
type EvalSummary struct {
	// ControlEvaluations is the number of control_evaluations rows the engine
	// appended across the tenant's active controls (one per control × cell).
	ControlEvaluations int
	// FreshnessRows is the number of evidence_freshness rows the refresh
	// UPSERTed (one per active control).
	FreshnessRows int
}

// EvaluateSeededTenant drives the production evaluator for a freshly-seeded
// demo tenant so its controls show real STATE / FRESHNESS / LAST OBSERVED
// instead of "—". It is the shared post-Apply hook for BOTH seed call sites
// (the atlas-cli `demo seed` command and the admindemo "Generate demo data"
// HTTP handler) so the two never drift.
//
// appPool MUST be the app-role pool (NOSUPERUSER NOBYPASSRLS) so RLS is
// enforced on the evaluation writes — the BYPASSRLS seed pool is the WRONG
// pool here (it would defeat invariant #6). tenantID is the seeded tenant
// (Result.TenantID from Seeder.Apply).
//
// Two stages, in order:
//
//  1. eval.Engine.EvaluateAll(TriggerManual, FarFuture) — computes
//     control_evaluations for every active control from the seeded ledger.
//  2. freshness.Store.Refresh — computes evidence_freshness for every active
//     control from the same ledger.
//
// Both are idempotent: EvaluateAll appends one immutable row per
// (control, cell, run) and the read surfaces project the latest; Refresh
// UPSERTs one row per control. Re-running (idempotent re-seed, or a second
// pass) never duplicates state or corrupts the ledger (AC-5).
func EvaluateSeededTenant(ctx context.Context, appPool *pgxpool.Pool, tenantID uuid.UUID) (EvalSummary, error) {
	if appPool == nil {
		return EvalSummary{}, fmt.Errorf("demoseed: EvaluateSeededTenant: nil app pool (need the RLS-enforced app-role pool)")
	}
	if tenantID == uuid.Nil {
		return EvalSummary{}, fmt.Errorf("demoseed: EvaluateSeededTenant: zero tenant id")
	}

	// Bind the tenant via RLS. The engine + freshness store both read the
	// tenant from the context (tenancy.TenantFromContext) and apply it as the
	// app.current_tenant GUC inside their own transactions — invariant #6.
	tctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return EvalSummary{}, fmt.Errorf("demoseed: EvaluateSeededTenant: bind tenant: %w", err)
	}

	// Stage 1: control_evaluations. NewEngineFactory builds a per-tenant
	// RLS-bound Engine over the app pool (the same factory the production
	// scheduler + ingest subscriber use). TriggerManual labels these rows as
	// operator-driven (the demo seed is an explicit operator action), distinct
	// from the scheduled/ingest production triggers. FarFuture is the live
	// horizon ("all evidence up to now"), matching the production live-state
	// callers.
	engine := eval.NewEngineFactory(appPool)()
	evalRows, err := engine.EvaluateAll(tctx, eval.TriggerManual, eval.FarFuture)
	if err != nil {
		return EvalSummary{}, fmt.Errorf("demoseed: EvaluateSeededTenant: evaluate controls: %w", err)
	}

	// Stage 2: evidence_freshness. Refresh reads the same ledger and UPSERTs
	// the freshness read model per active control.
	freshRows, err := freshness.NewStore(appPool).Refresh(tctx)
	if err != nil {
		return EvalSummary{}, fmt.Errorf("demoseed: EvaluateSeededTenant: refresh freshness: %w", err)
	}

	return EvalSummary{
		ControlEvaluations: evalRows,
		FreshnessRows:      freshRows,
	}, nil
}
