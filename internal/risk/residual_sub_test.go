package risk

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
)

// fakeMsg records how handle settled a message. The embedded interface is
// nil: handle calling any other method panics, which fails the test.
type fakeMsg struct {
	jetstream.Msg
	data    []byte
	headers nats.Header
	acks    int
	naks    int
	terms   int
}

func (m *fakeMsg) Data() []byte                     { return m.data }
func (m *fakeMsg) Headers() nats.Header             { return m.headers }
func (m *fakeMsg) Ack() error                       { m.acks++; return nil }
func (m *fakeMsg) NakWithDelay(time.Duration) error { m.naks++; return nil }
func (m *fakeMsg) Term() error                      { m.terms++; return nil }
func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return nil, errors.New("fakeMsg: no metadata")
}
func (m *fakeMsg) settled() (acks, naks, terms int) { return m.acks, m.naks, m.terms }
func newFakeMsg(t *testing.T, tenant, control uuid.UUID) *fakeMsg {
	t.Helper()
	data, err := proto.Marshal(&evidencev1.EvidenceRecord{ControlId: control.String()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	h := nats.Header{}
	h.Set(streambuf.HeaderCredentialTenant, tenant.String())
	return &fakeMsg{data: data, headers: h}
}

// fakeDeriver returns a fixed error per risk and records every risk it was
// asked to recompute.
type fakeDeriver struct {
	errs  map[uuid.UUID]error
	calls []uuid.UUID
}

func (d *fakeDeriver) DeriveAndPersist(_ context.Context, riskID uuid.UUID, _ bool) (ResidualResult, error) {
	d.calls = append(d.calls, riskID)
	return ResidualResult{RiskID: riskID}, d.errs[riskID]
}

func newTestSubscriber(risks []uuid.UUID, d *fakeDeriver) *ResidualSubscriber {
	s := NewResidualSubscriber(nil, "", nil, nil)
	s.listLinkedRisks = func(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) { return risks, nil }
	s.newDeriver = func() residualRecomputer { return d }
	return s
}

// The underivable error handle sees is the one Derive builds: the sentinel
// wrapped around inherentScalar's parse error.
func underivableErr(riskID uuid.UUID) error {
	_, cause := inherentScalar("nist_800_30", []byte(`{"likelihood":"high"}`))
	return fmt.Errorf("%w: risk %s inherent_score: %w", ErrUnderivable, riskID, cause)
}

func TestResidualSubscriberHandle(t *testing.T) {
	bad, good1, good2 := uuid.New(), uuid.New(), uuid.New()
	transientErr := errors.New("conn reset")

	cases := []struct {
		name      string
		risks     []uuid.UUID
		errs      map[uuid.UUID]error
		wantAcks  int
		wantNaks  int
		wantTerms int
		wantUnder uint64
	}{
		{
			name:     "all good acks",
			risks:    []uuid.UUID{good1, good2},
			wantAcks: 1,
		},
		{
			name:      "malformed score terms once and the other risks still recompute",
			risks:     []uuid.UUID{bad, good1, good2},
			errs:      map[uuid.UUID]error{bad: underivableErr(bad)},
			wantTerms: 1,
			wantUnder: 1,
		},
		{
			name:     "transient error naks after recomputing the rest",
			risks:    []uuid.UUID{good1, bad, good2},
			errs:     map[uuid.UUID]error{bad: fmt.Errorf("update residual_score: %w", transientErr)},
			wantNaks: 1,
		},
		{
			name:      "transient wins over underivable so the retry still happens",
			risks:     []uuid.UUID{bad, good1},
			errs:      map[uuid.UUID]error{bad: underivableErr(bad), good1: transientErr},
			wantNaks:  1,
			wantUnder: 1,
		},
		{
			name:     "vanished risk is skipped and the message acks",
			risks:    []uuid.UUID{bad, good1},
			errs:     map[uuid.UUID]error{bad: ErrNotFound},
			wantAcks: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDeriver{errs: tc.errs}
			s := newTestSubscriber(tc.risks, d)
			msg := newFakeMsg(t, uuid.New(), uuid.New())
			before := UnderivableResiduals()

			s.handle(context.Background(), msg)

			if acks, naks, terms := msg.settled(); acks != tc.wantAcks || naks != tc.wantNaks || terms != tc.wantTerms {
				t.Fatalf("settled acks/naks/terms = %d/%d/%d, want %d/%d/%d",
					acks, naks, terms, tc.wantAcks, tc.wantNaks, tc.wantTerms)
			}
			if len(d.calls) != len(tc.risks) {
				t.Fatalf("recomputed %d risks, want all %d: %v", len(d.calls), len(tc.risks), d.calls)
			}
			if got := UnderivableResiduals() - before; got != tc.wantUnder {
				t.Fatalf("underivable count delta = %d, want %d", got, tc.wantUnder)
			}
		})
	}
}
