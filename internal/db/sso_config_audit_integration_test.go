//go:build integration

// #2609 — the me_audit_log action CHECK gains 'tenant_sso_config_update' and
// keeps every value it had.
//
// The values before this migration are read from the CHECK in
// 20261226000000_risk_vendor_owner_users.sql, the definition it replaces, so
// a value dropped by the rewrite fails here rather than at the next audit
// write that uses it. The down migration is checked to refuse the new value
// again, and to refuse to run while a row with it exists.

package db_test

import (
	"context"
	"os"
	"testing"
	"time"
)

const (
	ssoConfigAuditPrior = "../../migrations/sql/20261226000000_risk_vendor_owner_users.sql"
	ssoConfigAuditUp    = "../../migrations/sql/20261260000000_sso_config_audit.sql"
	ssoConfigAuditDown  = "../../migrations/sql/20261260000000_sso_config_audit.down.sql"
	ssoConfigAuditNew   = "tenant_sso_config_update"
)

func TestSSOConfigAuditMigration_KeepsEveryAction(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	prior := meAuditActions(t, ssoConfigAuditPrior)
	current := meAuditActions(t, ssoConfigAuditUp)

	inCurrent := map[string]bool{}
	for _, a := range current {
		inCurrent[a] = true
	}
	for _, a := range prior {
		if !inCurrent[a] {
			t.Errorf("action %q from 20261226000000 is missing from 20261260000000", a)
		}
	}
	if !inCurrent[ssoConfigAuditNew] {
		t.Errorf("20261260000000 does not add %q", ssoConfigAuditNew)
	}
	if len(current) != len(prior)+1 {
		t.Errorf("20261260000000 lists %d actions, want %d", len(current), len(prior)+1)
	}

	down, err := os.ReadFile(ssoConfigAuditDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	up, err := os.ReadFile(ssoConfigAuditUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// An atlas_migrate transaction that always rolls back, so the schema and
	// data are untouched for later tests.
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, a := range current {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after 20261260000000", a)
		}
	}

	// The down migration refuses while an SSO audit row exists (audit
	// evidence is not deleted).
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Error("down migration applied while a tenant_sso_config_update row exists")
	}
	_ = sp.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM me_audit_log WHERE action = $1`, ssoConfigAuditNew); err != nil {
		t.Fatalf("clear new-value rows: %v", err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	if insertsAction(ctx, t, tx, ssoConfigAuditNew) {
		t.Errorf("after the down migration me_audit_log still accepts %q", ssoConfigAuditNew)
	}
	for _, a := range prior {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after the down migration", a)
		}
	}

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	if !insertsAction(ctx, t, tx, ssoConfigAuditNew) {
		t.Errorf("after re-applying the up migration me_audit_log refuses %q", ssoConfigAuditNew)
	}
}
