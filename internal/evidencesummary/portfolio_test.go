// Pure-Go unit tests for the portfolio (multi-control) evidence-summary service
// (slice 750). No Postgres, no build tag — the slice-353 Q-2 fast tier. They
// exercise the cross-control suppression branches, the two-level bound, the
// cross-control grounding gate, and the portfolio numeric-claim verification via
// fakes. The full request path (real RLS, real cross-tenant resolution, the real
// control-set filter) lives in portfolio_integration_test.go.
package evidencesummary

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// ----- fakes -----

type fakePortfolioReader struct {
	set PortfolioSet
	err error
}

func (f fakePortfolioReader) PortfolioSet(_ context.Context, _ PortfolioFilter) (PortfolioSet, error) {
	return f.set, f.err
}

// controlWith builds one control's corpus slice with `n` records (all owned ids).
func controlWith(ctrlID uuid.UUID, evIDs ...uuid.UUID) ControlEvidence {
	ce := ControlEvidence{ControlID: ctrlID, ControlTitle: "ctrl " + ctrlID.String()[:8], TotalCount: len(evIDs)}
	for i, ev := range evIDs {
		ce.Records = append(ce.Records, EvidenceFact{
			EvidenceID:   ev,
			EvidenceKind: "access_review.completion",
			Result:       "pass",
			ObservedAt:   time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}
	return ce
}

func portfolioSvcWith(reader PortfolioEvidenceReader, draft string, resolver CitationResolver) *PortfolioService {
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text: draft, ModelName: "stub-model", ModelVersion: "1", ModelProvider: "stub",
	}}
	return NewPortfolioService(reader, client, resolver)
}

// ownedFromSet builds a fakeResolver that owns every control id + record id in a
// portfolio set (the "all citable ids resolve" case).
func ownedFromSet(set PortfolioSet) fakeResolver {
	owned := map[uuid.UUID]string{}
	for _, c := range set.Controls {
		owned[c.ControlID] = KindControl
		for _, e := range c.Records {
			owned[e.EvidenceID] = KindEvidence
		}
	}
	return fakeResolver{owned: owned}
}

// ----- AC-3 unit: numeric verification across claim shapes -----

func TestVerifyPortfolioNumbers(t *testing.T) {
	rollup := Rollup{
		ControlsInSummary:       3,
		TotalMatched:            5,
		ControlsWithEvidence:    2,
		ControlsWithoutEvidence: 1,
		TotalRecords:            4,
	}
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"only-allowed-counts", "3 of 5 controls; 2 have evidence, 1 is a gap; 4 records.", true},
		{"zero-is-allowed", "0 controls are fully covered.", true},
		{"no-numbers", "Some controls have recent evidence; others have none.", true},
		{"fabricated-count-40-of-40", "40 of 40 controls have fresh evidence.", false},
		{"fabricated-absent-number", "Across 12 controls, 9 have evidence.", false}, // 12, 9 not in rollup
		{"decimal-fabrication", "Coverage is 84.5 percent.", false},
		{"uuid-not-a-claim", fmt.Sprintf("Control (%s) shows 2 records.", uuid.New()), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := verifyPortfolioNumbers(tt.text, rollup)
			if got != tt.want {
				t.Errorf("verifyPortfolioNumbers(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// The "fabricated-total" case above: 5 and 5 ARE in the allowed set (TotalMatched
// = 5), so "5 of 5" passes the NUMERIC gate — the lie there ("covered") is a
// coverage claim caught by the citation/grounding discipline, not a numeric one.
// Assert that explicitly so the boundary is documented.
func TestVerifyPortfolioNumbers_FiveOfFiveIsNumericallyValid(t *testing.T) {
	rollup := Rollup{ControlsInSummary: 5, TotalMatched: 5, ControlsWithEvidence: 5, ControlsWithoutEvidence: 0, TotalRecords: 7}
	if !verifyPortfolioNumbers("5 of 5 controls; 7 records.", rollup) {
		t.Error("5 of 5 should pass the numeric gate when the rollup says 5 of 5")
	}
}

// ----- AC-1 unit: two-level bound holds in the rollup -----

func TestComputeRollup_TwoLevelBound(t *testing.T) {
	var set PortfolioSet
	set.TotalControls = 30 // filter matched 30, but only the cap entered the corpus
	// One control with evidence, one without.
	c1 := controlWith(uuid.New(), uuid.New(), uuid.New())
	c2 := ControlEvidence{ControlID: uuid.New(), ControlTitle: "gap", TotalCount: 0}
	set.Controls = []ControlEvidence{c1, c2}

	r := computeRollup(set)
	if r.ControlsInSummary != 2 {
		t.Errorf("ControlsInSummary = %d, want 2", r.ControlsInSummary)
	}
	if r.TotalMatched != 30 {
		t.Errorf("TotalMatched = %d, want 30 (the N in K-of-N)", r.TotalMatched)
	}
	if r.ControlsWithEvidence != 1 || r.ControlsWithoutEvidence != 1 {
		t.Errorf("with=%d without=%d, want 1/1", r.ControlsWithEvidence, r.ControlsWithoutEvidence)
	}
	if r.TotalRecords != 2 {
		t.Errorf("TotalRecords = %d, want 2", r.TotalRecords)
	}
}

func TestPortfolioAllowedIDs_UnionAcrossControls(t *testing.T) {
	c1 := controlWith(uuid.New(), uuid.New(), uuid.New())
	c2 := controlWith(uuid.New(), uuid.New())
	set := PortfolioSet{Controls: []ControlEvidence{c1, c2}}
	allowed := portfolioAllowedIDs(set)
	// 2 control ids + 3 evidence ids = 5 citable ids.
	if len(allowed) != 5 {
		t.Fatalf("allowed-id set size = %d, want 5 (every control id + record id)", len(allowed))
	}
	for _, c := range set.Controls {
		if !allowed[c.ControlID] {
			t.Errorf("control id %s missing from grounding set", c.ControlID)
		}
		for _, e := range c.Records {
			if !allowed[e.EvidenceID] {
				t.Errorf("evidence id %s missing from grounding set", e.EvidenceID)
			}
		}
	}
}

// ----- service: valid cross-control summary renders -----

func TestPortfolioSummarize_ValidRenders(t *testing.T) {
	c1 := controlWith(uuid.New(), uuid.New())
	c2 := controlWith(uuid.New(), uuid.New())
	set := PortfolioSet{
		Filter:        PortfolioFilter{Family: "IAC"},
		Controls:      []ControlEvidence{c1, c2},
		TotalControls: 2,
	}
	// Draft cites both controls + both records, states only allowed counts.
	draft := fmt.Sprintf(
		"Across the 2 controls in this summary, 2 have current live evidence and 2 records are on record. Control (%s) cites (%s); control (%s) cites (%s).",
		c1.ControlID, c1.Records[0].EvidenceID, c2.ControlID, c2.Records[0].EvidenceID)
	svc := portfolioSvcWith(fakePortfolioReader{set: set}, draft, ownedFromSet(set))

	sum, err := svc.PortfolioSummarize(context.Background(), set.Filter)
	if err != nil {
		t.Fatalf("PortfolioSummarize: %v", err)
	}
	if sum.Suppressed {
		t.Fatalf("expected rendered, got suppressed (reason %q)", sum.Reason)
	}
	if sum.Text != draft {
		t.Error("summary text not surfaced verbatim")
	}
	if sum.Rollup.ControlsInSummary != 2 || sum.Rollup.TotalRecords != 2 {
		t.Errorf("rollup mismatch: %+v", sum.Rollup)
	}
	// The deterministic set is always echoed back (AC-7).
	if len(sum.PortfolioSet.Controls) != 2 {
		t.Errorf("PortfolioSet not echoed back (got %d controls)", len(sum.PortfolioSet.Controls))
	}
}

// ----- AC-3 service: a fabricated count suppresses -----

func TestPortfolioSummarize_FabricatedCountSuppresses(t *testing.T) {
	c1 := controlWith(uuid.New(), uuid.New())
	set := PortfolioSet{Controls: []ControlEvidence{c1}, TotalControls: 1, Filter: PortfolioFilter{}}
	// Rollup: 1 in summary, 1 matched, 1 with evidence, 0 gaps, 1 record. The
	// draft fabricates "40 of 40".
	draft := fmt.Sprintf("All 40 of 40 controls are covered. Control (%s) cites (%s).",
		c1.ControlID, c1.Records[0].EvidenceID)
	svc := portfolioSvcWith(fakePortfolioReader{set: set}, draft, ownedFromSet(set))

	sum, err := svc.PortfolioSummarize(context.Background(), set.Filter)
	if err != nil {
		t.Fatalf("PortfolioSummarize: %v", err)
	}
	if !sum.Suppressed {
		t.Fatal("expected suppression of fabricated count")
	}
	if sum.Reason != ReasonNumericMismatch {
		t.Errorf("reason = %q, want %q", sum.Reason, ReasonNumericMismatch)
	}
	if sum.Text != "" {
		t.Error("suppressed summary must not carry text")
	}
	// Deterministic set still echoed back (graceful degradation, AC-7).
	if len(sum.PortfolioSet.Controls) != 1 {
		t.Error("PortfolioSet must still be returned on suppression")
	}
}

// ----- AC-2 service: an out-of-grounding citation suppresses -----

func TestPortfolioSummarize_UngroundedCitationSuppresses(t *testing.T) {
	c1 := controlWith(uuid.New(), uuid.New())
	set := PortfolioSet{Controls: []ControlEvidence{c1}, TotalControls: 1}
	// Draft cites a stranger id that is NOT in the grounding set (even if the
	// resolver would own it, the grounding gate fails first).
	stranger := uuid.New()
	draft := fmt.Sprintf("1 control, 1 record. See (%s) and (%s).", c1.ControlID, stranger)
	resolver := ownedFromSet(set)
	resolver.owned[stranger] = KindEvidence // resolver owns it, grounding does not.
	svc := portfolioSvcWith(fakePortfolioReader{set: set}, draft, resolver)

	sum, err := svc.PortfolioSummarize(context.Background(), PortfolioFilter{})
	if err != nil {
		t.Fatalf("PortfolioSummarize: %v", err)
	}
	if !sum.Suppressed || sum.Reason != ReasonUnresolvedCitation {
		t.Fatalf("expected unresolved-citation suppression, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
}

// ----- service: empty corpus degrades without a model call -----

func TestPortfolioSummarize_NoEvidenceDegrades(t *testing.T) {
	// A matched control with zero records => rollup.TotalRecords == 0.
	gap := ControlEvidence{ControlID: uuid.New(), ControlTitle: "gap", TotalCount: 0}
	set := PortfolioSet{Controls: []ControlEvidence{gap}, TotalControls: 1}
	svc := portfolioSvcWith(fakePortfolioReader{set: set}, "should not be used", ownedFromSet(set))

	sum, err := svc.PortfolioSummarize(context.Background(), PortfolioFilter{})
	if err != nil {
		t.Fatalf("PortfolioSummarize: %v", err)
	}
	if !sum.Suppressed || sum.Reason != ReasonNoEvidence {
		t.Fatalf("expected no-evidence suppression, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
}

// ----- service: a generation backend error degrades gracefully -----

func TestPortfolioSummarize_GenerationErrorDegrades(t *testing.T) {
	c1 := controlWith(uuid.New(), uuid.New())
	set := PortfolioSet{Controls: []ControlEvidence{c1}, TotalControls: 1}
	client := &llm.StubClient{Err: errors.New("ollama unreachable")}
	svc := NewPortfolioService(fakePortfolioReader{set: set}, client, ownedFromSet(set))

	sum, err := svc.PortfolioSummarize(context.Background(), PortfolioFilter{})
	if err != nil {
		t.Fatalf("PortfolioSummarize must not error on backend failure: %v", err)
	}
	if !sum.Suppressed || sum.Reason != ReasonGenerationUnavailable {
		t.Fatalf("expected generation-unavailable suppression, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	// The leak-safe reason carries no backend detail.
	if strings.Contains(sum.Reason, "ollama") {
		t.Error("suppression reason must not carry backend error detail (slice-367)")
	}
}

// ----- service: a reader error is surfaced as an error -----

func TestPortfolioSummarize_ReaderErrorSurfaces(t *testing.T) {
	reader := fakePortfolioReader{err: errors.New("db down")}
	svc := portfolioSvcWith(reader, "unused", fakeResolver{})
	_, err := svc.PortfolioSummarize(context.Background(), PortfolioFilter{})
	if err == nil {
		t.Fatal("expected a read error to surface (no evidence set to render)")
	}
}

// ----- numeric: overflow digit run fails verification -----

func TestVerifyPortfolioNumbers_OverflowFails(t *testing.T) {
	rollup := Rollup{ControlsInSummary: 1, TotalMatched: 1, ControlsWithEvidence: 1, TotalRecords: 1}
	// A digit run far beyond int64 must fail (the sentinel is not an allowed value).
	if verifyPortfolioNumbers("There are 99999999999999999999999999 controls.", rollup) {
		t.Error("an overflowing digit run must fail numeric verification")
	}
}

// ----- constructor smoke (NewPortfolioService already covered; exercise both) ---

func TestNewPortfolioService_NotNil(t *testing.T) {
	svc := NewPortfolioService(fakePortfolioReader{}, &llm.StubClient{}, fakeResolver{})
	if svc == nil {
		t.Fatal("NewPortfolioService returned nil")
	}
}

// ----- prompt: both bounds + rollup are stated honestly -----

func TestBuildPortfolioPrompt_StatesBoundsAndRollup(t *testing.T) {
	c1 := controlWith(uuid.New(), uuid.New())
	set := PortfolioSet{
		Filter:        PortfolioFilter{Family: "IAC"},
		Controls:      []ControlEvidence{c1},
		TotalControls: 17,
	}
	rollup := computeRollup(set)
	prompt := buildPortfolioPrompt(set, rollup)
	for _, want := range []string{
		"control family IAC",
		fmt.Sprintf("%d most-relevant of %d matched controls", rollup.ControlsInSummary, 17),
		fmt.Sprintf("up to %d most-recent", MaxRecordsPerControl),
		"Deterministic rollup",
		c1.ControlID.String(),
		c1.Records[0].EvidenceID.String(),
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n--- prompt ---\n%s", want, prompt)
		}
	}
}

// ----- filter Mode classification -----

func TestPortfolioFilter_Mode(t *testing.T) {
	if (PortfolioFilter{}).Mode() != "program" {
		t.Error("empty filter should be program mode")
	}
	if (PortfolioFilter{Family: "IAC"}).Mode() != "family" {
		t.Error("family filter mode")
	}
	if (PortfolioFilter{FrameworkVersionID: uuid.New()}).Mode() != "framework" {
		t.Error("framework filter mode")
	}
}
