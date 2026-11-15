// #2210: the coverage section grounds on the board pack's coverage figure,
// not the slice-031 tenant-wide one. These tests pin the gate to the new
// integers and the pack's one-decimal rendering.
package boardnarrative

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/board"
)

// postureRollup is a coverage rollup for a tenant whose two scoped
// frameworks average 41.9% (the pack prints 41.93 as "41.9"), with a
// tenant-wide evidence freshness rate of 67%.
func postureRollup(t *testing.T, cov *board.CoverageSummary) (Rollup, string) {
	t.Helper()
	ctrlID := uuid.NewString()
	b := board.Brief{
		PeriodEnd: "2026-10-31",
		Frameworks: []board.FrameworkPosture{
			{Slug: "soc2", Name: "SOC 2", CoveragePct: 63.86, State: "at-risk"},
			{Slug: "iso27001", Name: "ISO 27001", CoveragePct: 20, State: "at-risk"},
			{Slug: "pci", Name: "PCI DSS", ScopeNotSet: true, State: board.PostureScopeNotSet},
		},
		Coverage: cov,
		Drift:    board.DriftSummary{WindowDays: 30, Delta: -4, FlippedOutCount: 4},
	}
	r, err := RollupFromBrief(b, []Excerpt{{ID: ctrlID, Kind: KindControl, Title: "Access reviews"}})
	if err != nil {
		t.Fatalf("RollupFromBrief: %v", err)
	}
	return r, ctrlID
}

func coverageDraft(item1, item2, ctrlID string) string {
	return strings.Join([]string{
		"## Control coverage summary",
		"1. " + item1,
		"2. " + item2,
		"3. Over the last 30 days the net change in passing controls was -4; 4 controls drifted out of passing.",
		"4. The program runs against 3 frameworks (" + ctrlID + ").",
		"",
	}, "\n")
}

func TestCoverageGate_PacksFigure(t *testing.T) {
	t.Parallel()
	r, ctrlID := postureRollup(t, &board.CoverageSummary{CoveragePct: 41.93, ScopedFrameworkCount: 2, EvidenceFreshnessRatePct: 67})
	fresh := "The tenant-wide evidence freshness rate is 67%."
	cases := []struct {
		name  string
		item1 string
		item2 string
		want  bool
	}{
		{"pack-figure-passes", "Requirement coverage averages 41.9% across the 2 frameworks with an activated scope.", fresh, true},
		{"rounded-figure-rejects", "Requirement coverage averages 42% across the 2 frameworks with an activated scope.", fresh, false},
		{"unrendered-precision-rejects", "Requirement coverage averages 41.93% across the 2 frameworks with an activated scope.", fresh, false},
		{"other-decimal-rejects", "Requirement coverage averages 41.8% across the 2 frameworks with an activated scope.", fresh, false},
		{"count-as-percent-rejects", "Requirement coverage averages 41.9% across 2% of frameworks.", fresh, false},
		{"freshness-as-decimal-rejects", "Requirement coverage averages 41.9% across the 2 frameworks with an activated scope.", "The evidence freshness rate is 67.0%.", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := r.verifyDraftNumbers(coverageDraft(tc.item1, tc.item2, ctrlID)); got != tc.want {
				t.Fatalf("verifyDraftNumbers = %v, want %v", got, tc.want)
			}
		})
	}
}

// A stale tenant-wide coverage figure that is not also the freshness rate is
// rejected outright: the brief no longer carries it, so the gate has no
// number to match it to.
func TestCoverageGate_StaleTenantWideFigureRejected(t *testing.T) {
	t.Parallel()
	// Pre-#2210 this tenant's brief said coverage 88 (fresh controls with
	// evidence / controls with evidence). The rollup now carries 41.9.
	r, ctrlID := postureRollup(t, &board.CoverageSummary{CoveragePct: 41.93, ScopedFrameworkCount: 2, EvidenceFreshnessRatePct: 67})
	draft := coverageDraft("Control coverage stands at 88%.", "The evidence freshness rate is 67%.", ctrlID)
	if r.verifyDraftNumbers(draft) {
		t.Fatal("a draft stating the retired tenant-wide coverage figure passed the gate")
	}
	if r.AllowedNumbers()[88] {
		t.Fatal("AllowedNumbers admits the retired tenant-wide figure")
	}
}

// A whole-number coverage figure is stated as the pack prints it ("75").
func TestCoverageGate_WholeNumberFigure(t *testing.T) {
	t.Parallel()
	r, ctrlID := postureRollup(t, &board.CoverageSummary{CoveragePct: 75, ScopedFrameworkCount: 2, EvidenceFreshnessRatePct: 67})
	if !r.verifyDraftNumbers(coverageDraft("Requirement coverage averages 75% across the 2 frameworks with an activated scope.", "The evidence freshness rate is 67%.", ctrlID)) {
		t.Fatal("the pack's whole-number figure was rejected")
	}
	if r.verifyDraftNumbers(coverageDraft("Requirement coverage averages 75.0% across the 2 frameworks.", "The evidence freshness rate is 67%.", ctrlID)) {
		t.Fatal("75.0 is not how the pack renders 75 and must be rejected")
	}
}

// No activated scope: the section says so in words. A 0% coverage claim is
// rejected even though 0 may be a real count elsewhere in the rollup.
func TestCoverageGate_NoScopedFramework(t *testing.T) {
	t.Parallel()
	r, ctrlID := postureRollup(t, &board.CoverageSummary{EvidenceFreshnessRatePct: 67})
	r.FlippedOutCount = 0 // 0 is now a legitimate count in the rollup
	words := coverageDraft("No framework has an activated scope, so no coverage figure is reported.", "The evidence freshness rate is 67%.", ctrlID)
	words = strings.Replace(words, "4 controls drifted out", "0 controls drifted out", 1)
	if !r.verifyDraftNumbers(words) {
		t.Fatal("the no-scope sentence was rejected")
	}
	zero := strings.Replace(words, "No framework has an activated scope, so no coverage figure is reported.", "Requirement coverage is 0%.", 1)
	if r.verifyDraftNumbers(zero) {
		t.Fatal("a 0% coverage claim passed with no scoped framework")
	}
	prompt := buildPrompt(r)
	if !strings.Contains(prompt, "control_coverage_pct: not reported, because no framework has an activated scope") ||
		strings.Contains(prompt, "scoped_framework_count") {
		t.Fatalf("prompt for an unscoped tenant:\n%s", prompt)
	}
}

func TestCoveragePrompt_StatesThePacksFigure(t *testing.T) {
	t.Parallel()
	r, _ := postureRollup(t, &board.CoverageSummary{CoveragePct: 41.93, ScopedFrameworkCount: 2, EvidenceFreshnessRatePct: 67})
	prompt := buildPrompt(r)
	for _, want := range []string{
		"control_coverage_pct: 41.9 (",
		"scoped_framework_count: 2",
		"evidence_freshness_rate_pct: 67 (tenant-wide",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(buildSystemPrompt(), "call it the evidence freshness rate, never coverage") {
		t.Error("system prompt does not keep the freshness rate apart from coverage")
	}
	if promptVersion != "boardnarrative-coverage-v2" {
		t.Errorf("promptVersion = %q, want boardnarrative-coverage-v2", promptVersion)
	}
}

// A brief assembled before #2210 carries only the tenant-wide figure; the
// coverage section refuses to ground on it.
func TestRollupFromBrief_LegacyBriefHasNoCoverage(t *testing.T) {
	t.Parallel()
	b := board.Brief{
		PeriodEnd:  "2026-04-30",
		Frameworks: []board.FrameworkPosture{{Slug: "soc2", Name: "SOC 2", CoveragePct: 94, FreshnessPct: 88}},
	}
	if _, err := RollupFromBrief(b, nil); !errors.Is(err, ErrNoBriefData) {
		t.Fatalf("RollupFromBrief(legacy) err = %v, want ErrNoBriefData", err)
	}
}
