//go:build integration

// Integration tests for security-atlas#1876: rows left in the all-zeros
// tenant by OAuth clients that were unbound before #1739.
//
//   - ListUnboundRows counts all-zeros rows per tenant-scoped table and
//     ignores other tenants' rows.
//   - PurgeUnboundRows refuses a confirmation that does not match the
//     listing and deletes nothing.
//   - PurgeUnboundRows deletes a parent table's rows after the child rows
//     that reference them (controls before evidence_records by name, so
//     the first pass trips the composite foreign key), and leaves other
//     tenants' rows alone.
//
// Each test runs inside a transaction on the BYPASSRLS migrate pool and
// rolls it back, so the purge never touches rows outside the test, even
// all-zeros rows another suite left behind.
package oauthclient_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// beginRolledBack opens a transaction on the migrate pool that is rolled
// back when the test ends.
func beginRolledBack(t *testing.T) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := dbtest.NewMigratePool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

// seedControlWithEvidence inserts one control and one evidence record that
// references it, both in tenant.
func seedControlWithEvidence(t *testing.T, tx pgx.Tx, tenant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	control := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		 VALUES ($1, $2, 'unbound-rows test', 'IAC', 'manual_attested', $3)`, control, tenant, "unbound-rows-"+control.String(),
	); err != nil {
		t.Fatalf("insert control: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO evidence_records (id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash)
		 VALUES ($1, $2, $3, 'IAC-01', now(), '{}'::jsonb, 'pass', 'sha256:test')`, uuid.New(), tenant, control,
	); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}
}

func countOf(counts []oauthclient.UnboundTableCount, table string) int64 {
	for _, c := range counts {
		if c.Table == table {
			return c.Rows
		}
	}
	return 0
}

func countTenantRows(t *testing.T, tx pgx.Tx, table string, tenant uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := tx.QueryRow(context.Background(),
		"SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()+" WHERE tenant_id = $1", tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestListUnboundRows_CountsOnlyAllZeros(t *testing.T) {
	tx := beginRolledBack(t)
	ctx := context.Background()

	before, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	seedControlWithEvidence(t, tx, uuid.Nil)
	seedControlWithEvidence(t, tx, uuid.Nil)
	seedControlWithEvidence(t, tx, uuid.New())

	after, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	for _, table := range []string{"controls", "evidence_records"} {
		if got, want := countOf(after, table), countOf(before, table)+2; got != want {
			t.Errorf("%s = %d, want %d", table, got, want)
		}
	}
	if got, want := oauthclient.TotalRows(after), oauthclient.TotalRows(before)+4; got != want {
		t.Errorf("total = %d, want %d", got, want)
	}
	for _, c := range after {
		if c.Rows <= 0 {
			t.Errorf("listing includes %s with %d rows; empty tables are left out", c.Table, c.Rows)
		}
	}
}

func TestPurgeUnboundRows_RefusesWrongConfirmation(t *testing.T) {
	tx := beginRolledBack(t)
	ctx := context.Background()
	seedControlWithEvidence(t, tx, uuid.Nil)

	listed, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	total := oauthclient.TotalRows(listed)

	_, err = oauthclient.PurgeUnboundRows(ctx, tx, total+1)
	if !errors.Is(err, oauthclient.ErrPurgeNotConfirmed) {
		t.Fatalf("err = %v, want ErrPurgeNotConfirmed", err)
	}
	again, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	if got := oauthclient.TotalRows(again); got != total {
		t.Errorf("after refused purge total = %d, want %d (nothing deleted)", got, total)
	}
}

func TestPurgeUnboundRows_DeletesChildrenFirstAndSparesOtherTenants(t *testing.T) {
	tx := beginRolledBack(t)
	ctx := context.Background()
	other := uuid.New()
	seedControlWithEvidence(t, tx, uuid.Nil)
	seedControlWithEvidence(t, tx, other)

	listed, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	purged, err := oauthclient.PurgeUnboundRows(ctx, tx, oauthclient.TotalRows(listed))
	if err != nil {
		t.Fatalf("PurgeUnboundRows: %v", err)
	}
	if got, want := oauthclient.TotalRows(purged), oauthclient.TotalRows(listed); got != want {
		t.Errorf("purged total = %d, want %d", got, want)
	}
	if countOf(purged, "controls") == 0 || countOf(purged, "evidence_records") == 0 {
		t.Errorf("purge log %v is missing controls or evidence_records", purged)
	}
	remaining, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("after purge %v remain, want none", remaining)
	}
	for _, table := range []string{"controls", "evidence_records"} {
		if got := countTenantRows(t, tx, table, other); got != 1 {
			t.Errorf("other tenant's %s = %d, want 1 (purge must not touch it)", table, got)
		}
	}
}
