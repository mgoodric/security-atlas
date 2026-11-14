//go:build integration

// #1830: ProcessInTx is the in-transaction variant domain writers use.
// These tests pin its transaction contract: an accepted record and its
// audit row commit or roll back with the caller's transaction, and a
// refusal's audit row survives the caller rolling back.

package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// inTenantTx runs fn in an app-role transaction with tenant's GUC set,
// committing when commit is true and rolling back otherwise. ctx carries
// an AuditOutbox, flushed after a commit, the way a domain store does it.
func inTenantTx(t *testing.T, pool *pgxpool.Pool, tenant string, commit bool, fn func(context.Context, pgx.Tx) error) error {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	ctx, audit := ingest.WithAuditOutbox(ctx)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		audit.Flush(ctx)
	}
	return nil
}

// captureSink installs a default audit sink writing to memory and returns
// a function that shuts it down and reports how many events it wrote.
// These tests do not run in parallel, so the package-level sink is safe
// to swap.
func captureSink(t *testing.T) func() int64 {
	t.Helper()
	s, err := sink.New(sink.Options{Writer: &bytes.Buffer{}, HMACKey: bytes.Repeat([]byte("k"), sink.MinHMACKeyLen)})
	if err != nil {
		t.Fatalf("sink.New: %v", err)
	}
	sink.SetDefault(s)
	t.Cleanup(func() { sink.SetDefault(nil) })
	return func() int64 {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatalf("sink shutdown: %v", err)
		}
		emitted, _, _, _ := s.StatsSnapshot()
		return emitted
	}
}

// #1933: the accepted record's sink event leaves the process only after
// the caller commits. A caller that rolls back never emits it.
func TestProcessInTx_SinkEventWaitsForCommit(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")

	emitted := captureSink(t)
	err := inTenantTx(t, pool, tenantA, false, func(ctx context.Context, tx pgx.Tx) error {
		_, _, perr := svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{})
		return perr
	})
	if err != nil {
		t.Fatalf("ProcessInTx: %v", err)
	}
	if n := emitted(); n != 0 {
		t.Fatalf("sink events after rollback = %d, want 0", n)
	}

	emitted = captureSink(t)
	err = inTenantTx(t, pool, tenantA, true, func(ctx context.Context, tx pgx.Tx) error {
		_, _, perr := svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{})
		return perr
	})
	if err != nil {
		t.Fatalf("ProcessInTx: %v", err)
	}
	if n := emitted(); n != 1 {
		t.Fatalf("sink events after commit and flush = %d, want 1", n)
	}

	// Without the flush the event stays held in the outbox.
	ctx, err := tenancy.WithTenant(context.Background(), tenantA)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	ctx, audit := ingest.WithAuditOutbox(ctx)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	if _, _, err := svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{}); err != nil {
		t.Fatalf("ProcessInTx: %v", err)
	}
	if n := audit.Len(); n != 1 {
		t.Fatalf("outbox holds %d events, want 1", n)
	}
}

// #1933: ProcessInTx refuses a context with no AuditOutbox before it
// writes anything, so no sink event can be emitted ahead of the commit.
func TestProcessInTx_RequiresAuditOutbox(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")
	before := countEvidenceRecords(t, pool, tenantA)

	ctx, err := tenancy.WithTenant(context.Background(), tenantA)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	if _, _, err := svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{}); !errors.Is(err, ingest.ErrNoAuditOutbox) {
		t.Fatalf("ProcessInTx: got %v, want ErrNoAuditOutbox", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := countEvidenceRecords(t, pool, tenantA); got != before {
		t.Fatalf("evidence rows = %d, want %d", got, before)
	}
}

// #1933: a domain writer's system credential is recorded as such in
// provenance, not as an API key.
func TestProcessInTx_SystemCredentialProvenance(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := ingest.SystemCredential("system:test-writer", tenantA)

	var receipt ingest.Receipt
	err := inTenantTx(t, pool, tenantA, true, func(ctx context.Context, tx pgx.Tx) error {
		var perr error
		receipt, _, perr = svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{})
		return perr
	})
	if err != nil {
		t.Fatalf("ProcessInTx: %v", err)
	}
	var credType string
	if err := dbtest.NewMigratePool(t).QueryRow(context.Background(), `
		SELECT provenance->>'credential_type' FROM evidence_records WHERE id = $1
	`, receipt.RecordID).Scan(&credType); err != nil {
		t.Fatalf("fetch provenance: %v", err)
	}
	if credType != "system" {
		t.Fatalf("provenance credential_type = %q, want system", credType)
	}
}

func auditDecisions(t *testing.T, pool *pgxpool.Pool, tenant, credID string) []string {
	t.Helper()
	var out []string
	for _, e := range listAuditEntries(t, pool, tenant, credID) {
		out = append(out, e.Decision)
	}
	return out
}

func TestProcessInTx_CommitsWithCaller(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")
	before := countEvidenceRecords(t, pool, tenantA)

	var receipt ingest.Receipt
	err := inTenantTx(t, pool, tenantA, true, func(ctx context.Context, tx pgx.Tx) error {
		var perr error
		receipt, _, perr = svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{})
		return perr
	})
	if err != nil {
		t.Fatalf("ProcessInTx: %v", err)
	}
	if receipt.RecordID == "" || receipt.Hash == "" {
		t.Fatalf("receipt missing fields: %+v", receipt)
	}
	if got := countEvidenceRecords(t, pool, tenantA); got != before+1 {
		t.Fatalf("evidence rows = %d, want %d", got, before+1)
	}
	if d := auditDecisions(t, pool, tenantA, cred.ID); len(d) != 1 || d[0] != "accepted" {
		t.Fatalf("audit decisions = %v, want [accepted]", d)
	}
}

func TestProcessInTx_CallerRollbackDropsRecordAndAcceptAudit(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")
	before := countEvidenceRecords(t, pool, tenantA)

	err := inTenantTx(t, pool, tenantA, false, func(ctx context.Context, tx pgx.Tx) error {
		_, _, perr := svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{})
		return perr
	})
	if err != nil {
		t.Fatalf("ProcessInTx: %v", err)
	}
	if got := countEvidenceRecords(t, pool, tenantA); got != before {
		t.Fatalf("evidence rows after rollback = %d, want %d", got, before)
	}
	if d := auditDecisions(t, pool, tenantA, cred.ID); len(d) != 0 {
		t.Fatalf("audit decisions after rollback = %v, want none", d)
	}
}

func TestProcessInTx_RefusalAuditSurvivesCallerRollback(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")
	before := countEvidenceRecords(t, pool, tenantA)

	rec := record(t)
	rec.ObservedAt = timestamppb.New(time.Now().UTC().Add(-ingest.MaxObservedAtSkew - time.Hour))
	err := inTenantTx(t, pool, tenantA, true, func(ctx context.Context, tx pgx.Tx) error {
		_, decision, perr := svc.ProcessInTx(ctx, tx, rec, cred, ingest.LedgerOptions{})
		if decision != ingest.DecisionRejectedObservedAtSkew {
			t.Errorf("decision = %v, want RejectedObservedAtSkew", decision)
		}
		return perr
	})
	if !errors.Is(err, ingest.ErrObservedAtSkew) {
		t.Fatalf("ProcessInTx: got %v, want ErrObservedAtSkew", err)
	}
	if got := countEvidenceRecords(t, pool, tenantA); got != before {
		t.Fatalf("evidence rows = %d, want %d", got, before)
	}
	if d := auditDecisions(t, pool, tenantA, cred.ID); len(d) != 1 || d[0] != "rejected_observed_at_skew" {
		t.Fatalf("audit decisions = %v, want [rejected_observed_at_skew]", d)
	}
}

func TestProcessInTx_LedgerOptions(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")
	validUntil := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Microsecond)

	var receipt ingest.Receipt
	err := inTenantTx(t, pool, tenantA, true, func(ctx context.Context, tx pgx.Tx) error {
		var perr error
		receipt, _, perr = svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{
			FreshnessClass: "quarterly",
			ValidUntil:     validUntil,
		})
		return perr
	})
	if err != nil {
		t.Fatalf("ProcessInTx: %v", err)
	}
	admin := dbtest.NewMigratePool(t)
	var freshness string
	var got time.Time
	if err := admin.QueryRow(context.Background(), `
		SELECT freshness_class::text, valid_until FROM evidence_records WHERE id = $1
	`, receipt.RecordID).Scan(&freshness, &got); err != nil {
		t.Fatalf("fetch record: %v", err)
	}
	if freshness != "quarterly" || !got.Equal(validUntil) {
		t.Fatalf("freshness/valid_until = %s/%s, want quarterly/%s", freshness, got, validUntil)
	}
}

func TestProcessInTx_RefusesTenantMismatch(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")
	other := dbtest.SeedTenant(t, dbtest.NewMigratePool(t))

	err := inTenantTx(t, pool, other, false, func(ctx context.Context, tx pgx.Tx) error {
		_, decision, perr := svc.ProcessInTx(ctx, tx, record(t), cred, ingest.LedgerOptions{})
		if decision != ingest.DecisionRejectedUnauthenticated {
			t.Errorf("decision = %v, want RejectedUnauthenticated", decision)
		}
		return perr
	})
	if err == nil {
		t.Fatal("ProcessInTx under another tenant's context succeeded, want refusal")
	}
}
