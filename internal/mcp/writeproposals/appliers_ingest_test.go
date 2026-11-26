package writeproposals

// Pure-Go unit coverage for the #1751 evidence-applier routing: the
// update_control_state and push_evidence appliers hand their record to
// ingest.Service.Process and never write evidence_records themselves.
// The DB-backed confirm flow (skew rejection + audit row, accepted
// ingest, retry) is in ingest_integration_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// With no ingest service the evidence appliers fail closed before
// touching tx; there is no direct-ledger fallback.
func TestEvidenceAppliers_FailClosedWithoutIngest(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		apply Applier
		input string
	}{
		"push_evidence": {
			NewPushEvidenceApplier(nil),
			`{"control_id":"11111111-1111-1111-1111-111111111111","kind":"manual.attestation.v1","result":"pass"}`,
		},
		"update_control_state": {
			NewUpdateControlStateApplier(nil),
			`{"control_id":"11111111-1111-1111-1111-111111111111","state":"fail"}`,
		},
	}
	for name, tc := range cases {
		p := Proposal{ID: uuid.New(), TenantID: uuid.New(), ToolInput: json.RawMessage(tc.input)}
		if _, err := tc.apply(context.Background(), nil, p); !errors.Is(err, ErrIngestUnavailable) {
			t.Fatalf("%s: err = %v, want ErrIngestUnavailable", name, err)
		}
	}
}

type recordingProcessor struct{ calls int }

func (r *recordingProcessor) Process(context.Context, *evidencev1.EvidenceRecord, credstore.Credential) (ingest.Receipt, ingest.Decision, error) {
	r.calls++
	return ingest.Receipt{}, ingest.DecisionAccepted, nil
}

// The approver credential must be on ctx and in the proposal's tenant;
// otherwise nothing reaches ingest.
func TestEvidenceAppliers_RequireApproverCredentialInTenant(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	input := json.RawMessage(`{"control_id":"11111111-1111-1111-1111-111111111111","kind":"manual.attestation.v1","result":"pass"}`)
	for name, ctx := range map[string]context.Context{
		"no credential":    context.Background(),
		"foreign tenant":   authctx.WithCredential(context.Background(), credstore.Credential{ID: "key_x", TenantID: uuid.NewString()}),
		"tenantless token": authctx.WithCredential(context.Background(), credstore.Credential{ID: "key_x"}),
	} {
		proc := &recordingProcessor{}
		p := Proposal{ID: uuid.New(), TenantID: tenant, ToolInput: input}
		if _, err := NewPushEvidenceApplier(proc)(ctx, nil, p); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: err = %v, want ErrInvalidInput", name, err)
		}
		if proc.calls != 0 {
			t.Fatalf("%s: ingest called %d times, want 0", name, proc.calls)
		}
	}
}

func TestApplyPushEvidence_RejectsNonObjectPayload(t *testing.T) {
	t.Parallel()
	p := Proposal{ToolInput: json.RawMessage(
		`{"control_id":"11111111-1111-1111-1111-111111111111","kind":"k","result":"pass","payload":[1]}`)}
	if _, err := NewPushEvidenceApplier(nil)(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "payload must be a JSON object") {
		t.Fatalf("err = %v, want payload-object err", err)
	}
}

// proposalRecord carries the proposed observed_at unchanged (so ingest's
// skew check sees it) and keys idempotency on the proposal id.
func TestProposalRecord_Shape(t *testing.T) {
	t.Parallel()
	p := Proposal{ID: uuid.New()}
	control := uuid.New()
	observed := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	rec, err := proposalRecord(p, ToolPushEvidence, "manual.attestation.v1", DefaultPushSchemaVersion,
		control, observed, evidencev1.Result_RESULT_PASS, map[string]any{"attestor": "a"})
	if err != nil {
		t.Fatalf("proposalRecord: %v", err)
	}
	if rec.GetIdempotencyKey() != "mcp-proposal:"+p.ID.String() {
		t.Fatalf("idempotency_key = %q", rec.GetIdempotencyKey())
	}
	if !rec.GetObservedAt().AsTime().Equal(observed) {
		t.Fatalf("observed_at = %v, want %v", rec.GetObservedAt().AsTime(), observed)
	}
	if rec.GetControlId() != control.String() || rec.GetSchemaVersion() != "1.0.0" ||
		rec.GetEvidenceKind() != "manual.attestation.v1" || len(rec.GetScope()) != 1 ||
		rec.GetSourceAttribution().GetSessionId() != p.ID.String() {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := proposalRecord(p, ToolPushEvidence, "k", "1.0.0", control, observed,
		evidencev1.Result_RESULT_PASS, map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("proposalRecord(unencodable payload) = nil err, want build-payload err")
	}
}
