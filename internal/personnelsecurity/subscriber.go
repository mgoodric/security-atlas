// subscriber.go — the OE-661 runtime wiring for the OE-630 personnel-security
// primitive's HRIS trigger.
//
// The HRIS connectors (Rippling + BambooHR, slices 491/573) are source-side
// processes: pull and subscribe profiles alike, every worker-lifecycle fact
// leaves the connector via the one canonical platform wire —
// EvidenceIngestService.Push — and lands on slice 015's EVIDENCE_INGEST
// JetStream stream (constitutional invariant #3). The platform-side point
// where worker status transitions become observable is therefore that stream,
// and WorkerEventSubscriber binds its own durable consumer to it (the same
// shape as slice 012's eval subscriber and slice 016's freshness/drift
// subscriber — independent durable consumers on a Limits-retention stream
// each get every message, so the checklist reaction never races or blocks the
// ledger write).
//
// For each hris.worker_lifecycle.v1 record the subscriber decodes the
// PII-bounded worker payload and invokes Store.HandleWorkerEvent under the
// record's tenant context. Idempotency is two-layered:
//
//   - The connector's push idempotency key is HOUR-TRUNCATED (a roster
//     re-observation within the hour collapses at the ledger), but a fresh
//     hour re-observes the same lifecycle fact as a NEW record. The
//     subscriber therefore derives the STABLE source event id from the
//     lifecycle fact itself — worker id + workflow kind + the joiner/leaver
//     date — so every re-observation of the same fact maps to the same
//     (tenant, source, source_event_id) and HandleWorkerEvent's dedup (plus
//     the DB unique index) yields exactly one checklist.
//   - A rehire (new start date) or a later termination (new end date) is a
//     genuinely new event and gets a new checklist.
//
// Boundary (parent OE-630): this consumer creates evidence-and-task-tracking
// checklists ONLY. It never provisions or deprovisions access.
package personnelsecurity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// WorkerEventConsumerDurable is the JetStream durable name for the
// personnel-security worker-event consumer. Distinct from slice 015's
// `evidence_ingest_worker`, slice 012's `evidence_eval_worker`, and slice
// 016's `evidence_freshness_drift_worker` so all consumers are independent.
const WorkerEventConsumerDurable = "personnel_security_worker"

// WorkerLifecycleEvidenceKind is the evidence kind the HRIS connector family
// emits (connectors/hris/workerrecord.EvidenceKind). Declared here rather
// than imported so the platform's filter is explicit at the consumption
// point; a drift between the two constants is caught by the integration test
// that pushes a real connector-built record.
const WorkerLifecycleEvidenceKind = "hris.worker_lifecycle.v1"

// EventRecencyWindow bounds how old a joiner/leaver fact may be (relative to
// the record's observed-at) and still trigger a checklist. The pull profile
// re-observes the FULL roster — including workers hired years ago and leavers
// long gone — and a first connector sync must not backfill a checklist (and,
// for leavers, an instantly-overdue high-priority notification) for every
// historical lifecycle fact. Thirty days matches the honest meaning of
// "event": recent enough that the onboarding/offboarding work is plausibly
// still actionable. A fact with NO date is exempt — the first observation is
// the platform's first knowledge of it.
const EventRecencyWindow = 30 * 24 * time.Hour

// checklistCreator narrows what the subscriber needs from the Store — the
// single HandleWorkerEvent call invoked from handle(). A seam so unit tests
// can stub the store without Postgres.
type checklistCreator interface {
	HandleWorkerEvent(ctx context.Context, w worker.Worker, sourceEventID string, controlID uuid.UUID) (Checklist, error)
}

// WorkerEventSubscriber binds a durable JetStream consumer to slice 015's
// evidence-ingest stream and creates joiner/leaver checklists from HRIS
// worker-lifecycle records.
type WorkerEventSubscriber struct {
	stream  jetstream.Stream
	subject string
	creator checklistCreator
	logger  *slog.Logger
	durable string
}

// NewWorkerEventSubscriber constructs a WorkerEventSubscriber over a
// slice-015 JetStream stream. `subject` is the stream's publish subject
// (streambuf.DefaultSubject). `store` must be app-role-backed so the
// checklist writes are RLS-honest.
func NewWorkerEventSubscriber(stream jetstream.Stream, subject string, store *Store, logger *slog.Logger) *WorkerEventSubscriber {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &WorkerEventSubscriber{
		stream:  stream,
		subject: subject,
		creator: store,
		logger:  logger,
		durable: WorkerEventConsumerDurable,
	}
}

// Start runs the subscriber until ctx is cancelled. It creates/updates its
// own durable consumer, so it receives every record independently of the
// ledger-writer and the other reaction consumers.
func (s *WorkerEventSubscriber) Start(ctx context.Context) error {
	consumer, err := s.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       s.durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       60 * time.Second,
		MaxDeliver:    -1, // HandleWorkerEvent is idempotent — safe to redeliver
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return fmt.Errorf("personnel security subscriber: consumer create: %w", err)
	}
	s.logger.Info("personnel security worker-event subscriber started", "durable", s.durable, "subject", s.subject)

	msgs, err := consumer.Messages(jetstream.PullMaxMessages(32))
	if err != nil {
		return fmt.Errorf("personnel security subscriber: messages: %w", err)
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
			s.logger.Warn("personnel security subscriber: pull error", "err", err.Error())
			continue
		}
		s.handle(ctx, msg)
	}
}

// handle decodes one evidence record and, when it is a joiner/leaver
// lifecycle fact inside the recency window, creates the checklist. Ack
// semantics mirror the eval subscriber: Ack on success AND on every
// non-retryable skip (wrong kind, non-event status, stale fact, malformed
// payload — redelivery would fail identically); Term on a broken envelope
// (bad tenant header); Nak on a transient store error so redelivery retries.
// A redelivery after a mid-flight failure re-runs HandleWorkerEvent, whose
// source-event dedup (backed by the personnel_security_checklists unique
// index on tenant+source+source_event_id) collapses it — never a duplicate
// checklist.
func (s *WorkerEventSubscriber) handle(ctx context.Context, msg jetstream.Msg) {
	hdr := msg.Headers()
	// Fast-path filter on the published kind header — no payload decode for
	// the overwhelming majority of evidence traffic that is not HRIS.
	if hdr.Get(streambuf.HeaderEvidenceKind) != WorkerLifecycleEvidenceKind {
		_ = msg.Ack()
		return
	}
	var rec evidencev1.EvidenceRecord
	if err := proto.Unmarshal(msg.Data(), &rec); err != nil {
		s.logger.Warn("personnel security subscriber: unmarshal failed; terming", "err", err.Error())
		_ = msg.Term()
		return
	}
	if rec.EvidenceKind != WorkerLifecycleEvidenceKind {
		_ = msg.Ack()
		return
	}
	tenantID, err := uuid.Parse(hdr.Get(streambuf.HeaderCredentialTenant))
	if err != nil {
		s.logger.Warn("personnel security subscriber: bad tenant header; terming", "err", err.Error())
		_ = msg.Term()
		return
	}
	w, err := workerFromRecord(&rec)
	if err != nil {
		// Malformed for our purposes (e.g. missing worker_id). The ledger
		// writer owns schema validation; for this consumer the message is a
		// permanent skip.
		s.logger.Warn("personnel security subscriber: skipping record", "err", err.Error())
		_ = msg.Ack()
		return
	}
	eventID, kind, ok := workerEventID(w)
	if !ok {
		// Not a joiner or leaver (on_leave / unknown) — nothing to create.
		_ = msg.Ack()
		return
	}
	if !withinEventWindow(w, kind) {
		s.logger.Debug("personnel security subscriber: lifecycle fact outside recency window; skipping",
			"kind", string(kind), "source", string(w.SourceHRIS))
		_ = msg.Ack()
		return
	}
	// Control resolution (OE-661 decision D2): pass through the control the
	// connector operator bound the roster to when it is a control UUID;
	// otherwise (SCF-anchor ref such as "scf:IAC-07", or empty) fall back to
	// a nil control — the checklist's completion evidence then carries
	// DefaultControlRef, exactly like a manual checklist without a control.
	controlID, err := uuid.Parse(strings.TrimSpace(rec.ControlId))
	if err != nil {
		controlID = uuid.Nil
	}
	tctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		s.logger.Warn("personnel security subscriber: tenant ctx; terming", "err", err.Error())
		_ = msg.Term()
		return
	}
	if _, err := s.creator.HandleWorkerEvent(tctx, w, eventID, controlID); err != nil {
		if errors.Is(err, ErrNoChecklistForEvent) || errors.Is(err, ErrInvalidSource) || errors.Is(err, ErrPersonRequired) {
			_ = msg.Ack()
			return
		}
		s.logger.Warn("personnel security subscriber: handle worker event; will redeliver",
			"tenant", tenantID, "err", err.Error())
		_ = msg.NakWithDelay(2 * time.Second)
		return
	}
	if err := msg.Ack(); err != nil {
		s.logger.Warn("personnel security subscriber: ack failed", "err", err.Error())
	}
}

// workerFromRecord maps a hris.worker_lifecycle.v1 payload back to the
// normalized worker shape HandleWorkerEvent consumes. Field names are the
// schema's (internal/api/schemaregistry/schemas/hris.worker_lifecycle). Only
// the lifecycle fields the checklist needs are read — the payload is
// PII-bounded at the connector (P0-491-3) and this decoder adds no field
// beyond worker.Worker's.
func workerFromRecord(rec *evidencev1.EvidenceRecord) (worker.Worker, error) {
	if rec.Payload == nil {
		return worker.Worker{}, errors.New("personnel security: record has no payload")
	}
	pm := rec.Payload.AsMap()
	id := strings.TrimSpace(payloadString(pm, "worker_id"))
	if id == "" {
		return worker.Worker{}, errors.New("personnel security: payload missing worker_id")
	}
	w := worker.Worker{
		SourceHRIS: worker.HRIS(payloadString(pm, "source_hris")),
		WorkerID:   id,
		Status:     worker.EmploymentStatus(payloadString(pm, "employment_status")),
		StartDate:  payloadDate(pm, "start_date"),
		EndDate:    payloadDate(pm, "end_date"),
		WorkEmail:  strings.TrimSpace(payloadString(pm, "work_email")),
	}
	if rec.ObservedAt != nil {
		w.ObservedAt = rec.ObservedAt.AsTime()
	}
	return w, nil
}

// workerEventID derives the stable source event id for a worker-lifecycle
// fact: "<worker_id>|<workflow_kind>|<event-date>". The event date is the
// joiner fact's start date or the leaver fact's end date (YYYY-MM-DD; empty
// when the source reported none), so every hourly re-observation of the same
// fact maps to the same id, while a rehire or a later termination gets a
// fresh one. Returns ok=false when the status is not a joiner/leaver.
func workerEventID(w worker.Worker) (string, WorkflowKind, bool) {
	kind, ok := workflowFromWorker(w)
	if !ok {
		return "", "", false
	}
	date := ""
	if at := eventDate(w, kind); !at.IsZero() {
		date = at.UTC().Format("2006-01-02")
	}
	return w.WorkerID + "|" + string(kind) + "|" + date, kind, true
}

// eventDate is the lifecycle fact's own date: start for a joiner, end for a
// leaver.
func eventDate(w worker.Worker, kind WorkflowKind) time.Time {
	if kind == WorkflowOffboarding {
		return w.EndDate
	}
	return w.StartDate
}

// withinEventWindow reports whether the lifecycle fact is recent enough
// (relative to the record's observed-at) to be an actionable event rather
// than roster history. A dated fact qualifies when its date is no more than
// EventRecencyWindow before observed-at (future-dated joiners always
// qualify). An undated fact qualifies unconditionally — the observation
// itself is the event signal.
func withinEventWindow(w worker.Worker, kind WorkflowKind) bool {
	at := eventDate(w, kind)
	if at.IsZero() {
		return true
	}
	observed := w.ObservedAt
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	return !at.Before(observed.Add(-EventRecencyWindow))
}

func payloadString(pm map[string]any, key string) string {
	v, _ := pm[key].(string)
	return v
}

func payloadDate(pm map[string]any, key string) time.Time {
	raw := strings.TrimSpace(payloadString(pm, key))
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

// discardWriter is a no-op io.Writer for the default discard logger.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
