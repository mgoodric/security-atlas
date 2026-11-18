//go:build integration

// Integration tests for #2449: tombstoning an action plan, or unlinking a
// risk from one, refuses to leave a mitigate risk with no linked control
// and no live action plan, including under concurrent writes.
//
// Run with:  go test -tags=integration -p 1 ./internal/actionplan/...

package actionplan_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

type mitigationEnv struct {
	admin  *pgxpool.Pool
	store  *actionplan.Store
	tenant string
	owner  uuid.UUID
	ctx    context.Context
}

func newMitigationEnv(t *testing.T) mitigationEnv {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin, append([]string{"risk_control_links"}, cleanupTables...)...)
	return mitigationEnv{
		admin:  admin,
		store:  actionplan.NewStore(app),
		tenant: tenant,
		owner:  seedUser(t, admin, tenant),
		ctx:    ctxFor(t, tenant),
	}
}

// riskWith inserts a risk with the given treatment.
func (e mitigationEnv) riskWith(t *testing.T, treatment string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := e.admin.Exec(context.Background(), `
		INSERT INTO risks (id, tenant_id, title, category_id, treatment)
		VALUES ($1, $2, 'Mitigation risk', risk_category_id($2, 'operational'), $3)
	`, id, e.tenant, treatment); err != nil {
		t.Fatalf("seed %s risk: %v", treatment, err)
	}
	return id
}

// planLinkedTo creates a plan linked to each risk.
func (e mitigationEnv) planLinkedTo(t *testing.T, risks ...uuid.UUID) uuid.UUID {
	t.Helper()
	p, err := e.store.Create(e.ctx, validCreate(e.owner))
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	for _, r := range risks {
		if err := e.store.LinkRisk(e.ctx, p.ID, r, e.owner); err != nil {
			t.Fatalf("link risk: %v", err)
		}
	}
	return p.ID
}

func (e mitigationEnv) linkControl(t *testing.T, risk uuid.UUID) {
	t.Helper()
	control := seedControl(t, e.admin, e.tenant)
	if _, err := e.admin.Exec(context.Background(),
		`INSERT INTO risk_control_links (risk_id, control_id, tenant_id) VALUES ($1, $2, $3)`,
		risk, control, e.tenant); err != nil {
		t.Fatalf("link control to risk: %v", err)
	}
}

func (e mitigationEnv) livePlans(t *testing.T, risk uuid.UUID) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(), `
		SELECT count(*) FROM action_plan_risks apr
		JOIN action_plans ap ON ap.id = apr.action_plan_id
		WHERE apr.risk_id = $1 AND ap.tombstoned_at IS NULL`, risk).Scan(&n); err != nil {
		t.Fatalf("count live plans: %v", err)
	}
	return n
}

func wantMitigationRequired(t *testing.T, err error, risk uuid.UUID) {
	t.Helper()
	var mr *actionplan.MitigationRequiredError
	if !errors.As(err, &mr) || !errors.Is(err, actionplan.ErrMitigationRequired) {
		t.Fatalf("err = %v, want MitigationRequiredError", err)
	}
	if mr.RiskID != risk {
		t.Fatalf("refusal names risk %s, want %s", mr.RiskID, risk)
	}
}

func TestTombstone_RefusesLastMitigation(t *testing.T) {
	e := newMitigationEnv(t)
	r := e.riskWith(t, "mitigate")
	plan := e.planLinkedTo(t, r)

	wantMitigationRequired(t, e.store.Tombstone(e.ctx, plan, e.owner), r)
	if _, err := e.store.Get(e.ctx, plan); err != nil {
		t.Fatalf("plan should still be live after the refusal: %v", err)
	}
}

func TestTombstone_AllowedWhenAnotherMitigationRemains(t *testing.T) {
	e := newMitigationEnv(t)
	withControl := e.riskWith(t, "mitigate")
	e.linkControl(t, withControl)
	withPlan := e.riskWith(t, "mitigate")
	e.planLinkedTo(t, withPlan)
	plan := e.planLinkedTo(t, withControl, withPlan)

	if err := e.store.Tombstone(e.ctx, plan, e.owner); err != nil {
		t.Fatalf("Tombstone: %v", err)
	}
}

func TestUnlinkRisk_RefusesLastMitigation(t *testing.T) {
	e := newMitigationEnv(t)
	r := e.riskWith(t, "mitigate")
	plan := e.planLinkedTo(t, r)

	wantMitigationRequired(t, e.store.UnlinkRisk(e.ctx, plan, r, e.owner), r)
	if n := e.livePlans(t, r); n != 1 {
		t.Fatalf("live plans after refusal = %d, want 1", n)
	}
}

// A non-mitigate risk does not count links: its last plan goes either way.
func TestNonMitigateRiskUnaffected(t *testing.T) {
	e := newMitigationEnv(t)
	a := e.riskWith(t, "avoid")
	b := e.riskWith(t, "untreated")
	unlinkPlan := e.planLinkedTo(t, a)
	tombstonePlan := e.planLinkedTo(t, b)

	if err := e.store.UnlinkRisk(e.ctx, unlinkPlan, a, e.owner); err != nil {
		t.Fatalf("UnlinkRisk on avoid risk: %v", err)
	}
	if err := e.store.Tombstone(e.ctx, tombstonePlan, e.owner); err != nil {
		t.Fatalf("Tombstone over untreated risk: %v", err)
	}
}

// A closed mitigate risk is exempt: it blocks neither a tombstone nor an
// unlink of its last plan.
func TestClosedMitigateRiskDoesNotBlock(t *testing.T) {
	e := newMitigationEnv(t)
	unlinkRisk := e.riskWith(t, "mitigate")
	tombstoneRisk := e.riskWith(t, "mitigate")
	unlinkPlan := e.planLinkedTo(t, unlinkRisk)
	tombstonePlan := e.planLinkedTo(t, tombstoneRisk)
	if _, err := e.admin.Exec(context.Background(), `
		UPDATE risks SET status = 'closed', closure_reason = 'not_applicable',
		       closed_at = now(), closed_by = 'test'
		WHERE id = ANY($1)`, []uuid.UUID{unlinkRisk, tombstoneRisk}); err != nil {
		t.Fatalf("close risks: %v", err)
	}

	if err := e.store.UnlinkRisk(e.ctx, unlinkPlan, unlinkRisk, e.owner); err != nil {
		t.Fatalf("UnlinkRisk on closed mitigate risk: %v", err)
	}
	if err := e.store.Tombstone(e.ctx, tombstonePlan, e.owner); err != nil {
		t.Fatalf("Tombstone over closed mitigate risk: %v", err)
	}
}

// A tombstone over several risks refuses as a whole when any one would
// break: the plan stays live and every link stays.
func TestTombstone_MultiRiskRefusesWhole(t *testing.T) {
	e := newMitigationEnv(t)
	safe := e.riskWith(t, "mitigate")
	e.linkControl(t, safe)
	other := e.riskWith(t, "avoid")
	bare := e.riskWith(t, "mitigate")
	plan := e.planLinkedTo(t, safe, other, bare)

	wantMitigationRequired(t, e.store.Tombstone(e.ctx, plan, e.owner), bare)
	if _, err := e.store.Get(e.ctx, plan); err != nil {
		t.Fatalf("plan should still be live: %v", err)
	}
	for _, r := range []uuid.UUID{safe, other, bare} {
		if n := e.livePlans(t, r); n != 1 {
			t.Fatalf("risk %s live plans = %d, want 1", r, n)
		}
	}
}

// raceTwo forces both writes to reach the risk row lock before either
// acts: the test holds the risk row, starts both, waits, then releases.
// Exactly one may succeed; the other must see the first's commit and
// refuse. Without the lock and re-count, both count the other's plan as
// live and both succeed.
func (e mitigationEnv) raceTwo(t *testing.T, risk uuid.UUID, a, b func(context.Context) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM risks WHERE id = $1 FOR NO KEY UPDATE`, risk); err != nil {
		t.Fatalf("hold risk row: %v", err)
	}
	done := make(chan error, 2)
	for _, op := range []func(context.Context) error{a, b} {
		go func() { done <- op(ctxFor(t, e.tenant)) }()
	}
	select {
	case err := <-done:
		t.Fatalf("a write returned (%v) while the risk row was held; want it to wait", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("release risk row: %v", err)
	}
	var ok, refused int
	for range 2 {
		select {
		case err := <-done:
			switch {
			case err == nil:
				ok++
			case errors.Is(err, actionplan.ErrMitigationRequired):
				refused++
			default:
				t.Fatalf("unexpected error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a write still waiting 10s after the risk row was released")
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("ok=%d refused=%d, want exactly one of each", ok, refused)
	}
	if n := e.livePlans(t, risk); n != 1 {
		t.Fatalf("live plans after the race = %d, want 1", n)
	}
}

func TestConcurrentTombstones_OneRefused(t *testing.T) {
	e := newMitigationEnv(t)
	r := e.riskWith(t, "mitigate")
	p1, p2 := e.planLinkedTo(t, r), e.planLinkedTo(t, r)
	e.raceTwo(t, r,
		func(ctx context.Context) error { return e.store.Tombstone(ctx, p1, e.owner) },
		func(ctx context.Context) error { return e.store.Tombstone(ctx, p2, e.owner) })
}

func TestConcurrentUnlinks_OneRefused(t *testing.T) {
	e := newMitigationEnv(t)
	r := e.riskWith(t, "mitigate")
	p1, p2 := e.planLinkedTo(t, r), e.planLinkedTo(t, r)
	e.raceTwo(t, r,
		func(ctx context.Context) error { return e.store.UnlinkRisk(ctx, p1, r, e.owner) },
		func(ctx context.Context) error { return e.store.UnlinkRisk(ctx, p2, r, e.owner) })
}

func TestConcurrentTombstoneAndUnlink_OneRefused(t *testing.T) {
	e := newMitigationEnv(t)
	r := e.riskWith(t, "mitigate")
	p1, p2 := e.planLinkedTo(t, r), e.planLinkedTo(t, r)
	e.raceTwo(t, r,
		func(ctx context.Context) error { return e.store.Tombstone(ctx, p1, e.owner) },
		func(ctx context.Context) error { return e.store.UnlinkRisk(ctx, p2, r, e.owner) })
}
