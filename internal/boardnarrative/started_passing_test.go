// #2195: both drift-bearing sections (coverage, drift activity) carry the
// brief's started-passing count beside flipped-out, name delta as a net change
// in passing controls, and the numeric gate admits the count only when the
// brief recorded one.

package boardnarrative

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/board"
)

// threeGainsOneLossBrief: 3 controls started passing, 1 drifted out, and the
// net change is +1 (one control failed and recovered inside the window), so
// gains minus losses (2) is not a ground-truth number.
func threeGainsOneLossBrief(started *int) board.Brief {
	return board.Brief{
		PeriodEnd: "2026-05-31",
		Frameworks: []board.FrameworkPosture{
			{Slug: "soc2", Name: "SOC 2", CoveragePct: 84, FreshnessPct: 91, Delta: 1},
		},
		Coverage: &board.CoverageSummary{CoveragePct: 84, ScopedFrameworkCount: 1, EvidenceFreshnessRatePct: 91},
		Drift: board.DriftSummary{
			WindowDays: 30, Delta: 1, FlippedOutCount: 1, StartedPassingCount: started,
		},
	}
}

func TestStartedPassing_ThreeGainsOneLoss(t *testing.T) {
	t.Parallel()
	three := 3
	b := threeGainsOneLossBrief(&three)
	for _, section := range []SectionKey{SectionControlCoverage, SectionDriftActivity} {
		t.Run(string(section), func(t *testing.T) {
			t.Parallel()
			r, err := RollupForSection(b, nil, section)
			if err != nil {
				t.Fatalf("RollupForSection: %v", err)
			}
			if r.StartedPassingCount == nil || *r.StartedPassingCount != 3 {
				t.Fatalf("rollup StartedPassingCount = %v, want 3", r.StartedPassingCount)
			}
			def, _ := sectionDef(section)
			prompt := def.userPrompt(r)
			for _, want := range []string{
				"net_change_in_passing_controls_30d: 1\n",
				"controls_started_passing: 3\n",
				"controls_drifted_out: 1\n",
			} {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q\ngot:\n%s", want, prompt)
				}
			}
			if !strings.Contains(def.systemPrompt(), "how many controls started passing") {
				t.Errorf("section template does not ask for the started-passing count")
			}
			if got := sectionContextInputs(section, r)["controls_started_passing"]; got != 3 {
				t.Errorf("context inputs controls_started_passing = %v, want 3", got)
			}

			allowed := r.AllowedNumbers()
			if !VerifyNumbers("Over 30 days the net change was +1: 3 controls started passing and 1 drifted out.", allowed, r.PeriodEnd) {
				t.Errorf("gate rejected the ground-truth numbers; allowed=%v", allowed)
			}
			if VerifyNumbers("2 more controls pass than drifted out.", allowed, r.PeriodEnd) {
				t.Errorf("gate admitted gains minus losses (2), which is not ground truth")
			}
		})
	}
}

// A brief stored before #2195 has no started-passing count: the prompt does
// not state one, and the gate admits no gains number for it.
func TestStartedPassing_LegacyBrief(t *testing.T) {
	t.Parallel()
	b := threeGainsOneLossBrief(nil)
	for _, section := range []SectionKey{SectionControlCoverage, SectionDriftActivity} {
		t.Run(string(section), func(t *testing.T) {
			t.Parallel()
			r, err := RollupForSection(b, nil, section)
			if err != nil {
				t.Fatalf("RollupForSection: %v", err)
			}
			def, _ := sectionDef(section)
			if prompt := def.userPrompt(r); strings.Contains(prompt, "controls_started_passing") {
				t.Errorf("legacy prompt states a started-passing count:\n%s", prompt)
			}
			if _, ok := sectionContextInputs(section, r)["controls_started_passing"]; ok {
				t.Errorf("legacy context inputs carry controls_started_passing")
			}
			if VerifyNumbers("3 controls started passing.", r.AllowedNumbers(), r.PeriodEnd) {
				t.Errorf("gate admitted a started-passing count the brief never recorded")
			}
		})
	}
}
