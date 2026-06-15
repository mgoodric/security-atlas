//go:build integration

// Integration tests for slice 384: ActionPlan CRUD + M2M linkage + RLS +
// append-only audit + state machine, against real Postgres. RLS cannot be
// tested against a fake DB (memory rule: "Never mock the DB"). Uses the
// slice-435 dbtest harness (NewAppPool / NewMigratePool / WithTenantCtx).
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

// cleanupTables lists the slice-384 tables (children before parents) plus the
// seed FK targets, in FK-safe DELETE order for dbtest.SeedTenant.
var cleanupTables = []string{
	"action_plan_audit_log",
	"action_plan_risks",
	"action_plan_controls",
	"action_plans",
	"risks",
	"controls",
	"users",
}

// seedTenant returns a fresh tenant id with FK-safe cleanup registered.
func seedTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, cleanupTables...)
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	return dbtest.WithTenantCtx(t, tenant)
}

// seedUser inserts a tenant user (owner_id FK target).
func seedUser(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, 'Owner', 'active')
	`, id, tenant, id.String()+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// seedRisk inserts a risk (M2M target). treatment='avoid' carries no extra
// required fields.
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

// seedControl inserts a control (M2M target).
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

func validCreate(owner uuid.UUID) actionplan.CreateInput {
	return actionplan.CreateInput{
		Title:           "Close the IAC-06 freshness gap",
		Description:     "Customer X Q2 2026 TPRM finding #4 remediation.",
		TriggeringEvent: "Customer X Q2 2026 TPRM finding #4",
		OwnerID:         owner,
		Actor:           owner,
	}
}

// ---- AC-10 / AC-16: create writes a plan + a created audit row ----

func TestCreate_HappyPath_WritesAuditRow(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	p, err := store.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Status != actionplan.StatusDraft {
		t.Errorf("new plan status = %q, want draft", p.Status)
	}
	entries, err := store.ListAuditLog(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if len(entries) != 1 || entries[0].ActionType != actionplan.ActionCreated {
		t.Fatalf("audit log = %+v, want one 'created' row", entries)
	}
}

// ---- AC-10 / P0-384-8: due_date 5-year cap ----

func TestCreate_RejectsDueDateBeyondFiveYears(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(owner)
	tooFar := time.Now().UTC().AddDate(5, 0, 2)
	in.DueDate = &tooFar
	if _, err := store.Create(ctx, in); !errors.Is(err, actionplan.ErrDueDateTooFar) {
		t.Fatalf("Create with far due_date: got %v, want ErrDueDateTooFar", err)
	}
}

// ---- AC-10: owner must be a tenant user ----

func TestCreate_RejectsOwnerNotInTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(uuid.New()) // random, non-existent owner
	if _, err := store.Create(ctx, in); !errors.Is(err, actionplan.ErrOwnerNotInTenant) {
		t.Fatalf("Create with foreign owner: got %v, want ErrOwnerNotInTenant", err)
	}
}

// ---- AC-28: cross-tenant SELECT returns zero rows (RLS) ----

func TestRLS_CrossTenantSelectReturnsNothing(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := seedTenant(t, admin)
	tenantB := seedTenant(t, admin)
	ownerA := seedUser(t, admin, tenantA)
	store := actionplan.NewStore(app)

	created, err := store.Create(ctxFor(t, tenantA), validCreate(ownerA))
	if err != nil {
		t.Fatalf("Create (A): %v", err)
	}

	// Tenant B cannot see tenant A's plan via Get...
	if _, err := store.Get(ctxFor(t, tenantB), created.ID); !errors.Is(err, actionplan.ErrNotFound) {
		t.Fatalf("cross-tenant Get: got %v, want ErrNotFound", err)
	}
	// ...nor via List.
	rows, err := store.List(ctxFor(t, tenantB), actionplan.ListFilter{})
	if err != nil {
		t.Fatalf("List (B): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("cross-tenant List returned %d rows, want 0", len(rows))
	}
}

// ---- AC-29 / P0-384-4: cross-tenant linkage returns 404 ----

func TestLinkRisk_CrossTenantTargetReturns404(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := seedTenant(t, admin)
	tenantB := seedTenant(t, admin)
	ownerA := seedUser(t, admin, tenantA)
	store := actionplan.NewStore(app)

	plan, err := store.Create(ctxFor(t, tenantA), validCreate(ownerA))
	if err != nil {
		t.Fatalf("Create (A): %v", err)
	}
	// A risk that lives in tenant B.
	riskB := seedRisk(t, admin, tenantB)

	// Tenant A tries to link tenant B's risk -> 404 (existence-leak guard).
	if err := store.LinkRisk(ctxFor(t, tenantA), plan.ID, riskB, ownerA); !errors.Is(err, actionplan.ErrLinkTargetNotFound) {
		t.Fatalf("cross-tenant LinkRisk: got %v, want ErrLinkTargetNotFound", err)
	}
}

func TestLinkControl_CrossTenantTargetReturns404(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := seedTenant(t, admin)
	tenantB := seedTenant(t, admin)
	ownerA := seedUser(t, admin, tenantA)
	store := actionplan.NewStore(app)

	plan, err := store.Create(ctxFor(t, tenantA), validCreate(ownerA))
	if err != nil {
		t.Fatalf("Create (A): %v", err)
	}
	controlB := seedControl(t, admin, tenantB)
	if err := store.LinkControl(ctxFor(t, tenantA), plan.ID, controlB, ownerA); !errors.Is(err, actionplan.ErrLinkTargetNotFound) {
		t.Fatalf("cross-tenant LinkControl: got %v, want ErrLinkTargetNotFound", err)
	}
}

// ---- AC-17 / AC-19: link happy path + 409 already-linked ----

func TestLinkRisk_HappyPathThen409(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	risk := seedRisk(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	plan, err := store.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.LinkRisk(ctx, plan.ID, risk, owner); err != nil {
		t.Fatalf("LinkRisk: %v", err)
	}
	if err := store.LinkRisk(ctx, plan.ID, risk, owner); !errors.Is(err, actionplan.ErrAlreadyLinked) {
		t.Fatalf("double LinkRisk: got %v, want ErrAlreadyLinked", err)
	}
	// Linkage appears on the read shape.
	_, lk, err := store.GetWithLinkage(ctx, plan.ID)
	if err != nil {
		t.Fatalf("GetWithLinkage: %v", err)
	}
	if len(lk.Risks) != 1 || lk.Risks[0].TargetID != risk {
		t.Fatalf("linkage risks = %+v, want one entry for %s", lk.Risks, risk)
	}
	// Unlink, then 404 on second unlink.
	if err := store.UnlinkRisk(ctx, plan.ID, risk, owner); err != nil {
		t.Fatalf("UnlinkRisk: %v", err)
	}
	if err := store.UnlinkRisk(ctx, plan.ID, risk, owner); !errors.Is(err, actionplan.ErrNotLinked) {
		t.Fatalf("double UnlinkRisk: got %v, want ErrNotLinked", err)
	}
}

// ---- AC-21 / P0-384-7: 50-risk cap ----

func TestLinkRisk_EnforcesFiftyCap(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	plan, err := store.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < actionplan.MaxLinkedRisks; i++ {
		risk := seedRisk(t, admin, tenant)
		if err := store.LinkRisk(ctx, plan.ID, risk, owner); err != nil {
			t.Fatalf("LinkRisk #%d: %v", i, err)
		}
	}
	// The 51st link is rejected.
	extra := seedRisk(t, admin, tenant)
	if err := store.LinkRisk(ctx, plan.ID, extra, owner); !errors.Is(err, actionplan.ErrLimitExceeded) {
		t.Fatalf("51st LinkRisk: got %v, want ErrLimitExceeded", err)
	}
}

// ---- AC-30: state machine valid + invalid transitions (>= 6 cases) ----

func TestStateMachine_Transitions(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	store := actionplan.NewStore(app)

	cases := []struct {
		name      string
		path      []string // sequence of statuses to PATCH to, in order
		wantError bool     // whether the FINAL transition should fail
	}{
		// Valid multi-step paths.
		{"draft->in_progress", []string{actionplan.StatusInProgress}, false},
		{"draft->in_progress->completed", []string{actionplan.StatusInProgress, actionplan.StatusCompleted}, false},
		{"...->completed->verified", []string{actionplan.StatusInProgress, actionplan.StatusCompleted, actionplan.StatusVerified}, false},
		{"in_progress->blocked->in_progress", []string{actionplan.StatusInProgress, actionplan.StatusBlocked, actionplan.StatusInProgress}, false},
		// Invalid final transitions (each starts from a valid prefix).
		{"draft->completed (skip)", []string{actionplan.StatusCompleted}, true},
		{"draft->verified (skip)", []string{actionplan.StatusVerified}, true},
		{"verified->draft (terminal/back-to-draft)", []string{actionplan.StatusInProgress, actionplan.StatusCompleted, actionplan.StatusVerified, actionplan.StatusDraft}, true},
		{"completed->draft (back-to-draft)", []string{actionplan.StatusInProgress, actionplan.StatusCompleted, actionplan.StatusDraft}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tenant := seedTenant(t, admin)
			owner := seedUser(t, admin, tenant)
			ctx := ctxFor(t, tenant)
			plan, err := store.Create(ctx, validCreate(owner))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var lastErr error
			for i, status := range tc.path {
				s := status
				_, lastErr = store.Update(ctx, plan.ID, actionplan.UpdateInput{Status: &s, Actor: owner})
				// All but the final step in a wantError path must succeed.
				if tc.wantError && i < len(tc.path)-1 && lastErr != nil {
					t.Fatalf("prefix step %d (%s) failed unexpectedly: %v", i, status, lastErr)
				}
			}
			if tc.wantError {
				if !errors.Is(lastErr, actionplan.ErrIllegalTransition) {
					t.Fatalf("final transition: got %v, want ErrIllegalTransition", lastErr)
				}
			} else if lastErr != nil {
				t.Fatalf("valid path failed: %v", lastErr)
			}
		})
	}
}

// ---- AC-14 / P0-384-6: soft-delete (tombstone), GET returns 404 ----

func TestTombstone_SoftDeletePreservesRow(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	plan, err := store.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Tombstone(ctx, plan.ID, owner); err != nil {
		t.Fatalf("Tombstone: %v", err)
	}
	if _, err := store.Get(ctx, plan.ID); !errors.Is(err, actionplan.ErrNotFound) {
		t.Fatalf("Get after tombstone: got %v, want ErrNotFound", err)
	}
	// The row is preserved (not hard-deleted): the admin pool still sees it.
	var tombstonedAt *time.Time
	if err := admin.QueryRow(context.Background(),
		`SELECT tombstoned_at FROM action_plans WHERE id = $1`, plan.ID).Scan(&tombstonedAt); err != nil {
		t.Fatalf("admin select tombstoned row: %v", err)
	}
	if tombstonedAt == nil {
		t.Fatalf("tombstoned_at should be set, row should still exist")
	}
	// The tombstone wrote an audit row (the trail is preserved).
	entries, err := store.ListAuditLog(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	var sawTombstone bool
	for _, e := range entries {
		if e.ActionType == actionplan.ActionTombstoned {
			sawTombstone = true
		}
	}
	if !sawTombstone {
		t.Fatalf("expected a 'tombstoned' audit row, got %+v", entries)
	}
}

// ---- AC-9: audit log is append-only (UPDATE denied by DB trigger) ----

func TestAuditLog_AppendOnly_UpdateDenied(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	plan, err := store.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	entries, err := store.ListAuditLog(ctx, plan.ID)
	if err != nil || len(entries) == 0 {
		t.Fatalf("ListAuditLog: %v (n=%d)", err, len(entries))
	}
	// Even the privileged admin (BYPASSRLS) pool cannot UPDATE the audit row:
	// the BEFORE UPDATE trigger raises. This is the AC-9 guarantee that goes
	// beyond policy omission.
	_, uerr := admin.Exec(context.Background(),
		`UPDATE action_plan_audit_log SET action_type = 'updated' WHERE id = $1`, entries[0].ID)
	if uerr == nil {
		t.Fatalf("UPDATE on action_plan_audit_log should be denied by the append-only trigger")
	}
	_, derr := admin.Exec(context.Background(),
		`DELETE FROM action_plan_audit_log WHERE id = $1`, entries[0].ID)
	if derr == nil {
		t.Fatalf("DELETE on action_plan_audit_log should be denied by the append-only trigger")
	}
}

// ---- AC-27: audit-period-freezing snapshot includes only created_at <= frozen_at ----

func TestListSnapshot_FreezeHorizon(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	// An "old" plan whose created_at we backdate via the admin pool.
	oldPlan, err := store.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create old: %v", err)
	}
	backdated := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := admin.Exec(context.Background(),
		`UPDATE action_plans SET created_at = $2 WHERE id = $1`, oldPlan.ID, backdated); err != nil {
		t.Fatalf("backdate old plan: %v", err)
	}
	// A "new" plan created after the freeze horizon.
	if _, err := store.Create(ctx, validCreate(owner)); err != nil {
		t.Fatalf("Create new: %v", err)
	}

	frozenAt := time.Now().UTC().Add(-24 * time.Hour) // between old and new
	snap, err := store.ListSnapshot(ctx, frozenAt)
	if err != nil {
		t.Fatalf("ListSnapshot: %v", err)
	}
	if len(snap) != 1 || snap[0].ID != oldPlan.ID {
		t.Fatalf("snapshot = %+v, want only the backdated plan %s", snap, oldPlan.ID)
	}
}
