//go:build integration

// #2175: a create_risk proposal stays pending while Risk is switched off.
// Confirming it is refused and leaves the row untouched; once Risk is
// switched back on the same proposal applies.

package writeproposals_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

func TestConfirm_CreateRiskWaitsForRiskSwitch(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin,
		"mcp_write_proposals", "risks", "feature_flags")
	ctx := ctxFor(t, tenant)
	flags := featureflag.NewStore(app)
	store := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(app), nil).
		WithSwitches(moduleaccess.NewViews(moduleaccess.NewStore(app), flags))

	created, err := store.Create(ctx, writeproposals.CreateInput{
		ToolName:       writeproposals.ToolCreateRisk,
		ToolInput:      json.RawMessage(`{"title":"Waits for Risk","category":"operational"}`),
		AIModelName:    "llama3.1:8b-instruct-q5",
		AIModelVersion: "2026-05-01",
		CreatedBy:      "key_" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	risks := func() int {
		t.Helper()
		var n int
		if err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM risks WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
			t.Fatalf("count risks: %v", err)
		}
		return n
	}

	if _, err := flags.Set(ctx, "risk.enabled", false, "test", "#2175"); err != nil {
		t.Fatalf("switch Risk off: %v", err)
	}
	if _, err := store.Confirm(ctx, created.ID, "key_approver"); !errors.Is(err, writeproposals.ErrModuleOff) {
		t.Fatalf("Confirm with Risk off: err = %v; want ErrModuleOff", err)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != writeproposals.StateAIProposed || got.HumanApproved || got.HumanApprover != nil {
		t.Fatalf("Risk off: proposal is %s (approved=%v); want ai_proposed, unapproved", got.State, got.HumanApproved)
	}
	if n := risks(); n != 0 {
		t.Fatalf("Risk off: %d risks written; want 0", n)
	}

	if _, err := flags.Set(ctx, "risk.enabled", true, "test", "#2175"); err != nil {
		t.Fatalf("switch Risk on: %v", err)
	}
	applied, err := store.Confirm(ctx, created.ID, "key_approver")
	if err != nil {
		t.Fatalf("Confirm with Risk on: %v", err)
	}
	if applied.State != writeproposals.StateApplied || applied.AppliedSubject == nil {
		t.Fatalf("Risk on: proposal is %s; want applied with a subject", applied.State)
	}
	if n := risks(); n != 1 {
		t.Fatalf("Risk on: %d risks written; want 1", n)
	}
}
