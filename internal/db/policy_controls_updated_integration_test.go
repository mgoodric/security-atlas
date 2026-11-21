//go:build integration

// #2852 — 20261283000000_policy_controls_updated. policy_audit_log accepts
// 'controls_updated' with the link lists before and after. The down
// migration refuses while such a row exists, and once applied refuses
// 'controls_updated' again; the up migration re-applies.
//
// The test runs in one atlas_migrate transaction that is rolled back.

package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	policyControlsUpdatedUp   = "../../migrations/sql/20261283000000_policy_controls_updated.sql"
	policyControlsUpdatedDown = "../../migrations/sql/20261283000000_policy_controls_updated.down.sql"
)

func TestPolicyControlsUpdatedMigration(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(policyControlsUpdatedUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(policyControlsUpdatedDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenant := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, false)`,
		tenant, "controls-2852-"+tenant.String()[:8]); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	const insert = `INSERT INTO policy_audit_log
		(tenant_id, policy_id, action, actor, previous_linked_control_ids, new_linked_control_ids)
		VALUES ($1, $2, 'controls_updated', 'user-1', '{}', ARRAY[$3::uuid])`
	if _, err := tx.Exec(ctx, insert, tenant, uuid.New(), uuid.New()); err != nil {
		t.Fatalf("insert controls_updated row: %v", err)
	}

	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Errorf("down migration applied while a controls_updated row exists")
	}
	_ = sp.Rollback(ctx)

	for _, q := range []string{
		`ALTER TABLE policy_audit_log DISABLE TRIGGER policy_audit_log_no_update_trg`,
		`DELETE FROM policy_audit_log WHERE action = 'controls_updated'
			OR previous_linked_control_ids IS NOT NULL OR new_linked_control_ids IS NOT NULL`,
		`ALTER TABLE policy_audit_log ENABLE TRIGGER policy_audit_log_no_update_trg`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	sp, err = tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx,
		`INSERT INTO policy_audit_log (tenant_id, policy_id, action, actor) VALUES ($1, $2, 'controls_updated', 'user-1')`,
		tenant, uuid.New()); err == nil {
		t.Errorf("after the down migration policy_audit_log still accepts 'controls_updated'")
	}
	_ = sp.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	if _, err := tx.Exec(ctx, insert, tenant, uuid.New(), uuid.New()); err != nil {
		t.Errorf("after re-applying the up migration a controls_updated row is refused: %v", err)
	}
}
