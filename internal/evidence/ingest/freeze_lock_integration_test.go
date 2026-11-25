//go:build integration

// #3038: live ingest against a concurrent audit-period freeze. A row's
// ingested_at is its transaction's start, but it is visible only once that
// transaction commits, so a freeze that runs in between could stamp a
// frozen_at after the row's ingested_at without having hashed the row.
// Neither test relies on timing: one freezes inside the ingest's open
// transaction through a hook, the other waits on pg_locks.

package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type freezeFixture struct {
	admin   *pgxpool.Pool
	app     *pgxpool.Pool
	tenant  string
	ctrl    uuid.UUID
	svc     *ingest.Service
	periods *period.Store
	ctx     context.Context
	period  period.Period
}

func setupFreezeFixture(t *testing.T) *freezeFixture {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
			`DELETE FROM evidence_audit_log WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})

	fwID, verID := uuid.New(), uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, '#3038 test framework', $2, 'test')`, fwID, "f3038-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')`, verID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	ctrl := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, '#3038 control', 'AAA', 'automated', 'test-bundle-3038')`, ctrl, tenant); err != nil {
		t.Fatalf("seed control: %v", err)
	}

	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	f := &freezeFixture{
		admin: admin, app: app, tenant: tenant, ctrl: ctrl,
		svc:     ingest.New(app, reg),
		periods: period.NewStore(app),
		ctx:     dbtest.WithTenantCtx(t, tenant),
	}
	now := time.Now().UTC()
	f.period, err = f.periods.Create(f.ctx, period.CreateInput{
		Name: "live", FrameworkVersionID: verID,
		PeriodStart: now.AddDate(0, -1, 0), PeriodEnd: now.AddDate(0, 1, 0), CreatedBy: "key_test_3038",
	})
	if err != nil {
		t.Fatalf("Create period: %v", err)
	}
	return f
}

func (f *freezeFixture) cred() credstore.Credential {
	return credstore.Credential{ID: "key_admin_" + uuid.NewString()[:8], TenantID: f.tenant, IsAdmin: true}
}

// attestation is a live manual.attestation record for the fixture's
// control, observed an hour ago: inside any freeze taken now.
func (f *freezeFixture) attestation(t *testing.T) *evidencev1.EvidenceRecord {
	t.Helper()
	payload, err := structpb.NewStruct(map[string]any{
		"attestor":         "ciso@example.com",
		"statement":        "Access reviews completed",
		"result":           "pass",
		"period_start":     "2026-01-01",
		"period_end":       "2026-12-31",
		"report_reference": "#3038",
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey:    "live-" + uuid.NewString(),
		EvidenceKind:      "manual.attestation.v1",
		SchemaVersion:     "1.3.0",
		ControlId:         f.ctrl.String(),
		Scope:             []*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}},
		ObservedAt:        timestamppb.New(time.Now().Add(-time.Hour)),
		Result:            evidencev1.Result_RESULT_PASS,
		Payload:           payload,
		SourceAttribution: &evidencev1.SourceAttribution{ActorType: "human", ActorId: "ciso@example.com"},
	}
}

func (f *freezeFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.admin.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// assertHashReplays recomputes the period's revision-1 hash at its stored
// frozen_at (both ADR 0003 bounds) and compares it with the stored hash.
func (f *freezeFixture) assertHashReplays(t *testing.T) period.Period {
	t.Helper()
	got, err := f.periods.Get(f.ctx, f.period.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	h, err := f.periods.ReplayHash(f.ctx, f.period.ID, 1)
	if err != nil || !bytes.Equal(h, got.FrozenHash) {
		t.Fatalf("replay revision 1 = %x (%v), stored frozen_hash %x", h, err, got.FrozenHash)
	}
	return got
}

// A freeze that commits after the ingest's transaction began, but before
// the ingest takes the lock, would admit the row (ingested_at = that start,
// before frozen_at) to a population its hash never covered. The ingest
// refuses that attempt and retries in a new transaction, so the row lands
// after frozen_at and the stored hash still replays.
func TestProcess_FreezeBetweenBeginAndLockRetries(t *testing.T) {
	f := setupFreezeFixture(t)
	attempts := 0
	ingest.SetTestHooks(f.svc, func(attempt int) {
		attempts = attempt
		if attempt == 1 {
			if _, err := f.periods.Freeze(f.ctx, f.period.ID, "key_test_3038", time.Time{}); err != nil {
				t.Errorf("Freeze inside the ingest: %v", err)
			}
		}
	}, nil)
	receipt, decision, err := f.svc.Process(context.Background(), f.attestation(t), f.cred())
	if err != nil || decision != ingest.DecisionAccepted {
		t.Fatalf("Process: decision %s, err %v", decision, err)
	}
	got := f.assertHashReplays(t)
	if !receipt.IngestedAt.After(*got.FrozenAt) {
		t.Fatalf("ingested_at %s not after frozen_at %s", receipt.IngestedAt, *got.FrozenAt)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (refused once, then retried)", attempts)
	}
}

// waitForFreezeLockWaiter blocks until a session waits on this test's
// tenant key in namespace 14 (advisorylock.TenantEvidenceFreeze); the objid
// match keeps a concurrent test's tenant from satisfying the wait (#3053).
func (f *freezeFixture) waitForFreezeLockWaiter(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if f.count(t, `SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND classid = 14
			  AND objid = hashtext($1::uuid::text)::oid AND NOT granted`, f.tenant) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no session waited on the tenant evidence-freeze lock")
}

// A freeze that starts while an ingest's row is written but not committed
// waits for the commit, then hashes with the row in view: its period_frozen
// log counts it and the stored hash replays.
func TestProcess_FreezeDuringOpenIngestWaits(t *testing.T) {
	f := setupFreezeFixture(t)
	done := make(chan error, 1)
	ingest.SetTestHooks(f.svc, nil, func(attempt int) {
		if attempt != 1 {
			return
		}
		go func() {
			_, err := f.periods.Freeze(f.ctx, f.period.ID, "key_test_3038", time.Time{})
			done <- err
		}()
		f.waitForFreezeLockWaiter(t)
		select {
		case err := <-done:
			t.Fatalf("freeze finished while the ingest held the lock: %v", err)
		default:
		}
	})
	if _, decision, err := f.svc.Process(context.Background(), f.attestation(t), f.cred()); err != nil || decision != ingest.DecisionAccepted {
		t.Fatalf("Process: decision %s, err %v", decision, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if n := f.count(t, `SELECT (detail->>'evidence_n')::int FROM audit_period_audit_log
		WHERE tenant_id = $1 AND audit_period_id = $2 AND action = 'period_frozen'`, f.tenant, f.period.ID); n != 1 {
		t.Fatalf("period_frozen evidence_n = %d, want the ingested row counted", n)
	}
	f.assertHashReplays(t)
}

// processInDomainTx runs ProcessInTx the way a domain writer does: in its own
// tenant transaction at iso, rolled back when ProcessInTx fails.
func (f *freezeFixture) processInDomainTx(t *testing.T, rec *evidencev1.EvidenceRecord, iso pgx.TxIsoLevel) (ingest.Decision, error) {
	t.Helper()
	ctx, _ := ingest.WithAuditOutbox(f.ctx)
	tx, err := f.app.BeginTx(ctx, pgx.TxOptions{IsoLevel: iso})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	_, decision, perr := f.svc.ProcessInTx(ctx, tx, rec, f.cred(), ingest.LedgerOptions{})
	if perr != nil {
		return decision, perr
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return decision, nil
}

// A domain writer's transaction cannot be retried by ingest, so a freeze
// that commits between its BEGIN and the lock refuses the write: nothing
// lands, the refusal is audited as rejected_concurrent_freeze rather than an
// internal error, and the period's hash still replays (#3060).
func TestProcessInTx_FreezeBetweenBeginAndLockRefuses(t *testing.T) {
	f := setupFreezeFixture(t)
	ingest.SetTestHooks(f.svc, func(int) {
		if _, err := f.periods.Freeze(f.ctx, f.period.ID, "key_test_3060", time.Time{}); err != nil {
			t.Errorf("Freeze inside the domain transaction: %v", err)
		}
	}, nil)
	rec := f.attestation(t)
	decision, err := f.processInDomainTx(t, rec, pgx.ReadCommitted)
	if !errors.Is(err, ingest.ErrConcurrentFreeze) || decision != ingest.DecisionRejectedConcurrentFreeze {
		t.Fatalf("ProcessInTx: decision %s, err %v; want rejected_concurrent_freeze / ErrConcurrentFreeze", decision, err)
	}
	if n := f.count(t, `SELECT count(*) FROM evidence_records WHERE tenant_id = $1 AND idempotency_key = $2`, f.tenant, rec.IdempotencyKey); n != 0 {
		t.Fatalf("evidence rows = %d, want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM evidence_audit_log WHERE tenant_id = $1 AND idempotency_key = $2 AND decision = 'rejected_concurrent_freeze'`, f.tenant, rec.IdempotencyKey); n != 1 {
		t.Fatalf("rejected_concurrent_freeze audit rows = %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM evidence_audit_log WHERE tenant_id = $1 AND idempotency_key = $2 AND decision <> 'rejected_concurrent_freeze'`, f.tenant, rec.IdempotencyKey); n != 0 {
		t.Fatalf("other audit rows = %d, want 0", n)
	}
	f.assertHashReplays(t)
}

// The concurrent-freeze check reads a snapshot taken after the lock, which
// only READ COMMITTED gives. A domain transaction at a higher level fails
// closed instead of writing a row the check could not vouch for (#3060).
func TestProcessInTx_RefusesAboveReadCommitted(t *testing.T) {
	f := setupFreezeFixture(t)
	for _, iso := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		rec := f.attestation(t)
		if _, err := f.processInDomainTx(t, rec, iso); !errors.Is(err, ingest.ErrIsolationLevel) {
			t.Fatalf("%s: err %v, want ErrIsolationLevel", iso, err)
		}
		if n := f.count(t, `SELECT count(*) FROM evidence_records WHERE tenant_id = $1 AND idempotency_key = $2`, f.tenant, rec.IdempotencyKey); n != 0 {
			t.Fatalf("%s: evidence rows = %d, want 0", iso, n)
		}
	}
}
