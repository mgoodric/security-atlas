// Slice 441 — service-orchestration unit tests. These drive Service.Suggest +
// Service.Approve through fake seams (a fake retriever, the llm.StubClient, a
// fake resolver, a fake store) so the three suggestion outcomes (drafted /
// insufficient / suppressed) + the approval guard are exercised WITHOUT
// Postgres or a live Ollama. The DB-backed RLS proofs are in integration_test.go.

package qaisuggest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// ----- fakes -----

type fakeRetriever struct {
	text       string
	textErr    error
	candidates []Candidate
}

func (f fakeRetriever) QuestionText(_ context.Context, _ uuid.UUID) (string, error) {
	return f.text, f.textErr
}
func (f fakeRetriever) RetrieveCandidates(_ context.Context, _ []string) ([]Candidate, error) {
	return f.candidates, nil
}

type fakeStore struct {
	persisted     int
	persistedText string
	approveCalls  int
	approver      string
}

func (f *fakeStore) PersistDraft(_ context.Context, _ uuid.UUID, narrative string, _ []byte, _ Provenance) (string, error) {
	f.persisted++
	f.persistedText = narrative
	return "answer-id-1", nil
}
func (f *fakeStore) Approve(_ context.Context, _ uuid.UUID, narrative, _ string, approver string) (ApprovedAnswer, error) {
	f.approveCalls++
	f.approver = approver
	return ApprovedAnswer{
		AnswerID:      "answer-id-1",
		Narrative:     narrative,
		HumanApproved: true,
		HumanApprover: approver,
	}, nil
}

func newSvc(t *testing.T, ret Retriever, draft string, res CitationResolver, st ApprovalStore) *Service {
	t.Helper()
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}}
	return NewService(ret, client, res, st)
}

// ----- Suggest: insufficient evidence (structural — no candidates) -----

func TestSuggest_NoCandidates_Insufficient(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	svc := newSvc(t, fakeRetriever{text: "Do you encrypt?", candidates: nil},
		"unused", fakeResolver{}, st)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.InsufficientEvidence || out.Reason != ReasonInsufficientEvidence {
		t.Fatalf("want insufficient, got %+v", out)
	}
	if st.persisted != 0 {
		t.Error("insufficient outcome must NOT persist (P0-441-2)")
	}
}

// ----- Suggest: model emits the insufficiency sentinel -----

func TestSuggest_ModelSaysInsufficient(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	st := &fakeStore{}
	ret := fakeRetriever{
		text:       "encrypt at rest?",
		candidates: []Candidate{{ID: a.String(), Kind: KindPolicy, Title: "encrypt policy", Excerpt: "x"}},
	}
	svc := newSvc(t, ret, "INSUFFICIENT_EVIDENCE", fakeResolver{owned: map[uuid.UUID]CandidateKind{a: KindPolicy}}, st)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.InsufficientEvidence {
		t.Fatalf("want insufficient (model sentinel), got %+v", out)
	}
	if st.persisted != 0 {
		t.Error("sentinel outcome must not persist")
	}
}

// ----- Suggest: fabricated citation -> suppressed, nothing persisted -----

func TestSuggest_FabricatedCitation_Suppressed(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	fabricated := uuid.New()
	st := &fakeStore{}
	ret := fakeRetriever{
		text:       "encrypt at rest?",
		candidates: []Candidate{{ID: a.String(), Kind: KindPolicy, Title: "encrypt policy", Excerpt: "x"}},
	}
	// Model cites a fabricated id not in the candidate set.
	draft := "Yes (" + fabricated.String() + ")."
	svc := newSvc(t, ret, draft, fakeResolver{owned: map[uuid.UUID]CandidateKind{a: KindPolicy}}, st)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.Suppressed || out.Reason != ReasonUnresolvedCitation {
		t.Fatalf("want suppressed unresolved, got %+v", out)
	}
	if st.persisted != 0 {
		t.Error("a suppressed draft must NEVER be persisted (P0-441-4)")
	}
	if out.Draft != "" {
		t.Error("suppressed suggestion must not surface draft text")
	}
}

// ----- Suggest: valid cited draft -> drafted + persisted unapproved -----

func TestSuggest_ValidDraft_PersistedUnapproved(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	st := &fakeStore{}
	ret := fakeRetriever{
		text:       "Do you encrypt at rest?",
		candidates: []Candidate{{ID: a.String(), Kind: KindPolicy, Title: "encrypt at rest policy", Excerpt: "AES-256"}},
	}
	draft := "Yes, AES-256 at rest (" + a.String() + ")."
	svc := newSvc(t, ret, draft, fakeResolver{owned: map[uuid.UUID]CandidateKind{a: KindPolicy}}, st)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New(), AuthoredBy: "key_grc"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Suppressed || out.InsufficientEvidence {
		t.Fatalf("want drafted, got %+v", out)
	}
	if out.AnswerID != "answer-id-1" || out.Draft != draft {
		t.Errorf("draft not surfaced: %+v", out)
	}
	if len(out.Citations) != 1 {
		t.Errorf("want 1 resolved citation, got %d", len(out.Citations))
	}
	if st.persisted != 1 {
		t.Errorf("valid draft should persist exactly once, got %d", st.persisted)
	}
	if out.CloudRouted {
		t.Error("v0 local Ollama must not flag cloud routing")
	}
}

// ----- Suggest: backend unavailable -> graceful suppression -----

func TestSuggest_BackendDown_Suppressed(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	st := &fakeStore{}
	ret := fakeRetriever{
		text:       "encrypt?",
		candidates: []Candidate{{ID: a.String(), Kind: KindPolicy, Title: "encrypt policy", Excerpt: "x"}},
	}
	client := &llm.StubClient{Err: llm.ErrBackend}
	svc := NewService(ret, client, fakeResolver{}, st)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.Suppressed || out.Reason != ReasonGenerationUnavailable {
		t.Fatalf("want graceful generation_unavailable, got %+v", out)
	}
	if st.persisted != 0 {
		t.Error("backend-down must not persist")
	}
}

// ----- Suggest: question not found surfaces the sentinel error -----

func TestSuggest_QuestionNotFound(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	ret := fakeRetriever{textErr: ErrQuestionNotFound}
	svc := newSvc(t, ret, "x", fakeResolver{}, st)
	_, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if !errors.Is(err, ErrQuestionNotFound) {
		t.Fatalf("want ErrQuestionNotFound, got %v", err)
	}
}

// ----- Approve: blank approver rejected (P0-441-8 Go mirror) -----

func TestApprove_BlankApproverRejected(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	svc := newSvc(t, fakeRetriever{}, "x", fakeResolver{}, st)
	for _, approver := range []string{"", "   "} {
		_, err := svc.Approve(context.Background(), ApproveParams{
			AnswerID: uuid.New(),
			Approver: approver,
		})
		if !errors.Is(err, ErrApproverRequired) {
			t.Fatalf("approver=%q: want ErrApproverRequired, got %v", approver, err)
		}
		if st.approveCalls != 0 {
			t.Fatal("blank-approver approval must never reach the store")
		}
	}
}

func TestApprove_RecordsApprover(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	svc := newSvc(t, fakeRetriever{}, "x", fakeResolver{}, st)
	got, err := svc.Approve(context.Background(), ApproveParams{
		AnswerID:  uuid.New(),
		Narrative: "Yes, approved.",
		Approver:  "key_grc",
	})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !got.HumanApproved || got.HumanApprover != "key_grc" {
		t.Fatalf("approval did not record approver: %+v", got)
	}
	if st.approveCalls != 1 || st.approver != "key_grc" {
		t.Errorf("store approve not called correctly: calls=%d approver=%q", st.approveCalls, st.approver)
	}
}
