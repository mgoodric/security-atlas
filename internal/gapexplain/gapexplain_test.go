// Pure-Go unit tests for the gap-explanation surface (slice 444, slice-353
// Q-2 helpers convention): no Postgres, no build tag, fast t.Parallel() table
// tests over the pure-Go branches — citation parsing, the strict
// citation-validation gate, the prompt builder, and the Service orchestration
// against a fake RollupReader + the llm.StubClient. The integration tier
// (integration_test.go) is the safety net for the RLS / cross-tenant
// guarantees that need a real Postgres (AC-8/9/10).
package gapexplain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// ---- fakes ----

// fakeRollups returns a fixed rollup (or error) for Service tests.
type fakeRollups struct {
	rollup Rollup
	err    error
}

func (f fakeRollups) Rollup(_ context.Context, _ uuid.UUID) (Rollup, error) {
	return f.rollup, f.err
}

// fakeResolver resolves ONLY the ids in `owned` to tenant-owned citations.
// Any other id is treated as not visible under RLS (ok=false) — the unit-tier
// stand-in for a cross-tenant or non-existent id.
type fakeResolver struct {
	owned map[uuid.UUID]Citation
	err   error
}

func (f fakeResolver) Resolve(_ context.Context, id uuid.UUID) (Citation, bool, error) {
	if f.err != nil {
		return Citation{}, false, f.err
	}
	c, ok := f.owned[id]
	return c, ok, nil
}

// sampleRollup builds a rollup with a known control id + one evidence excerpt.
func sampleRollup() (Rollup, uuid.UUID, uuid.UUID) {
	ctrlID := uuid.New()
	evID := uuid.New()
	observed := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	validUntil := observed.Add(30 * 24 * time.Hour)
	return Rollup{
		ControlID:        ctrlID,
		ControlTitle:     "Access reviews are performed quarterly",
		FreshnessClass:   "quarterly",
		LatestObservedAt: &observed,
		ValidUntil:       &validUntil,
		IsStale:          true,
		EvidenceCount:    1,
		Evidence: []EvidenceFact{
			{EvidenceID: evID, EvidenceKind: "access_review.completion", Result: "pass", ObservedAt: observed},
		},
	}, ctrlID, evID
}

// ---- citation parsing ----

func TestParseCitedIDs(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	b := uuid.New()
	text := "The control (" + a.String() + ") is stale; see evidence " + b.String() +
		" and again (" + a.String() + ")."
	got := parseCitedIDs(text)
	if len(got) != 2 {
		t.Fatalf("parseCitedIDs returned %d ids, want 2 (deduped): %v", len(got), got)
	}
	if got[0] != a || got[1] != b {
		t.Fatalf("parseCitedIDs preserved order incorrectly: %v", got)
	}
}

func TestParseCitedIDs_NoUUIDs(t *testing.T) {
	t.Parallel()
	if got := parseCitedIDs("no ids here, just prose about freshness"); len(got) != 0 {
		t.Fatalf("expected no ids, got %v", got)
	}
}

// ---- strict citation validation gate (AC-4 / AC-9 fast path) ----

func TestValidateCitations(t *testing.T) {
	t.Parallel()
	rollup, ctrlID, evID := sampleRollup()
	allowed := allowedIDs(rollup)
	owned := map[uuid.UUID]Citation{
		ctrlID: {Kind: KindControl, ID: ctrlID.String()},
		evID:   {Kind: KindEvidence, ID: evID.String()},
	}

	tests := []struct {
		name       string
		text       string
		resolver   CitationResolver
		wantOK     bool
		wantReason string
		wantCites  int
	}{
		{
			name:      "valid control + evidence citation resolves",
			text:      "Control (" + ctrlID.String() + ") is stale; latest evidence " + evID.String() + " is past its window.",
			resolver:  fakeResolver{owned: owned},
			wantOK:    true,
			wantCites: 2,
		},
		{
			name:       "no citations at all is suppressed",
			text:       "This control is stale because its evidence aged out.",
			resolver:   fakeResolver{owned: owned},
			wantReason: ReasonNoCitations,
		},
		{
			name:       "fabricated id outside the grounding set is suppressed",
			text:       "Control (" + ctrlID.String() + ") cites a made-up record " + uuid.New().String() + ".",
			resolver:   fakeResolver{owned: owned},
			wantReason: ReasonUnresolvedCitation,
		},
		{
			name: "allowed id that does not resolve tenant-owned is suppressed",
			text: "Control (" + ctrlID.String() + ") and evidence " + evID.String() + ".",
			// resolver knows the control but NOT the evidence id -> not owned.
			resolver:   fakeResolver{owned: map[uuid.UUID]Citation{ctrlID: owned[ctrlID]}},
			wantReason: ReasonUnresolvedCitation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cites, ok, reason, err := validateCitations(context.Background(), tt.resolver, tt.text, allowed)
			if err != nil {
				t.Fatalf("validateCitations err = %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (reason %q)", ok, tt.wantOK, reason)
			}
			if !ok && reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tt.wantReason)
			}
			if ok && len(cites) != tt.wantCites {
				t.Fatalf("got %d citations, want %d", len(cites), tt.wantCites)
			}
		})
	}
}

func TestValidateCitations_ResolverError(t *testing.T) {
	t.Parallel()
	rollup, ctrlID, _ := sampleRollup()
	_, ok, _, err := validateCitations(
		context.Background(),
		fakeResolver{err: errors.New("db down")},
		"Control ("+ctrlID.String()+") is stale.",
		allowedIDs(rollup),
	)
	if err == nil {
		t.Fatal("expected resolver error to propagate")
	}
	if ok {
		t.Fatal("expected ok=false on resolver error")
	}
}

// ---- prompt builder ----

func TestBuildPrompt_GroundsTheModel(t *testing.T) {
	t.Parallel()
	rollup, ctrlID, evID := sampleRollup()
	block := buildPrompt(rollup)
	if !strings.Contains(block, ctrlID.String()) {
		t.Error("prompt block omits the control id")
	}
	if !strings.Contains(block, evID.String()) {
		t.Error("prompt block omits the cited evidence id")
	}
	if !strings.Contains(block, "Currently stale (in a freshness gap): true") {
		t.Error("prompt block omits the deterministic stale fact")
	}
	if !strings.Contains(block, "access_review.completion") {
		t.Error("prompt block omits the cited excerpt kind")
	}
}

// ---- Service orchestration ----

// stubReturning builds an llm.StubClient that returns the given text with stub
// provenance.
func stubReturning(text string) *llm.StubClient {
	return &llm.StubClient{Result: llm.GenerateResult{
		Text:          text,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "stub",
	}}
}

func TestService_Explain_ValidCitations(t *testing.T) {
	t.Parallel()
	rollup, ctrlID, evID := sampleRollup()
	draft := "Control (" + ctrlID.String() + ") is in a freshness gap; its most recent evidence " +
		evID.String() + " is past the quarterly window."
	svc := NewService(
		fakeRollups{rollup: rollup},
		stubReturning(draft),
		fakeResolver{owned: map[uuid.UUID]Citation{
			ctrlID: {Kind: KindControl, ID: ctrlID.String()},
			evID:   {Kind: KindEvidence, ID: evID.String()},
		}},
	)
	exp, err := svc.Explain(context.Background(), ctrlID)
	if err != nil {
		t.Fatalf("Explain err = %v", err)
	}
	if exp.Suppressed {
		t.Fatalf("expected not suppressed; reason=%q", exp.Reason)
	}
	if exp.Text != draft {
		t.Errorf("Text not surfaced verbatim")
	}
	if len(exp.Citations) != 2 {
		t.Errorf("got %d citations, want 2", len(exp.Citations))
	}
	if exp.ModelName != "stub-model" {
		t.Errorf("model provenance not surfaced: %q", exp.ModelName)
	}
	// AC-7: rollup always present.
	if exp.Rollup.ControlID != ctrlID {
		t.Error("rollup not returned on success")
	}
}

func TestService_Explain_SuppressesFabricatedCitation(t *testing.T) {
	t.Parallel()
	rollup, ctrlID, _ := sampleRollup()
	// Model cites a control id that is NOT in the grounding set.
	draft := "Per record (" + uuid.New().String() + ") this control is fine."
	svc := NewService(
		fakeRollups{rollup: rollup},
		stubReturning(draft),
		fakeResolver{owned: map[uuid.UUID]Citation{ctrlID: {Kind: KindControl, ID: ctrlID.String()}}},
	)
	exp, err := svc.Explain(context.Background(), ctrlID)
	if err != nil {
		t.Fatalf("Explain err = %v", err)
	}
	if !exp.Suppressed || exp.Reason != ReasonUnresolvedCitation {
		t.Fatalf("expected suppression with %q, got suppressed=%v reason=%q", ReasonUnresolvedCitation, exp.Suppressed, exp.Reason)
	}
	if exp.Text != "" {
		t.Error("suppressed explanation must not surface text")
	}
	// AC-7: rollup STILL present on suppression.
	if exp.Rollup.ControlID != ctrlID {
		t.Error("rollup not returned on suppression")
	}
	// Provenance still surfaced (a generation ran).
	if exp.ModelName != "stub-model" {
		t.Error("provenance should be surfaced even when text is suppressed")
	}
}

func TestService_Explain_GenerationUnavailable(t *testing.T) {
	t.Parallel()
	rollup, ctrlID, _ := sampleRollup()
	svc := NewService(
		fakeRollups{rollup: rollup},
		&llm.StubClient{Err: llm.ErrBackend},
		fakeResolver{},
	)
	exp, err := svc.Explain(context.Background(), ctrlID)
	if err != nil {
		t.Fatalf("Explain err = %v (graceful degradation must not error)", err)
	}
	if !exp.Suppressed || exp.Reason != ReasonGenerationUnavailable {
		t.Fatalf("expected %q, got suppressed=%v reason=%q", ReasonGenerationUnavailable, exp.Suppressed, exp.Reason)
	}
	// AC-7: rollup STILL present when the model is down.
	if exp.Rollup.ControlID != ctrlID {
		t.Error("rollup not returned when generation unavailable")
	}
}

func TestService_Explain_RollupErrorPropagates(t *testing.T) {
	t.Parallel()
	svc := NewService(
		fakeRollups{err: errors.New("db unreachable")},
		stubReturning("unused"),
		fakeResolver{},
	)
	if _, err := svc.Explain(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected rollup-assembly error to propagate (no rollup to render)")
	}
}
