//go:build integration

// #1830: ProcessInTx is the in-transaction variant domain writers use.
// These tests pin its transaction contract: an accepted record and its
// audit row commit or roll back with the caller's transaction, and a
// refusal's audit row survives the caller rolling back.

package ingest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// inTenantTx runs fn in an app-role transaction with tenant's GUC set,
// committing when commit is true and rolling back otherwise.
func inTenantTx(t *testing.T, pool *pgxpool.Pool, tenant string, commit bool, fn func(context.Context, pgx.Tx) error) error {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
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
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if commit {
		return tx.Commit(ctx)
	}
	return nil
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
