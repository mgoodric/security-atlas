//go:build integration

// #1742 review B1 and B2: a freeze racing an import, and the observed_at
// bound on amendments. Shares the fixture in integration_test.go.

package historicalimport_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/evidence/historicalimport"
)

func (f *fixture) openPeriod(t *testing.T, name string, start, end time.Time) period.Period {
	t.Helper()
	p, err := f.periods.Create(f.ctx, period.CreateInput{
		Name: name, FrameworkVersionID: f.fw, PeriodStart: start, PeriodEnd: end, CreatedBy: "key_test_1742",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return p
}

func (f *fixture) assertRevision1Replays(t *testing.T, id uuid.UUID) {
	t.Helper()
	got, err := f.periods.Get(f.ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	h, err := f.periods.ReplayHash(f.ctx, id, 1)
	if err != nil || !bytes.Equal(h, got.FrozenHash) {
		t.Fatalf("replay revision 1 = %x (%v), stored frozen_hash %x", h, err, got.FrozenHash)
	}
}

// B1: a freeze that commits after the import's transaction began, but
// before the import takes the tenant lock, would admit the import's rows
// (ingested_at = the import's start) to a population its hash never
// covered. The import refuses that attempt and retries, so the period's
// original frozen_hash still replays. The records lie outside the period's
// window: the freeze hash is tenant-wide, so they would still change it.
func TestImport_FreezeBetweenBeginAndLockRetries(t *testing.T) {
	f := setup(t)
	p := f.openPeriod(t, "FY26 H1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	attempts := 0
	historicalimport.SetTestHooks(f.importer, func(attempt int) {
		attempts = attempt
		if attempt == 1 {
			if _, err := f.periods.Freeze(f.ctx, p.ID, "key_test_1742", time.Time{}); err != nil {
				t.Errorf("Freeze inside the import: %v", err)
			}
		}
	}, nil)
	if _, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{
		Source: "s", Records: []*evidencev1.EvidenceRecord{f.attestation(t, time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC))},
	}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (refused once, then retried)", attempts)
	}
	f.assertRevision1Replays(t, p.ID)
}

// waitForAdvisoryLockWaiter blocks until a session is waiting on this
// test's tenant key in namespace 14 (advisorylock.TenantEvidenceFreeze): the
// freeze under test has reached the lock the open import holds. The objid
// match keeps a concurrent test's tenant from satisfying the wait (#3053).
func (f *fixture) waitForAdvisoryLockWaiter(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n := f.count(t, `SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND classid = 14
			  AND objid = hashtext($1::uuid::text)::oid AND NOT granted`, f.tenant)
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no session waited on the tenant evidence-freeze lock")
}

// B1, and review S2/N-B: a freeze that starts while an import's transaction
// holds the tenant lock waits for the import to commit. It then stamps
// frozen_at from the database clock and hashes with the imported rows in
// view: its period_frozen log counts them, and its frozen_hash replays.
func TestImport_FreezeDuringOpenImportWaits(t *testing.T) {
	f := setup(t)
	p := f.openPeriod(t, "FY26 H1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	done := make(chan error, 1)
	historicalimport.SetTestHooks(f.importer, nil, func(attempt int) {
		if attempt != 1 {
			return
		}
		go func() {
			_, err := f.periods.Freeze(f.ctx, p.ID, "key_test_1742", time.Time{})
			done <- err
		}()
		// Deterministic: return only once the freeze is blocked on the lock
		// this open import holds.
		f.waitForAdvisoryLockWaiter(t)
		select {
		case err := <-done:
			t.Fatalf("freeze finished while the import held the lock: %v", err)
		default:
		}
	})
	if _, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{
		Source: "s", Records: []*evidencev1.EvidenceRecord{f.attestation(t, time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC))},
	}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	// The freeze's evidence set counts every row in the tenant's ledger,
	// the imported one included.
	total := f.evidenceCount(t)
	if n := f.count(t, `SELECT (detail->>'evidence_n')::int FROM audit_period_audit_log
		WHERE tenant_id = $1 AND audit_period_id = $2 AND action = 'period_frozen'`, f.tenant, p.ID); n != total || total != 1 {
		t.Fatalf("period_frozen evidence_n = %d, tenant evidence rows = %d; want the imported row counted", n, total)
	}
	f.assertRevision1Replays(t, p.ID)
}

// B2 (invariant #10): in a period frozen before its window ends, a record
// observed after frozen_at is not in the frozen population. It is neither
// a frozen hit nor listed in a revision, and the frozen reads keep the
// observed_at bound.
func TestImport_EarlyFreezeObservedAtBound(t *testing.T) {
	f := setup(t)
	frozenAt := time.Date(2025, 6, 30, 12, 0, 0, 0, time.UTC)
	p := f.frozenPeriod(t, "FY25 frozen early", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), frozenAt)
	before := f.attestation(t, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC))
	after := f.attestation(t, time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC))

	// Unnamed: only the pre-freeze record is a hit.
	_, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{
		Source: "s", Records: []*evidencev1.EvidenceRecord{before, after},
	})
	var fpe *historicalimport.FrozenPeriodError
	if !errors.As(err, &fpe) || len(fpe.Hits) != 1 || fpe.Hits[0].Records != 1 {
		t.Fatalf("err = %v, want one frozen hit with 1 record", err)
	}
	// A batch of only post-freeze records imports without amending.
	if _, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{
		Source: "s", Records: []*evidencev1.EvidenceRecord{f.attestation(t, time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC))},
	}); err != nil {
		t.Fatalf("post-freeze-only import: %v", err)
	}

	res, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{
		Source: "s", Records: []*evidencev1.EvidenceRecord{before, after},
		AmendFrozen: []uuid.UUID{p.ID}, AmendReason: "late FY25 H1 results",
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Amendments) != 1 || len(res.Amendments[0].Revision.EvidenceRecordIDs) != 1 {
		t.Fatalf("amendments = %+v, want one revision listing only the pre-freeze record", res.Amendments)
	}
	obs, err := f.periods.ControlState(f.ctx, p.ID, f.ctrl)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(obs) != 1 || !obs[0].ObservedAt.Equal(time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("frozen control state = %+v, want only the pre-freeze record", obs)
	}
	f.assertRevision1Replays(t, p.ID)
}

// Matt, 2026-11-24: amend_frozen without a reason is refused, and nothing is
// written.
func TestImport_AmendNeedsReason(t *testing.T) {
	f := setup(t)
	p := f.frozenPeriod(t, "FY25", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	_, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{
		Source: "s", Records: []*evidencev1.EvidenceRecord{f.attestation(t, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC))},
		AmendFrozen: []uuid.UUID{p.ID},
	})
	if !errors.Is(err, historicalimport.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if n := f.evidenceCount(t); n != 0 {
		t.Fatalf("evidence rows = %d, want 0", n)
	}
}
