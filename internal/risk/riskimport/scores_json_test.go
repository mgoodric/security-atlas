package riskimport

import (
	"reflect"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestParseScoresJSON pins the risk export's score columns (#2898): JSON in
// the score's shape, read through the create path's rule for the batch's
// methodology.
func TestParseScoresJSON(t *testing.T) {
	t.Parallel()
	grid := func(l, i float64) map[string]any { return map[string]any{"likelihood": l, "impact": i} }
	fair := func(lef, lm float64) map[string]any {
		return map[string]any{"loss_event_frequency": lef, "loss_magnitude": lm, "annualized_loss_exposure": lef * lm}
	}
	const header = "title,category,inherent_score,residual_score\n"
	cases := []struct {
		name        string
		methodology dbx.RiskMethodology
		row         string
		want        scoreRow
		detail      string
	}{
		{"grid with residual", dbx.RiskMethodologyNist80030, `A,operational,"{""likelihood"":4,""impact"":5}","{""likelihood"":2,""impact"":3}"`,
			scoreRow{Inherent: grid(4, 5), Residual: grid(2, 3)}, ""},
		{"grid with the export's unscored residual", dbx.RiskMethodologyQualitative5x5, `A,operational,"{""likelihood"":1,""impact"":2}",{}`,
			scoreRow{Inherent: grid(1, 2)}, ""},
		{"the export's unscored risk", dbx.RiskMethodologyNist80030, `A,operational,{},{}`, scoreRow{}, ""},
		{"null is unscored", dbx.RiskMethodologyFair, `A,operational,null,`, scoreRow{}, ""},
		{"fair with residual", dbx.RiskMethodologyFair,
			`A,operational,"{""loss_event_frequency"":0.5,""loss_magnitude"":1000,""annualized_loss_exposure"":500}","{""loss_event_frequency"":0.1,""loss_magnitude"":1000}"`,
			scoreRow{Inherent: fair(0.5, 1000), Residual: fair(0.1, 1000)}, ""},
		{"fair legacy aliases are normalised", dbx.RiskMethodologyFair, `A,operational,"{""lef"":2,""lm"":10}",`,
			scoreRow{Inherent: fair(2, 10), Residual: fair(2, 10)}, ""},
		{"a fair score in a grid batch", dbx.RiskMethodologyNist80030, `A,operational,"{""loss_event_frequency"":0.5,""loss_magnitude"":1000}",`,
			scoreRow{Rejected: RejectInvalidScore}, ""},
		{"a grid score in a fair batch", dbx.RiskMethodologyFair, `A,operational,"{""likelihood"":4,""impact"":5}",`,
			scoreRow{Rejected: RejectInvalidScore}, ""},
		{"out of range", dbx.RiskMethodologyQualitative5x5, `A,operational,"{""likelihood"":6,""impact"":5}",`,
			scoreRow{Rejected: RejectInvalidScore}, ""},
		{"a string score field", dbx.RiskMethodologyNist80030, `A,operational,"{""likelihood"":4,""impact"":5}","{""score"":""12""}"`,
			scoreRow{Rejected: RejectInvalidScore}, ""},
		{"not JSON", dbx.RiskMethodologyNist80030, `A,operational,High,`, scoreRow{Rejected: RejectInvalidScore}, ""},
		{"not an object", dbx.RiskMethodologyNist80030, `A,operational,12,`, scoreRow{Rejected: RejectInvalidScore}, ""},
		{"a residual without an inherent", dbx.RiskMethodologyNist80030, `A,operational,{},"{""likelihood"":2,""impact"":3}"`,
			scoreRow{Rejected: RejectInvalidScore},
			"residual scores need inherent scores: give inherent likelihood and inherent impact, or inherent score, too"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCSVWith([]byte(header+tc.row+"\n"), ParseOptions{Methodology: tc.methodology})
			if err != nil {
				t.Fatalf("ParseCSVWith: %v", err)
			}
			if len(got.UnmappedColumns) != 0 {
				t.Errorf("unmapped = %q", got.UnmappedColumns)
			}
			if rows := scoreRows(t, got); !reflect.DeepEqual(rows[2], tc.want) {
				t.Errorf("row 2 = %+v, want %+v (rejected %+v)", rows[2], tc.want, got.Rejected)
			}
			if tc.detail != "" && (len(got.Rejected) != 1 || got.Rejected[0].Detail != tc.detail) {
				t.Errorf("rejected = %+v, want detail %q", got.Rejected, tc.detail)
			}
		})
	}
}

// TestParseScoresJSONAndPair: a score is given as JSON or as its pair of
// cells, never both on one row; a row may give each score its own way.
func TestParseScoresJSONAndPair(t *testing.T) {
	t.Parallel()
	csv := "title,category,inherent_score,Likelihood,Impact,Residual Likelihood,Residual Impact\n" +
		`Both,operational,"{""likelihood"":4,""impact"":5}",4,5,,` + "\n" + // row 2
		`JSON inherent pair residual,operational,"{""likelihood"":4,""impact"":5}",,,2,2` + "\n" + // row 3
		`Pair only,operational,,3,3,,` + "\n" // row 4
	got, err := ParseCSVWith([]byte(csv), ParseOptions{})
	if err != nil {
		t.Fatalf("ParseCSVWith: %v", err)
	}
	rows := scoreRows(t, got)
	const both = "give the score as inherent score or as inherent likelihood and inherent impact, not both"
	if rows[2].Rejected != RejectInvalidScore || len(got.Rejected) != 1 || got.Rejected[0].Detail != both {
		t.Errorf("row 2 = %+v (rejected %+v)", rows[2], got.Rejected)
	}
	want3 := scoreRow{Inherent: map[string]any{"likelihood": 4.0, "impact": 5.0}, Residual: map[string]any{"likelihood": 2.0, "impact": 2.0}}
	if !reflect.DeepEqual(rows[3], want3) {
		t.Errorf("row 3 = %+v, want %+v", rows[3], want3)
	}
	if want4 := (scoreRow{Inherent: map[string]any{"likelihood": 3.0, "impact": 3.0}}); !reflect.DeepEqual(rows[4], want4) {
		t.Errorf("row 4 = %+v, want %+v", rows[4], want4)
	}
}
