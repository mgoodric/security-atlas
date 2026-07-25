//go:build integration

// Integration tests for slice 173: MCP write tools + HITL approval.
// Covers the store layer + DB-level invariants against real Postgres.
// Memory rule: "Never mock the DB" — every test exercises the real
// migration, the real RLS policies, and the real CHECK constraints.
//
// Run with:  go test -tags=integration -race ./internal/mcp/writeproposals/...

package writeproposals_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// freshTenant returns a brand-new tenant id and registers cleanup of the rows
// this slice's tests create (a pure tenant-scoped DELETE returning a string),
// so it delegates to dbtest.SeedTenant (slice 435 / 742 drain batch 18).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, "mcp_write_proposals")
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func validCreate() writeproposals.CreateInput {
	return writeproposals.CreateInput{
		ToolName:       writeproposals.ToolCreateRisk,
		ToolInput:      json.RawMessage(`{"title":"Cross-region failover","category":"operational"}`),
		AIModelName:    "llama3.1:8b-instruct-q5",
		AIModelVersion: "2026-05-01",
		CreatedBy:      "key_" + uuid.NewString(),
	}
}

// ----- ISC-22 / ISC-50: Create files a proposal at state=ai_proposed -----

func TestCreate_HappyPath(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := writeproposals.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.State != writeproposals.StateAIProposed {
		t.Fatalf("state = %q, want %q", created.State, writeproposals.StateAIProposed)
	}
	if !created.AIAssisted {
		t.Fatal("ai_assisted must be true")
	}
	if created.HumanApproved {
		t.Fatal("human_approved must be false at creation time")
	}
	if created.HumanApprover != nil {
		t.Fatalf("human_approver must be nil at creation time, got %v", created.HumanApprover)
	}
}

// ----- ISC-23: Get returns one row tenant-scoped via RLS -----

func TestGet_RoundTrip(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := writeproposals.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("id mismatch: %v vs %v", got.ID, created.ID)
	}
}

// ----- ISC-24 + ISC-53: cross-tenant RLS blocks reads -----

func TestList_CrossTenantIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := writeproposals.NewStore(app)

	ctxA := ctxFor(t, tenantA)
	if _, err := store.Create(ctxA, validCreate()); err != nil {
		t.Fatalf("Create A: %v", err)
	}

	ctxB := ctxFor(t, tenantB)
	listB, err := store.List(ctxB, writeproposals.ListFilter{})
	if err != nil {
		t.Fatalf("List B: %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("tenant B saw %d proposals from tenant A — RLS broken", len(listB))
	}
}

// ----- ISC-25 / ISC-26 / ISC-51: Confirm flips state + runs Applier -----

func TestConfirm_FlipsStateAndRunsApplier(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)

	appliedSubject := "risk-" + uuid.NewString()
	applierCalls := 0
	store := writeproposals.NewStore(app).WithApplier(writeproposals.ToolCreateRisk,
		func(ctx context.Context, tx pgx.Tx, p writeproposals.Proposal) (string, error) {
			applierCalls++
			return appliedSubject, nil
		})
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	approver := "key_" + uuid.NewString()
	confirmed, err := store.Confirm(ctx, created.ID, approver)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.State != writeproposals.StateApplied {
		t.Fatalf("state = %q, want %q", confirmed.State, writeproposals.StateApplied)
	}
	if !confirmed.HumanApproved {
		t.Fatal("human_approved must be true after confirm")
	}
	if confirmed.HumanApprover == nil || *confirmed.HumanApprover != approver {
		t.Fatalf("human_approver mismatch: %v vs %v", confirmed.HumanApprover, approver)
	}
	if confirmed.AppliedSubject == nil || *confirmed.AppliedSubject != appliedSubject {
		t.Fatalf("applied_subject mismatch: %v vs %v", confirmed.AppliedSubject, appliedSubject)
	}
	if applierCalls != 1 {
		t.Fatalf("applier called %d times, want 1", applierCalls)
	}
}

// ----- ISC-28 + ISC-54: double-confirm rejected as wrong state -----

func TestConfirm_RejectsAlreadyApplied(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := writeproposals.NewStore(app).WithApplier(writeproposals.ToolCreateRisk,
		func(ctx context.Context, tx pgx.Tx, p writeproposals.Proposal) (string, error) {
			return "subject-1", nil
		})
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Confirm(ctx, created.ID, "key_a"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := store.Confirm(ctx, created.ID, "key_b"); !errors.Is(err, writeproposals.ErrWrongState) {
		t.Fatalf("expected ErrWrongState on double-confirm, got %v", err)
	}
}

// ----- ISC-27 + ISC-52: Reject is terminal -----

func TestReject_HappyPath(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := writeproposals.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rejected, err := store.Reject(ctx, created.ID, "Title too vague")
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if rejected.State != writeproposals.StateRejected {
		t.Fatalf("state = %q, want %q", rejected.State, writeproposals.StateRejected)
	}
	if rejected.RejectedAt == nil {
		t.Fatal("rejected_at must be set")
	}
	if rejected.RejectReason == nil || *rejected.RejectReason != "Title too vague" {
		t.Fatalf("reject_reason mismatch: %v", rejected.RejectReason)
	}
}

// ----- ISC-29: confirm of rejected proposal blocked -----

func TestConfirm_RejectsAlreadyRejected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := writeproposals.NewStore(app).WithApplier(writeproposals.ToolCreateRisk,
		func(ctx context.Context, tx pgx.Tx, p writeproposals.Proposal) (string, error) {
			return "subject-1", nil
		})
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Reject(ctx, created.ID, "no"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if _, err := store.Confirm(ctx, created.ID, "key_a"); !errors.Is(err, writeproposals.ErrWrongState) {
		t.Fatalf("expected ErrWrongState confirming rejected proposal, got %v", err)
	}
}

// ----- ISC-A4 + ISC-56: schema CHECK enforces AI-assist invariant at DB level -----

func TestSchemaInvariant_BlocksApprovedWithoutApprover(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := context.Background()

	// Bypass the store layer + RLS: insert via admin pool with the
	// constraint-violating shape. The DB CHECK MUST reject.
	_, err := admin.Exec(ctx, `
		INSERT INTO mcp_write_proposals (
			tenant_id, tool_name, tool_input,
			state, ai_assisted, ai_model_name, ai_model_version,
			human_approved, human_approver,
			applied_at, applied_subject,
			created_by
		) VALUES (
			$1, 'create_risk', '{}'::jsonb,
			'applied', TRUE, 'm', 'v',
			TRUE, NULL,
			now(), 'subj-1',
			'key_test'
		)
	`, tenant)
	if err == nil {
		t.Fatal("expected CHECK violation, got nil error — schema invariant broken")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("expected 23514 check_violation, got %v", err)
	}
	if pgErr.ConstraintName != "mcp_wp_ai_assist_invariant" {
		t.Fatalf("wrong constraint fired: %s", pgErr.ConstraintName)
	}
}

// ----- ISC-A5: pending-cap enforced at the store layer -----

func TestCreate_EnforcesPendingCap(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := writeproposals.NewStore(app).WithPendingCap(2)
	ctx := ctxFor(t, tenant)

	user := "key_" + uuid.NewString()
	for i := 0; i < 2; i++ {
		in := validCreate()
		in.CreatedBy = user
		if _, err := store.Create(ctx, in); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	in := validCreate()
	in.CreatedBy = user
	if _, err := store.Create(ctx, in); !errors.Is(err, writeproposals.ErrPendingCapExceeded) {
		t.Fatalf("expected ErrPendingCapExceeded, got %v", err)
	}
	// Confirming one slot should free it up so a fresh create lands.
	list, err := store.List(ctx, writeproposals.ListFilter{State: writeproposals.StateAIProposed})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	store.WithApplier(writeproposals.ToolCreateRisk,
		func(ctx context.Context, tx pgx.Tx, p writeproposals.Proposal) (string, error) {
			return "ok", nil
		})
	if _, err := store.Confirm(ctx, list[0].ID, "key_app"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	in2 := validCreate()
	in2.CreatedBy = user
	if _, err := store.Create(ctx, in2); err != nil {
		t.Fatalf("Create after Confirm freed slot: %v", err)
	}
}

// ----- ISC-21 + AllowedTools defense-in-depth -----

func TestCreate_RejectsUnknownTool(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := writeproposals.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate()
	in.ToolName = "delete_tenant"
	if _, err := store.Create(ctx, in); !errors.Is(err, writeproposals.ErrUnknownTool) {
		t.Fatalf("expected ErrUnknownTool, got %v", err)
	}
}

// ----- ISC-A1: applier-error rolls back; state stays ai_proposed -----

func TestConfirm_ApplierErrorRollsBack(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := writeproposals.NewStore(app).WithApplier(writeproposals.ToolCreateRisk,
		func(ctx context.Context, tx pgx.Tx, p writeproposals.Proposal) (string, error) {
			return "", errors.New("downstream rejected")
		})
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Confirm(ctx, created.ID, "key_app"); err == nil {
		t.Fatal("expected Confirm to surface applier error")
	} else if !strings.Contains(err.Error(), "downstream rejected") {
		t.Fatalf("error did not wrap applier error: %v", err)
	}
	// Re-read: state must be ai_proposed.
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != writeproposals.StateAIProposed {
		t.Fatalf("state = %q after applier failure, want ai_proposed", got.State)
	}
	if got.HumanApproved {
		t.Fatal("human_approved must remain false after applier failure")
	}
}
