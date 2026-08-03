// Unit tests for the OE-661 WorkerEventSubscriber dispatch logic and the
// pure helpers behind it (slice-353 pure-Go pre-DB convention: fast
// t.Parallel() table tests, no Postgres, no NATS).
//
// The handle() branches deliberately covered:
//
//  1. non-HRIS kind header            → Ack, store never reached (fast path)
//  2. HRIS kind + undecodable body    → Term (poison)
//  3. bad tenant header               → Term (poison)
//  4. payload missing worker_id       → Ack, store never reached
//  5. non-event status (on_leave)     → Ack, store never reached
//  6. lifecycle fact outside window   → Ack, store never reached
//  7. valid leaver fact               → HandleWorkerEvent under the record's
//                                       tenant with the stable event id +
//                                       control passthrough; Ack
//  8. anchor-ref control id           → uuid.Nil control (DefaultControlRef
//                                       fallback at evidence time)
//  9. store transient error           → NakWithDelay (will retry)
// 10. store sentinel (no checklist)   → Ack (not retryable)
//
// Branches deliberately left to integration (a real JetStream + Postgres is
// the only honest test): Start's consumer create + pull loop, the
// HandleWorkerEvent dedup against the DB unique index, and the overdue sweep.

package personnelsecurity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/hris/workerrecord"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ===== test doubles =====

// fakeMsg implements just enough of jetstream.Msg for handle() to drive it
// through every branch (the slice-282 eval-consumer pattern).
type fakeMsg struct {
	data    []byte
	headers nats.Header

	mu    sync.Mutex
	acks  int
	naks  int
	terms int
}

func newFakeMsg(data []byte, headers nats.Header) *fakeMsg {
	if headers == nil {
		headers = nats.Header{}
	}
	return &fakeMsg{data: data, headers: headers}
}

func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return nil, errors.New("fakeMsg: metadata unused")
}
func (m *fakeMsg) Data() []byte         { return m.data }
func (m *fakeMsg) Headers() nats.Header { return m.headers }
func (m *fakeMsg) Subject() string      { return "test.subject" }
func (m *fakeMsg) Reply() string        { return "" }
func (m *fakeMsg) Ack() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acks++
	return nil
}
func (m *fakeMsg) DoubleAck(context.Context) error { return nil }
func (m *fakeMsg) Nak() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.naks++
	return nil
}
func (m *fakeMsg) NakWithDelay(time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.naks++
	return nil
}
func (m *fakeMsg) InProgress() error { return nil }
func (m *fakeMsg) Term() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terms++
	return nil
}
func (m *fakeMsg) TermWithReason(string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terms++
	return nil
}

func (m *fakeMsg) outcome() (acks, naks, terms int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acks, m.naks, m.terms
}

// fakeCreator stubs checklistCreator, recording the call so tests can assert
// the store WAS or WAS NOT reached and with what arguments.
type creatorCall struct {
	tenant    string
	worker    worker.Worker
	eventID   string
	controlID uuid.UUID
}

type fakeCreator struct {
	mu    sync.Mutex
	calls []creatorCall
	err   error
}

func (f *fakeCreator) HandleWorkerEvent(ctx context.Context, w worker.Worker, sourceEventID string, controlID uuid.UUID) (Checklist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tenant, _ := tenancy.TenantFromContext(ctx)
	f.calls = append(f.calls, creatorCall{tenant: tenant, worker: w, eventID: sourceEventID, controlID: controlID})
	if f.err != nil {
		return Checklist{}, f.err
	}
	return Checklist{ID: uuid.New()}, nil
}

func (f *fakeCreator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newTestSubscriber builds a subscriber with the discard logger and the
// given creator stub; stream/subject stay nil because handle() never touches
// them.
func newTestSubscriber(creator checklistCreator) *WorkerEventSubscriber {
	s := NewWorkerEventSubscriber(nil, "", nil, nil)
	s.creator = creator
	return s
}

// buildWorkerMsg constructs a real connector-built record (the exact bytes
// the Rippling/BambooHR cmd layer pushes) wrapped in a fakeMsg with the
// slice-015 headers the publisher sets.
func buildWorkerMsg(t *testing.T, w worker.Worker, controlID, tenant string) *fakeMsg {
	t.Helper()
	rec, err := workerrecord.Build(w, controlID, "connector:rippling:hris@test", "hris", "production")
	if err != nil {
		t.Fatalf("workerrecord.Build: %v", err)
	}
	data, err := proto.Marshal(rec)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	hdr := nats.Header{}
	hdr.Set(streambuf.HeaderEvidenceKind, rec.EvidenceKind)
	hdr.Set(streambuf.HeaderCredentialTenant, tenant)
	return newFakeMsg(data, hdr)
}

func terminatedWorker(observed time.Time, end time.Time) worker.Worker {
	return worker.Worker{
		SourceHRIS: worker.HRISRippling,
		WorkerID:   "rip-w1",
		Status:     worker.StatusTerminated,
		EndDate:    end,
		WorkEmail:  "leaver@example.com",
		ObservedAt: observed,
	}
}

// ===== handle() branches =====

func TestEvidenceKindConstantMatchesConnector(t *testing.T) {
	t.Parallel()
	if workerrecord.EvidenceKind != WorkerLifecycleEvidenceKind {
		t.Fatalf("platform filter %q != connector kind %q", WorkerLifecycleEvidenceKind, workerrecord.EvidenceKind)
	}
}

func TestHandleSkipsNonHRISKind(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{}
	s := newTestSubscriber(creator)
	hdr := nats.Header{}
	hdr.Set(streambuf.HeaderEvidenceKind, "sast.scan_result.v1")
	msg := newFakeMsg([]byte("irrelevant"), hdr)
	s.handle(context.Background(), msg)
	acks, naks, terms := msg.outcome()
	if acks != 1 || naks != 0 || terms != 0 || creator.callCount() != 0 {
		t.Fatalf("acks/naks/terms/calls = %d/%d/%d/%d, want 1/0/0/0", acks, naks, terms, creator.callCount())
	}
}

func TestHandleTermsOnUndecodableBody(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{}
	s := newTestSubscriber(creator)
	hdr := nats.Header{}
	hdr.Set(streambuf.HeaderEvidenceKind, WorkerLifecycleEvidenceKind)
	msg := newFakeMsg([]byte{0xff, 0x01, 0x02, 0xff}, hdr)
	s.handle(context.Background(), msg)
	if _, _, terms := msg.outcome(); terms != 1 || creator.callCount() != 0 {
		t.Fatalf("terms/calls = %d/%d, want 1/0", terms, creator.callCount())
	}
}

func TestHandleTermsOnBadTenantHeader(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{}
	s := newTestSubscriber(creator)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	msg := buildWorkerMsg(t, terminatedWorker(now, now.Add(-24*time.Hour)), "", "not-a-uuid")
	s.handle(context.Background(), msg)
	if _, _, terms := msg.outcome(); terms != 1 || creator.callCount() != 0 {
		t.Fatalf("terms/calls = %d/%d, want 1/0", terms, creator.callCount())
	}
}

func TestHandleSkipsNonEventStatus(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{}
	s := newTestSubscriber(creator)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	w := terminatedWorker(now, time.Time{})
	w.Status = worker.StatusOnLeave
	msg := buildWorkerMsg(t, w, "", uuid.NewString())
	s.handle(context.Background(), msg)
	acks, _, _ := msg.outcome()
	if acks != 1 || creator.callCount() != 0 {
		t.Fatalf("acks/calls = %d/%d, want 1/0", acks, creator.callCount())
	}
}

func TestHandleSkipsStaleLifecycleFact(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{}
	s := newTestSubscriber(creator)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	// Terminated 90 days before the observation — roster history, not an event.
	msg := buildWorkerMsg(t, terminatedWorker(now, now.Add(-90*24*time.Hour)), "", uuid.NewString())
	s.handle(context.Background(), msg)
	acks, _, _ := msg.outcome()
	if acks != 1 || creator.callCount() != 0 {
		t.Fatalf("acks/calls = %d/%d, want 1/0", acks, creator.callCount())
	}
}

func TestHandleCreatesChecklistForRecentLeaver(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{}
	s := newTestSubscriber(creator)
	tenant := uuid.NewString()
	controlID := uuid.New()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	msg := buildWorkerMsg(t, terminatedWorker(now, end), controlID.String(), tenant)
	s.handle(context.Background(), msg)
	acks, naks, terms := msg.outcome()
	if acks != 1 || naks != 0 || terms != 0 {
		t.Fatalf("acks/naks/terms = %d/%d/%d, want 1/0/0", acks, naks, terms)
	}
	if creator.callCount() != 1 {
		t.Fatalf("store calls = %d, want 1", creator.callCount())
	}
	call := creator.calls[0]
	if call.tenant != tenant {
		t.Errorf("tenant = %q, want %q", call.tenant, tenant)
	}
	if want := "rip-w1|offboarding|2026-08-01"; call.eventID != want {
		t.Errorf("event id = %q, want %q", call.eventID, want)
	}
	if call.controlID != controlID {
		t.Errorf("control = %s, want %s", call.controlID, controlID)
	}
	if call.worker.Status != worker.StatusTerminated || call.worker.SourceHRIS != worker.HRISRippling {
		t.Errorf("worker round-trip = %+v", call.worker)
	}
	if call.worker.WorkEmail != "leaver@example.com" {
		t.Errorf("work email round-trip = %q", call.worker.WorkEmail)
	}
}

func TestHandleAnchorRefControlFallsBackToNil(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{}
	s := newTestSubscriber(creator)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	msg := buildWorkerMsg(t, terminatedWorker(now, now.Add(-24*time.Hour)), "scf:IAC-07", uuid.NewString())
	s.handle(context.Background(), msg)
	if creator.callCount() != 1 {
		t.Fatalf("store calls = %d, want 1", creator.callCount())
	}
	if creator.calls[0].controlID != uuid.Nil {
		t.Fatalf("control = %s, want uuid.Nil", creator.calls[0].controlID)
	}
}

func TestHandleNaksOnTransientStoreError(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{err: errors.New("connection refused")}
	s := newTestSubscriber(creator)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	msg := buildWorkerMsg(t, terminatedWorker(now, now.Add(-24*time.Hour)), "", uuid.NewString())
	s.handle(context.Background(), msg)
	acks, naks, _ := msg.outcome()
	if acks != 0 || naks != 1 {
		t.Fatalf("acks/naks = %d/%d, want 0/1", acks, naks)
	}
}

func TestHandleAcksOnStoreSentinel(t *testing.T) {
	t.Parallel()
	creator := &fakeCreator{err: ErrNoChecklistForEvent}
	s := newTestSubscriber(creator)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	msg := buildWorkerMsg(t, terminatedWorker(now, now.Add(-24*time.Hour)), "", uuid.NewString())
	s.handle(context.Background(), msg)
	acks, naks, _ := msg.outcome()
	if acks != 1 || naks != 0 {
		t.Fatalf("acks/naks = %d/%d, want 1/0", acks, naks)
	}
}

// ===== pure helpers =====

func TestWorkerEventID(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		w      worker.Worker
		wantID string
		wantOK bool
	}{
		{"joiner keyed by start date", worker.Worker{WorkerID: "w1", Status: worker.StatusActive, StartDate: start}, "w1|onboarding|2026-07-20", true},
		{"pending joiner", worker.Worker{WorkerID: "w1", Status: worker.StatusPending, StartDate: start}, "w1|onboarding|2026-07-20", true},
		{"leaver keyed by end date", worker.Worker{WorkerID: "w1", Status: worker.StatusTerminated, StartDate: start, EndDate: end}, "w1|offboarding|2026-08-01", true},
		{"undated leaver", worker.Worker{WorkerID: "w1", Status: worker.StatusTerminated}, "w1|offboarding|", true},
		{"on_leave is not an event", worker.Worker{WorkerID: "w1", Status: worker.StatusOnLeave}, "", false},
		{"unknown is not an event", worker.Worker{WorkerID: "w1", Status: worker.StatusUnknown}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, _, ok := workerEventID(tc.w)
			if ok != tc.wantOK || id != tc.wantID {
				t.Fatalf("workerEventID = %q/%v, want %q/%v", id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

func TestWithinEventWindow(t *testing.T) {
	t.Parallel()
	observed := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		w    worker.Worker
		kind WorkflowKind
		want bool
	}{
		{"recent leaver", worker.Worker{Status: worker.StatusTerminated, EndDate: observed.Add(-48 * time.Hour), ObservedAt: observed}, WorkflowOffboarding, true},
		{"historical leaver", worker.Worker{Status: worker.StatusTerminated, EndDate: observed.Add(-31 * 24 * time.Hour), ObservedAt: observed}, WorkflowOffboarding, false},
		{"undated leaver always qualifies", worker.Worker{Status: worker.StatusTerminated, ObservedAt: observed}, WorkflowOffboarding, true},
		{"future-dated joiner", worker.Worker{Status: worker.StatusPending, StartDate: observed.Add(14 * 24 * time.Hour), ObservedAt: observed}, WorkflowOnboarding, true},
		{"tenured active worker", worker.Worker{Status: worker.StatusActive, StartDate: observed.Add(-2 * 365 * 24 * time.Hour), ObservedAt: observed}, WorkflowOnboarding, false},
		{"boundary: exactly at window edge", worker.Worker{Status: worker.StatusActive, StartDate: observed.Add(-EventRecencyWindow), ObservedAt: observed}, WorkflowOnboarding, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := withinEventWindow(tc.w, tc.kind); got != tc.want {
				t.Fatalf("withinEventWindow = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkerFromRecordRejectsMissingWorkerID(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	rec, err := workerrecord.Build(worker.Worker{
		SourceHRIS: worker.HRISBambooHR,
		WorkerID:   "bhr-9",
		Status:     worker.StatusActive,
		ObservedAt: now,
	}, "", "connector:test", "hris", "production")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Simulate a record whose payload lost worker_id.
	delete(rec.Payload.Fields, "worker_id")
	if _, err := workerFromRecord(rec); err == nil {
		t.Fatal("workerFromRecord accepted a payload without worker_id")
	}
	if _, err := workerFromRecord(&evidencev1.EvidenceRecord{}); err == nil {
		t.Fatal("workerFromRecord accepted a record without payload")
	}
}

func TestPayloadDateToleratesGarbage(t *testing.T) {
	t.Parallel()
	pm := map[string]any{"start_date": "not-a-date", "end_date": 42}
	if got := payloadDate(pm, "start_date"); !got.IsZero() {
		t.Fatalf("garbage date parsed to %v", got)
	}
	if got := payloadDate(pm, "end_date"); !got.IsZero() {
		t.Fatalf("non-string date parsed to %v", got)
	}
	if got := payloadDate(pm, "missing"); !got.IsZero() {
		t.Fatalf("missing date parsed to %v", got)
	}
}
