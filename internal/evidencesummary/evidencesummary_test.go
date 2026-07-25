// Pure-Go unit tests for the evidence-summary service + citation logic (no
// Postgres, no build tag — slice-353 Q-2 fast tier). They exercise the
// suppression branches (no-evidence, generation-unavailable, no-citations,
// unresolved-citation), the grounding/bounding gate, and the always-return-the-
// deterministic-set graceful-degradation contract via fakes. The full request
// path (real RLS, real cross-tenant resolution) lives in integration_test.go.
package evidencesummary

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// ----- fakes -----

type fakeReader struct {
	set EvidenceSet
	err error
}

func (f fakeReader) EvidenceSet(_ context.Context, _ uuid.UUID) (EvidenceSet, error) {
	return f.set, f.err
}

// fakeResolver resolves any id present in `owned`, mirroring the RLS-scoped
// Store.Resolve: an id outside the set (cross-tenant / fabricated) returns
// ok=false.
type fakeResolver struct {
	owned map[uuid.UUID]string // id -> kind
	err   error
}

func (f fakeResolver) Resolve(_ context.Context, id uuid.UUID) (Citation, bool, error) {
	if f.err != nil {
		return Citation{}, false, f.err
	}
	kind, ok := f.owned[id]
	if !ok {
		return Citation{}, false, nil
	}
	return Citation{Kind: kind, ID: id.String()}, true, nil
}

func setWith(ctrlID uuid.UUID, evIDs ...uuid.UUID) EvidenceSet {
	set := EvidenceSet{ControlID: ctrlID, ControlTitle: "test control", TotalCount: len(evIDs)}
	for i, ev := range evIDs {
		set.Records = append(set.Records, EvidenceFact{
			EvidenceID:   ev,
			EvidenceKind: "access_review.completion",
			Result:       "pass",
			ObservedAt:   time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}
	return set
}

func svcWith(reader EvidenceReader, draft string, resolver CitationResolver) *Service {
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text: draft, ModelName: "stub-model", ModelVersion: "1", ModelProvider: "stub",
	}}
	return NewService(reader, client, resolver)
}

// ----- happy path: valid grounded citation renders -----

func TestSummarize_ValidCitationRenders(t *testing.T) {
	ctrlID, evID := uuid.New(), uuid.New()
	set := setWith(ctrlID, evID)
	draft := fmt.Sprintf("Control (%s) evidence (%s) passed.", ctrlID, evID)
	resolver := fakeResolver{owned: map[uuid.UUID]string{ctrlID: KindControl, evID: KindEvidence}}

	sum, err := svcWith(fakeReader{set: set}, draft, resolver).Summarize(context.Background(), ctrlID)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.Suppressed {
		t.Fatalf("expected rendered summary, got suppressed (%q)", sum.Reason)
	}
	if sum.Text != draft {
		t.Error("summary text not surfaced verbatim")
	}
	if len(sum.Citations) != 2 {
		t.Errorf("want 2 resolved citations, got %d", len(sum.Citations))
	}
	if sum.ModelName != "stub-model" {
		t.Error("model provenance must be surfaced (AC-6)")
	}
	// AC-7: the deterministic set is always present.
	if sum.EvidenceSet.ControlID != ctrlID || len(sum.EvidenceSet.Records) != 1 {
		t.Error("deterministic evidence set must be returned alongside the summary")
	}
}

// ----- no-evidence: suppress without burning a model call -----

func TestSummarize_NoEvidenceSuppressed(t *testing.T) {
	ctrlID := uuid.New()
	set := EvidenceSet{ControlID: ctrlID, ControlTitle: "empty", TotalCount: 0}
	resolver := fakeResolver{owned: map[uuid.UUID]string{ctrlID: KindControl}}

	sum, err := svcWith(fakeReader{set: set}, "ignored", resolver).Summarize(context.Background(), ctrlID)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !sum.Suppressed || sum.Reason != ReasonNoEvidence {
		t.Fatalf("want suppressed/no_evidence, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	if sum.Text != "" {
		t.Error("no text when there is no evidence")
	}
}

// ----- generation unavailable: suppress, evidence still returned (AC-7) -----

func TestSummarize_GenerationUnavailableSuppressed(t *testing.T) {
	ctrlID, evID := uuid.New(), uuid.New()
	set := setWith(ctrlID, evID)
	client := &llm.StubClient{Err: errors.New("ollama unreachable")}
	svc := NewService(fakeReader{set: set}, client,
		fakeResolver{owned: map[uuid.UUID]string{ctrlID: KindControl, evID: KindEvidence}})

	sum, err := svc.Summarize(context.Background(), ctrlID)
	if err != nil {
		t.Fatalf("Summarize must not error on backend failure (graceful degradation): %v", err)
	}
	if !sum.Suppressed || sum.Reason != ReasonGenerationUnavailable {
		t.Fatalf("want suppressed/generation_unavailable, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	// AC-7 / P0-502-7: the deterministic evidence list never blocks on the LLM.
	if len(sum.EvidenceSet.Records) != 1 {
		t.Error("evidence set must still render when generation is unavailable")
	}
}

// ----- no-citations: a summary with no grounding is suppressed -----

func TestSummarize_NoCitationsSuppressed(t *testing.T) {
	ctrlID, evID := uuid.New(), uuid.New()
	set := setWith(ctrlID, evID)
	draft := "The evidence generally looks fine." // no UUID cited
	resolver := fakeResolver{owned: map[uuid.UUID]string{ctrlID: KindControl, evID: KindEvidence}}

	sum, _ := svcWith(fakeReader{set: set}, draft, resolver).Summarize(context.Background(), ctrlID)
	if !sum.Suppressed || sum.Reason != ReasonNoCitations {
		t.Fatalf("want suppressed/no_citations, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
}

// ----- fabricated id (outside the grounding set) is suppressed (P0-502-1) -----

func TestSummarize_FabricatedIDSuppressed(t *testing.T) {
	ctrlID, evID := uuid.New(), uuid.New()
	set := setWith(ctrlID, evID)
	fabricated := uuid.New()
	draft := fmt.Sprintf("Control (%s) cites (%s).", ctrlID, fabricated)
	// Even if the resolver WOULD own the fabricated id, the grounding gate fails
	// it first: the model may only cite what it was shown.
	resolver := fakeResolver{owned: map[uuid.UUID]string{ctrlID: KindControl, evID: KindEvidence, fabricated: KindEvidence}}

	sum, _ := svcWith(fakeReader{set: set}, draft, resolver).Summarize(context.Background(), ctrlID)
	if !sum.Suppressed || sum.Reason != ReasonUnresolvedCitation {
		t.Fatalf("want suppressed/unresolved_citation for an ungrounded id, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	if sum.Text != "" {
		t.Error("fabricated-citation summary must not surface text (P0-502-1)")
	}
}

// ----- grounded but unresolvable id (cross-tenant analogue) is suppressed -----

func TestSummarize_UnresolvableGroundedIDSuppressed(t *testing.T) {
	ctrlID, evID := uuid.New(), uuid.New()
	set := setWith(ctrlID, evID)
	draft := fmt.Sprintf("Control (%s) evidence (%s).", ctrlID, evID)
	// The id is in the grounding set but the resolver cannot confirm ownership
	// (the cross-tenant mechanism: invisible under RLS).
	resolver := fakeResolver{owned: map[uuid.UUID]string{ctrlID: KindControl}} // evID NOT owned

	sum, _ := svcWith(fakeReader{set: set}, draft, resolver).Summarize(context.Background(), ctrlID)
	if !sum.Suppressed || sum.Reason != ReasonUnresolvedCitation {
		t.Fatalf("want suppressed/unresolved_citation, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
}

// ----- read error is a hard error (no set to render) -----

func TestSummarize_ReadErrorIsHardError(t *testing.T) {
	ctrlID := uuid.New()
	_, err := svcWith(fakeReader{err: errors.New("db down")}, "x", fakeResolver{}).
		Summarize(context.Background(), ctrlID)
	if err == nil {
		t.Fatal("a genuine evidence-read failure must be returned as an error")
	}
}

// ----- citation-parsing + allowedIDs unit coverage -----

func TestParseCitedIDs_DistinctOrdered(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	text := fmt.Sprintf("see %s and %s and again %s; not-a-uuid 123", a, b, a)
	got := parseCitedIDs(text)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("parseCitedIDs = %v, want [%s %s] (distinct, first-seen order)", got, a, b)
	}
}

func TestAllowedIDs_ControlPlusEvidence(t *testing.T) {
	ctrlID, e1, e2 := uuid.New(), uuid.New(), uuid.New()
	allowed := allowedIDs(setWith(ctrlID, e1, e2))
	for _, id := range []uuid.UUID{ctrlID, e1, e2} {
		if !allowed[id] {
			t.Errorf("id %s should be allowed (control + cited excerpts)", id)
		}
	}
	if allowed[uuid.New()] {
		t.Error("a random id must not be in the grounding set")
	}
}

func TestBuildPrompt_GroundsAndBounds(t *testing.T) {
	ctrlID, evID := uuid.New(), uuid.New()
	set := setWith(ctrlID, evID)
	set.TotalCount = 50 // history longer than the bound
	p := buildPrompt(set)
	for _, want := range []string{ctrlID.String(), evID.String(), "CURRENT LIVE", "of 50 total"} {
		if !contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
