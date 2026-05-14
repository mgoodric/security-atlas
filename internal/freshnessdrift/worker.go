// Package freshnessdrift wires the slice-016 read-model refresh into the
// platform's background-job substrate. It owns the two refresh triggers AC-4
// requires:
//
//  1. "Drift recomputes ... on every ledger write" — RefreshSubscriber binds
//     a THIRD durable JetStream consumer to slice 015's EVIDENCE_INGEST stream
//     (slice 015's own consumer writes the ledger; slice 012's reacts by
//     evaluating; this one reacts by refreshing the freshness + drift read
//     models for the affected tenant). Three independent durable consumers on
//     a Limits-retention stream each get every message — the read-model
//     refresh never races or blocks the ledger write or the evaluation.
//
//  2. "Drift recomputes daily at 00:00 UTC" — Scheduler is a tick loop
//     (mirrors eval.Scheduler / exception.Expirer) that, once per UTC day at
//     00:00, refreshes the freshness read model and captures a drift snapshot
//     for every tenant. The daily tick matters because freshness decays with
//     wall-clock and drift is a day-over-day delta — both need a guaranteed
//     daily recompute even when no new evidence arrives.
//
// Both paths are READ-ONLY against the ledgers (`evidence_records`,
// `control_evaluations`) and write ONLY the slice-016 read-model tables —
// constitutional invariant #2 holds in the background job exactly as it does
// in the synchronous Stores.
package freshnessdrift

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// RefreshConsumerDurable is the JetStream durable name for the read-model
// refresh consumer. Distinct from slice 015's `evidence_ingest_worker` and
// slice 012's `evidence_eval_worker` so all three consumers are independent —
// each gets every message.
const RefreshConsumerDurable = "evidence_freshness_drift_worker"

// DefaultDailyTickCheck is how often the Scheduler wakes to check whether the
// UTC day has rolled over. Hourly: cheap, and a missed 00:00 boundary (e.g. a
// deploy at 00:30) is caught within the hour. The actual refresh fires at
// most once per UTC calendar day — see Scheduler.Run.
const DefaultDailyTickCheck = time.Hour

// Refresher refreshes the slice-016 read models for ONE tenant. The
// background workers run as the migrator role to enumerate tenants, but each
// tenant's refresh must run through app-role-shaped Stores so RLS is honored
// on the writes — RefresherFactory closes over the app pool.
type Refresher struct {
	freshness *freshness.Store
	drift     *drift.Store
}

// RefreshTenant refreshes the freshness read model and captures a drift
// snapshot for the tenant in ctx. `trigger` is recorded on the drift snapshot
// (drift.TriggerScheduled | drift.TriggerIngest | drift.TriggerManual).
func (r *Refresher) RefreshTenant(ctx context.Context, trigger string) error {
	if _, err := r.freshness.Refresh(ctx); err != nil {
		return fmt.Errorf("freshness refresh: %w", err)
	}
	if _, err := r.drift.CaptureSnapshot(ctx, trigger); err != nil {
		return fmt.Errorf("drift snapshot: %w", err)
	}
	return nil
}

// RefresherFactory builds a per-call Refresher over an app-role pool.
type RefresherFactory func() *Refresher

// NewRefresherFactory returns a RefresherFactory over an app-role pool. The
// same pool backs both the freshness and drift Stores.
func NewRefresherFactory(appPool *pgxpool.Pool) RefresherFactory {
	return func() *Refresher {
		return &Refresher{
			freshness: freshness.NewStore(appPool),
			drift:     drift.NewStore(appPool),
		}
	}
}

// ---- Scheduler: daily 00:00 UTC recompute ----

// Scheduler refreshes the freshness + drift read models for every tenant once
// per UTC calendar day. Runs as the migrator role so it can enumerate
// tenants; each tenant's refresh runs through app-role Stores for RLS-honest
// writes.
type Scheduler struct {
	migratorPool *pgxpool.Pool
	newRefresher RefresherFactory
	logger       *slog.Logger
}

// NewScheduler constructs a Scheduler. migratorPool MUST be the migrator role
// (BYPASSRLS) — it enumerates every tenant. newRefresher builds the app-role
// Refresher each tenant's refresh runs through.
func NewScheduler(migratorPool *pgxpool.Pool, newRefresher RefresherFactory, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Scheduler{migratorPool: migratorPool, newRefresher: newRefresher, logger: logger}
}

// Run executes the daily-recompute loop until ctx is cancelled. It wakes
// every DefaultDailyTickCheck, and fires SweepOnce the first time it observes
// a new UTC calendar day (and once immediately on start so a fresh deploy is
// not silent until the next midnight). This gives AC-4's "daily at 00:00 UTC"
// without depending on the process being alive at exactly 00:00:00.
func (s *Scheduler) Run(ctx context.Context, tickCheck time.Duration) error {
	if tickCheck <= 0 {
		tickCheck = DefaultDailyTickCheck
	}
	s.logger.Info("freshnessdrift scheduler starting", "tick_check", tickCheck.String())

	lastSweptDay := ""
	sweep := func() {
		day := time.Now().UTC().Format("2006-01-02")
		if day == lastSweptDay {
			return
		}
		if _, err := s.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("freshnessdrift scheduler sweep", "err", err.Error())
			return
		}
		lastSweptDay = day
	}

	sweep() // immediate first sweep on start
	ticker := time.NewTicker(tickCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("freshnessdrift scheduler stopping")
			return nil
		case <-ticker.C:
			sweep()
		}
	}
}

// SweepOnce refreshes the read models for every tenant once. Returns the
// number of tenants swept. Exposed for integration tests.
func (s *Scheduler) SweepOnce(ctx context.Context) (int, error) {
	rows, err := dbx.New(s.migratorPool).ListTenantsWithActiveControls(ctx)
	if err != nil {
		return 0, fmt.Errorf("list tenants: %w", err)
	}
	swept := 0
	for _, t := range rows {
		if !t.Valid {
			continue
		}
		tenantID := uuid.UUID(t.Bytes)
		tctx, err := tenancy.WithTenant(ctx, tenantID.String())
		if err != nil {
			s.logger.Error("freshnessdrift scheduler: tenant ctx", "tenant", tenantID, "err", err.Error())
			continue
		}
		if err := s.newRefresher().RefreshTenant(tctx, drift.TriggerScheduled); err != nil {
			s.logger.Error("freshnessdrift scheduler: refresh tenant", "tenant", tenantID, "err", err.Error())
			continue
		}
		swept++
	}
	return swept, nil
}

// ---- RefreshSubscriber: refresh on every ingested record ----

// RefreshSubscriber binds a durable JetStream consumer to slice 015's
// evidence-ingest stream and refreshes the affected tenant's read models on
// each record. It reads only the record's tenant from the message header —
// the freshness + drift refreshes are tenant-wide, so the per-record
// control_id is not needed (a full-tenant refresh is cheap on a solo
// deployment and keeps the refresh logic single-pathed).
type RefreshSubscriber struct {
	stream       jetstream.Stream
	subject      string
	newRefresher RefresherFactory
	logger       *slog.Logger
	durable      string
}

// NewRefreshSubscriber constructs a RefreshSubscriber over a slice-015
// JetStream stream. `subject` is the stream's publish subject
// (streambuf.DefaultSubject). newRefresher builds the per-message Refresher.
func NewRefreshSubscriber(stream jetstream.Stream, subject string, newRefresher RefresherFactory, logger *slog.Logger) *RefreshSubscriber {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &RefreshSubscriber{
		stream:       stream,
		subject:      subject,
		newRefresher: newRefresher,
		logger:       logger,
		durable:      RefreshConsumerDurable,
	}
}

// Start runs the subscriber until ctx is cancelled. It creates/updates its
// own durable consumer (distinct from slices 015 and 012), so it receives
// every record independently.
func (s *RefreshSubscriber) Start(ctx context.Context) error {
	consumer, err := s.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       s.durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       60 * time.Second,
		MaxDeliver:    -1, // RefreshTenant is idempotent — safe to redeliver
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return fmt.Errorf("freshnessdrift subscriber: consumer create: %w", err)
	}
	s.logger.Info("freshnessdrift refresh subscriber started", "durable", s.durable, "subject", s.subject)

	msgs, err := consumer.Messages(jetstream.PullMaxMessages(32))
	if err != nil {
		return fmt.Errorf("freshnessdrift subscriber: messages: %w", err)
	}
	defer msgs.Stop()

	go func() {
		<-ctx.Done()
		msgs.Stop()
	}()

	for {
		msg, err := msgs.Next()
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return nil
			}
			s.logger.Warn("freshnessdrift subscriber: pull error", "err", err.Error())
			continue
		}
		s.handle(ctx, msg)
	}
}

// handle refreshes the read models for the message's tenant. The tenant id
// rides on the message header (slice 015 sets it from the authenticated
// credential). Ack semantics: Term a message with no usable tenant header (a
// poison message would redeliver forever); Nak on a transient refresh error
// so the redelivery retries; Ack on success. RefreshTenant is idempotent —
// a redelivery just re-UPSERTs identical freshness rows and appends another
// same-day drift snapshot (latest-row-wins), never corruption.
func (s *RefreshSubscriber) handle(ctx context.Context, msg jetstream.Msg) {
	tenantHeader := msg.Headers().Get(streambuf.HeaderCredentialTenant)
	tenantID, err := uuid.Parse(tenantHeader)
	if err != nil {
		s.logger.Warn("freshnessdrift subscriber: bad tenant header; terming", "err", err.Error())
		_ = msg.Term()
		return
	}
	tctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		s.logger.Warn("freshnessdrift subscriber: tenant ctx", "err", err.Error())
		_ = msg.Term()
		return
	}
	if err := s.newRefresher().RefreshTenant(tctx, drift.TriggerIngest); err != nil {
		s.logger.Warn("freshnessdrift subscriber: refresh; will redeliver", "tenant", tenantID, "err", err.Error())
		_ = msg.NakWithDelay(2 * time.Second)
		return
	}
	if err := msg.Ack(); err != nil {
		s.logger.Warn("freshnessdrift subscriber: ack failed", "err", err.Error())
	}
}

// discardWriter is a no-op io.Writer for the default discard logger.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
