//go:build integration

// #2609: creating and deleting a group-role mapping each write a
// me_audit_log row in the same transaction: the actor and group -> role.
package grouprole_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
)

type mappingAuditRow struct {
	Action    string
	UserID    uuid.UUID
	Principal *string
	Before    map[string]any
	After     map[string]any
}

// mappingAuditRows reads tenant's mapping audit rows, oldest first, as the
// app role under the tenant's RLS context.
func mappingAuditRows(t *testing.T, tenant uuid.UUID) []mappingAuditRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, tenant.String()); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `
		SELECT action, user_id, actor_principal, before, after
		FROM me_audit_log
		WHERE action IN ($1, $2)
		ORDER BY occurred_at, id`, grouprole.AuditActionMappingCreate, grouprole.AuditActionMappingDelete)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []mappingAuditRow
	for rows.Next() {
		var r mappingAuditRow
		var before, after []byte
		if err := rows.Scan(&r.Action, &r.UserID, &r.Principal, &before, &after); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(before, &r.Before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(after, &r.After); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMappingAudit_CreateAndDeleteWriteRows(t *testing.T) {
	tenant := seedTenant(t, "grouprole-audit")
	ctx := tenantCtx(t, tenant)
	store := grouprole.NewStore(appPool)
	user := uuid.New()
	by := grouprole.Actor{UserID: user, Principal: user.String()}

	m, err := store.Create(ctx, grouprole.CreateMappingInput{GroupRef: "SecTeam", Role: "auditor"}, by)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Delete(ctx, m.ID, by); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Deleting an unknown mapping writes no row.
	if err := store.Delete(ctx, m.ID, by); err != grouprole.ErrMappingNotFound {
		t.Fatalf("second delete err = %v, want ErrMappingNotFound", err)
	}

	rows := mappingAuditRows(t, tenant)
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2 (create, delete)", len(rows))
	}
	create, del := rows[0], rows[1]
	if create.Action != grouprole.AuditActionMappingCreate || del.Action != grouprole.AuditActionMappingDelete {
		t.Fatalf("actions = %s, %s", create.Action, del.Action)
	}
	for _, row := range rows {
		if row.UserID != user {
			t.Errorf("%s user_id = %s, want %s", row.Action, row.UserID, user)
		}
		if row.Principal == nil || *row.Principal != user.String() {
			t.Errorf("%s actor_principal = %v, want %s", row.Action, row.Principal, user)
		}
	}
	want := map[string]any{
		"mapping_id":    m.ID.String(),
		"group_ref":     "SecTeam",
		"role":          "auditor",
		"idp_config_id": nil,
	}
	if len(create.Before) != 0 {
		t.Errorf("create before = %v, want {}", create.Before)
	}
	assertMappingFields(t, "create after", create.After, want)
	assertMappingFields(t, "delete before", del.Before, want)
	if len(del.After) != 0 {
		t.Errorf("delete after = %v, want {}", del.After)
	}
}

// A failed audit write rolls the create and the delete back. The write is
// made to fail for real: actor_principal longer than its CHECK allows.
func TestMappingAudit_FailedAuditRollsBack(t *testing.T) {
	tenant := seedTenant(t, "grouprole-audit-rollback")
	ctx := tenantCtx(t, tenant)
	store := grouprole.NewStore(appPool)

	seed, err := store.Create(ctx, grouprole.CreateMappingInput{GroupRef: "Kept", Role: "auditor"}, grouprole.Actor{Principal: "seed"})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}
	bad := grouprole.Actor{Principal: strings.Repeat("x", 300)}
	if _, err := store.Create(ctx, grouprole.CreateMappingInput{GroupRef: "Dropped", Role: "admin"}, bad); err == nil {
		t.Error("create succeeded with a failing audit write")
	}
	if err := store.Delete(ctx, seed.ID, bad); err == nil {
		t.Error("delete succeeded with a failing audit write")
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != seed.ID {
		t.Errorf("mappings = %+v; want only %s", got, seed.ID)
	}
	if n := len(mappingAuditRows(t, tenant)); n != 1 {
		t.Errorf("audit rows = %d, want 1 (the seed create only)", n)
	}
}

// Another tenant cannot read the audit rows.
func TestMappingAudit_TenantIsolated(t *testing.T) {
	tenantA := seedTenant(t, "grouprole-audit-a")
	tenantB := seedTenant(t, "grouprole-audit-b")
	store := grouprole.NewStore(appPool)
	if _, err := store.Create(tenantCtx(t, tenantA), grouprole.CreateMappingInput{GroupRef: "Admins", Role: "admin"}, grouprole.Actor{Principal: "bootstrap-admin"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if n := len(mappingAuditRows(t, tenantA)); n != 1 {
		t.Fatalf("tenant A audit rows = %d, want 1", n)
	}
	if n := len(mappingAuditRows(t, tenantB)); n != 0 {
		t.Errorf("tenant B sees %d of tenant A's mapping audit rows", n)
	}
}

func assertMappingFields(t *testing.T, label string, got, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s.%s = %v, want %v", label, k, got[k], v)
		}
	}
}
