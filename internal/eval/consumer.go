// consumer.go — the background-job substrate for AC-2.
//
// AC-2 has two halves:
//
//  1. "Background job runs evaluation on every new evidence ingest (NATS
//     consumer from slice 015)" — IngestSubscriber binds a SECOND durable
//     JetStream consumer to slice 015's EVIDENCE_INGEST stream. Slice 015's
//     own consumer writes the ledger; this one reacts to the same records
//     and re-evaluates the affected control. Two independent durable
//     consumers on a Limits-retention stream each get every message — the
//     evaluation reaction never races or blocks the ledger write.
//
//  2. "and on a schedule for time-based recomputation" — Scheduler is a tick
//     loop (mirrors slice 021's exception.Expirer) that re-evaluates every
//     active control for every tenant. Time-based recompute matters because
//     freshness decays with wall-clock: a control that was `fresh` yesterday
//     is `stale` today even though no new evidence arrived.
//
// Both paths are READ-ONLY against the evidence ledger and write ONLY
// control_evaluations — constitutional invariant #2 holds in the background
// job exactly as it does in the synchronous engine.
package eval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultRecomputeInterval is the cadence of the time-based recompute. Hourly
// is a sensible default — fine-grained enough that a `realtime` (24h) control
// flips to `stale` within an hour of its window closing, cheap enough for a
// solo-deployment VM. The platform binary can override.
const DefaultRecomputeInterval = time.Hour

// EvalConsumerDurable is the JetStream durable name for the evaluation
// reaction consumer. Distinct from slice 015's `evidence_ingest_worker` so
// the two consumers are independent — each gets every message.
const EvalConsumerDurable = "evidence_eval_worker"

// engineFactory builds a per-tenant Engine. The scheduler and subscriber run
// as the migrator role (BYPASSRLS) to enumerate tenants, but each tenant's
// evaluation must run through an app-role-shaped Engine so RLS is honored on
// the writes. The factory closes over the app pool + a scope-cell resolver.
type engineFactory func() *Engine

// NewEngineFactory returns an engineFactory over an app-role pool. The same
// pool backs the eval Store and the scope.Store cell resolver.
func NewEngineFactory(appPool *pgxpool.Pool) engineFactory {
	return func() *Engine {
		return NewEngine(NewStore(appPool), scope.NewStore(appPool))
	}
}

// ---- Scheduler: time-based recompute ----

// Scheduler re-evaluates every active control for every tenant on a tick.
// Runs as the migrator role so it can enumerate tenants; each tenant's
// evaluation runs through an app-role Engine for RLS-honest writes.
type Scheduler struct {
	migratorPool *pgxpool.Pool
	newEngine    engineFactory
	logger       *slog.Logger
}

// NewScheduler constructs a Scheduler. migratorPool MUST be the migrator
// role (BYPASSRLS) — ListTenantsWithActiveControls enumerates every tenant.
// newEngine builds the app-role Engine each tenant's evaluation runs through.
func NewScheduler(migratorPool *pgxpool.Pool, newEngine engineFactory, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Scheduler{migratorPool: migratorPool, newEngine: newEngine, logger: logger}
}

// Run executes the recompute tick loop until ctx is cancelled. The first
// sweep runs immediately so a fresh deploy does not sit silent for an hour.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultRecomputeInterval
	}
	s.logger.Info("eval scheduler starting", "interval", interval.String())
	if _, err := s.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Error("eval scheduler initial sweep", "err", err.Error())
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("eval scheduler stopping")
			return nil
		case <-ticker.C:
			if _, err := s.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Error("eval scheduler sweep", "err", err.Error())
			}
		}
	}
}

// SweepOnce re-evaluates every active control for every tenant once. Returns
// the total rows written for observability. Exposed for integration tests.
func (s *Scheduler) SweepOnce(ctx context.Context) (int, error) {
	rows, err := dbx.New(s.migratorPool).ListTenantsWithActiveControls(ctx)
	if err != nil {
		return 0, fmt.Errorf("list tenants: %w", err)
	}
	total := 0
	for _, t := range rows {
		if !t.Valid {
			continue
		}
		tenantID := uuid.UUID(t.Bytes)
		tctx, err := tenancy.WithTenant(ctx, tenantID.String())
		if err != nil {
			s.logger.Error("eval scheduler: tenant ctx", "tenant", tenantID, "err", err.Error())
			continue
		}
		n, err := s.newEngine().EvaluateAll(tctx, TriggerScheduled, FarFuture)
		if err != nil {
			s.logger.Error("eval scheduler: evaluate tenant", "tenant", tenantID, "err", err.Error())
			continue
		}
		total += n
	}
	return total, nil
}

// ---- IngestSubscriber: evaluate on every ingested record ----

// IngestSubscriber binds a second durable JetStream consumer to slice 015's
// evidence-ingest stream and re-evaluates the affected control on each
// record. It reads the record's tenant + control_id from the message and
// runs EvaluateControl for that control.
type IngestSubscriber struct {
	stream    jetstream.Stream
	subject   string
	newEngine engineFactory
	logger    *slog.Logger
	durable   string
}

// NewIngestSubscriber constructs an IngestSubscriber over a slice-015
// JetStream stream. `subject` is the stream's publish subject
// (streambuf.DefaultSubject). newEngine builds the per-evaluation Engine.
func NewIngestSubscriber(stream jetstream.Stream, subject string, newEngine engineFactory, logger *slog.Logger) *IngestSubscriber {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &IngestSubscriber{
		stream:    stream,
		subject:   subject,
		newEngine: newEngine,
		logger:    logger,
		durable:   EvalConsumerDurable,
	}
}

// Start runs the subscriber until ctx is cancelled. It creates/updates its
// own durable consumer (distinct from slice 015's), so it receives every
// record independently of the ledger-writer consumer.
func (s *IngestSubscriber) Start(ctx context.Context) error {
	consumer, err := s.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       s.durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       60 * time.Second,
		MaxDeliver:    -1, // EvaluateControl is idempotent — safe to redeliver
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return fmt.Errorf("eval subscriber: consumer create: %w", err)
	}
	s.logger.Info("eval ingest subscriber started", "durable", s.durable, "subject", s.subject)

	msgs, err := consumer.Messages(jetstream.PullMaxMessages(32))
	if err != nil {
		return fmt.Errorf("eval subscriber: messages: %w", err)
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
			s.logger.Warn("eval subscriber: pull error", "err", err.Error())
			continue
		}
		s.handle(ctx, msg)
	}
}

// handle decodes one evidence record and re-evaluates its control. The
// tenant id rides on the message header (slice 015 sets
// HeaderCredentialTenant from the authenticated credential — the proto
// EvidenceRecord itself carries no tenant). Ack semantics: Ack on success or
// on a non-retryable decode failure (a poison message would redeliver
// forever); Nak on a transient evaluation error so the redelivery retries.
// EvaluateControl is idempotent, so a redelivery just appends another
// identical-computed-columns row — never corruption.
func (s *IngestSubscriber) handle(ctx context.Context, msg jetstream.Msg) {
	var rec evidencev1.EvidenceRecord
	if err := proto.Unmarshal(msg.Data(), &rec); err != nil {
		s.logger.Warn("eval subscriber: unmarshal failed; terming", "err", err.Error())
		_ = msg.Term()
		return
	}
	tenantID, err := uuid.Parse(msg.Headers().Get(streambuf.HeaderCredentialTenant))
	if err != nil {
		s.logger.Warn("eval subscriber: bad tenant header; terming", "err", err.Error())
		_ = msg.Term()
		return
	}
	controlID, err := uuid.Parse(rec.ControlId)
	if err != nil {
		// control_id is a free-form string in the SDK (may be an SCF anchor
		// like "scf:VPM-04"). When it is not a UUID there is no control row
		// to evaluate by id — slice 015's ledger writer still stores the
		// record under control_ref. Ack and move on; the scheduled sweep
		// picks up anchor-ref controls via their UUID.
		s.logger.Debug("eval subscriber: control_id not a uuid; skipping eval", "control_ref", rec.ControlId)
		_ = msg.Ack()
		return
	}
	tctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		s.logger.Warn("eval subscriber: tenant ctx", "err", err.Error())
		_ = msg.Term()
		return
	}
	if _, err := s.newEngine().EvaluateControl(tctx, controlID, TriggerIngest, FarFuture); err != nil {
		if errors.Is(err, ErrControlNotFound) {
			// The record references a control that does not exist in-tenant
			// — not retryable. Ack so it does not redeliver forever.
			s.logger.Debug("eval subscriber: control not found; acking", "control", controlID)
			_ = msg.Ack()
			return
		}
		s.logger.Warn("eval subscriber: evaluate; will redeliver", "control", controlID, "err", err.Error())
		_ = msg.NakWithDelay(2 * time.Second)
		return
	}
	if err := msg.Ack(); err != nil {
		s.logger.Warn("eval subscriber: ack failed", "err", err.Error())
	}
}

// discardWriter is a no-op io.Writer for the default discard logger.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
