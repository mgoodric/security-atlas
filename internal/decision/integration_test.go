//go:build integration

// Integration tests for slice 055: Decision Log CRUD + linkage.
// Covers every AC against real Postgres. RLS cannot be tested against a
// fake DB (memory rule: "Never mock the DB").
//
// Run with:  go test -tags=integration -race ./internal/decision/...

package decision_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/decision"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ---- harness helpers ----

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

// freshTenant returns a fresh tenant UUID and registers cleanup that wipes
// every decisions-related row for it (and the seed risks/controls/exceptions).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM decisions_audit WHERE tenant_id = $1`,
			`DELETE FROM decision_risks WHERE tenant_id = $1`,
			`DELETE FROM decision_controls WHERE tenant_id = $1`,
			`DELETE FROM decision_exceptions WHERE tenant_id = $1`,
			`DELETE FROM decision_scope_predicates WHERE tenant_id = $1`,
			`DELETE FROM notifications WHERE tenant_id = $1`,
			`UPDATE decisions SET superseded_by = NULL WHERE tenant_id = $1`,
			`DELETE FROM decisions WHERE tenant_id = $1`,
			`DELETE FROM exceptions WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
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

// seedRisk inserts a risk row directly via the admin pool (BYPASSRLS).
// treatment='avoid' is used because it is the one treatment status that
// carries no extra required fields (the risks_accept_fields_required and
// risks_transfer_* CHECK constraints only bind 'accept' / 'transfer').
func seedRisk(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (id, tenant_id, title, category, treatment)
		VALUES ($1, $2, 'Test risk', 'operational', 'avoid')
	`, id, tenant); err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	return id
}

// seedControl inserts a control row. slice 009 added a NOT NULL bundle_id;
// synthesise a legacy_<uuid> matching the slice-009 backfill pattern.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	bundleID := "legacy_" + id.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'Test control', 'IAC', 'automated', $3)
	`, id, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// seedException inserts an exception row tied to a control.
func seedException(t *testing.T, admin *pgxpool.Pool, tenant string, controlID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO exceptions (id, tenant_id, control_id, scope_cell_predicate, justification, requested_by, expires_at)
		VALUES ($1, $2, $3, '{}'::jsonb, 'test waiver', 'tester', now() + interval '90 days')
	`, id, tenant, controlID); err != nil {
		t.Fatalf("seed exception: %v", err)
	}
	return id
}

// validCreate returns a CreateInput with sensible defaults.
func validCreate(maker string) decision.CreateInput {
	revisit := time.Now().UTC().AddDate(0, 0, 30)
	return decision.CreateInput{
		Title:         "Ship MVP, defer SAML to v1.2",
		Narrative:     "Customer demand prioritises GA over SSO depth.",
		Constraints:   []string{"time-pressure", "dependency-blocked"},
		Tradeoffs:     "SSO customers wait one minor release.",
		DecisionMaker: maker,
		DecidedAt:     time.Now().UTC(),
		RevisitBy:     &revisit,
	}
}

// ---- AC-1 / AC-8: create with required fields + DL-id format ----

func TestCreate_HappyPath_GeneratesDecisionID(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	d, err := store.Create(ctx, validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.ID == uuid.Nil {
		t.Fatal("expected non-nil id")
	}
	if d.Status != decision.StatusActive {
		t.Fatalf("expected status=active, got %q", d.Status)
	}
	// AC-1: DL-YYYY-MM-DD-NNNN format. First decision of the day -> 0001.
	wantPrefix := "DL-" + time.Now().UTC().Format("2006-01-02") + "-"
	if !strings.HasPrefix(d.DecisionID, wantPrefix) || !strings.HasSuffix(d.DecisionID, "-0001") {
		t.Fatalf("decision_id %q does not match DL-YYYY-MM-DD-0001", d.DecisionID)
	}
	// AC-3: a `created` audit row is written.
	entries, err := store.ListAudit(ctx, d.ID)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != decision.ActionCreated {
		t.Fatalf("expected 1 created audit row, got %+v", entries)
	}
}

func TestCreate_DecisionIDSequenceIncrements(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	d1, err := store.Create(ctx, validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create d1: %v", err)
	}
	d2, err := store.Create(ctx, validCreate("bob@example.com"))
	if err != nil {
		t.Fatalf("Create d2: %v", err)
	}
	if !strings.HasSuffix(d1.DecisionID, "-0001") {
		t.Fatalf("d1 decision_id %q expected suffix -0001", d1.DecisionID)
	}
	if !strings.HasSuffix(d2.DecisionID, "-0002") {
		t.Fatalf("d2 decision_id %q expected suffix -0002", d2.DecisionID)
	}
}

func TestCreate_RejectsMissingRequiredFields(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	noTitle := validCreate("alice@example.com")
	noTitle.Title = ""
	if _, err := store.Create(ctx, noTitle); !errors.Is(err, decision.ErrTitleRequired) {
		t.Fatalf("expected ErrTitleRequired, got %v", err)
	}

	noMaker := validCreate("")
	if _, err := store.Create(ctx, noMaker); !errors.Is(err, decision.ErrDecisionMakerRequired) {
		t.Fatalf("expected ErrDecisionMakerRequired, got %v", err)
	}

	noDecidedAt := validCreate("alice@example.com")
	noDecidedAt.DecidedAt = time.Time{}
	if _, err := store.Create(ctx, noDecidedAt); !errors.Is(err, decision.ErrDecidedAtRequired) {
		t.Fatalf("expected ErrDecidedAtRequired, got %v", err)
	}
}

// ---- AC-2: get with linkage + list filters ----

func TestList_FilterByStatus(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	d1, err := store.Create(ctx, validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	repl, err := store.Create(ctx, validCreate("bob@example.com"))
	if err != nil {
		t.Fatalf("Create replacement: %v", err)
	}
	if _, err := store.Supersede(ctx, d1.ID, repl.ID, "carol@example.com"); err != nil {
		t.Fatalf("Supersede: %v", err)
	}

	active, err := store.List(ctx, decision.ListFilter{Status: decision.StatusActive})
	if err != nil {
		t.Fatalf("List active: %v", err)
	}
	for _, d := range active {
		if d.Status != decision.StatusActive {
			t.Fatalf("status filter leaked %q", d.Status)
		}
	}
	superseded, err := store.List(ctx, decision.ListFilter{Status: decision.StatusSuperseded})
	if err != nil {
		t.Fatalf("List superseded: %v", err)
	}
	if len(superseded) != 1 || superseded[0].ID != d1.ID {
		t.Fatalf("expected 1 superseded decision (d1), got %+v", superseded)
	}
}

func TestList_FilterByRevisitWindow(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	soon := validCreate("alice@example.com")
	soonDate := time.Now().UTC().AddDate(0, 0, 10)
	soon.RevisitBy = &soonDate
	dSoon, err := store.Create(ctx, soon)
	if err != nil {
		t.Fatalf("Create soon: %v", err)
	}

	far := validCreate("bob@example.com")
	farDate := time.Now().UTC().AddDate(0, 0, 200)
	far.RevisitBy = &farDate
	if _, err := store.Create(ctx, far); err != nil {
		t.Fatalf("Create far: %v", err)
	}

	within30, err := store.List(ctx, decision.ListFilter{RevisitDueWithinDays: 30})
	if err != nil {
		t.Fatalf("List revisit window: %v", err)
	}
	if len(within30) != 1 || within30[0].ID != dSoon.ID {
		t.Fatalf("expected only the 10-day-out decision in a 30-day window, got %+v", within30)
	}
}

// ---- AC-8: create-with-links, GET returns all linkage, supersede chain ----

func TestIntegration_CreateLinkSupersede(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	riskID := seedRisk(t, admin, tenant)
	controlID := seedControl(t, admin, tenant)
	exceptionID := seedException(t, admin, tenant, controlID)

	d, err := store.Create(ctx, validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// AC-5: link a risk + control + exception.
	if err := store.AddLink(ctx, d.ID, decision.LinkRisk, riskID, "alice@example.com"); err != nil {
		t.Fatalf("AddLink risk: %v", err)
	}
	if err := store.AddLink(ctx, d.ID, decision.LinkControl, controlID, "alice@example.com"); err != nil {
		t.Fatalf("AddLink control: %v", err)
	}
	if err := store.AddLink(ctx, d.ID, decision.LinkException, exceptionID, "alice@example.com"); err != nil {
		t.Fatalf("AddLink exception: %v", err)
	}
	// AC-5 idempotency: re-linking the same risk is a no-op (no error).
	if err := store.AddLink(ctx, d.ID, decision.LinkRisk, riskID, "alice@example.com"); err != nil {
		t.Fatalf("AddLink risk (idempotent): %v", err)
	}

	// AC-2: GET returns all linkage in one response.
	got, lk, err := store.GetWithLinkage(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetWithLinkage: %v", err)
	}
	if got.ID != d.ID {
		t.Fatalf("GetWithLinkage returned wrong decision")
	}
	if len(lk.Risks) != 1 || lk.Risks[0].TargetID != riskID {
		t.Fatalf("expected 1 risk link, got %+v", lk.Risks)
	}
	if len(lk.Controls) != 1 || lk.Controls[0].TargetID != controlID {
		t.Fatalf("expected 1 control link, got %+v", lk.Controls)
	}
	if len(lk.Exceptions) != 1 || lk.Exceptions[0].TargetID != exceptionID {
		t.Fatalf("expected 1 exception link, got %+v", lk.Exceptions)
	}

	// AC-5: unlink the risk.
	if err := store.RemoveLink(ctx, d.ID, decision.LinkRisk, riskID, "alice@example.com"); err != nil {
		t.Fatalf("RemoveLink risk: %v", err)
	}
	_, lk2, err := store.GetWithLinkage(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetWithLinkage post-unlink: %v", err)
	}
	if len(lk2.Risks) != 0 {
		t.Fatalf("expected 0 risk links after unlink, got %+v", lk2.Risks)
	}

	// AC-4 + AC-8: supersede d with a new decision; old becomes superseded,
	// reachable via superseded_by from the new one.
	repl, err := store.Create(ctx, validCreate("bob@example.com"))
	if err != nil {
		t.Fatalf("Create replacement: %v", err)
	}
	superseded, err := store.Supersede(ctx, d.ID, repl.ID, "carol@example.com")
	if err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if superseded.Status != decision.StatusSuperseded {
		t.Fatalf("expected status=superseded, got %q", superseded.Status)
	}
	if superseded.SupersededBy == nil || *superseded.SupersededBy != repl.ID {
		t.Fatalf("expected superseded_by=%s, got %v", repl.ID, superseded.SupersededBy)
	}
	// AC-4: the old decision is NOT deleted -- still readable.
	old, err := store.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("Get superseded decision (must still exist): %v", err)
	}
	if old.ID != d.ID {
		t.Fatalf("superseded decision not preserved")
	}

	// AC-3: audit trail captures created + 4 link ops + superseded.
	entries, err := store.ListAudit(ctx, d.ID)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	var sawSuperseded bool
	for _, e := range entries {
		if e.Action == decision.ActionSuperseded {
			sawSuperseded = true
		}
	}
	if !sawSuperseded {
		t.Fatalf("expected a superseded audit row, got %+v", entries)
	}
}

// ---- AC-4: supersede on a non-active decision is a 409-equivalent ----

func TestSupersede_RejectsNonActive(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	d, err := store.Create(ctx, validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r1, err := store.Create(ctx, validCreate("bob@example.com"))
	if err != nil {
		t.Fatalf("Create r1: %v", err)
	}
	r2, err := store.Create(ctx, validCreate("carol@example.com"))
	if err != nil {
		t.Fatalf("Create r2: %v", err)
	}
	if _, err := store.Supersede(ctx, d.ID, r1.ID, "x@example.com"); err != nil {
		t.Fatalf("first Supersede: %v", err)
	}
	// d is now `superseded`; a second supersede must fail with ErrWrongState.
	if _, err := store.Supersede(ctx, d.ID, r2.ID, "x@example.com"); !errors.Is(err, decision.ErrWrongState) {
		t.Fatalf("expected ErrWrongState on re-supersede, got %v", err)
	}
}

// ---- AC-3: PATCH writes an append-only audit row ----

func TestUpdate_WritesAuditRow(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	d, err := store.Create(ctx, validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newTitle := "Ship MVP, defer SAML to v1.3"
	updated, err := store.Update(ctx, d.ID, decision.UpdateInput{Title: &newTitle, Actor: "alice@example.com"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != newTitle {
		t.Fatalf("title not updated: %q", updated.Title)
	}
	entries, err := store.ListAudit(ctx, d.ID)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	var sawUpdated bool
	for _, e := range entries {
		if e.Action == decision.ActionUpdated {
			sawUpdated = true
		}
	}
	if !sawUpdated {
		t.Fatalf("expected an updated audit row, got %+v", entries)
	}
}

// ---- AC-9: cross-tenant linkage returns ErrCrossTenantLink + audit row ----

func TestAddLink_CrossTenantDenied(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := decision.NewStore(app)

	// A decision in tenant A.
	ctxA := ctxFor(t, tenantA)
	d, err := store.Create(ctxA, validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create in tenant A: %v", err)
	}
	// A risk in tenant B.
	foreignRisk := seedRisk(t, admin, tenantB)

	// AC-9: linking A's decision to B's risk must return ErrCrossTenantLink
	// (the handler maps this to 404 -- existence-leak prevention).
	err = store.AddLink(ctxA, d.ID, decision.LinkRisk, foreignRisk, "alice@example.com")
	if !errors.Is(err, decision.ErrCrossTenantLink) {
		t.Fatalf("expected ErrCrossTenantLink, got %v", err)
	}
	// Same for a control and an exception target that don't resolve.
	if err := store.AddLink(ctxA, d.ID, decision.LinkControl, uuid.New(), "alice@example.com"); !errors.Is(err, decision.ErrCrossTenantLink) {
		t.Fatalf("expected ErrCrossTenantLink for control, got %v", err)
	}
	if err := store.AddLink(ctxA, d.ID, decision.LinkException, uuid.New(), "alice@example.com"); !errors.Is(err, decision.ErrCrossTenantLink) {
		t.Fatalf("expected ErrCrossTenantLink for exception, got %v", err)
	}
	if err := store.AddLink(ctxA, d.ID, decision.LinkScopePredicate, uuid.New(), "alice@example.com"); !errors.Is(err, decision.ErrCrossTenantLink) {
		t.Fatalf("expected ErrCrossTenantLink for scope predicate, got %v", err)
	}

	// AC-9: the failed attempt is recorded in decisions_audit.
	entries, err := store.ListAudit(ctxA, d.ID)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	var denied int
	for _, e := range entries {
		if e.Action == decision.ActionCrossTenantLinkDenied {
			denied++
		}
	}
	if denied != 4 {
		t.Fatalf("expected 4 cross_tenant_link_denied audit rows, got %d (%+v)", denied, entries)
	}

	// And the link did not actually land.
	_, lk, err := store.GetWithLinkage(ctxA, d.ID)
	if err != nil {
		t.Fatalf("GetWithLinkage: %v", err)
	}
	if len(lk.Risks)+len(lk.Controls)+len(lk.Exceptions)+len(lk.ScopePredicates) != 0 {
		t.Fatalf("expected no links to have landed, got %+v", lk)
	}
}

// ---- AC-9: a decision in another tenant is invisible (404-equivalent) ----

func TestGet_CrossTenantInvisible(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := decision.NewStore(app)

	d, err := store.Create(ctxFor(t, tenantA), validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create in tenant A: %v", err)
	}
	// Tenant B cannot see tenant A's decision.
	if _, err := store.Get(ctxFor(t, tenantB), d.ID); !errors.Is(err, decision.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant Get, got %v", err)
	}
}

// ---- AC-6: overdue surface + daily notifier emits once, never repeats ----

func TestOverdue_NotifierEmitsOncePerDecision(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	migrate := openPool(t, adminDSN(t))
	defer migrate.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	// A decision whose revisit_by is already in the past.
	overdueIn := validCreate("alice@example.com")
	past := time.Now().UTC().AddDate(0, 0, -5)
	overdueIn.RevisitBy = &past
	d, err := store.Create(ctx, overdueIn)
	if err != nil {
		t.Fatalf("Create overdue: %v", err)
	}

	// AC-6: it surfaces in the overdue list.
	overdue, err := store.Overdue(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("Overdue: %v", err)
	}
	if len(overdue) != 1 || overdue[0].ID != d.ID {
		t.Fatalf("expected 1 overdue decision, got %+v", overdue)
	}

	// AC-6: the daily notifier emits one notification.
	notifier := decision.NewNotifier(migrate, nil)
	emitted, err := notifier.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if emitted < 1 {
		t.Fatalf("expected at least 1 notification emitted, got %d", emitted)
	}

	// AC-6 P0 anti-criterion: a second sweep emits NOTHING for the same
	// decision (the overdue_notified audit row is the dedup marker).
	emitted2, err := notifier.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce (2nd): %v", err)
	}
	for _, e := range mustAudit(t, store, ctx, d.ID) {
		_ = e
	}
	// Count overdue_notified rows for this decision -- must be exactly 1.
	notifiedRows := 0
	for _, e := range mustAudit(t, store, ctx, d.ID) {
		if e.Action == decision.ActionOverdueNotified {
			notifiedRows++
		}
	}
	if notifiedRows != 1 {
		t.Fatalf("expected exactly 1 overdue_notified audit row, got %d (2nd sweep emitted %d)", notifiedRows, emitted2)
	}

	// The notification recipient is the decision_maker.
	var recipient string
	if err := admin.QueryRow(context.Background(),
		`SELECT recipient_user_id FROM notifications WHERE tenant_id = $1 AND type = $2`,
		tenant, decision.NotificationTypeOverdue,
	).Scan(&recipient); err != nil {
		t.Fatalf("query notification recipient: %v", err)
	}
	if recipient != "alice@example.com" {
		t.Fatalf("expected notification recipient=alice@example.com, got %q", recipient)
	}
}

func mustAudit(t *testing.T, store *decision.Store, ctx context.Context, id uuid.UUID) []decision.AuditEntry {
	t.Helper()
	entries, err := store.ListAudit(ctx, id)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	return entries
}

// ---- AC-7: OSCAL audit-narrative emission ----

func TestNarrative_EmitRemarksFormatAndOptOut(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := decision.NewStore(app)
	ctx := ctxFor(t, tenant)

	controlID := seedControl(t, admin, tenant)
	riskID := seedRisk(t, admin, tenant)

	d, err := store.Create(ctx, validCreate("alice@example.com"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.AddLink(ctx, d.ID, decision.LinkControl, controlID, "alice@example.com"); err != nil {
		t.Fatalf("AddLink control: %v", err)
	}
	if err := store.AddLink(ctx, d.ID, decision.LinkRisk, riskID, "alice@example.com"); err != nil {
		t.Fatalf("AddLink risk: %v", err)
	}

	got, _, err := store.GetWithLinkage(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetWithLinkage: %v", err)
	}

	// AC-7: the narrative format matches the issue spec.
	remarks := decision.EmitRemarks([]decision.NarrativeInput{{
		Decision:         got,
		LinkedControlIDs: []uuid.UUID{controlID},
		LinkedRiskIDs:    []string{riskID.String()},
	}})
	if len(remarks) != 1 {
		t.Fatalf("expected 1 emitted remark, got %d", len(remarks))
	}
	text := remarks[0].Text
	for _, want := range []string{
		"[" + got.DecisionID + "]",
		got.Title,
		"alice@example.com",
		got.DecidedAt.UTC().Format("2006-01-02"),
		"Linked risks: " + riskID.String(),
		"Revisit:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("narrative %q missing %q", text, want)
		}
	}

	// AC-7 / P0 anti-criterion: an opted-out decision is excluded.
	optedOut, err := store.SetAuditNarrativeOptOut(ctx, d.ID, true, "alice@example.com")
	if err != nil {
		t.Fatalf("SetAuditNarrativeOptOut: %v", err)
	}
	remarks2 := decision.EmitRemarks([]decision.NarrativeInput{{
		Decision:         optedOut,
		LinkedControlIDs: []uuid.UUID{controlID},
		LinkedRiskIDs:    []string{riskID.String()},
	}})
	if len(remarks2) != 0 {
		t.Fatalf("expected opted-out decision to emit no remarks, got %+v", remarks2)
	}
}
