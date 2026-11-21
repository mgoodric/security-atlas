//go:build integration

// #2854 — 20261289000000_policy_sync_takeover. policy_audit_log accepts
// 'sync_takeover' with the policy's previous attribution. The down
// migration refuses while such a row exists, and once applied refuses
// 'sync_takeover' again; the up migration re-applies.
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
	policySyncTakeoverUp   = "../../migrations/sql/20261289000000_policy_sync_takeover.sql"
	policySyncTakeoverDown = "../../migrations/sql/20261289000000_policy_sync_takeover.down.sql"
)

func TestPolicySyncTakeoverMigration(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(policySyncTakeoverUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(policySyncTakeoverDown)
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
		tenant, "takeover-2854-"+tenant.String()[:8]); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	const insert = `INSERT INTO policy_audit_log
		(tenant_id, policy_id, action, actor, previous_source_attribution)
		VALUES ($1, $2, 'sync_takeover', 'key-1', 'imported')`
	if _, err := tx.Exec(ctx, insert, tenant, uuid.New()); err != nil {
		t.Fatalf("insert sync_takeover row: %v", err)
	}

	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Errorf("down migration applied while a sync_takeover row exists")
	}
	_ = sp.Rollback(ctx)

	for _, q := range []string{
		`ALTER TABLE policy_audit_log DISABLE TRIGGER policy_audit_log_no_update_trg`,
		`DELETE FROM policy_audit_log WHERE action = 'sync_takeover' OR previous_source_attribution IS NOT NULL`,
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
		`INSERT INTO policy_audit_log (tenant_id, policy_id, action, actor) VALUES ($1, $2, 'sync_takeover', 'key-1')`,
		tenant, uuid.New()); err == nil {
		t.Errorf("after the down migration policy_audit_log still accepts 'sync_takeover'")
	}
	_ = sp.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	if _, err := tx.Exec(ctx, insert, tenant, uuid.New()); err != nil {
		t.Errorf("after re-applying the up migration a sync_takeover row is refused: %v", err)
	}
}
