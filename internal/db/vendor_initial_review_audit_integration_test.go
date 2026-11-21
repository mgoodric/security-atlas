//go:build integration

// #2201 — the me_audit_log action CHECK gains
// 'tenant_vendor_initial_review_update' and keeps every value it had.
//
// The values before this migration are read from the CHECK in
// 20261031000000_self_approval.sql, the definition it replaces, so a value
// dropped by the rewrite fails here rather than at the next audit write that
// uses it. The down migration is checked to refuse the new value again.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	priorMeAuditActionsMigration = "../../migrations/sql/20261031000000_self_approval.sql"
	vendorInitialReviewAuditUp   = "../../migrations/sql/20261116000000_vendor_initial_review_audit.sql"
	vendorInitialReviewAuditDown = "../../migrations/sql/20261116000000_vendor_initial_review_audit.down.sql"
	vendorInitialReviewAction    = "tenant_vendor_initial_review_update"
)

// meAuditActions returns the quoted values of the me_audit_log action CHECK
// defined in the migration at path.
func meAuditActions(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block := regexp.MustCompile(`(?s)ADD CONSTRAINT me_audit_log_action_check\s+CHECK \(action IN \((.*?)\)\);`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("%s: no me_audit_log_action_check found", path)
	}
	var out []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(block[1], -1) {
		out = append(out, string(m[1]))
	}
	if len(out) == 0 {
		t.Fatalf("%s: me_audit_log_action_check lists no values", path)
	}
	return out
}

// insertsAction reports whether an me_audit_log row with action inserts,
// under a savepoint so a refusal leaves tx usable.
func insertsAction(ctx context.Context, t *testing.T, tx pgx.Tx, action string) bool {
	t.Helper()
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	_, err = sp.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action) VALUES ($1, $2, $3)`,
		uuid.New(), uuid.New(), action)
	if err != nil {
		_ = sp.Rollback(ctx)
		return false
	}
	if err := sp.Commit(ctx); err != nil {
		t.Fatalf("release savepoint: %v", err)
	}
	return true
}

func TestVendorInitialReviewAuditMigration_KeepsEveryAction(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	prior := meAuditActions(t, priorMeAuditActionsMigration)
	current := meAuditActions(t, vendorInitialReviewAuditUp)

	// The new list is the old one plus the new value, nothing dropped.
	inCurrent := map[string]bool{}
	for _, a := range current {
		inCurrent[a] = true
	}
	for _, a := range prior {
		if !inCurrent[a] {
			t.Errorf("action %q from 20261031000000 is missing from 20261116000000", a)
		}
	}
	if !inCurrent[vendorInitialReviewAction] {
		t.Errorf("20261116000000 does not add %q", vendorInitialReviewAction)
	}

	down, err := os.ReadFile(vendorInitialReviewAuditDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	up, err := os.ReadFile(vendorInitialReviewAuditUp)
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

	// The migrated schema: every prior value and the new one insert.
	for _, a := range append(append([]string{}, prior...), vendorInitialReviewAction) {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after 20261116000000", a)
		}
	}

	// The down migration refuses while a new-value row exists (audit
	// evidence is not deleted), then narrows the CHECK once it is gone.
	// Rows other tests wrote with actions the down CHECK does not list are
	// cleared too, so only that row blocks (#2865).
	downActions := meAuditActions(t, vendorInitialReviewAuditDown)
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, `DELETE FROM me_audit_log WHERE NOT (action = ANY($1)) AND action <> $2`, downActions, vendorInitialReviewAction); err != nil {
		t.Fatalf("narrow to %s rows: %v", vendorInitialReviewAction, err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Error("down migration applied while a tenant_vendor_initial_review_update row exists")
	}
	_ = sp.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM me_audit_log WHERE NOT (action = ANY($1))`, downActions); err != nil {
		t.Fatalf("clear new-value rows: %v", err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	if insertsAction(ctx, t, tx, vendorInitialReviewAction) {
		t.Errorf("after the down migration me_audit_log still accepts %q", vendorInitialReviewAction)
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
	if !insertsAction(ctx, t, tx, vendorInitialReviewAction) {
		t.Errorf("after re-applying the up migration me_audit_log refuses %q", vendorInitialReviewAction)
	}
}
