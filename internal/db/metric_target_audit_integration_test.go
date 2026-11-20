//go:build integration

// #2572 — the me_audit_log action CHECK gains metric_target_set and keeps
// every value it had.
//
// The values before this migration are read from the CHECK in
// 20261263000000_group_mapping_api_key_audit.sql, the definition it
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
	metricTargetAuditPrior = "../../migrations/sql/20261263000000_group_mapping_api_key_audit.sql"
	metricTargetAuditUp    = "../../migrations/sql/20261269000000_metric_target_audit.sql"
	metricTargetAuditDown  = "../../migrations/sql/20261269000000_metric_target_audit.down.sql"
	metricTargetAuditSet   = "metric_target_set"
)

func TestMetricTargetAuditMigration_KeepsEveryAction(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	prior := meAuditActions(t, metricTargetAuditPrior)
	current := meAuditActions(t, metricTargetAuditUp)

	inCurrent := map[string]bool{}
	for _, a := range current {
		inCurrent[a] = true
	}
	for _, a := range prior {
		if !inCurrent[a] {
			t.Errorf("action %q from 20261263000000 is missing from 20261269000000", a)
		}
	}
	if !inCurrent[metricTargetAuditSet] {
		t.Errorf("20261269000000 does not add %q", metricTargetAuditSet)
	}
	if len(current) != len(prior)+1 {
		t.Errorf("20261269000000 lists %d actions, want %d", len(current), len(prior)+1)
	}

	down, err := os.ReadFile(metricTargetAuditDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	up, err := os.ReadFile(metricTargetAuditUp)
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
			t.Errorf("me_audit_log refuses action %q after 20261269000000", a)
		}
	}

	// The down migration refuses while a metric_target_set row exists
	// (audit evidence is not deleted). Rows other tests wrote with actions
	// newer than this migration are cleared too, so only that row blocks.
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, `DELETE FROM me_audit_log WHERE NOT (action = ANY($1)) AND action <> $2`, prior, metricTargetAuditSet); err != nil {
		t.Fatalf("narrow to %s rows: %v", metricTargetAuditSet, err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Errorf("down migration applied while a %s row exists", metricTargetAuditSet)
	}
	_ = sp.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM me_audit_log WHERE NOT (action = ANY($1))`, prior); err != nil {
		t.Fatalf("clear new-value rows: %v", err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	if insertsAction(ctx, t, tx, metricTargetAuditSet) {
		t.Errorf("after the down migration me_audit_log still accepts %q", metricTargetAuditSet)
	}
	for _, a := range prior {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after the down migration", a)
		}
	}

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	if !insertsAction(ctx, t, tx, metricTargetAuditSet) {
		t.Errorf("after re-applying the up migration me_audit_log refuses %q", metricTargetAuditSet)
	}
}
