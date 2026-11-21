//go:build integration

// #2760 — the me_audit_log action CHECK gains tenant_sso_config_delete and keeps
// every value it had.
//
// The values before this migration are read from the CHECK in
// 20261269000000_metric_target_audit.sql, the definition it
// replaces, so a value dropped by the rewrite fails here rather than at the
// next audit write that uses it. The down migration is checked to refuse
// the new value again, and to refuse to run while a row with it exists.

package db_test

import (
	"context"
	"os"
	"testing"
	"time"
)

const (
	ssoConfigDeleteAuditPrior = "../../migrations/sql/20261269000000_metric_target_audit.sql"
	ssoConfigDeleteAuditUp    = "../../migrations/sql/20261272000001_sso_config_delete_audit.sql"
	ssoConfigDeleteAuditDown  = "../../migrations/sql/20261272000001_sso_config_delete_audit.down.sql"
	ssoConfigDeleteAuditSet   = "tenant_sso_config_delete"
)

func TestSSOConfigDeleteAuditMigration_KeepsEveryAction(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	prior := meAuditActions(t, ssoConfigDeleteAuditPrior)
	current := meAuditActions(t, ssoConfigDeleteAuditUp)

	inCurrent := map[string]bool{}
	for _, a := range current {
		inCurrent[a] = true
	}
	for _, a := range prior {
		if !inCurrent[a] {
			t.Errorf("action %q from 20261269000000 is missing from 20261272000001", a)
		}
	}
	if !inCurrent[ssoConfigDeleteAuditSet] {
		t.Errorf("20261272000001 does not add %q", ssoConfigDeleteAuditSet)
	}
	if len(current) != len(prior)+1 {
		t.Errorf("20261272000001 lists %d actions, want %d", len(current), len(prior)+1)
	}

	down, err := os.ReadFile(ssoConfigDeleteAuditDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	up, err := os.ReadFile(ssoConfigDeleteAuditUp)
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
			t.Errorf("me_audit_log refuses action %q after 20261272000001", a)
		}
	}

	// The down migration refuses while a tenant_sso_config_delete row exists
	// (audit evidence is not deleted). Rows other tests wrote with actions
	// newer than this migration are cleared too, so only that row blocks.
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, `DELETE FROM me_audit_log WHERE NOT (action = ANY($1)) AND action <> $2`, prior, ssoConfigDeleteAuditSet); err != nil {
		t.Fatalf("narrow to %s rows: %v", ssoConfigDeleteAuditSet, err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Errorf("down migration applied while a %s row exists", ssoConfigDeleteAuditSet)
	}
	_ = sp.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM me_audit_log WHERE NOT (action = ANY($1))`, prior); err != nil {
		t.Fatalf("clear new-value rows: %v", err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	if insertsAction(ctx, t, tx, ssoConfigDeleteAuditSet) {
		t.Errorf("after the down migration me_audit_log still accepts %q", ssoConfigDeleteAuditSet)
	}
	for _, a := range prior {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after the down migration", a)
		}
	}

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	if !insertsAction(ctx, t, tx, ssoConfigDeleteAuditSet) {
		t.Errorf("after re-applying the up migration me_audit_log refuses %q", ssoConfigDeleteAuditSet)
	}
}
