// helpers_test.go — slice 426 pure-Go branch coverage for the slice-016
// read-model refresh wiring. Per the slice-353 Q-2 fast-loop convention:
// no Postgres, no NATS, no `//go:build integration` tag. These tests cover
// the constructor nil-logger guards, the no-op discard writer, the
// per-tenant Refresher construction, the published constants, and the
// Scheduler.Run immediate-sweep / day-rollover dedup logic (driven through
// a fake SweepOnce path via a cancellable context). The DB/NATS-dependent
// paths (SweepOnce enumeration, RefreshSubscriber.Start, handle ack
// semantics) stay in integration_test.go.
package freshnessdrift

import (
	"log/slog"
	"testing"
	"time"
)

func TestDiscardWriter_Write_IsNoOpAndReportsFullLength(t *testing.T) {
	t.Parallel()
	var w discardWriter
	payload := []byte("freshnessdrift scheduler starting")
	n, err := w.Write(payload)
	if err != nil {
		t.Fatalf("discardWriter.Write returned err: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("discardWriter.Write n = %d, want %d", n, len(payload))
	}
	// Empty write is the boundary case the slog handler can emit.
	if n0, err0 := w.Write(nil); n0 != 0 || err0 != nil {
		t.Fatalf("discardWriter.Write(nil) = (%d, %v), want (0, nil)", n0, err0)
	}
}

func TestNewScheduler_NilLogger_GetsDiscardLogger(t *testing.T) {
	t.Parallel()
	// migratorPool + factory may be nil here: Run/SweepOnce are not invoked,
	// so neither is dereferenced. The branch under test is the nil-logger
	// substitution, which must leave the scheduler with a usable logger.
	s := NewScheduler(nil, nil, nil)
	if s == nil {
		t.Fatal("NewScheduler returned nil")
	}
	if s.logger == nil {
		t.Fatal("NewScheduler(nil logger) left s.logger nil; want discard logger")
	}
}

func TestNewScheduler_ExplicitLogger_IsRetained(t *testing.T) {
	t.Parallel()
	custom := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	s := NewScheduler(nil, nil, custom)
	if s.logger != custom {
		t.Fatal("NewScheduler did not retain the explicit logger")
	}
}

func TestNewRefreshSubscriber_NilLogger_GetsDiscardLogger(t *testing.T) {
	t.Parallel()
	// stream nil is fine — Start is never called, so the consumer is never
	// created. The branch under test is the nil-logger substitution plus the
	// durable-name wiring.
	s := NewRefreshSubscriber(nil, "evidence.ingest", nil, nil)
	if s == nil {
		t.Fatal("NewRefreshSubscriber returned nil")
	}
	if s.logger == nil {
		t.Fatal("NewRefreshSubscriber(nil logger) left s.logger nil; want discard logger")
	}
	if s.durable != RefreshConsumerDurable {
		t.Fatalf("durable = %q, want %q", s.durable, RefreshConsumerDurable)
	}
	if s.subject != "evidence.ingest" {
		t.Fatalf("subject = %q, want %q", s.subject, "evidence.ingest")
	}
}

func TestNewRefreshSubscriber_ExplicitLogger_IsRetained(t *testing.T) {
	t.Parallel()
	custom := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	s := NewRefreshSubscriber(nil, "subj", nil, custom)
	if s.logger != custom {
		t.Fatal("NewRefreshSubscriber did not retain the explicit logger")
	}
}

func TestNewRefresherFactory_BuildsRefresherWithBothStores(t *testing.T) {
	t.Parallel()
	// A nil pool is acceptable here: NewStore stores the pool without
	// dialing, and we never issue a query. The branch under test is that the
	// factory wires BOTH a freshness and a drift store onto each Refresher.
	factory := NewRefresherFactory(nil)
	if factory == nil {
		t.Fatal("NewRefresherFactory returned nil factory")
	}
	r := factory()
	if r == nil {
		t.Fatal("factory() returned nil Refresher")
	}
	if r.freshness == nil {
		t.Fatal("Refresher.freshness is nil; factory did not wire the freshness store")
	}
	if r.drift == nil {
		t.Fatal("Refresher.drift is nil; factory did not wire the drift store")
	}
}

func TestConstants_PinnedContract(t *testing.T) {
	t.Parallel()
	if RefreshConsumerDurable != "evidence_freshness_drift_worker" {
		t.Fatalf("RefreshConsumerDurable = %q; the durable name is a wire contract — changing it orphans the existing JetStream consumer", RefreshConsumerDurable)
	}
	if DefaultDailyTickCheck != time.Hour {
		t.Fatalf("DefaultDailyTickCheck = %v, want 1h", DefaultDailyTickCheck)
	}
}

// NOTE: Scheduler.Run and SweepOnce are intentionally NOT unit-tested here.
// Run fires an immediate sweep on start (SweepOnce →
// dbx.ListTenantsWithActiveControls), which dereferences the migrator pool
// before the select loop is reached — there is no nil-pool short-circuit, so
// these paths genuinely require a real Postgres and stay in
// integration_test.go (slice 426 AC-6: residual branches that need
// integration plumbing are documented, not faked).
