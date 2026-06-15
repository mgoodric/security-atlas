//go:build integration

// Integration tests for slice 021: Exception / waiver workflow.
// Covers every AC against real Postgres. RLS cannot be tested against a
// fake DB (memory rule: "Never mock the DB").
//
// Run with:  go test -tags=integration -race ./internal/exception/...

package exception_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ---- harness helpers ----

// freshTenant returns a brand-new tenant id and registers cleanup of every
// table the exception tests touch. Pure tenant-scoped DELETE in FK order
// (audit log, then exceptions, then controls), so it delegates to
// dbtest.SeedTenant (slice 435 / 742 drain).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"exception_audit_log",
		"exceptions",
		"controls",
	)
}

// seedControl creates a control row directly via the admin pool (BYPASSRLS).
// slice 009 added a NOT NULL bundle_id; synthesise a legacy_<uuid> matching
// the slice-009 backfill pattern.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	bundleID := "legacy_" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'Test control', 'IAC', 'automated', $3)
	`, ctrlID, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// validCreate returns a CreateInput with sensible defaults; tests override
// individual fields.
func validCreate(controlID uuid.UUID, requester string) exception.CreateInput {
	return exception.CreateInput{
		ControlID:     controlID,
		Justification: "Vendor compatibility blocker — Q3 fix planned",
		RequestedBy:   requester,
		ExpiresAt:     time.Now().UTC().Add(90 * 24 * time.Hour),
		CompensatingControls: []string{
			"Weekly SRE manual log review",
			"Quarterly executive attestation",
		},
	}
}

// ---- AC-1: POST /v1/exceptions creates with required fields ----

func TestCreate_HappyPath(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate(ctrl, "key_requester"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != exception.StateRequested {
		t.Fatalf("expected status=requested, got %q", created.Status)
	}
	if created.ID == uuid.Nil {
		t.Fatal("expected non-nil id")
	}
	if len(created.CompensatingControls) != 2 {
		t.Fatalf("expected 2 compensating_controls, got %d", len(created.CompensatingControls))
	}
	// AC-7: audit log row written for the request.
	entries, err := store.ListAuditLog(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != exception.ActionRequested {
		t.Fatalf("expected 1 audit row action=requested, got %+v", entries)
	}
}

// ---- AC-1 validation: required fields ----

func TestCreate_RejectsMissingControl(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(uuid.Nil, "key_requester")
	_, err := store.Create(ctx, in)
	if !errors.Is(err, exception.ErrControlRequired) {
		t.Fatalf("expected ErrControlRequired, got %v", err)
	}
}

func TestCreate_RejectsMissingJustification(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(ctrl, "key_requester")
	in.Justification = ""
	_, err := store.Create(ctx, in)
	if !errors.Is(err, exception.ErrJustificationRequired) {
		t.Fatalf("expected ErrJustificationRequired, got %v", err)
	}
}

func TestCreate_RejectsMissingExpiresAt(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(ctrl, "key_requester")
	in.ExpiresAt = time.Time{}
	_, err := store.Create(ctx, in)
	if !errors.Is(err, exception.ErrExpiresAtRequired) {
		t.Fatalf("expected ErrExpiresAtRequired, got %v", err)
	}
}

// ---- AC-2 / anti-criterion P0: 365-day cap ----

func TestCreate_RejectsExpiresAtOver365Days(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(ctrl, "key_requester")
	// 366 days from now -- one day over the cap.
	in.ExpiresAt = time.Now().UTC().Add(366 * 24 * time.Hour)
	_, err := store.Create(ctx, in)
	if !errors.Is(err, exception.ErrExpiresAtExceedsCap) {
		t.Fatalf("expected ErrExpiresAtExceedsCap, got %v", err)
	}
}

func TestCreate_Accepts365DayBoundary(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	// Use Store-provided `Now` so the cap computation is deterministic.
	// expires_at = now + 365d exactly -- on the boundary, should pass.
	now := time.Now().UTC()
	in := validCreate(ctrl, "key_requester")
	in.Now = now
	// Step back 1ms to keep the DB CHECK (which compares against the
	// DB-side requested_at default of now() at insert time) comfortable
	// against clock drift.
	in.ExpiresAt = now.Add(365*24*time.Hour - time.Second)
	if _, err := store.Create(ctx, in); err != nil {
		t.Fatalf("Create on boundary: %v", err)
	}
}

// ---- AC-2: expires_at in past rejected ----

func TestCreate_RejectsExpiresAtInPast(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(ctrl, "key_requester")
	in.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	_, err := store.Create(ctx, in)
	if !errors.Is(err, exception.ErrExpiresAtInPast) {
		t.Fatalf("expected ErrExpiresAtInPast, got %v", err)
	}
}

// ---- AC-3: requested -> approved transition ----

func TestApprove_Transitions(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate(ctrl, "key_requester"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := store.Approve(ctx, created.ID, "key_approver")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != exception.StateApproved {
		t.Fatalf("expected status=approved, got %q", approved.Status)
	}
	if approved.ApprovedBy == nil || *approved.ApprovedBy != "key_approver" {
		t.Fatalf("expected approved_by=key_approver, got %v", approved.ApprovedBy)
	}
	// AC-7: audit log row for approved.
	entries, _ := store.ListAuditLog(ctx, created.ID)
	if len(entries) != 2 || entries[1].Action != exception.ActionApproved {
		t.Fatalf("expected request+approved audit rows, got %+v", entries)
	}
}

// ---- AC-3 / SoD (ISC-43): same credential cannot file + approve ----

func TestApprove_RejectsSelfApproval(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate(ctrl, "key_same"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = store.Approve(ctx, created.ID, "key_same")
	if !errors.Is(err, exception.ErrSegregationOfDuties) {
		t.Fatalf("expected ErrSegregationOfDuties, got %v", err)
	}
}

func TestDeny_RejectsSelfDenial(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate(ctrl, "key_same"))
	_, err := store.Deny(ctx, created.ID, "key_same", "trying to deny myself")
	if !errors.Is(err, exception.ErrSegregationOfDuties) {
		t.Fatalf("expected ErrSegregationOfDuties, got %v", err)
	}
}

// ---- AC-3: requested -> denied transition ----

func TestDeny_Transitions(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	denied, err := store.Deny(ctx, created.ID, "key_approver", "no executive backing")
	if err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if denied.Status != exception.StateDenied {
		t.Fatalf("expected status=denied, got %q", denied.Status)
	}
	entries, _ := store.ListAuditLog(ctx, created.ID)
	if len(entries) != 2 || entries[1].Action != exception.ActionDenied {
		t.Fatalf("expected request+denied audit rows, got %+v", entries)
	}
}

// ---- AC-4 enable: approved -> active transition ----

func TestActivate_Transitions(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	if _, err := store.Approve(ctx, created.ID, "key_approver"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	active, err := store.Activate(ctx, created.ID, "key_approver", time.Time{})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if active.Status != exception.StateActive {
		t.Fatalf("expected status=active, got %q", active.Status)
	}
	if active.EffectiveFrom == nil {
		t.Fatal("expected effective_from to be set")
	}
}

// ---- Wrong-state transitions return ErrWrongState ----

func TestApprove_RejectsAlreadyDenied(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	_, _ = store.Deny(ctx, created.ID, "key_approver", "")
	_, err := store.Approve(ctx, created.ID, "key_approver")
	if !errors.Is(err, exception.ErrWrongState) {
		t.Fatalf("expected ErrWrongState, got %v", err)
	}
}

func TestActivate_RejectsNonApprovedState(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	// Try to activate without approving first.
	_, err := store.Activate(ctx, created.ID, "key_approver", time.Time{})
	if !errors.Is(err, exception.ErrWrongState) {
		t.Fatalf("expected ErrWrongState, got %v", err)
	}
}

// ---- AC-4 read accessor: Active(controlID) returns active rows only ----

func TestActive_ReturnsActiveRowsOnly(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	// One active, one requested-only (should not appear in Active set).
	a, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	_, _ = store.Approve(ctx, a.ID, "key_approver")
	_, _ = store.Activate(ctx, a.ID, "key_approver", time.Time{})
	_, _ = store.Create(ctx, validCreate(ctrl, "key_requester2"))

	got, err := store.Active(ctx, ctrl)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 active row, got %d", len(got))
	}
	if got[0].ID != a.ID {
		t.Fatalf("expected id=%s, got %s", a.ID, got[0].ID)
	}
}

// ---- AC-5: auto-expiry marks expires_at < now active rows expired ----

func TestExpirer_ExpiresActiveRows(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	// Seed an active exception, then push expires_at into the past via
	// admin pool (the API rejects past-dated expires_at on Create; admin
	// pool bypasses RLS to set it directly for test purposes).
	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	_, _ = store.Approve(ctx, created.ID, "key_approver")
	_, _ = store.Activate(ctx, created.ID, "key_approver", time.Time{})
	if _, err := admin.Exec(context.Background(),
		`UPDATE exceptions SET expires_at = now() - interval '1 hour' WHERE id = $1`,
		created.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	expirer := exception.NewExpirer(admin, nil)
	n, err := expirer.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least 1 row expired, got %d", n)
	}
	post, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get post-sweep: %v", err)
	}
	if post.Status != exception.StateExpired {
		t.Fatalf("expected status=expired, got %q", post.Status)
	}
	if post.ExpiredAt == nil {
		t.Fatal("expected expired_at to be set")
	}
	// Anti-criterion P0: audit log row for expired with actor=SystemActor.
	entries, _ := store.ListAuditLog(ctx, created.ID)
	if len(entries) < 4 {
		t.Fatalf("expected request+approve+activate+expired audit rows, got %d entries", len(entries))
	}
	last := entries[len(entries)-1]
	if last.Action != exception.ActionExpired || last.Actor != exception.SystemActor {
		t.Fatalf("expected last entry action=expired actor=%s, got action=%s actor=%s",
			exception.SystemActor, last.Action, last.Actor)
	}
}

// ---- AC-5 idempotence: second sweep is a no-op ----

func TestExpirer_IsIdempotent(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	_, _ = store.Approve(ctx, created.ID, "key_approver")
	_, _ = store.Activate(ctx, created.ID, "key_approver", time.Time{})
	_, _ = admin.Exec(context.Background(),
		`UPDATE exceptions SET expires_at = now() - interval '1 hour' WHERE id = $1`, created.ID)

	expirer := exception.NewExpirer(admin, nil)
	first, err := expirer.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("first SweepOnce: %v", err)
	}
	second, err := expirer.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("second SweepOnce: %v", err)
	}
	if first < 1 || second != 0 {
		t.Fatalf("expected first>=1 and second=0, got first=%d second=%d", first, second)
	}
}

// ---- AC-6: GET /v1/exceptions/expiring ----

func TestExpiring_ReturnsRowsInsideWindow(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	// One expiring inside 30d, one expiring after.
	in1 := validCreate(ctrl, "key_requester")
	in1.ExpiresAt = time.Now().UTC().Add(15 * 24 * time.Hour)
	e1, _ := store.Create(ctx, in1)
	_, _ = store.Approve(ctx, e1.ID, "key_approver")
	_, _ = store.Activate(ctx, e1.ID, "key_approver", time.Time{})

	in2 := validCreate(ctrl, "key_requester2")
	in2.ExpiresAt = time.Now().UTC().Add(60 * 24 * time.Hour)
	e2, _ := store.Create(ctx, in2)
	_, _ = store.Approve(ctx, e2.ID, "key_approver")
	_, _ = store.Activate(ctx, e2.ID, "key_approver", time.Time{})

	rows, err := store.Expiring(ctx, time.Now().UTC(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Expiring: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != e1.ID {
		t.Fatalf("expected 1 row id=%s, got %+v", e1.ID, rows)
	}
}

// ---- AC-7: every state transition writes one audit row ----

func TestAuditLog_CapturesFullLifecycle(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	_, _ = store.Approve(ctx, created.ID, "key_approver")
	_, _ = store.Activate(ctx, created.ID, "key_approver", time.Time{})

	entries, err := store.ListAuditLog(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	want := []string{exception.ActionRequested, exception.ActionApproved, exception.ActionActivated}
	if len(entries) != len(want) {
		t.Fatalf("expected %d audit rows, got %d", len(want), len(entries))
	}
	for i, w := range want {
		if entries[i].Action != w {
			t.Fatalf("entry[%d].Action = %s, want %s", i, entries[i].Action, w)
		}
	}
}

// ---- Invariant 6: cross-tenant SELECT under RLS returns 0 rows ----

func TestRLS_CrossTenantInvisible(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctrlA := seedControl(t, admin, tenantA)
	store := exception.NewStore(app)

	ctxA := ctxFor(t, tenantA)
	ctxB := ctxFor(t, tenantB)

	created, err := store.Create(ctxA, validCreate(ctrlA, "key_requester"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Tenant B must not see tenant A's exception.
	_, err = store.Get(ctxB, created.ID)
	if !errors.Is(err, exception.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant Get, got %v", err)
	}
	listB, err := store.List(ctxB, exception.ListFilter{})
	if err != nil {
		t.Fatalf("List(tenantB): %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("tenant B saw %d rows for tenant A's exception; RLS bypassed", len(listB))
	}
}

// ---- D3 composite FK: cross-tenant control_id rejected ----

func TestCreate_RejectsCrossTenantControl(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctrlB := seedControl(t, admin, tenantB)
	store := exception.NewStore(app)
	ctxA := ctxFor(t, tenantA)

	in := validCreate(ctrlB, "key_requester") // tenant A trying to reference tenant B's control
	_, err := store.Create(ctxA, in)
	if err == nil {
		t.Fatal("expected FK error for cross-tenant control reference")
	}
	// We unwrap and accept any pgconn FK violation OR the friendly app
	// error string.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code != "23503" {
			t.Fatalf("expected SQLSTATE 23503, got %s", pgErr.Code)
		}
		return
	}
	// Fallback: error string mentions control_id existence
	if !strings.Contains(err.Error(), "control_id") {
		t.Fatalf("expected FK or control_id error, got %v", err)
	}
}

// ---- audit_log append-only: no UPDATE/DELETE policy ----

func TestAuditLog_IsAppendOnly(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)
	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))

	// Try DELETE via app pool with tenant GUC applied -- RLS denies (no
	// tenant_delete policy).
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM exception_audit_log WHERE tenant_id = $1 AND exception_id = $2`, tenant, created.ID)
	if err != nil {
		// Some pg versions raise an error instead of silently 0-rows;
		// either outcome means append-only is enforced.
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Logf("DELETE returned non-pgErr: %v", err)
		}
		return
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("DELETE on audit log affected %d rows; expected 0 (append-only)", tag.RowsAffected())
	}
}
