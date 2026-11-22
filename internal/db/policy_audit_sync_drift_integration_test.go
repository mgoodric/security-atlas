//go:build integration

// #2910 — 20261298000000_policy_audit_sync_drift. policy_audit_log takes a
// hex sha256 in sync_drift_body_sha256 on a 'sync_takeover' row only. The
// down migration refuses while such a row exists, and once applied drops
// the column; the up migration re-applies.
//
// The test runs in one atlas_migrate transaction that is rolled back.

package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	policyAuditSyncDriftUp   = "../../migrations/sql/20261298000000_policy_audit_sync_drift.sql"
	policyAuditSyncDriftDown = "../../migrations/sql/20261298000000_policy_audit_sync_drift.down.sql"
)

func TestPolicyAuditSyncDriftMigration(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(policyAuditSyncDriftUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(policyAuditSyncDriftDown)
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
		tenant, "syncdrift-2910-"+tenant.String()[:8]); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	hash := strings.Repeat("ab", 32)
	const insert = `INSERT INTO policy_audit_log
		(tenant_id, policy_id, action, actor, sync_drift_body_sha256)
		VALUES ($1, $2, $3, 'key-1', $4)`

	for _, bad := range []struct{ name, action, hash string }{
		{"not a hex sha256", "sync_takeover", "not-a-hash"},
		{"another action", "synced", hash},
	} {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		if _, err := sp.Exec(ctx, insert, tenant, uuid.New(), bad.action, bad.hash); err == nil {
			t.Errorf("%s: policy_audit_log accepted sync_drift_body_sha256 %q on %q", bad.name, bad.hash, bad.action)
		}
		_ = sp.Rollback(ctx)
	}
	if _, err := tx.Exec(ctx, insert, tenant, uuid.New(), "sync_takeover", hash); err != nil {
		t.Fatalf("insert sync_takeover row with a drifted hash: %v", err)
	}

	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Errorf("down migration applied while a row carries sync_drift_body_sha256")
	}
	_ = sp.Rollback(ctx)

	for _, q := range []string{
		`ALTER TABLE policy_audit_log DISABLE TRIGGER policy_audit_log_no_update_trg`,
		`DELETE FROM policy_audit_log WHERE sync_drift_body_sha256 IS NOT NULL`,
		`ALTER TABLE policy_audit_log ENABLE TRIGGER policy_audit_log_no_update_trg`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'policy_audit_log' AND column_name = 'sync_drift_body_sha256'`).Scan(&n); err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if n != 0 {
		t.Errorf("after the down migration policy_audit_log still has sync_drift_body_sha256")
	}

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	if _, err := tx.Exec(ctx, insert, tenant, uuid.New(), "sync_takeover", hash); err != nil {
		t.Errorf("after re-applying the up migration a drifted hash is refused: %v", err)
	}
}
