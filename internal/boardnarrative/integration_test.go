//go:build integration

// Slice 440 — integration tests for the board-narrative AI v0 surface. Real
// Postgres + the real boardnarrative.Store (RLS-backed citation resolution +
// citable-excerpt read + draft persistence + approval) + the llm.StubClient
// (NO live Ollama — the slice-498 CI seam). The stub lets us craft a VALID
// draft (cites a real tenant-owned id, correct numbers, right shape), a
// FABRICATED-number draft, a BAD-SHAPE draft, and a CROSS-TENANT-citation
// draft, and assert the deterministic guardrail + suppression + persistence +
// cross-tenant + DB-approval-guard behavior.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/boardnarrative/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage (AC-14..AC-19):
//
//	AC-14  a valid generation passes all guardrails and reaches draft state
//	AC-15  a draft with a fabricated number is auto-rejected (guardrail 5)
//	AC-16  a draft with an unresolvable citation is rejected (guardrail 4)
//	AC-17  a draft that breaks section shape is rejected (guardrail 6)
//	AC-18  a Tenant-B generation cannot cite a Tenant-A control (cross-tenant)
//	AC-19  a section cannot be human_approved=TRUE without human_approver
package boardnarrative_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/boardnarrative"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// freshTenant returns a new tenant id + registers cleanup of this slice's rows.
// CARVE-OUT (742 drain batch 15): this helper does MORE than a tenant-scoped
// DELETE — it INSERTs a `tenants` row first, which dbtest.SeedTenant does not
// express — so it stays inline; only its pool is re-routed to the slice-435
// dbtest harness by the callers (admin := dbtest.NewMigratePool(t)).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, tenant, "slice440-"+tenant[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM board_narrative_sections WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
			`DELETE FROM tenants WHERE id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// seedControlWithEvidence inserts a control + a passing evidence record so the
// control is a citable excerpt. Returns the control id + evidence id.
func seedControlWithEvidence(t *testing.T, admin *pgxpool.Pool, tenant, title string) (string, string) {
	t.Helper()
	ctrlID := uuid.NewString()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, description, control_family, implementation_type, lifecycle_state, bundle_id)
		VALUES ($1, $2, $3, 'desc', 'AC', 'automated', 'active', $4)
	`, ctrlID, tenant, title, "bundle_440_"+ctrlID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	evID := uuid.NewString()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', $5)
	`, evID, tenant, ctrlID, ctrlID, "hash-"+evID); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return ctrlID, evID
}

// fixedRollup builds a deterministic rollup whose citable excerpt is the given
// control id. The numbers are fixed so the stub draft can match them.
func fixedRollup(periodEnd, ctrlID string) boardnarrative.Rollup {
	b := board.Brief{
		PeriodEnd:  periodEnd,
		Frameworks: []board.FrameworkPosture{{Slug: "soc2", Name: "SOC 2", CoveragePct: 84, FreshnessPct: 91, Delta: -3}},
		Drift:      board.DriftSummary{WindowDays: 30, Delta: -3, FlippedOutCount: 3},
	}
	r, _ := boardnarrative.RollupFromBrief(b, []boardnarrative.Excerpt{
		{ID: ctrlID, Kind: boardnarrative.KindControl, Title: "Access reviews", Excerpt: "quarterly"},
	})
	return r
}

// fixedRollupSource is a RollupSource returning a pre-built rollup (the brief
// data path is unit-tested; here we pin the numbers so the stub draft is
// deterministic and the DB-backed citation + persistence + approval are what's
// exercised).
type fixedRollupSource struct{ r boardnarrative.Rollup }

func (s fixedRollupSource) CoverageRollup(_ context.Context, _ string) (boardnarrative.Rollup, error) {
	return s.r, nil
}

func (s fixedRollupSource) SectionRollup(_ context.Context, _ boardnarrative.SectionKey, _ string) (boardnarrative.Rollup, error) {
	return s.r, nil
}

func validStubDraft(ctrlID string) string {
	return strings.Join([]string{
		"## Control coverage summary",
		"1. Program control coverage stands at 84% for the period.",
		"2. Evidence freshness within the 30-day window is 91%.",
		"3. Over the last 30 days the net drift was -3; 3 controls drifted out of passing.",
		"4. The program runs against 1 framework; coverage is grounded in control (" + ctrlID + ").",
		"",
	}, "\n")
}

func newService(t *testing.T, appPool *pgxpool.Pool, rollup boardnarrative.Rollup, draft string) (*boardnarrative.Service, *boardnarrative.Store) {
	t.Helper()
	store := boardnarrative.NewStore(appPool, nil) // assembler unused (fixedRollupSource supplies the rollup)
	stub := llm.NewStubClient()
	stub.Result = llm.GenerateResult{Text: draft, ModelName: "llama3.1", ModelVersion: "8b-instruct-q5", ModelProvider: "ollama-local"}
	audit := boardnarrative.NewAuditSink(llm.NewAuditWriter(appPool))
	svc := boardnarrative.NewService(fixedRollupSource{r: rollup}, stub, store, audit, store)
	return svc, store
}

// ----- AC-14: valid generation reaches draft state -----

func TestIntegration_ValidGenerationDrafts(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	ctrlID, _ := seedControlWithEvidence(t, admin, tenant, "Access reviews")
	rollup := fixedRollup("2026-05-31", ctrlID)
	svc, store := newService(t, app, rollup, validStubDraft(ctrlID))

	out, err := svc.Generate(ctx, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out.Suppressed {
		t.Fatalf("valid draft suppressed: %q", out.Reason)
	}
	if out.RecordID == "" {
		t.Fatalf("no record id on valid draft")
	}
	// The persisted record is an UNAPPROVED, ai_assisted draft.
	rec, err := store.GetSection(ctx, uuid.MustParse(out.RecordID))
	if err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if !rec.AiAssisted || rec.HumanApproved || rec.HumanApprover != nil {
		t.Fatalf("draft must be ai_assisted + unapproved + no approver: %+v", rec)
	}
	if rec.ModelProvider != "ollama-local" || rec.PromptVersion == "" {
		t.Fatalf("provenance not persisted: %+v", rec)
	}
}

// ----- AC-15: fabricated number auto-rejects (guardrail 5) -----

func TestIntegration_FabricatedNumberRejected(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	ctrlID, _ := seedControlWithEvidence(t, admin, tenant, "Access reviews")
	rollup := fixedRollup("2026-05-31", ctrlID)
	// Coverage 85 instead of the ground-truth 84 — a fabricated statistic.
	bad := strings.Replace(validStubDraft(ctrlID), "stands at 84%", "stands at 85%", 1)
	svc, _ := newService(t, app, rollup, bad)

	out, err := svc.Generate(ctx, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !out.Suppressed || out.Reason != boardnarrative.ReasonNumericMismatch {
		t.Fatalf("want suppressed/numeric_mismatch, got suppressed=%v reason=%q", out.Suppressed, out.Reason)
	}
	if out.RecordID != "" {
		t.Fatalf("a draft that failed numeric verification must persist nothing (P0-440-4)")
	}
	// Nothing persisted in the table.
	assertNoSections(t, admin, tenant)
}

// ----- AC-16: unresolvable citation rejected (guardrail 4) -----

func TestIntegration_UnresolvableCitationRejected(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	ctrlID, _ := seedControlWithEvidence(t, admin, tenant, "Access reviews")
	rollup := fixedRollup("2026-05-31", ctrlID)
	// The draft cites a UUID that is NOT in the grounding set (invented).
	invented := uuid.NewString()
	bad := strings.Replace(validStubDraft(ctrlID), ctrlID, invented, 1)
	svc, _ := newService(t, app, rollup, bad)

	out, err := svc.Generate(ctx, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !out.Suppressed || out.Reason != boardnarrative.ReasonUnresolvedCitation {
		t.Fatalf("want suppressed/unresolved_citation, got suppressed=%v reason=%q", out.Suppressed, out.Reason)
	}
	assertNoSections(t, admin, tenant)
}

// ----- AC-17: bad section shape rejected (guardrail 6) -----

func TestIntegration_BadShapeRejected(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	ctrlID, _ := seedControlWithEvidence(t, admin, tenant, "Access reviews")
	rollup := fixedRollup("2026-05-31", ctrlID)
	bad := "Freestyle prose: coverage is 84%, freshness 91%, drifted 3 over 30 days, 1 framework (" + ctrlID + ")."
	svc, _ := newService(t, app, rollup, bad)

	out, err := svc.Generate(ctx, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !out.Suppressed || out.Reason != boardnarrative.ReasonBadShape {
		t.Fatalf("want suppressed/section_shape_violation, got suppressed=%v reason=%q", out.Suppressed, out.Reason)
	}
	assertNoSections(t, admin, tenant)
}

// ----- AC-18: cross-tenant citation impossible (threat-model I) -----

func TestIntegration_CrossTenantCitationRejected(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Tenant A owns a control. Tenant B generates a narrative whose draft tries
	// to cite Tenant A's control id. Under B's RLS context the id is invisible,
	// so citation resolution fails and the draft is suppressed — B can never
	// cite A's row (P0-440-3).
	ctrlA, _ := seedControlWithEvidence(t, admin, tenantA, "A-only control")
	ctrlB, _ := seedControlWithEvidence(t, admin, tenantB, "B control")

	ctxB := tenantCtx(t, tenantB)
	// Build B's rollup grounding on B's control, but the stub draft cites A's id
	// instead — the worst case: a model that leaked a cross-tenant id.
	rollupB := fixedRollup("2026-05-31", ctrlB)
	bad := strings.Replace(validStubDraft(ctrlB), ctrlB, ctrlA, 1)
	svc, store := newService(t, app, rollupB, bad)

	out, err := svc.Generate(ctxB, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "b-user"})
	if err != nil {
		t.Fatalf("Generate(B): %v", err)
	}
	if !out.Suppressed || out.Reason != boardnarrative.ReasonUnresolvedCitation {
		t.Fatalf("Tenant B citing Tenant A must be suppressed/unresolved, got suppressed=%v reason=%q", out.Suppressed, out.Reason)
	}
	assertNoSections(t, admin, tenantB)

	// Independently prove the resolver: B cannot resolve A's id, but A can.
	if _, ok, _ := store.Resolve(ctxB, uuid.MustParse(ctrlA)); ok {
		t.Fatalf("Tenant B resolved Tenant A's control id — RLS isolation breached")
	}
	ctxA := tenantCtx(t, tenantA)
	if _, ok, err := store.Resolve(ctxA, uuid.MustParse(ctrlA)); err != nil || !ok {
		t.Fatalf("Tenant A must resolve its own control: ok=%v err=%v", ok, err)
	}
}

// ----- AC-19: DB guard — human_approved=TRUE requires human_approver -----

func TestIntegration_ApprovalRequiresApprover(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	ctrlID, _ := seedControlWithEvidence(t, admin, tenant, "Access reviews")
	rollup := fixedRollup("2026-05-31", ctrlID)
	svc, store := newService(t, app, rollup, validStubDraft(ctrlID))

	out, err := svc.Generate(ctx, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
	if err != nil || out.Suppressed {
		t.Fatalf("setup generate failed: err=%v suppressed=%v reason=%q", err, out.Suppressed, out.Reason)
	}
	recID := uuid.MustParse(out.RecordID)

	// Service-level: a blank approver is rejected before the DB round-trip.
	if _, err := svc.Approve(ctx, boardnarrative.ApproveParams{RecordID: recID, FinalText: "x", Approver: "  "}); err != boardnarrative.ErrApproverRequired {
		t.Fatalf("blank approver: want ErrApproverRequired, got %v", err)
	}

	// DB-level: prove the CHECK directly — an UPDATE that sets human_approved
	// without an approver must fail with a check_violation (the schema guard,
	// independent of the Go layer).
	_, derr := admin.Exec(context.Background(), `
		UPDATE board_narrative_sections
		SET human_approved = TRUE, human_approver = NULL
		WHERE id = $1
	`, recID)
	if derr == nil {
		t.Fatalf("DB CHECK did not reject human_approved=TRUE with NULL approver (P0-440-2)")
	}
	if !strings.Contains(derr.Error(), "ai_assist_invariant") && !strings.Contains(strings.ToLower(derr.Error()), "check") {
		t.Fatalf("expected the ai_assist_invariant check_violation, got: %v", derr)
	}

	// A real approval succeeds and records the approver.
	approved, err := svc.Approve(ctx, boardnarrative.ApproveParams{RecordID: recID, FinalText: "Final coverage text.", Approver: "alice"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !approved.HumanApproved || approved.HumanApprover != "alice" || approved.FinalText != "Final coverage text." {
		t.Fatalf("approval state wrong: %+v", approved)
	}
	rec, _ := store.GetSection(ctx, recID)
	if !rec.HumanApproved || rec.HumanApprover == nil || *rec.HumanApprover != "alice" {
		t.Fatalf("DB row not approved with approver: %+v", rec)
	}
}

// assertNoSections fails if any board_narrative_sections row exists for the
// tenant (proves a suppressed draft persisted NOTHING — P0-440-4). Uses the
// admin pool to count directly, bypassing RLS, so the assertion is independent
// of the Store's read path.
func assertNoSections(t *testing.T, admin *pgxpool.Pool, tenant string) {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM board_narrative_sections WHERE tenant_id = $1`, tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count sections: %v", err)
	}
	if n != 0 {
		t.Fatalf("suppressed draft persisted %d row(s); want 0 (P0-440-4)", n)
	}
}
