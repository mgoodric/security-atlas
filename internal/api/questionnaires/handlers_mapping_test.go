// Slice 755 — pure-Go unit tests for the SCF-mapping suggest/approve/reject
// handler branches: the role gate, the missing-tenant / missing-credential
// 401, the nil-service 503, the bad-uuid 400, and the service-error → HTTP
// status mapping (404 / 409 / 400 / 500) plus the 200 render. The service is
// a real *qmapsuggest.Service backed by in-memory fakes (the slice-353 Q-2
// fast-loop convention), so none of these touch Postgres. The happy-path DB
// behavior (RLS, approver guard at the DB tier, canonical-thereafter) is
// proven in internal/qmapsuggest/integration_test.go.

package questionnaires

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/qmapsuggest"
)

const (
	testQID      = "22222222-2222-2222-2222-222222222222"
	testProposal = "33333333-3333-3333-3333-333333333333"
)

// mapReader is an in-memory qmapsuggest.Reader. textErr drives the
// question-lookup error branches; an empty candidates slice drives the
// manual-only path.
type mapReader struct {
	text       string
	textErr    error
	candidates []qmapsuggest.Candidate
}

func (f mapReader) QuestionTextForMapping(context.Context, uuid.UUID) (string, error) {
	return f.text, f.textErr
}

func (f mapReader) RetrieveCandidates(context.Context, []string) ([]qmapsuggest.Candidate, error) {
	return f.candidates, nil
}

func (f mapReader) ResolveAnchor(context.Context, string) (qmapsuggest.Candidate, bool, error) {
	return qmapsuggest.Candidate{}, false, nil
}

// mapStore is an in-memory qmapsuggest.ProposalStore whose approve/reject
// errors are configurable per test.
type mapStore struct {
	approveErr error
	rejectErr  error
}

func (f *mapStore) PersistProposal(context.Context, uuid.UUID, qmapsuggest.Candidate, string, []string, qmapsuggest.Provenance) (string, error) {
	return testProposal, nil
}

func (f *mapStore) Approve(_ context.Context, id uuid.UUID, approver string) (qmapsuggest.ApprovedProposal, error) {
	if f.approveErr != nil {
		return qmapsuggest.ApprovedProposal{}, f.approveErr
	}
	return qmapsuggest.ApprovedProposal{
		ProposalID:    id.String(),
		HumanApproved: true,
		HumanApprover: approver,
	}, nil
}

func (f *mapStore) Reject(_ context.Context, id uuid.UUID, _ string) (qmapsuggest.RejectedProposal, error) {
	if f.rejectErr != nil {
		return qmapsuggest.RejectedProposal{}, f.rejectErr
	}
	return qmapsuggest.RejectedProposal{ProposalID: id.String(), Status: "rejected"}, nil
}

func (f *mapStore) RecordSuppression(context.Context, uuid.UUID, string, string, qmapsuggest.Provenance) error {
	return nil
}

// mapAudit is a no-op qmapsuggest.AuditSink.
type mapAudit struct{}

func (mapAudit) Write(context.Context, llm.Generation) (dbx.AiGeneration, error) {
	return dbx.AiGeneration{}, nil
}

// mappingHandler wires a Handler whose mapping service runs against the
// supplied fakes; the slice-441 suggest surface stays nil (independent).
func mappingHandler(reader qmapsuggest.Reader, store qmapsuggest.ProposalStore) *Handler {
	svc := qmapsuggest.NewService(reader, llm.NewStubClient(), store, mapAudit{})
	return NewWithAI(nil, nil, svc)
}

func grcCred() *credstore.Credential {
	return &credstore.Credential{ID: "key_grc", TenantID: testTenant, IsApprover: true}
}

// guardCases exercises the shared guard ladder (401 / 403 / 503) that all
// three mapping handlers repeat, without reaching any service call.
func guardCases(t *testing.T, name string, fn func(h *Handler) http.HandlerFunc) {
	t.Helper()
	// Missing tenant + credential -> 401.
	h := New(nil)
	if w := route(fn(h), reqWith(t, "POST", "/x", "", nil), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("%s no-tenant: got %d, want 401", name, w.Code)
	}
	// Tenant present, credential missing -> 401.
	if w := route(fn(h), reqWith(t, "POST", "/x", testTenant, nil), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("%s no-cred: got %d, want 401", name, w.Code)
	}
	// Non-approver, non-admin -> 403.
	viewer := credstore.Credential{ID: "key_viewer", TenantID: testTenant}
	if w := route(fn(h), reqWith(t, "POST", "/x", testTenant, &viewer), nil); w.Code != http.StatusForbidden {
		t.Errorf("%s viewer: got %d, want 403", name, w.Code)
	}
	// Authorized but mapping service not wired -> 503, not a panic.
	if w := route(fn(h), reqWith(t, "POST", "/x", testTenant, grcCred()), nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("%s nil-service: got %d, want 503", name, w.Code)
	}
}

func TestMappingSuggest_Guards(t *testing.T) {
	t.Parallel()
	guardCases(t, "suggest", func(h *Handler) http.HandlerFunc { return h.MappingSuggest })
}

func TestMappingApprove_Guards(t *testing.T) {
	t.Parallel()
	guardCases(t, "approve", func(h *Handler) http.HandlerFunc { return h.MappingApprove })
}

func TestMappingReject_Guards(t *testing.T) {
	t.Parallel()
	guardCases(t, "reject", func(h *Handler) http.HandlerFunc { return h.MappingReject })
}

func TestMappingSuggest_BadUUID(t *testing.T) {
	t.Parallel()
	h := mappingHandler(mapReader{}, &mapStore{})
	w := route(h.MappingSuggest, reqWith(t, "POST", "/x", testTenant, grcCred()),
		map[string]string{"qid": "not-a-uuid"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad qid: got %d, want 400", w.Code)
	}
}

func TestMappingSuggest_ServiceErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"question not found", qmapsuggest.ErrQuestionNotFound, http.StatusNotFound},
		{"already mapped", qmapsuggest.ErrQuestionCanonical, http.StatusConflict},
		{"internal", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := mappingHandler(mapReader{textErr: tc.err}, &mapStore{})
			w := route(h.MappingSuggest, reqWith(t, "POST", "/x", testTenant, grcCred()),
				map[string]string{"qid": testQID})
			if w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestMappingSuggest_ManualOnlyOK(t *testing.T) {
	t.Parallel()
	// Zero catalog candidates -> 200 with the explicit "map manually" shape,
	// never a fabricated anchor (anti-criterion P0-755-1).
	h := mappingHandler(mapReader{text: "some question"}, &mapStore{})
	w := route(h.MappingSuggest, reqWith(t, "POST", "/x", testTenant, grcCred()),
		map[string]string{"qid": testQID})
	if w.Code != http.StatusOK {
		t.Fatalf("manual-only: got %d, want 200", w.Code)
	}
	var out qmapsuggest.Proposal
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.ManualOnly || out.Reason != qmapsuggest.ReasonNoCandidates {
		t.Errorf("manual-only body: got %+v", out)
	}
	if out.SCFAnchorID != "" || out.ProposalID != "" {
		t.Errorf("manual-only must not name an anchor or proposal: %+v", out)
	}
}

func TestMappingApprove_BadUUID(t *testing.T) {
	t.Parallel()
	h := mappingHandler(mapReader{}, &mapStore{})
	w := route(h.MappingApprove, reqWith(t, "POST", "/x", testTenant, grcCred()),
		map[string]string{"proposalID": "not-a-uuid"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad proposalID: got %d, want 400", w.Code)
	}
}

func TestMappingApprove_EmptyApprover(t *testing.T) {
	t.Parallel()
	// A credential with an empty ID passes tenantCred (TenantID is what it
	// requires) but MUST be refused by the service's approver guard — the
	// constitutional no-approver-no-approval invariant at the Go tier.
	h := mappingHandler(mapReader{}, &mapStore{})
	cred := credstore.Credential{ID: "", TenantID: testTenant, IsAdmin: true}
	w := route(h.MappingApprove, reqWith(t, "POST", "/x", testTenant, &cred),
		map[string]string{"proposalID": testProposal})
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty approver: got %d, want 400", w.Code)
	}
}

func TestMappingApprove_ServiceErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"proposal not found", qmapsuggest.ErrProposalNotFound, http.StatusNotFound},
		{"already mapped", qmapsuggest.ErrQuestionCanonical, http.StatusConflict},
		{"internal", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := mappingHandler(mapReader{}, &mapStore{approveErr: tc.err})
			w := route(h.MappingApprove, reqWith(t, "POST", "/x", testTenant, grcCred()),
				map[string]string{"proposalID": testProposal})
			if w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestMappingApprove_OK(t *testing.T) {
	t.Parallel()
	h := mappingHandler(mapReader{}, &mapStore{})
	w := route(h.MappingApprove, reqWith(t, "POST", "/x", testTenant, grcCred()),
		map[string]string{"proposalID": testProposal})
	if w.Code != http.StatusOK {
		t.Fatalf("approve: got %d, want 200", w.Code)
	}
	var out qmapsuggest.ApprovedProposal
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.HumanApproved || out.HumanApprover != "key_grc" {
		t.Errorf("approve body: got %+v, want human_approved by key_grc", out)
	}
}

func TestMappingReject_BadUUID(t *testing.T) {
	t.Parallel()
	h := mappingHandler(mapReader{}, &mapStore{})
	w := route(h.MappingReject, reqWith(t, "POST", "/x", testTenant, grcCred()),
		map[string]string{"proposalID": "not-a-uuid"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad proposalID: got %d, want 400", w.Code)
	}
}

func TestMappingReject_ServiceErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"proposal not found", qmapsuggest.ErrProposalNotFound, http.StatusNotFound},
		{"internal", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := mappingHandler(mapReader{}, &mapStore{rejectErr: tc.err})
			w := route(h.MappingReject, reqWith(t, "POST", "/x", testTenant, grcCred()),
				map[string]string{"proposalID": testProposal})
			if w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestMappingReject_OK(t *testing.T) {
	t.Parallel()
	h := mappingHandler(mapReader{}, &mapStore{})
	w := route(h.MappingReject, reqWith(t, "POST", "/x", testTenant, grcCred()),
		map[string]string{"proposalID": testProposal})
	if w.Code != http.StatusOK {
		t.Fatalf("reject: got %d, want 200", w.Code)
	}
	var out qmapsuggest.RejectedProposal
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "rejected" || out.ProposalID != testProposal {
		t.Errorf("reject body: got %+v", out)
	}
}
