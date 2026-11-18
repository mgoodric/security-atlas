//go:build integration

// #2609 — the me_audit_log action CHECK gains the group-role mapping and API
// key audit actions and keeps every value it had.
//
// The values before this migration are read from the CHECK in
// 20261260000000_sso_scim_audit.sql, the definition it replaces, so
// a value dropped by the rewrite fails here rather than at the next audit
// write that uses it. The down migration is checked to refuse the new values
// again, and to refuse to run while a row with one of them exists.

package db_test

import (
	"context"
	"os"
	"testing"
	"time"
)

const (
	mappingKeyAuditPrior = "../../migrations/sql/20261260000000_sso_scim_audit.sql"
	mappingKeyAuditUp    = "../../migrations/sql/20261261000000_group_mapping_api_key_audit.sql"
	mappingKeyAuditDown  = "../../migrations/sql/20261261000000_group_mapping_api_key_audit.down.sql"
)

// mappingKeyAuditActions are the values 20261261000000 adds.
var mappingKeyAuditActions = []string{
	"group_role_mapping_create",
	"group_role_mapping_delete",
	"api_key_issue",
	"api_key_rotate",
	"api_key_revoke",
}

func TestGroupMappingAPIKeyAuditMigration_KeepsEveryAction(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	prior := meAuditActions(t, mappingKeyAuditPrior)
	current := meAuditActions(t, mappingKeyAuditUp)

	inCurrent := map[string]bool{}
	for _, a := range current {
		inCurrent[a] = true
	}
	for _, a := range prior {
		if !inCurrent[a] {
			t.Errorf("action %q from 20261260000000 is missing from 20261261000000", a)
		}
	}
	for _, a := range mappingKeyAuditActions {
		if !inCurrent[a] {
			t.Errorf("20261261000000 does not add %q", a)
		}
	}
	if len(current) != len(prior)+len(mappingKeyAuditActions) {
		t.Errorf("20261261000000 lists %d actions, want %d", len(current), len(prior)+len(mappingKeyAuditActions))
	}

	down, err := os.ReadFile(mappingKeyAuditDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	up, err := os.ReadFile(mappingKeyAuditUp)
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
			t.Errorf("me_audit_log refuses action %q after 20261261000000", a)
		}
	}

	// The down migration refuses while any one new-value row exists (audit
	// evidence is not deleted). Rows other tests wrote with actions newer
	// than this migration are cleared too, so only the kept row blocks.
	for _, keep := range mappingKeyAuditActions {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		if _, err := sp.Exec(ctx, `DELETE FROM me_audit_log WHERE NOT (action = ANY($1)) AND action <> $2`, prior, keep); err != nil {
			t.Fatalf("narrow to %s rows: %v", keep, err)
		}
		if _, err := sp.Exec(ctx, string(down)); err == nil {
			t.Errorf("down migration applied while a %s row exists", keep)
		}
		_ = sp.Rollback(ctx)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM me_audit_log WHERE NOT (action = ANY($1))`, prior); err != nil {
		t.Fatalf("clear new-value rows: %v", err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	for _, a := range mappingKeyAuditActions {
		if insertsAction(ctx, t, tx, a) {
			t.Errorf("after the down migration me_audit_log still accepts %q", a)
		}
	}
	for _, a := range prior {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after the down migration", a)
		}
	}

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	for _, a := range mappingKeyAuditActions {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("after re-applying the up migration me_audit_log refuses %q", a)
		}
	}
}
