//go:build integration

// #2450: create_risk and update_risk_treatment take owner_user_id, checked
// as REST checks it (risk.CheckOwnerUser): an active user of the risk's own
// tenant. A disabled user, a user of another tenant and an unknown id are
// refused with ErrInvalidInput and change nothing.
//
// Run with:  go test -tags=integration ./internal/mcp/writeproposals/ -run Owner

package writeproposals_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
)

func seedOwnerUser(t *testing.T, admin *pgxpool.Pool, tenant, status string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, 'Owner 2450', $4)`,
		id, tenant, id+"@owner-2450.test", status); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// readOwner returns a risk's owner_user_id ("" when NULL) and free text.
func readOwner(t *testing.T, admin *pgxpool.Pool, riskID string) (string, string) {
	t.Helper()
	var owner *uuid.UUID
	var text string
	if err := admin.QueryRow(context.Background(),
		`SELECT owner_user_id, treatment_owner FROM risks WHERE id = $1`, riskID,
	).Scan(&owner, &text); err != nil {
		t.Fatalf("read risk %s: %v", riskID, err)
	}
	if owner == nil {
		return "", text
	}
	return owner.String(), text
}

func TestApplyRiskOwner_OwnerUserIDIsAnActiveTenantUser(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := treatmentTenant(t, admin)
	other := treatmentTenant(t, admin)
	store := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(app), nil)
	ctx := ctxFor(t, tenant)

	alice := seedOwnerUser(t, admin, tenant, "active")
	bob := seedOwnerUser(t, admin, tenant, "active")
	refused := map[string]string{
		"disabled user":       seedOwnerUser(t, admin, tenant, "disabled"),
		"another tenant user": seedOwnerUser(t, admin, other, "active"),
		"no such user":        uuid.NewString(),
	}

	// create_risk stores the owner, and a picked owner replaces the text.
	applied, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolCreateRisk,
		`{"title":"Owned risk","category":"operational","treatment_owner":"risk team","owner_user_id":"`+alice+`"}`)
	if err != nil {
		t.Fatalf("confirm create_risk with an owner: %v", err)
	}
	riskID := *applied.AppliedSubject
	if owner, text := readOwner(t, admin, riskID); owner != alice || text != "" {
		t.Fatalf("created owner = %q, text %q; want %s and no text", owner, text, alice)
	}

	countRisks := func() int {
		t.Helper()
		var n int
		if err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM risks WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
			t.Fatalf("count risks: %v", err)
		}
		return n
	}
	risks := countRisks()

	for name, owner := range refused {
		_, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolCreateRisk,
			`{"title":"Refused `+name+`","category":"operational","owner_user_id":"`+owner+`"}`)
		if !errors.Is(err, writeproposals.ErrInvalidInput) {
			t.Errorf("create_risk with %s: got %v, want ErrInvalidInput", name, err)
		}
		_, err = proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
			`{"risk_id":"`+riskID+`","treatment":"avoid","owner_user_id":"`+owner+`"}`)
		if !errors.Is(err, writeproposals.ErrInvalidInput) {
			t.Errorf("update_risk_treatment with %s: got %v, want ErrInvalidInput", name, err)
		}
	}
	if n := countRisks(); n != risks {
		t.Errorf("refused owners created %d risks", n-risks)
	}
	if owner, _ := readOwner(t, admin, riskID); owner != alice {
		t.Errorf("refused owners changed the owner to %q", owner)
	}

	// A malformed id is refused when the proposal is filed.
	if _, err := store.Create(ctx, writeproposals.CreateInput{
		ToolName:       writeproposals.ToolCreateRisk,
		ToolInput:      []byte(`{"title":"Bad id","category":"operational","owner_user_id":"not-a-uuid"}`),
		AIModelName:    "llama3.1:8b-instruct-q5",
		AIModelVersion: "2026-05-01",
		CreatedBy:      "key_" + uuid.NewString(),
	}); !errors.Is(err, writeproposals.ErrInvalidInput) {
		t.Errorf("filing a non-UUID owner: got %v, want ErrInvalidInput", err)
	}

	// update_risk_treatment reassigns the owner; owner_user_id wins over
	// treatment_owner.
	if _, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolUpdateRiskTreatment,
		`{"risk_id":"`+riskID+`","treatment":"avoid","treatment_owner":"ignored","owner_user_id":"`+bob+`"}`); err != nil {
		t.Fatalf("update_risk_treatment with an owner: %v", err)
	}
	if owner, text := readOwner(t, admin, riskID); owner != bob || text != "" {
		t.Fatalf("after reassign owner = %q, text %q; want %s and no text", owner, text, bob)
	}
	var logged int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM risk_audit_log
		 WHERE risk_id = $1 AND action_type = 'fields_changed'
		   AND after_state ->> 'owner_user_id' = $2`, riskID, bob).Scan(&logged); err != nil {
		t.Fatalf("read risk audit: %v", err)
	}
	if logged != 1 {
		t.Errorf("owner reassign wrote %d fields_changed rows, want 1", logged)
	}
}
