//go:build integration

// Integration tests for slice 022: Policy library + version chain.
// Covers the AC machinery against real Postgres. RLS cannot be tested
// against a fake DB (memory rule: "Never mock the DB").
//
// Run with:  go test -tags=integration -race ./internal/policy/...
//
// PDF render is tested in a separate file (pdf_integration_test.go) so
// the chromedp dependency stays optional. This file does not need Chrome.

package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ---- harness helpers ----

// freshTenant CARVE-OUT (742 drain batch 15): cleanup is NOT a flat
// tenant-scoped DELETE — the first statement is column-scoped
// (`predecessor_id IS NOT NULL`) to drop self-FK successors before their
// predecessors, an ordering dbtest.SeedTenant cannot express (it only emits
// `WHERE tenant_id = $1` per table). So it stays inline; only its pool is
// re-routed to the slice-435 dbtest harness by the callers
// (admin := dbtest.NewMigratePool(t)). (batch-13 self-FK precedent.)
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		// Delete in dependency order: successors first (predecessor_id FK
		// may target them), then any rows.
		for _, stmt := range []string{
			`DELETE FROM policies WHERE tenant_id = $1 AND predecessor_id IS NOT NULL`,
			`DELETE FROM policies WHERE tenant_id = $1`,
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

// seedControl creates a control row directly via the admin pool (BYPASSRLS)
// so policies in tests can have linked controls. Mirrors the slice-021
// pattern.
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

func validCreate(linked []uuid.UUID, creator string) policy.CreateInput {
	return policy.CreateInput{
		Title:                       "Test Policy",
		Version:                     "1.0.0",
		BodyMd:                      "# Test\n\nBody.",
		OwnerRole:                   "tenant_admin",
		ApproverRole:                "security_lead",
		LinkedControlIDs:            linked,
		AcknowledgmentRequiredRoles: []string{"employee"},
		SourceAttribution:           policy.SourceTenantAuthored,
		CreatedBy:                   creator,
	}
}

// ---- AC-1 part A: POST /v1/policies creates a draft ----

func TestCreate_HappyPath(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate([]uuid.UUID{ctrl}, "key_author"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != policy.StateDraft {
		t.Fatalf("expected status=draft, got %q", created.Status)
	}
	if created.ID == uuid.Nil {
		t.Fatal("expected non-nil id")
	}
	if len(created.LinkedControlIDs) != 1 {
		t.Fatalf("expected 1 linked control, got %d", len(created.LinkedControlIDs))
	}
	if created.IsOrphan() {
		t.Fatal("expected IsOrphan=false for policy with linked control")
	}
}

// ---- AC-7: orphan_policy warning on read ----

func TestCreate_OrphanWarning(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(nil, "key_author")
	created, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !created.IsOrphan() {
		t.Fatal("expected IsOrphan=true for policy with no linked controls")
	}
}

// ---- AC-1: state machine transitions draft -> under_review -> approved ----

func TestStateMachine_Transitions(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate([]uuid.UUID{ctrl}, "key_author"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	submitted, err := store.SubmitForReview(ctx, created.ID, "key_author")
	if err != nil {
		t.Fatalf("SubmitForReview: %v", err)
	}
	if submitted.Status != policy.StateUnderReview {
		t.Fatalf("expected under_review, got %q", submitted.Status)
	}
	approved, err := store.Approve(ctx, created.ID, "key_approver")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != policy.StateApproved {
		t.Fatalf("expected approved, got %q", approved.Status)
	}
}

// ---- AC-1 part B: Publish creates a versioned row ----

func TestPublish_FirstVersion(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate([]uuid.UUID{ctrl}, "key_author"))
	if _, err := store.SubmitForReview(ctx, created.ID, "key_author"); err != nil {
		t.Fatalf("SubmitForReview: %v", err)
	}
	if _, err := store.Approve(ctx, created.ID, "key_approver"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	published, err := store.Publish(ctx, created.ID, policy.PublishInput{
		NewVersion:    "1.0.0",
		EffectiveDate: &now,
		PublishedBy:   "key_approver",
	})
	if err != nil {
		t.Fatalf("Publish first version: %v", err)
	}
	if published.Status != policy.StatePublished {
		t.Fatalf("expected published, got %q", published.Status)
	}
	if published.PredecessorID != nil {
		t.Fatalf("expected first publish to have predecessor_id=nil, got %v", published.PredecessorID)
	}
	if published.EffectiveDate == nil {
		t.Fatal("expected effective_date to be set on publish")
	}
}

// ---- AC-7 + anti-criterion P0: orphan publish is blocked ----

func TestPublish_OrphanRejected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	// Create orphan -> submit -> approve.
	created, _ := store.Create(ctx, validCreate(nil, "key_author"))
	if _, err := store.SubmitForReview(ctx, created.ID, "key_author"); err != nil {
		t.Fatalf("SubmitForReview: %v", err)
	}
	if _, err := store.Approve(ctx, created.ID, "key_approver"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	_, err := store.Publish(ctx, created.ID, policy.PublishInput{
		NewVersion:    "1.0.0",
		EffectiveDate: &now,
		PublishedBy:   "key_approver",
	})
	if !errors.Is(err, policy.ErrOrphanPublish) {
		t.Fatalf("expected ErrOrphanPublish, got %v", err)
	}
}

// ---- transitions reject wrong prior state ----

func TestApprove_RejectsFromDraft(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate([]uuid.UUID{ctrl}, "key_author"))
	// Skip SubmitForReview; attempt to approve directly from draft.
	_, err := store.Approve(ctx, created.ID, "key_approver")
	if !errors.Is(err, policy.ErrWrongState) {
		t.Fatalf("expected ErrWrongState, got %v", err)
	}
}

// ---- AC-1: version chain stays within tenant ----

func TestVersionChain_TenantBoundary(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctrlA := seedControl(t, admin, tenantA)
	store := policy.NewStore(app)
	ctxA := ctxFor(t, tenantA)
	ctxB := ctxFor(t, tenantB)

	created, _ := store.Create(ctxA, validCreate([]uuid.UUID{ctrlA}, "key_author"))
	// Tenant B cannot see Tenant A's policy.
	_, err := store.Get(ctxB, created.ID)
	if !errors.Is(err, policy.ErrNotFound) {
		t.Fatalf("expected ErrNotFound across tenants, got %v", err)
	}
	// Tenant A can.
	got, err := store.Get(ctxA, created.ID)
	if err != nil {
		t.Fatalf("Get within tenant: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("expected id %s, got %s", created.ID, got.ID)
	}
}

// ---- get returns ErrNotFound for unknown id ----

func TestGet_NotFound(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	_, err := store.Get(ctx, uuid.New())
	if !errors.Is(err, policy.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// ---- create validation: required fields ----

func TestCreate_ValidationErrors(t *testing.T) {
	app := dbtest.NewAppPool(t)
	store := policy.NewStore(app)
	ctx := ctxFor(t, uuid.NewString())

	cases := []struct {
		name string
		in   policy.CreateInput
		want error
	}{
		{"no title", policy.CreateInput{Version: "1.0.0", BodyMd: "b", OwnerRole: "o", ApproverRole: "a", CreatedBy: "c"}, policy.ErrTitleRequired},
		{"no version", policy.CreateInput{Title: "t", BodyMd: "b", OwnerRole: "o", ApproverRole: "a", CreatedBy: "c"}, policy.ErrVersionRequired},
		{"no body", policy.CreateInput{Title: "t", Version: "1.0.0", OwnerRole: "o", ApproverRole: "a", CreatedBy: "c"}, policy.ErrBodyRequired},
		{"no owner_role", policy.CreateInput{Title: "t", Version: "1.0.0", BodyMd: "b", ApproverRole: "a", CreatedBy: "c"}, policy.ErrOwnerRoleRequired},
		{"no approver_role", policy.CreateInput{Title: "t", Version: "1.0.0", BodyMd: "b", OwnerRole: "o", CreatedBy: "c"}, policy.ErrApproverRoleRequired},
		{"no created_by", policy.CreateInput{Title: "t", Version: "1.0.0", BodyMd: "b", OwnerRole: "o", ApproverRole: "a"}, policy.ErrCreatedByRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Create(ctx, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}
