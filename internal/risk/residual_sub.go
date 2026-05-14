// residual_sub.go — slice 020 event-driven residual recompute (AC-5).
//
// AC-5: "Residual recomputes within 60 seconds of any control_state change
// (via NATS subscriber)."
//
// Architecture note (recorded in docs/audit-log/020-...-decisions.md): slice
// 012 publishes NO control-state-change event — its IngestSubscriber consumes
// the evidence-ingest stream and writes `control_evaluations`, but emits
// nothing back to NATS. So there is no `control.state.changed` subject to
// subscribe to. The CAUSE of every control-state change is a new evidence
// record landing on slice 015's `evidence.ingest` stream. ResidualSubscriber
// therefore binds a THIRD durable JetStream consumer to that same stream
// (slice 015's ledger writer is the first, slice 012's IngestSubscriber the
// second). Three independent durable consumers on a Limits-retention stream
// each get every message — the residual recompute never races or blocks the
// other two.
//
// Race fix (recorded in the decisions log): slice 012's IngestSubscriber and
// this subscriber both fire on the same message, concurrently. This
// subscriber could otherwise read `control_evaluations` before slice 012 has
// written the new evaluation row, recomputing residual off stale state. The
// fix: ResidualDeriver.DeriveAndPersist is called with recompute=true, which
// re-evaluates each linked control from the ledger BEFORE reading its
// effectiveness. EvaluateControl is idempotent (append-only, latest-by-
// evaluated_at wins), so the extra evaluation row is harmless and the residual
// always reflects the just-ingested record.
//
// Constitutional invariant #2: the subscriber reads `control_evaluations` and
// `risk_control_links`, and writes ONLY `risks.residual_score`. It never
// touches `evidence_records`.
package risk

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
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ResidualConsumerDurable is the JetStream durable name for the residual
// recompute consumer. Distinct from slice 015's `evidence_ingest_worker` and
// slice 012's `evidence_eval_worker` so all three consumers are independent —
// each gets every message.
const ResidualConsumerDurable = "risk_residual_worker"

// deriverFactory builds a per-event ResidualDeriver. The subscriber runs as a
// process-level service; each event's recompute runs through an app-role
// Store + Engine so RLS is honored on the read and the residual write.
type deriverFactory func() *ResidualDeriver

// NewDeriverFactory returns a deriverFactory over an app-role pool. The same
// pool backs the risk.Store, the eval.Store, and the scope.Store cell
// resolver — every read and the residual write run RLS-honest.
func NewDeriverFactory(appPool *pgxpool.Pool) deriverFactory {
	return func() *ResidualDeriver {
		engine := eval.NewEngine(eval.NewStore(appPool), scope.NewStore(appPool))
		return NewResidualDeriver(NewStore(appPool), engine)
	}
}

// ResidualSubscriber binds a durable JetStream consumer to slice 015's
// evidence-ingest stream and recomputes residual for every risk linked to the
// affected control on each ingested record.
type ResidualSubscriber struct {
	stream     jetstream.Stream
	subject    string
	newDeriver deriverFactory
	// listLinkedRisks resolves the risks linked to a control, scoped to the
	// tenant on the message header. The subscriber runs it through an
	// app-role pool so RLS makes cross-tenant links invisible.
	appPool *pgxpool.Pool
	logger  *slog.Logger
	durable string
}

// NewResidualSubscriber constructs a ResidualSubscriber over a slice-015
// JetStream stream. `subject` is the stream's publish subject
// (streambuf.DefaultSubject). appPool is the app-role pool used to resolve
// linked risks per tenant.
func NewResidualSubscriber(stream jetstream.Stream, subject string, appPool *pgxpool.Pool, logger *slog.Logger) *ResidualSubscriber {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &ResidualSubscriber{
		stream:     stream,
		subject:    subject,
		newDeriver: NewDeriverFactory(appPool),
		appPool:    appPool,
		logger:     logger,
		durable:    ResidualConsumerDurable,
	}
}

// Start runs the subscriber until ctx is cancelled. It creates/updates its own
// durable consumer (distinct from slice 012/015's), so it receives every
// record independently.
func (s *ResidualSubscriber) Start(ctx context.Context) error {
	consumer, err := s.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       s.durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       60 * time.Second,
		MaxDeliver:    -1, // DeriveAndPersist is idempotent — safe to redeliver
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return fmt.Errorf("residual subscriber: consumer create: %w", err)
	}
	s.logger.Info("risk residual subscriber started", "durable", s.durable, "subject", s.subject)

	msgs, err := consumer.Messages(jetstream.PullMaxMessages(32))
	if err != nil {
		return fmt.Errorf("residual subscriber: messages: %w", err)
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
			s.logger.Warn("residual subscriber: pull error", "err", err.Error())
			continue
		}
		s.handle(ctx, msg)
	}
}

// handle decodes one evidence record and recomputes residual for every risk
// linked to its control. Tenant id rides on the message header (slice 015
// sets HeaderCredentialTenant). Ack semantics mirror slice 012's subscriber:
// Term a poison message (it would redeliver forever); Ack a record whose
// control_id is not a UUID or resolves to no linked risks (nothing to do);
// Nak a transient DB error so the redelivery retries. DeriveAndPersist is
// idempotent, so a redelivery just recomputes the same residual.
func (s *ResidualSubscriber) handle(ctx context.Context, msg jetstream.Msg) {
	var rec evidencev1.EvidenceRecord
	if err := proto.Unmarshal(msg.Data(), &rec); err != nil {
		s.logger.Warn("residual subscriber: unmarshal failed; terming", "err", err.Error())
		_ = msg.Term()
		return
	}
	tenantID, err := uuid.Parse(msg.Headers().Get(streambuf.HeaderCredentialTenant))
	if err != nil {
		s.logger.Warn("residual subscriber: bad tenant header; terming", "err", err.Error())
		_ = msg.Term()
		return
	}
	controlID, err := uuid.Parse(rec.ControlId)
	if err != nil {
		// control_id is a free-form string in the SDK (may be an SCF anchor
		// like "scf:VPM-04"). When it is not a UUID there is no control row
		// to resolve linked risks for. Ack and move on.
		s.logger.Debug("residual subscriber: control_id not a uuid; skipping", "control_ref", rec.ControlId)
		_ = msg.Ack()
		return
	}
	tctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		s.logger.Warn("residual subscriber: tenant ctx", "err", err.Error())
		_ = msg.Term()
		return
	}

	riskIDs, err := s.linkedRisks(tctx, tenantID, controlID)
	if err != nil {
		s.logger.Warn("residual subscriber: list linked risks; will redeliver", "control", controlID, "err", err.Error())
		_ = msg.NakWithDelay(2 * time.Second)
		return
	}
	if len(riskIDs) == 0 {
		// No risk links this control — nothing to recompute.
		_ = msg.Ack()
		return
	}

	deriver := s.newDeriver()
	for _, riskID := range riskIDs {
		// recompute=true: re-evaluate the control from the ledger before
		// reading effectiveness, closing the race with slice 012's ingest
		// subscriber (see file header).
		if _, err := deriver.DeriveAndPersist(tctx, riskID, true); err != nil {
			if errors.Is(err, ErrNotFound) {
				// The risk vanished between the link read and the recompute —
				// not retryable for this risk; continue with the rest.
				s.logger.Debug("residual subscriber: risk not found; skipping", "risk", riskID)
				continue
			}
			s.logger.Warn("residual subscriber: recompute; will redeliver", "risk", riskID, "err", err.Error())
			_ = msg.NakWithDelay(2 * time.Second)
			return
		}
	}
	if err := msg.Ack(); err != nil {
		s.logger.Warn("residual subscriber: ack failed", "err", err.Error())
	}
}

// linkedRisks resolves the risks that link the given control, scoped to the
// tenant. Runs through the app-role pool inside a tenant-GUC transaction so
// RLS makes cross-tenant links invisible.
func (s *ResidualSubscriber) linkedRisks(ctx context.Context, tenantID, controlID uuid.UUID) ([]uuid.UUID, error) {
	tx, err := s.appPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("residual subscriber: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := dbx.New(tx).ListRiskIDsLinkedToControl(ctx, dbx.ListRiskIDsLinkedToControlParams{
		TenantID:  pgUUID(tenantID),
		ControlID: pgUUID(controlID),
	})
	if err != nil {
		return nil, fmt.Errorf("list risk ids linked to control: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("residual subscriber: commit: %w", err)
	}
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = uuid.UUID(r.Bytes)
	}
	return out, nil
}

// discardWriter is a no-op io.Writer for the default discard logger.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
