//go:build integration

// Slice 751 — integration tests for the DETERMINISTIC, RLS-SCOPED exceptions
// aggregate the board brief freezes into `content.exceptions` and the AI-drafted
// exception-status narrative section grounds every numeric claim on.
//
// Two properties are only meaningful against a real Postgres, and both are
// load-bearing:
//
//   - CORRECTNESS. The aggregate counts what it says it counts: only 'active'
//     rows are in force; 'requested' / 'approved' / 'denied' / 'expired' rows
//     are not; past-due means expires_at has already passed; the oldest age is
//     measured from when the waiver began applying. If this is wrong, the
//     numeric-verification gate happily certifies a wrong number — the gate
//     checks the draft against the aggregate, never the aggregate against
//     reality.
//   - RLS SCOPING (invariant #6). Another tenant's waivers are invisible to the
//     count. A board-facing exception count that could include a foreign
//     tenant's waiver is worse than no count at all.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/board/...

package board_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
)

// exceptionSeed describes one waiver to insert. Times are expressed relative to
// the test's fixed clock so the fixture never drifts with wall-clock time (the
// 2026-06-29 lesson recorded on seedRisk).
type exceptionSeed struct {
	status         string
	requestedAgo   time.Duration // how long before the anchor the waiver was requested
	effectiveAgo   time.Duration // how long before the anchor it began applying (0 = NULL)
	expiresFromReq time.Duration // expires_at relative to requested_at (DB caps at 365d)
}

// seedException inserts one exception row for `tenant` against `ctrlID`,
// stamped relative to `anchor`. Uses the migrate pool (BYPASSRLS) so the
// fixture setup itself is not the thing under test.
func seedException(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, anchor time.Time, s exceptionSeed) uuid.UUID {
	t.Helper()
	id := uuid.New()
	requestedAt := anchor.Add(-s.requestedAgo)
	expiresAt := requestedAt.Add(s.expiresFromReq)

	var effectiveFrom, approvedAt, activatedAt *time.Time
	var approvedBy, activatedBy *string
	if s.effectiveAgo > 0 {
		ef := anchor.Add(-s.effectiveAgo)
		effectiveFrom = &ef
	}
	if s.status == "approved" || s.status == "active" {
		ap := requestedAt.Add(time.Hour)
		approvedAt = &ap
		approver := "approver@example.test"
		approvedBy = &approver
	}
	if s.status == "active" {
		ac := requestedAt.Add(2 * time.Hour)
		activatedAt = &ac
		activator := "approver@example.test"
		activatedBy = &activator
	}

	if _, err := admin.Exec(context.Background(), `
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate,
			justification, requested_by, requested_at,
			approved_by, approved_at, activated_by, activated_at,
			effective_from, expires_at, status
		)
		VALUES ($1, $2, $3, '{}'::jsonb,
		        'slice 751 fixture waiver', 'requester@example.test', $4,
		        $5, $6, $7, $8,
		        $9, $10, $11)
	`, id, tenant, ctrlID, requestedAt,
		approvedBy, approvedAt, activatedBy, activatedAt,
		effectiveFrom, expiresAt, s.status); err != nil {
		t.Fatalf("seed exception (%s): %v", s.status, err)
	}
	return id
}

// newBriefGenerator wires a Generator pinned to `anchor` with static freshness +
// drift readers — the exceptions aggregate is the only live DB read under test.
func newBriefGenerator(t *testing.T, app *pgxpool.Pool, anchor time.Time) *board.Generator {
	t.Helper()
	store := board.NewStore(app)
	gen := board.NewGenerator(store,
		fixedFreshness{rows: []freshness.ControlFreshness{{IsStale: false, EvidenceCount: 2}}},
		fixedDrift{report: drift.DriftReport{
			SinceDate:   anchor.AddDate(0, 0, -30),
			ThroughDate: anchor,
			Delta:       -3,
		}},
	).WithClock(func() time.Time { return anchor })
	return gen
}

// TestIntegration_ExceptionAggregate_CountsOnlyWhatIsInForce proves the three
// board-grade numbers are computed from the register correctly, and that the
// lifecycle states which are NOT in force are excluded.
func TestIntegration_ExceptionAggregate_CountsOnlyWhatIsInForce(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	anchor := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	seedFramework(t, admin, tenant, "soc2", "SOC 2")
	ctrlID := seedControl(t, admin, tenant)

	day := 24 * time.Hour
	// In force, the longest-standing: began applying 210 days ago, still valid.
	seedException(t, admin, tenant, ctrlID, anchor, exceptionSeed{
		status: "active", requestedAgo: 210 * day, effectiveAgo: 210 * day, expiresFromReq: 300 * day,
	})
	// In force, younger, still valid.
	seedException(t, admin, tenant, ctrlID, anchor, exceptionSeed{
		status: "active", requestedAgo: 30 * day, effectiveAgo: 30 * day, expiresFromReq: 90 * day,
	})
	// In force but ALREADY PAST its expiry date — the governance-hygiene case.
	seedException(t, admin, tenant, ctrlID, anchor, exceptionSeed{
		status: "active", requestedAgo: 100 * day, effectiveAgo: 100 * day, expiresFromReq: 90 * day,
	})
	// NOT in force — every one of these must be excluded from all three numbers.
	seedException(t, admin, tenant, ctrlID, anchor, exceptionSeed{
		status: "requested", requestedAgo: 400 * day, expiresFromReq: 30 * day,
	})
	seedException(t, admin, tenant, ctrlID, anchor, exceptionSeed{
		status: "approved", requestedAgo: 500 * day, expiresFromReq: 30 * day,
	})
	seedException(t, admin, tenant, ctrlID, anchor, exceptionSeed{
		status: "denied", requestedAgo: 600 * day, expiresFromReq: 30 * day,
	})
	seedException(t, admin, tenant, ctrlID, anchor, exceptionSeed{
		status: "expired", requestedAgo: 700 * day, effectiveAgo: 700 * day, expiresFromReq: 30 * day,
	})

	ctx := ctxFor(t, tenant)
	brief, err := newBriefGenerator(t, app, anchor).Assemble(ctx, "2026-05-31")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	got := brief.Exceptions
	want := board.ExceptionSummary{ActiveCount: 3, PastDueCount: 1, OldestActiveAgeDays: 210}
	if got != want {
		t.Fatalf("Brief.Exceptions = %+v, want %+v", got, want)
	}
}

// TestIntegration_ExceptionAggregate_EmptyRegister proves a program with no
// waivers reports an honest zero rather than erroring or reporting the age since
// the zero time (the NULL-MIN branch).
func TestIntegration_ExceptionAggregate_EmptyRegister(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	anchor := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	seedFramework(t, admin, tenant, "soc2", "SOC 2")

	ctx := ctxFor(t, tenant)
	brief, err := newBriefGenerator(t, app, anchor).Assemble(ctx, "2026-05-31")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if (brief.Exceptions != board.ExceptionSummary{}) {
		t.Fatalf("empty register: Brief.Exceptions = %+v, want all zero", brief.Exceptions)
	}
}

// TestIntegration_ExceptionAggregate_RLSScoped is the load-bearing isolation
// test: Tenant A's board-facing exception count must not move when Tenant B
// accumulates waivers. The aggregate runs under A's `app.current_tenant` GUC
// against a FORCE-RLS table, so B's rows are invisible — not filtered, invisible.
func TestIntegration_ExceptionAggregate_RLSScoped(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	anchor := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	seedFramework(t, admin, tenantA, "soc2", "SOC 2")
	seedFramework(t, admin, tenantB, "iso27001", "ISO 27001")
	ctrlA := seedControl(t, admin, tenantA)
	ctrlB := seedControl(t, admin, tenantB)

	// A holds exactly one active waiver, 45 days old, not past due.
	seedException(t, admin, tenantA, ctrlA, anchor, exceptionSeed{
		status: "active", requestedAgo: 45 * day, effectiveAgo: 45 * day, expiresFromReq: 180 * day,
	})
	// B holds five, four of them long past due — none may reach A's brief.
	// All were requested 300 days ago; the first runs the full 365-day maximum
	// (so it is still inside its window), the rest lapsed after 10 days.
	for i := 0; i < 5; i++ {
		expires := 365 * day
		if i > 0 {
			expires = 10 * day // long past due
		}
		seedException(t, admin, tenantB, ctrlB, anchor, exceptionSeed{
			status: "active", requestedAgo: 300 * day, effectiveAgo: 300 * day, expiresFromReq: expires,
		})
	}

	gen := newBriefGenerator(t, app, anchor)

	briefA, err := gen.Assemble(ctxFor(t, tenantA), "2026-05-31")
	if err != nil {
		t.Fatalf("Assemble(A): %v", err)
	}
	wantA := board.ExceptionSummary{ActiveCount: 1, PastDueCount: 0, OldestActiveAgeDays: 45}
	if briefA.Exceptions != wantA {
		t.Fatalf("Tenant A aggregate = %+v, want %+v — tenant B's waivers bled in", briefA.Exceptions, wantA)
	}

	briefB, err := gen.Assemble(ctxFor(t, tenantB), "2026-05-31")
	if err != nil {
		t.Fatalf("Assemble(B): %v", err)
	}
	wantB := board.ExceptionSummary{ActiveCount: 5, PastDueCount: 4, OldestActiveAgeDays: 300}
	if briefB.Exceptions != wantB {
		t.Fatalf("Tenant B aggregate = %+v, want %+v", briefB.Exceptions, wantB)
	}
}

// TestIntegration_ExceptionAggregate_FrozenIntoBrief proves the aggregate is
// part of the frozen, append-only snapshot: a generated brief round-trips its
// exceptions section verbatim through `board_briefs.content`.
func TestIntegration_ExceptionAggregate_FrozenIntoBrief(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	anchor := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	seedFramework(t, admin, tenant, "soc2", "SOC 2")
	ctrlID := seedControl(t, admin, tenant)
	seedException(t, admin, tenant, ctrlID, anchor, exceptionSeed{
		status: "active", requestedAgo: 120 * day, effectiveAgo: 120 * day, expiresFromReq: 90 * day,
	})

	ctx := ctxFor(t, tenant)
	stored, err := newBriefGenerator(t, app, anchor).Generate(ctx, "2026-05-31")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := board.ExceptionSummary{ActiveCount: 1, PastDueCount: 1, OldestActiveAgeDays: 120}
	if stored.Content.Exceptions != want {
		t.Fatalf("generated brief exceptions = %+v, want %+v", stored.Content.Exceptions, want)
	}

	// Re-read the frozen row: the aggregate survives the JSONB round trip.
	reread, err := board.NewStore(app).Get(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reread.Content.Exceptions != want {
		t.Fatalf("frozen brief exceptions = %+v, want %+v", reread.Content.Exceptions, want)
	}
}
