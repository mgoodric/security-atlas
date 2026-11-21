//go:build integration

// #2850 — 20261279000000_policy_sync. A 'synced' policy row must carry its
// source, page id, body hash, sync key and time, plus the historical
// approver, approval date and source URL; no other row may carry the sync
// columns. One published synced row per page of a source. The audit log
// accepts 'synced'. The down migration refuses while a synced policy or
// audit row exists, and once applied refuses 'synced' again.
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
	"github.com/jackc/pgx/v5"
)

const (
	policySyncUp   = "../../migrations/sql/20261279000000_policy_sync.sql"
	policySyncDown = "../../migrations/sql/20261279000000_policy_sync.down.sql"
)

// syncedRow is a full synced policy row; a case blanks one column.
type syncedRow struct {
	attribution, status                           string
	site, space, pageID, hash, syncedBy, approver *string
	syncedAt                                      *time.Time
	sourceURL                                     *string
}

func validSyncedRow() syncedRow {
	s := func(v string) *string { return &v }
	now := time.Now()
	return syncedRow{
		attribution: "synced", status: "published",
		site: s("acme.atlassian.net"), space: s("SEC"), pageID: s("1001"),
		hash: s(strings.Repeat("a", 64)), syncedBy: s("key_sync"), approver: s("Jane Doe"),
		syncedAt: &now, sourceURL: s("https://acme.atlassian.net/wiki/spaces/SEC/pages/1001"),
	}
}

// insertsSyncedRow reports whether r inserts, inside a savepoint that
// always rolls back unless keep is set.
func insertsSyncedRow(ctx context.Context, t *testing.T, tx pgx.Tx, tenant uuid.UUID, r syncedRow, keep bool) bool {
	t.Helper()
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	var approvedAt *time.Time
	if r.approver != nil {
		d := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
		approvedAt = &d
	}
	_, err = sp.Exec(ctx, `
		INSERT INTO policies
			(id, tenant_id, title, version, body_md, status, owner_role, approver_role, created_by,
			 effective_date, published_at, published_by, source_attribution,
			 historical_approver_name, historical_approved_at, source_url,
			 sync_site, sync_space, sync_page_id, synced_body_sha256, synced_by, synced_at)
		VALUES ($1, $2, 'Synced', '1.0', 'Body.', $3, 'unassigned', 'unassigned', 'key_sync',
			current_date, now(), 'key_sync', $4,
			$5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		uuid.New(), tenant, r.status, r.attribution,
		r.approver, approvedAt, r.sourceURL,
		r.site, r.space, r.pageID, r.hash, r.syncedBy, r.syncedAt)
	if err != nil || !keep {
		_ = sp.Rollback(ctx)
		return err == nil
	}
	if err := sp.Commit(ctx); err != nil {
		t.Fatalf("release savepoint: %v", err)
	}
	return true
}

func TestPolicySyncMigration(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(policySyncUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(policySyncDown)
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
		tenant, "sync-2850-"+tenant.String()[:8]); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	if !insertsSyncedRow(ctx, t, tx, tenant, validSyncedRow(), false) {
		t.Fatalf("a complete synced row is refused")
	}
	for name, blank := range map[string]func(*syncedRow){
		"no site":             func(r *syncedRow) { r.site = nil },
		"no space":            func(r *syncedRow) { r.space = nil },
		"no page id":          func(r *syncedRow) { r.pageID = nil },
		"no body hash":        func(r *syncedRow) { r.hash = nil },
		"bad body hash":       func(r *syncedRow) { h := "not-a-hash"; r.hash = &h },
		"no sync key":         func(r *syncedRow) { r.syncedBy = nil },
		"no sync time":        func(r *syncedRow) { r.syncedAt = nil },
		"no approver":         func(r *syncedRow) { r.approver = nil },
		"no source url":       func(r *syncedRow) { r.sourceURL = nil },
		"sync cols, authored": func(r *syncedRow) { r.attribution = "tenant_authored"; r.approver = nil; r.sourceURL = nil },
	} {
		r := validSyncedRow()
		blank(&r)
		if insertsSyncedRow(ctx, t, tx, tenant, r, false) {
			t.Errorf("%s: row accepted", name)
		}
	}

	// One published synced row per page of a source.
	if !insertsSyncedRow(ctx, t, tx, tenant, validSyncedRow(), true) {
		t.Fatalf("seed synced row refused")
	}
	if insertsSyncedRow(ctx, t, tx, tenant, validSyncedRow(), false) {
		t.Errorf("a second published synced row for the same page is accepted")
	}
	other := validSyncedRow()
	otherSpace := "OPS"
	other.space = &otherSpace
	if !insertsSyncedRow(ctx, t, tx, tenant, other, false) {
		t.Errorf("the same page id in another space is refused")
	}
	superseded := validSyncedRow()
	superseded.status = "superseded"
	if !insertsSyncedRow(ctx, t, tx, tenant, superseded, false) {
		t.Errorf("a superseded synced row beside the published one is refused")
	}

	// The audit log accepts 'synced'.
	if _, err := tx.Exec(ctx,
		`INSERT INTO policy_audit_log (tenant_id, policy_id, action, actor) VALUES ($1, $2, 'synced', 'key_sync')`,
		tenant, uuid.New()); err != nil {
		t.Fatalf("insert synced audit row: %v", err)
	}

	// The down migration refuses while a synced policy or audit row exists.
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Errorf("down migration applied while synced rows exist")
	}
	_ = sp.Rollback(ctx)

	// Clear every synced row, other tests' included (the transaction rolls
	// back). policy_audit_log is append-only, so its trigger is lifted for
	// the delete. controls_updated rows (#2852) and sync_takeover rows
	// (#2854) are written only on synced or taken-over policies, and the
	// down migration's action CHECK predates them.
	for _, q := range []string{
		`DELETE FROM policies WHERE source_attribution = 'synced'`,
		`ALTER TABLE policy_audit_log DISABLE TRIGGER policy_audit_log_no_update_trg`,
		`DELETE FROM policy_audit_log WHERE action IN ('synced', 'controls_updated', 'sync_takeover')`,
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
		`INSERT INTO policy_audit_log (tenant_id, policy_id, action, actor) VALUES ($1, $2, 'synced', 'key_sync')`,
		tenant, uuid.New()); err == nil {
		t.Errorf("after the down migration policy_audit_log still accepts 'synced'")
	}
	_ = sp.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	if !insertsSyncedRow(ctx, t, tx, tenant, validSyncedRow(), false) {
		t.Errorf("after re-applying the up migration a complete synced row is refused")
	}
}
