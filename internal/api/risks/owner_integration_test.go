//go:build integration

// #2129 — a risk's owner is a user. POST and PATCH /v1/risks set
// owner_user_id; GET returns it, and the free text no user matched as
// owner_unlinked. An owner that is not an active user of the risk's own
// tenant is a 422, and every owner change is a fields_changed audit row.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run Owner

package risks_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// seedOwnerUser inserts a user of tenant with the given status.
func seedOwnerUser(t *testing.T, admin *pgxpool.Pool, tenant, name, status string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, $4, $5)`,
		id, tenant, id+"@owner-2129.test", name, status); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func freshOwnerTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, "risk_audit_log", "risks", "org_units", "users")
}

func TestOwner_RiskOwnerUserSetAndReturned(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshOwnerTenant(t, admin)
	env := testServer(t, app, tenant)
	alice := seedOwnerUser(t, admin, tenant, "Alice Owner", "active")
	bob := seedOwnerUser(t, admin, tenant, "Bob Owner", "active")

	// Create with an owner: returned, and no unlinked text.
	resp, body := createPlaced(t, env, map[string]any{"owner_user_id": alice, "treatment_owner": "alice"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %v", resp.StatusCode, body)
	}
	id := riskField(t, body, "id").(string)
	if got := riskField(t, body, "owner_user_id"); got != alice {
		t.Fatalf("create returned owner_user_id %v, want %s", got, alice)
	}
	if got := riskField(t, body, "owner_unlinked"); got != "" {
		t.Fatalf("create returned owner_unlinked %q, want empty", got)
	}

	// PATCH to another owner: returned, read back, audit-logged.
	resp, body = patch(t, env, "/v1/risks/"+id, `{"owner_user_id":"`+bob+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d: %v", resp.StatusCode, body)
	}
	if got := riskField(t, body, "owner_user_id"); got != bob {
		t.Fatalf("patch returned owner_user_id %v, want %s", got, bob)
	}
	_, got := get(t, env, "/v1/risks/"+id)
	if v := riskField(t, got, "owner_user_id"); v != bob {
		t.Fatalf("GET reads back owner_user_id %v, want %s", v, bob)
	}
	entries := auditEntries(t, env, id)
	if len(entries) == 0 {
		t.Fatal("owner change wrote no audit row")
	}
	last := entries[0]
	before, _ := last["before"].(map[string]any)
	after, _ := last["after"].(map[string]any)
	if last["action"] != "fields_changed" || before["owner_user_id"] != alice || after["owner_user_id"] != bob {
		t.Fatalf("audit row = %v", last)
	}

	// The same owner again changes nothing and writes no row.
	n := len(entries)
	if resp, body := patch(t, env, "/v1/risks/"+id, `{"owner_user_id":"`+bob+`"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("same-owner patch status %d: %v", resp.StatusCode, body)
	}
	if got := len(auditEntries(t, env, id)); got != n {
		t.Fatalf("same-owner patch wrote %d audit rows", got-n)
	}

	// null clears it.
	resp, body = patch(t, env, "/v1/risks/"+id, `{"owner_user_id":null}`)
	if resp.StatusCode != http.StatusOK || riskField(t, body, "owner_user_id") != nil {
		t.Fatalf("clear: status %d, owner %v", resp.StatusCode, riskField(t, body, "owner_user_id"))
	}
}

func TestOwner_RiskUnlinkedTextIsReturnedUntilAUserIsPicked(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshOwnerTenant(t, admin)
	env := testServer(t, app, tenant)
	alice := seedOwnerUser(t, admin, tenant, "Alice Owner", "active")

	resp, body := createPlaced(t, env, map[string]any{"treatment_owner": "Someone in Finance"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %v", resp.StatusCode, body)
	}
	id := riskField(t, body, "id").(string)
	if riskField(t, body, "owner_user_id") != nil || riskField(t, body, "owner_unlinked") != "Someone in Finance" {
		t.Fatalf("unlinked risk: owner_user_id %v, owner_unlinked %v",
			riskField(t, body, "owner_user_id"), riskField(t, body, "owner_unlinked"))
	}

	resp, body = patch(t, env, "/v1/risks/"+id, `{"owner_user_id":"`+alice+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d: %v", resp.StatusCode, body)
	}
	if riskField(t, body, "owner_user_id") != alice || riskField(t, body, "owner_unlinked") != "" {
		t.Fatalf("after picking: owner_user_id %v, owner_unlinked %v",
			riskField(t, body, "owner_user_id"), riskField(t, body, "owner_unlinked"))
	}
	after, _ := auditEntries(t, env, id)[0]["after"].(map[string]any)
	if after["owner_user_id"] != alice || after["treatment_owner"] != "" {
		t.Fatalf("audit after_state = %v", after)
	}
}

func TestOwner_RiskOwnerMustBeAnActiveUserOfTheTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshOwnerTenant(t, admin)
	other := freshOwnerTenant(t, admin)
	env := testServer(t, app, tenant)
	disabled := seedOwnerUser(t, admin, tenant, "Dee Disabled", "disabled")
	foreign := seedOwnerUser(t, admin, other, "Fay Foreign", "active")
	id := createRecordRisk(t, env, nil)

	for name, owner := range map[string]string{
		"disabled user":       disabled,
		"another tenant user": foreign,
		"no such user":        uuid.NewString(),
	} {
		resp, body := createPlaced(t, env, map[string]any{"owner_user_id": owner})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("create with %s: status %d, want 422: %v", name, resp.StatusCode, body)
		}
		resp, body = patch(t, env, "/v1/risks/"+id, `{"owner_user_id":"`+owner+`"}`)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("patch with %s: status %d, want 422: %v", name, resp.StatusCode, body)
		}
	}
	if resp, _ := patch(t, env, "/v1/risks/"+id, `{"owner_user_id":"not-a-uuid"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-UUID owner: status %d, want 400", resp.StatusCode)
	}
	_, got := get(t, env, "/v1/risks/"+id)
	if v := riskField(t, got, "owner_user_id"); v != nil {
		t.Fatalf("a refused owner was stored: %v", v)
	}
}
