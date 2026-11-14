//go:build integration

// #2101 integration tests: exception approval follows the organisation's
// Allow self-approval setting. Off (the default), the requester cannot
// approve and exceptions_sod refuses a same-person row; on, the requester
// may approve, the row is flagged self_approved and the audit row says so.
// A machine credential never approves, whatever the setting. Real Postgres
// under RLS.

package exception_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/exception"
)

// approverPerson is a person approving through a key bound to them.
var approverPerson = approval.Approver{UserID: "4b0e7c52-5d1a-4f0e-9a51-21010000a001", CredentialID: "key_approver"}

// setAllowSelfApproval gives the tenant a tenants row with the setting.
func setAllowSelfApproval(t *testing.T, admin *pgxpool.Pool, tenant string, allow bool) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO tenants (id, name, allow_self_approval) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET allow_self_approval = EXCLUDED.allow_self_approval`,
		tenant, "exceptions-2101-"+tenant[:8], allow); err != nil {
		t.Fatalf("set allow_self_approval: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenant)
	})
}

func TestApprove_SelfApproval_FollowsTheSetting(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)
	requester := uuid.NewString()
	self := approval.Approver{UserID: requester, CredentialID: "jwt:another-session"}

	// Off (the default): the requester, from another session, is refused.
	setAllowSelfApproval(t, admin, tenant, false)
	off, err := store.Create(ctx, validCreate(ctrl, requester))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Approve(ctx, off.ID, self); !errors.Is(err, exception.ErrSegregationOfDuties) {
		t.Fatalf("self-approval with the setting off: err = %v, want ErrSegregationOfDuties", err)
	}
	// A second person approves, unflagged.
	second, err := store.Approve(ctx, off.ID, approverPerson)
	if err != nil {
		t.Fatalf("second person: %v", err)
	}
	if second.SelfApproved {
		t.Fatal("a second person's approval is flagged self-approved")
	}

	// On: the requester approves; the row and the audit row are marked.
	setAllowSelfApproval(t, admin, tenant, true)
	on, err := store.Create(ctx, validCreate(ctrl, requester))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, err := store.Approve(ctx, on.ID, self)
	if err != nil {
		t.Fatalf("self-approval with the setting on: %v", err)
	}
	if approved.Status != exception.StateApproved || !approved.SelfApproved {
		t.Fatalf("status %q self_approved %v, want approved and flagged", approved.Status, approved.SelfApproved)
	}
	if approved.ApprovedBy == nil || *approved.ApprovedBy != requester {
		t.Fatalf("approved_by = %v, want the requester %s", approved.ApprovedBy, requester)
	}
	entries, err := store.ListAuditLog(ctx, on.ID)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	last := entries[len(entries)-1]
	if last.Action != exception.ActionApproved || last.Reason != exception.SelfApprovedReason || last.Actor != requester {
		t.Fatalf("approval audit row = %+v, want action approved, actor the requester, reason %q", last, exception.SelfApprovedReason)
	}

	// Even on, a machine credential never approves.
	machine, err := store.Create(ctx, validCreate(ctrl, requester))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Approve(ctx, machine.ID, approval.Approver{CredentialID: "key_machine"}); !errors.Is(err, exception.ErrApproverNotPerson) {
		t.Fatalf("machine approval with the setting on: err = %v, want ErrApproverNotPerson", err)
	}
}

// TestSoDCheck_RelaxedOnlyForFlaggedRows pins the database leg: with the
// flag the same-person row is admitted, without it exceptions_sod refuses,
// whatever the application does.
func TestSoDCheck_RelaxedOnlyForFlaggedRows(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)
	requester := uuid.NewString()

	unflagged, err := store.Create(ctx, validCreate(ctrl, requester))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = admin.Exec(context.Background(),
		`UPDATE exceptions SET status = 'approved', approved_by = requested_by, approved_at = now() WHERE id = $1`, unflagged.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "exceptions_sod" {
		t.Fatalf("unflagged same-person approval: err = %v, want exceptions_sod violation", err)
	}

	flagged, err := store.Create(ctx, validCreate(ctrl, requester))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := admin.Exec(context.Background(),
		`UPDATE exceptions SET status = 'approved', approved_by = requested_by, approved_at = now(), self_approved = true WHERE id = $1`, flagged.ID); err != nil {
		t.Fatalf("flagged same-person approval: %v", err)
	}
}
