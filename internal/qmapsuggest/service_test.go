package qmapsuggest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/llm"
)

type fakeReader struct {
	text       string
	textErr    error
	candidates []Candidate
	resolve    map[string]Candidate
}

func (f fakeReader) QuestionTextForMapping(context.Context, uuid.UUID) (string, error) {
	return f.text, f.textErr
}

func (f fakeReader) RetrieveCandidates(context.Context, []string) ([]Candidate, error) {
	return f.candidates, nil
}

func (f fakeReader) ResolveAnchor(_ context.Context, scfID string) (Candidate, bool, error) {
	c, ok := f.resolve[scfID]
	return c, ok, nil
}

type fakeStore struct {
	persisted    int
	suppressed   []string
	approveCalls int
	approver     string
}

func (f *fakeStore) PersistProposal(_ context.Context, qID uuid.UUID, anchor Candidate, rationale string, _ []string, _ Provenance) (string, error) {
	f.persisted++
	return "11111111-1111-1111-1111-111111111111", nil
}

func (f *fakeStore) Approve(_ context.Context, _ uuid.UUID, approver string) (ApprovedProposal, error) {
	f.approveCalls++
	f.approver = approver
	return ApprovedProposal{HumanApproved: true, HumanApprover: approver}, nil
}

func (f *fakeStore) Reject(context.Context, uuid.UUID, string) (RejectedProposal, error) {
	return RejectedProposal{Status: "rejected"}, nil
}

func (f *fakeStore) RecordSuppression(_ context.Context, _ uuid.UUID, _ string, reason string, _ Provenance) error {
	f.suppressed = append(f.suppressed, reason)
	return nil
}

type fakeAudit struct{ writes int }

func (f *fakeAudit) Write(context.Context, llm.Generation) (dbx.AiGeneration, error) {
	f.writes++
	return dbx.AiGeneration{}, nil
}

func stubSvc(reader Reader, store ProposalStore, audit AuditSink, draft string) *Service {
	return NewService(reader, &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}}, store, audit)
}

func TestSuggest_NoCandidatesManualOnly(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	svc := stubSvc(fakeReader{text: "quantum lattice roadmap"}, st, &fakeAudit{}, "unused")
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.ManualOnly || out.Reason != ReasonNoCandidates {
		t.Fatalf("want manual-only no-candidates, got %+v", out)
	}
	if st.persisted != 0 {
		t.Fatal("no-candidates path must persist nothing")
	}
	if len(st.suppressed) != 1 || st.suppressed[0] != ReasonNoCandidates {
		t.Fatalf("no-candidates suppression audit not recorded: %+v", st.suppressed)
	}
}

func TestSuggest_FabricatedAnchorSuppressed(t *testing.T) {
	t.Parallel()
	c := Candidate{AnchorUUID: uuid.NewString(), SCFID: "IAC-06", Title: "MFA", Excerpt: "multi-factor authentication"}
	st := &fakeStore{}
	audit := &fakeAudit{}
	svc := stubSvc(fakeReader{
		text:       "Do you enforce MFA?",
		candidates: []Candidate{c},
		resolve:    map[string]Candidate{c.SCFID: c},
	}, st, audit, `{"scf_id":"IAC-99","rationale":"Looks like access control."}`)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New(), Actor: "key_grc"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.Suppressed || out.Reason != ReasonOutOfGrounding {
		t.Fatalf("want out_of_grounding suppression, got %+v", out)
	}
	if st.persisted != 0 {
		t.Fatal("fabricated anchor must not persist")
	}
	if len(st.suppressed) != 1 || st.suppressed[0] != ReasonOutOfGrounding {
		t.Fatalf("suppression audit reason not recorded: %+v", st.suppressed)
	}
	if audit.writes != 1 {
		t.Fatalf("generation audit writes = %d, want 1", audit.writes)
	}
}

func TestSuggest_ValidPickPersistsUnapprovedProposal(t *testing.T) {
	t.Parallel()
	c := Candidate{AnchorUUID: uuid.NewString(), SCFID: "IAC-06", Title: "MFA", Excerpt: "multi-factor authentication"}
	st := &fakeStore{}
	svc := stubSvc(fakeReader{
		text:       "Do you enforce MFA?",
		candidates: []Candidate{c},
		resolve:    map[string]Candidate{c.SCFID: c},
	}, st, &fakeAudit{}, `{"scf_id":"IAC-06","rationale":"The question asks about multi-factor access controls."}`)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New(), Actor: "key_grc"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Suppressed || out.ManualOnly || out.ProposalID == "" {
		t.Fatalf("want proposal, got %+v", out)
	}
	if out.SCFAnchorID != "IAC-06" || out.Rationale == "" {
		t.Fatalf("proposal fields not surfaced: %+v", out)
	}
	if st.persisted != 1 {
		t.Fatalf("persisted = %d, want 1", st.persisted)
	}
}

func TestSuggest_QuestionNotFound(t *testing.T) {
	t.Parallel()
	svc := stubSvc(fakeReader{textErr: ErrQuestionNotFound}, &fakeStore{}, nil, "x")
	_, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if !errors.Is(err, ErrQuestionNotFound) {
		t.Fatalf("want ErrQuestionNotFound, got %v", err)
	}
}

func TestApprove_BlankApproverRejected(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	svc := stubSvc(fakeReader{}, st, nil, "x")
	for _, approver := range []string{"", "  "} {
		if _, err := svc.Approve(context.Background(), uuid.New(), approver); !errors.Is(err, ErrApproverRequired) {
			t.Fatalf("approver %q: want ErrApproverRequired, got %v", approver, err)
		}
	}
	if st.approveCalls != 0 {
		t.Fatal("blank approver must not reach store")
	}
}
