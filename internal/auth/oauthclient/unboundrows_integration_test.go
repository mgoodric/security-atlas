//go:build integration

// Integration tests for security-atlas#1876: rows left in the all-zeros
// tenant by OAuth clients that were unbound before #1739.
//
//   - LedgerTables classifies every append-only table (the evidence ledger,
//     every *_audit / *_audit_log / *_log table) as a ledger, so a new one
//     the schema rule misses fails here.
//   - ListUnboundRows counts all-zeros rows per tenant-scoped table, ignores
//     other tenants' rows, and marks ledger tables, and tables whose rows a
//     ledger row references, as retained.
//   - PurgeUnboundRows refuses a confirmation that does not match the
//     purgeable total and deletes nothing.
//   - PurgeUnboundRows deletes a parent after the child rows that reference
//     it (populations before samples by name, so the first pass trips the
//     composite foreign key), spares other tenants, and writes an
//     append-only tenant_data_purges row.
//   - PurgeUnboundRows keeps ledger rows and the parents they reference.
//
// Each test runs inside a transaction on the BYPASSRLS migrate pool and
// rolls it back, so the purge never touches rows outside the test, even
// all-zeros rows another suite left behind.
package oauthclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// beginRolledBack opens a REPEATABLE READ transaction on the migrate pool
// that is rolled back when the test ends.
func beginRolledBack(t *testing.T) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := dbtest.NewMigratePool(t).BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func seedControl(t *testing.T, tx pgx.Tx, tenant uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := tx.Exec(context.Background(),
		`INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		 VALUES ($1, $2, 'unbound-rows test', 'IAC', 'manual_attested', $3)`, id, tenant, "unbound-rows-"+id.String(),
	); err != nil {
		t.Fatalf("insert control: %v", err)
	}
	return id
}

func seedEvidence(t *testing.T, tx pgx.Tx, tenant, control uuid.UUID) {
	t.Helper()
	if _, err := tx.Exec(context.Background(),
		`INSERT INTO evidence_records (id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash)
		 VALUES ($1, $2, $3, 'IAC-01', now(), '{}'::jsonb, 'pass', 'sha256:test')`, uuid.New(), tenant, control,
	); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}
}

// seedPopulationWithSample inserts a population on control and a sample
// drawn from it, both in tenant. samples references populations with
// RESTRICT, and both sort after controls, so a purge must delete samples
// before populations whether or not controls is retained.
func seedPopulationWithSample(t *testing.T, tx pgx.Tx, tenant, control uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pop := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO populations (id, tenant_id, control_id, scope_predicate, time_window_start, time_window_end, created_by)
		 VALUES ($1, $2, $3, '{}'::jsonb, now() - interval '1 day', now(), 'unbound-rows-test')`, pop, tenant, control,
	); err != nil {
		t.Fatalf("insert population: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO samples (id, tenant_id, population_id, n, seed, created_by)
		 VALUES ($1, $2, $3, 1, 'seed', 'unbound-rows-test')`, uuid.New(), tenant, pop,
	); err != nil {
		t.Fatalf("insert sample: %v", err)
	}
}

func seedRisk(t *testing.T, tx pgx.Tx, tenant uuid.UUID) {
	t.Helper()
	if _, err := tx.Exec(context.Background(),
		`INSERT INTO risks (id, tenant_id, title, category) VALUES ($1, $2, 'unbound-rows test', 'operational')`,
		uuid.New(), tenant,
	); err != nil {
		t.Fatalf("insert risk: %v", err)
	}
}

func find(counts []oauthclient.UnboundTableCount, table string) oauthclient.UnboundTableCount {
	for _, c := range counts {
		if c.Table == table {
			return c
		}
	}
	return oauthclient.UnboundTableCount{Table: table}
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

// knownLedgers are append-only tables named by ADR-0012 or guarded by a
// delete-refusing trigger. ledgerName catches the audit logs.
var (
	knownLedgers = []string{
		"evidence_records", "evidence_audit_log", "control_evaluations", "control_drift_snapshots",
		"action_plan_audit_log", "change_audit_log",
	}
	ledgerName = regexp.MustCompile(`(_audit|_audit_log|_log)$`)
	// notLedgers match ledgerName but are deliberately deletable: each
	// migration grants a tenant_delete policy (delivery and rollup
	// bookkeeping, not a record of fact). A new *_log table must be a
	// ledger or be added here on purpose.
	notLedgers = map[string]bool{
		"channel_delivery_log": true, // 20260608000000_slack_webhook_channels.sql
		"email_delivery_log":   true, // 20260607020000_email_delivery_channel.sql
		"staleness_rollup_log": true, // 20260609000000_staleness_rollup_log.sql
	}
)

func TestLedgerTables_CoverAppendOnlyTables(t *testing.T) {
	tx := beginRolledBack(t)
	ctx := context.Background()

	ledger, err := oauthclient.LedgerTables(ctx, tx)
	if err != nil {
		t.Fatalf("LedgerTables: %v", err)
	}
	isLedger := map[string]bool{}
	for _, l := range ledger {
		isLedger[l] = true
	}
	want := append([]string{}, knownLedgers...)
	rows, err := tx.Query(ctx,
		`SELECT c.relname FROM pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 JOIN pg_attribute a ON a.attrelid = c.oid
		 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		   AND a.attname = 'tenant_id' AND NOT a.attisdropped`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	for _, n := range names {
		if ledgerName.MatchString(n) && !notLedgers[n] {
			want = append(want, n)
		}
	}
	for _, table := range want {
		if !isLedger[table] {
			t.Errorf("%s is append-only but not classified as a ledger; a purge would delete from it", table)
		}
	}
	for _, purgeable := range []string{"controls", "risks", "populations", "samples"} {
		if isLedger[purgeable] {
			t.Errorf("%s is classified as a ledger, want purgeable", purgeable)
		}
	}
}

func TestListUnboundRows_CountsAndRetains(t *testing.T) {
	tx := beginRolledBack(t)
	ctx := context.Background()

	before, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	referenced := seedControl(t, tx, uuid.Nil)
	seedEvidence(t, tx, uuid.Nil, referenced)
	seedRisk(t, tx, uuid.Nil)
	seedRisk(t, tx, uuid.New())

	after, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	for table, added := range map[string]int64{"controls": 1, "evidence_records": 1, "risks": 1} {
		if got, want := find(after, table).Rows, find(before, table).Rows+added; got != want {
			t.Errorf("%s = %d, want %d", table, got, want)
		}
	}
	if got := find(after, "evidence_records").Retained; got != oauthclient.RetainedLedger {
		t.Errorf("evidence_records retained = %q, want %q", got, oauthclient.RetainedLedger)
	}
	if got := find(after, "controls").Retained; !strings.Contains(got, "evidence_records") {
		t.Errorf("controls retained = %q, want it kept because evidence_records references it", got)
	}
	if got := find(after, "risks").Retained; got != "" {
		t.Errorf("risks retained = %q, want purgeable", got)
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
	seedRisk(t, tx, uuid.Nil)

	listed, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	total := oauthclient.PurgeableRows(listed)

	_, err = oauthclient.PurgeUnboundRows(ctx, tx, total+1, "tester")
	if !errors.Is(err, oauthclient.ErrPurgeNotConfirmed) {
		t.Fatalf("err = %v, want ErrPurgeNotConfirmed", err)
	}
	again, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	if got := oauthclient.PurgeableRows(again); got != total {
		t.Errorf("after refused purge purgeable total = %d, want %d (nothing deleted)", got, total)
	}
}

func TestPurgeUnboundRows_PurgesInOrderSparesOthersAndRecords(t *testing.T) {
	tx := beginRolledBack(t)
	ctx := context.Background()
	other := uuid.New()

	control := seedControl(t, tx, uuid.Nil)
	seedPopulationWithSample(t, tx, uuid.Nil, control)
	seedRisk(t, tx, uuid.Nil)
	otherControl := seedControl(t, tx, other)
	seedEvidence(t, tx, other, otherControl)
	seedRisk(t, tx, other)

	listed, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	res, err := oauthclient.PurgeUnboundRows(ctx, tx, oauthclient.PurgeableRows(listed), "tester")
	if err != nil {
		t.Fatalf("PurgeUnboundRows: %v", err)
	}
	if got, want := oauthclient.PurgeableRows(res.Purged), oauthclient.PurgeableRows(listed); got != want {
		t.Errorf("purged total = %d, want %d", got, want)
	}
	// controls may be retained by all-zeros evidence another suite left in
	// a shared database; the tables below never are.
	for _, table := range []string{"populations", "samples", "risks"} {
		if find(res.Purged, table).Rows == 0 {
			t.Errorf("purge log %v is missing %s", res.Purged, table)
		}
		if got := countTenantRows(t, tx, table, uuid.Nil); got != 0 {
			t.Errorf("all-zeros %s after purge = %d, want 0", table, got)
		}
	}
	for table, want := range map[string]int64{"controls": 1, "evidence_records": 1, "risks": 1} {
		if got := countTenantRows(t, tx, table, other); got != want {
			t.Errorf("other tenant's %s = %d, want %d (purge must not touch it)", table, got, want)
		}
	}

	var (
		osUser string
		total  int64
		purged []byte
		dbRole string
	)
	if err := tx.QueryRow(ctx,
		`SELECT os_user, total_rows, purged, db_role FROM tenant_data_purges WHERE id = $1`, res.AuditID,
	).Scan(&osUser, &total, &purged, &dbRole); err != nil {
		t.Fatalf("read tenant_data_purges row %s: %v", res.AuditID, err)
	}
	var logged []oauthclient.UnboundTableCount
	if err := json.Unmarshal(purged, &logged); err != nil {
		t.Fatalf("decode purged: %v", err)
	}
	if osUser != "tester" || total != oauthclient.PurgeableRows(res.Purged) || len(logged) != len(res.Purged) || dbRole == "" {
		t.Errorf("tenant_data_purges row = (%q, %d, %v, %q), want (tester, %d, %v, <role>)",
			osUser, total, logged, dbRole, oauthclient.PurgeableRows(res.Purged), res.Purged)
	}

	if _, err := tx.Exec(ctx, `SAVEPOINT append_only`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM tenant_data_purges WHERE id = $1`, res.AuditID); err == nil {
		t.Error("deleting a tenant_data_purges row succeeded; the table must be append-only")
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT append_only`); err != nil {
		t.Fatalf("rollback to savepoint: %v", err)
	}
}

func TestPurgeUnboundRows_KeepsLedgerRowsAndTheirParents(t *testing.T) {
	tx := beginRolledBack(t)
	ctx := context.Background()
	control := seedControl(t, tx, uuid.Nil)
	seedEvidence(t, tx, uuid.Nil, control)
	seedRisk(t, tx, uuid.Nil)

	evidenceBefore := countTenantRows(t, tx, "evidence_records", uuid.Nil)
	controlsBefore := countTenantRows(t, tx, "controls", uuid.Nil)
	listed, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		t.Fatalf("ListUnboundRows: %v", err)
	}
	res, err := oauthclient.PurgeUnboundRows(ctx, tx, oauthclient.PurgeableRows(listed), "tester")
	if err != nil {
		t.Fatalf("PurgeUnboundRows: %v", err)
	}
	if got := countTenantRows(t, tx, "evidence_records", uuid.Nil); got != evidenceBefore {
		t.Errorf("all-zeros evidence_records after purge = %d, want %d (ledger is never purged)", got, evidenceBefore)
	}
	if got := countTenantRows(t, tx, "controls", uuid.Nil); got != controlsBefore {
		t.Errorf("all-zeros controls after purge = %d, want %d (referenced by the ledger)", got, controlsBefore)
	}
	if got := countTenantRows(t, tx, "risks", uuid.Nil); got != 0 {
		t.Errorf("all-zeros risks after purge = %d, want 0", got)
	}
	if find(res.Retained, "evidence_records").Rows == 0 || find(res.Retained, "controls").Rows == 0 {
		t.Errorf("purge result retained = %v, want evidence_records and controls", res.Retained)
	}
}
