// Unit tests for the IngestSubscriber message-dispatch logic in
// consumer.go. The load-bearing functions covered here are:
//
//   - IngestSubscriber.handle — the decode + tenant-header + control-id
//     routing path that turns a slice-015 JetStream message into an
//     EvaluateControl call. The handle() method has five distinct
//     branches and each one MUST surface the correct Ack vs Term vs Nak
//     so the broker either retries, terms (poison), or moves on. A bug
//     here either replays poison messages forever or silently drops
//     work.
//   - NewIngestSubscriber — the constructor's nil-logger fallback and
//     the evaluatorFactory bridge that lets *Engine flow through the
//     narrower controlEvaluator interface.
//   - discardWriter.Write — the no-op writer the default discard
//     logger writes into. Covered for completeness; one statement.
//
// The branches deliberately covered:
//
//   1. proto.Unmarshal fails           → Term  (poison message)
//   2. tenant header is not a UUID     → Term  (poison message)
//   3. control_id is not a UUID        → Ack   (anchor-ref control;
//                                              picked up by the
//                                              scheduled sweep instead)
//   4. EvaluateControl returns
//      ErrControlNotFound              → Ack   (not retryable)
//   5. EvaluateControl returns a
//      transient error                 → NakWithDelay(2s) (will retry)
//   6. EvaluateControl succeeds        → Ack
//
// Branches deliberately left to integration (a real JetStream is the
// only honest test):
//   - IngestSubscriber.Start (consumer create + pull loop)
//   - Scheduler.Run / Scheduler.SweepOnce (Postgres-touching)
//
// Slice 282 — coverage lift target. Pre-lift merged %: 67.2 (slice 279
// landed the helpers + the integration list-extension). This file is
// the consumer.go closure that lifts the package to >= 70%.

package eval

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
)

// ===== test doubles =====

// fakeMsg implements just enough of jetstream.Msg for handle() to drive
// it through every branch. Every Ack/Nak/Term/InProgress/DoubleAck call
// is recorded so the test can assert which terminal the message took.
type fakeMsg struct {
	data    []byte
	headers nats.Header

	mu        sync.Mutex
	acks      int
	naks      int
	nakDelays []time.Duration
	terms     int
	termsWith []string
	inProg    int
	doubleAck int

	ackErr error // optional override; default nil
}

func newFakeMsg(data []byte, headers nats.Header) *fakeMsg {
	if headers == nil {
		headers = nats.Header{}
	}
	return &fakeMsg{data: data, headers: headers}
}

func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return nil, errors.New("fakeMsg: metadata unused in eval handle()")
}
func (m *fakeMsg) Data() []byte         { return m.data }
func (m *fakeMsg) Headers() nats.Header { return m.headers }
func (m *fakeMsg) Subject() string      { return "test.subject" }
func (m *fakeMsg) Reply() string        { return "" }

func (m *fakeMsg) Ack() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acks++
	return m.ackErr
}
func (m *fakeMsg) DoubleAck(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.doubleAck++
	return nil
}
func (m *fakeMsg) Nak() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.naks++
	return nil
}
func (m *fakeMsg) NakWithDelay(d time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.naks++
	m.nakDelays = append(m.nakDelays, d)
	return nil
}
func (m *fakeMsg) InProgress() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inProg++
	return nil
}
func (m *fakeMsg) Term() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terms++
	return nil
}
func (m *fakeMsg) TermWithReason(reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terms++
	m.termsWith = append(m.termsWith, reason)
	return nil
}

// fakeEvaluator stubs controlEvaluator. Tests register the
// EvaluateControl result; calls are recorded so the test can assert the
// engine WAS or WAS NOT reached.
type fakeEvaluator struct {
	mu     sync.Mutex
	calls  []evaluatorCall
	result int
	err    error
}

type evaluatorCall struct {
	controlID uuid.UUID
	trigger   string
	asOf      time.Time
}

func (e *fakeEvaluator) EvaluateControl(_ context.Context, controlID uuid.UUID, trigger string, asOf time.Time) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, evaluatorCall{controlID, trigger, asOf})
	return e.result, e.err
}

func (e *fakeEvaluator) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

// newSubscriberWithEvaluator builds an IngestSubscriber whose
// evaluatorFactory returns the given fakeEvaluator on each call. The
// stream + subject are left zero — handle() does not touch either.
func newSubscriberWithEvaluator(eval *fakeEvaluator) *IngestSubscriber {
	s := NewIngestSubscriber(nil, "test.subject", func() *Engine { return nil }, nil)
	// Replace the wrapped engine factory with our fake. We swap the
	// internal field directly because the public constructor only
	// accepts a *Engine factory; the unit-test seam is the package-
	// private evaluatorFactory type.
	s.newEvaluator = func() controlEvaluator { return eval }
	return s
}

// validProtoRecord marshals a minimal EvidenceRecord with the given
// control_id string. Other fields are left zero; handle() only inspects
// control_id and the message header for tenant.
func validProtoRecord(t *testing.T, controlID string) []byte {
	t.Helper()
	rec := &evidencev1.EvidenceRecord{ControlId: controlID}
	b, err := proto.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal proto: %v", err)
	}
	return b
}

func validTenantHeader() nats.Header {
	h := nats.Header{}
	h.Set(streambuf.HeaderCredentialTenant, uuid.NewString())
	return h
}

// ===== handle: error/branch coverage =====

func TestHandle_BadProtoIsTerminated(t *testing.T) {
	t.Parallel()
	eval := &fakeEvaluator{}
	s := newSubscriberWithEvaluator(eval)

	msg := newFakeMsg([]byte{0xff, 0xff, 0xff}, validTenantHeader())
	s.handle(context.Background(), msg)

	if msg.terms != 1 {
		t.Fatalf("bad-proto: terms=%d, want 1", msg.terms)
	}
	if msg.acks != 0 || msg.naks != 0 {
		t.Fatalf("bad-proto: acks=%d naks=%d, want 0 (only Term)", msg.acks, msg.naks)
	}
	if eval.callCount() != 0 {
		t.Fatalf("bad-proto: engine called %d times, want 0", eval.callCount())
	}
}

func TestHandle_BadTenantHeaderIsTerminated(t *testing.T) {
	t.Parallel()
	eval := &fakeEvaluator{}
	s := newSubscriberWithEvaluator(eval)

	headers := nats.Header{}
	headers.Set(streambuf.HeaderCredentialTenant, "not-a-uuid")
	msg := newFakeMsg(validProtoRecord(t, uuid.NewString()), headers)
	s.handle(context.Background(), msg)

	if msg.terms != 1 {
		t.Fatalf("bad-tenant-header: terms=%d, want 1", msg.terms)
	}
	if eval.callCount() != 0 {
		t.Fatalf("bad-tenant-header: engine must not be called; got %d", eval.callCount())
	}
}

func TestHandle_MissingTenantHeaderIsTerminated(t *testing.T) {
	t.Parallel()
	eval := &fakeEvaluator{}
	s := newSubscriberWithEvaluator(eval)

	// No tenant header at all — uuid.Parse on "" must fail.
	msg := newFakeMsg(validProtoRecord(t, uuid.NewString()), nats.Header{})
	s.handle(context.Background(), msg)

	if msg.terms != 1 {
		t.Fatalf("missing-tenant: terms=%d, want 1", msg.terms)
	}
	if eval.callCount() != 0 {
		t.Fatalf("missing-tenant: engine must not be called; got %d", eval.callCount())
	}
}

func TestHandle_NonUUIDControlIDIsAckedNoEvalCall(t *testing.T) {
	t.Parallel()
	// control_id = "scf:VPM-04" is a valid SDK control_ref (the scheduled
	// sweep, not this subscriber, evaluates anchor-ref controls). handle()
	// must Ack so the broker moves on; the engine must not be called.
	eval := &fakeEvaluator{}
	s := newSubscriberWithEvaluator(eval)

	msg := newFakeMsg(validProtoRecord(t, "scf:VPM-04"), validTenantHeader())
	s.handle(context.Background(), msg)

	if msg.acks != 1 {
		t.Fatalf("non-uuid control: acks=%d, want 1", msg.acks)
	}
	if msg.terms != 0 || msg.naks != 0 {
		t.Fatalf("non-uuid control: terms=%d naks=%d, want 0", msg.terms, msg.naks)
	}
	if eval.callCount() != 0 {
		t.Fatalf("non-uuid control: engine called %d times, want 0", eval.callCount())
	}
}

func TestHandle_HappyPathAcksAndCallsEngine(t *testing.T) {
	t.Parallel()
	controlID := uuid.New()
	eval := &fakeEvaluator{result: 1}
	s := newSubscriberWithEvaluator(eval)

	msg := newFakeMsg(validProtoRecord(t, controlID.String()), validTenantHeader())
	s.handle(context.Background(), msg)

	if msg.acks != 1 {
		t.Fatalf("happy-path: acks=%d, want 1", msg.acks)
	}
	if msg.terms != 0 || msg.naks != 0 {
		t.Fatalf("happy-path: terms=%d naks=%d, want 0", msg.terms, msg.naks)
	}
	if eval.callCount() != 1 {
		t.Fatalf("happy-path: engine called %d times, want 1", eval.callCount())
	}
	if eval.calls[0].controlID != controlID {
		t.Fatalf("happy-path: engine got controlID %s, want %s", eval.calls[0].controlID, controlID)
	}
	if eval.calls[0].trigger != TriggerIngest {
		t.Fatalf("happy-path: trigger=%q, want %q", eval.calls[0].trigger, TriggerIngest)
	}
	if !eval.calls[0].asOf.Equal(FarFuture) {
		t.Fatalf("happy-path: asOf=%v, want FarFuture %v", eval.calls[0].asOf, FarFuture)
	}
}

func TestHandle_ControlNotFoundAcksNoNak(t *testing.T) {
	t.Parallel()
	// ErrControlNotFound is not retryable — the message must Ack so the
	// broker stops redelivering. Wrapping the sentinel is the canonical
	// engine return; errors.Is must still match.
	eval := &fakeEvaluator{err: fmt.Errorf("eval: %w", ErrControlNotFound)}
	s := newSubscriberWithEvaluator(eval)

	msg := newFakeMsg(validProtoRecord(t, uuid.NewString()), validTenantHeader())
	s.handle(context.Background(), msg)

	if msg.acks != 1 {
		t.Fatalf("control-not-found: acks=%d, want 1", msg.acks)
	}
	if msg.naks != 0 {
		t.Fatalf("control-not-found: naks=%d, want 0 (must NOT redeliver)", msg.naks)
	}
	if msg.terms != 0 {
		t.Fatalf("control-not-found: terms=%d, want 0 (Ack, not Term)", msg.terms)
	}
}

func TestHandle_TransientEngineErrorNaksWithDelay(t *testing.T) {
	t.Parallel()
	// A transient evaluation error (e.g., DB down, RLS context blip) must
	// Nak with a 2-second delay so the broker redelivers. EvaluateControl
	// is idempotent — redelivery is safe.
	eval := &fakeEvaluator{err: errors.New("evaluate: transient db error")}
	s := newSubscriberWithEvaluator(eval)

	msg := newFakeMsg(validProtoRecord(t, uuid.NewString()), validTenantHeader())
	s.handle(context.Background(), msg)

	if msg.naks != 1 {
		t.Fatalf("transient: naks=%d, want 1", msg.naks)
	}
	if len(msg.nakDelays) != 1 || msg.nakDelays[0] != 2*time.Second {
		t.Fatalf("transient: nakDelays=%v, want [2s]", msg.nakDelays)
	}
	if msg.acks != 0 || msg.terms != 0 {
		t.Fatalf("transient: acks=%d terms=%d, want 0", msg.acks, msg.terms)
	}
}

func TestHandle_AckErrorOnHappyPathDoesNotPanic(t *testing.T) {
	t.Parallel()
	// If the final Ack returns an error (broker is unhealthy), handle()
	// must log and return — never panic. The branch is a one-liner but
	// it has historically been the source of silent message-loss bugs
	// when a panic took the goroutine down.
	eval := &fakeEvaluator{result: 1}
	s := newSubscriberWithEvaluator(eval)

	msg := newFakeMsg(validProtoRecord(t, uuid.NewString()), validTenantHeader())
	msg.ackErr = errors.New("broker hung up")
	s.handle(context.Background(), msg)

	if msg.acks != 1 {
		t.Fatalf("ack-err: Ack must still have been attempted; got acks=%d", msg.acks)
	}
	if eval.callCount() != 1 {
		t.Fatalf("ack-err: engine should run before Ack failure; got %d calls", eval.callCount())
	}
}

// ===== NewIngestSubscriber constructor =====

func TestNewIngestSubscriber_NilLoggerFallsBackToDiscard(t *testing.T) {
	t.Parallel()
	s := NewIngestSubscriber(nil, "subj", func() *Engine { return nil }, nil)
	if s == nil {
		t.Fatal("NewIngestSubscriber returned nil")
	}
	if s.logger == nil {
		t.Fatal("nil logger arg must be replaced with a discard logger")
	}
	if s.durable != EvalConsumerDurable {
		t.Fatalf("durable=%q, want %q", s.durable, EvalConsumerDurable)
	}
	if s.subject != "subj" {
		t.Fatalf("subject=%q, want %q", s.subject, "subj")
	}
	if s.newEvaluator == nil {
		t.Fatal("newEvaluator must be set by the constructor bridge")
	}
}

func TestNewIngestSubscriber_BridgesEngineFactoryToEvaluator(t *testing.T) {
	t.Parallel()
	// The constructor must wrap the public engineFactory (func() *Engine)
	// so that the unit-internal evaluatorFactory exposes the narrower
	// controlEvaluator interface. We can't construct a real *Engine here
	// (it needs a pool) — but we CAN verify that NewIngestSubscriber sets
	// newEvaluator to a non-nil closure that invokes the supplied
	// factory exactly once per call.
	calls := 0
	factory := func() *Engine {
		calls++
		return nil // controlEvaluator(nil) is fine — we never invoke it here
	}
	s := NewIngestSubscriber(nil, "subj", factory, nil)

	// Invoke the wrapped factory. The closure must call the underlying
	// engineFactory once per call to satisfy the "one engine per
	// evaluation" contract that the integration tests rely on.
	_ = s.newEvaluator()
	_ = s.newEvaluator()
	if calls != 2 {
		t.Fatalf("engineFactory called %d times, want 2 (one per newEvaluator())", calls)
	}
}

// ===== discardWriter =====

func TestDiscardWriter_WriteReturnsLenNilError(t *testing.T) {
	t.Parallel()
	w := discardWriter{}
	payload := []byte("anything")
	n, err := w.Write(payload)
	if err != nil {
		t.Fatalf("discardWriter.Write err=%v, want nil", err)
	}
	if n != len(payload) {
		t.Fatalf("discardWriter.Write n=%d, want %d", n, len(payload))
	}
	// An empty payload must also succeed and return n=0.
	n0, err := w.Write(nil)
	if err != nil {
		t.Fatalf("discardWriter.Write(nil) err=%v, want nil", err)
	}
	if n0 != 0 {
		t.Fatalf("discardWriter.Write(nil) n=%d, want 0", n0)
	}
}
