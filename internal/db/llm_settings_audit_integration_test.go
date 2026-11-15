//go:build integration

// #2224 — the me_audit_log action CHECK gains the three LLM settings audit
// actions and keeps every value it had.
//
// The values before this migration are read from the CHECK in
// 20261116000000_vendor_initial_review_audit.sql, the definition it replaces,
// so a value dropped by the rewrite fails here rather than at the next audit
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
	llmSettingsAuditPrior = "../../migrations/sql/20261116000000_vendor_initial_review_audit.sql"
	llmSettingsAuditUp    = "../../migrations/sql/20261120000000_llm_settings_audit.sql"
	llmSettingsAuditDown  = "../../migrations/sql/20261120000000_llm_settings_audit.down.sql"
)

// llmSettingsActions are the values 20261120000000 adds.
var llmSettingsActions = []string{
	"tenant_llm_routing_update",
	"tenant_llm_routing_clear",
	"control_import_settings_update",
}

func TestLLMSettingsAuditMigration_KeepsEveryAction(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	prior := meAuditActions(t, llmSettingsAuditPrior)
	current := meAuditActions(t, llmSettingsAuditUp)

	// The new list is the old one plus the new values, nothing dropped.
	inCurrent := map[string]bool{}
	for _, a := range current {
		inCurrent[a] = true
	}
	for _, a := range prior {
		if !inCurrent[a] {
			t.Errorf("action %q from 20261116000000 is missing from 20261120000000", a)
		}
	}
	for _, a := range llmSettingsActions {
		if !inCurrent[a] {
			t.Errorf("20261120000000 does not add %q", a)
		}
	}
	if len(current) != len(prior)+len(llmSettingsActions) {
		t.Errorf("20261120000000 lists %d actions, want %d", len(current), len(prior)+len(llmSettingsActions))
	}

	down, err := os.ReadFile(llmSettingsAuditDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	up, err := os.ReadFile(llmSettingsAuditUp)
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

	// The migrated schema: every prior value and the new ones insert.
	for _, a := range append(append([]string{}, prior...), llmSettingsActions...) {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after 20261120000000", a)
		}
	}

	// The down migration refuses while any new-value row exists (audit
	// evidence is not deleted), then narrows the CHECK once they are gone.
	for _, keep := range llmSettingsActions {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		if _, err := sp.Exec(ctx, `DELETE FROM me_audit_log WHERE action = ANY($1) AND action <> $2`, llmSettingsActions, keep); err != nil {
			t.Fatalf("narrow to %s rows: %v", keep, err)
		}
		if _, err := sp.Exec(ctx, string(down)); err == nil {
			t.Errorf("down migration applied while a %s row exists", keep)
		}
		_ = sp.Rollback(ctx)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM me_audit_log WHERE action = ANY($1)`, llmSettingsActions); err != nil {
		t.Fatalf("clear new-value rows: %v", err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	for _, a := range llmSettingsActions {
		if insertsAction(ctx, t, tx, a) {
			t.Errorf("after the down migration me_audit_log still accepts %q", a)
		}
	}
	for _, a := range prior {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after the down migration", a)
		}
	}

	// Up again re-applies cleanly.
	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	for _, a := range llmSettingsActions {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("after re-applying the up migration me_audit_log refuses %q", a)
		}
	}
}
