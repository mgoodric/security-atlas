// #2195: the brief's drift sentence states controls that started passing
// beside those that drifted out, and labels delta as the net change. A
// brief stored before started_passing_count existed still decodes and
// renders without claiming zero gains.

package board

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMovementClause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		d    DriftSummary
		want string
	}{
		{"three gains one loss", DriftSummary{FlippedOutCount: 1, StartedPassingCount: intPtr(3)},
			"3 control(s) started passing and 1 control(s) drifted out of passing"},
		{"gains only", DriftSummary{StartedPassingCount: intPtr(2)},
			"2 control(s) started passing and no controls drifted out of passing"},
		{"no movement", DriftSummary{StartedPassingCount: intPtr(0)},
			"no controls started or stopped passing"},
		{"legacy brief, loss", DriftSummary{FlippedOutCount: 1}, "1 control(s) drifted out of passing"},
		{"legacy brief, none", DriftSummary{}, "no controls drifted out of passing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.d.MovementClause(); got != tc.want {
				t.Errorf("MovementClause() = %q, want %q", got, tc.want)
			}
		})
	}
}

// threeGainsOneLoss has a net change of +1, not +2: one control failed and
// recovered inside the window, so gains minus losses is not delta.
func threeGainsOneLoss() DriftSummary {
	return DriftSummary{
		WindowDays: 30, Since: "2026-03-31", Through: "2026-04-30",
		Delta: 1, FlippedOutCount: 1, StartedPassingCount: intPtr(3),
	}
}

func TestRenderNarrative_ThreeGainsOneLoss(t *testing.T) {
	t.Parallel()
	b := sampleBrief()
	b.Drift = threeGainsOneLoss()
	md, err := RenderNarrative(b)
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	want := "the net change in passing controls is +1 — 3 control(s) started passing and 1 control(s) drifted out of passing."
	if !strings.Contains(md, want) {
		t.Errorf("narrative missing %q\ngot:\n%s", want, md)
	}
}

func TestBuildBriefHTML_ThreeGainsOneLoss(t *testing.T) {
	t.Parallel()
	sb := sampleStoredBrief()
	sb.Content.Drift = threeGainsOneLoss()
	got := buildBriefHTML(sb)
	want := "net change in passing controls +1 — 3 control(s) started passing and 1 control(s) drifted out of passing."
	if !strings.Contains(got, want) {
		t.Errorf("brief HTML missing %q", want)
	}
}

func TestDriftSummary_JSON(t *testing.T) {
	t.Parallel()
	// A brief stored before #2195 carries no started_passing_count.
	var legacy DriftSummary
	if err := json.Unmarshal([]byte(`{"window_days":30,"since":"2026-03-31","through":"2026-04-30","delta":-1,"flipped_out_count":1}`), &legacy); err != nil {
		t.Fatalf("decode legacy drift summary: %v", err)
	}
	if legacy.StartedPassingCount != nil {
		t.Fatalf("legacy StartedPassingCount = %d, want nil", *legacy.StartedPassingCount)
	}
	if got := legacy.MovementClause(); got != "1 control(s) drifted out of passing" {
		t.Errorf("legacy MovementClause() = %q", got)
	}

	// A new brief records the count, zero included.
	raw, err := json.Marshal(DriftSummary{StartedPassingCount: intPtr(0)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"started_passing_count":0`) {
		t.Errorf("zero started_passing_count must be stored, got %s", raw)
	}
}
