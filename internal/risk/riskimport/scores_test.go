package riskimport

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// scoreRow is what a score parse test asserts of one data row: its scores
// as decoded JSON when staged, or its rejection reason.
type scoreRow struct {
	Inherent, Residual map[string]any
	Rejected           string
}

func scoreRows(t *testing.T, res *ParseResult) map[int]scoreRow {
	t.Helper()
	out := map[int]scoreRow{}
	decode := func(raw json.RawMessage) map[string]any {
		if raw == nil {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode score %s: %v", raw, err)
		}
		return m
	}
	for _, r := range res.Risks {
		out[r.RowNumber] = scoreRow{Inherent: decode(r.InherentScore), Residual: decode(r.ResidualScore)}
	}
	for _, r := range res.Rejected {
		out[r.RowNumber] = scoreRow{Rejected: r.Reason}
	}
	return out
}

func TestParseScores(t *testing.T) {
	t.Parallel()
	grid := func(l, i float64) map[string]any { return map[string]any{"likelihood": l, "impact": i} }
	fair := func(lef, lm float64) map[string]any {
		return map[string]any{"loss_event_frequency": lef, "loss_magnitude": lm, "annualized_loss_exposure": lef * lm}
	}
	const gridHeader = "Title,Category,Inherent Likelihood,Inherent Impact,Residual Likelihood,Residual Impact\n"
	const gridRows = "" +
		"Scored,operational,4,5,2,3\n" + // row 2
		"Inherent only,operational,3,3,,\n" + // row 3
		"Unscored,operational,,,,\n" + // row 4
		"Half a pair,operational,3,,,\n" + // row 5
		"Out of range,operational,6,2,,\n" + // row 6
		"Not a number,operational,high,2,,\n" + // row 7
		"Residual without inherent,operational,,,2,2\n" + // row 8
		"Zero,operational,0,2,,\n" + // row 9
		"Fraction,operational,2.5,2,,\n" // row 10
	gridWant := map[int]scoreRow{
		2:  {Inherent: grid(4, 5), Residual: grid(2, 3)},
		3:  {Inherent: grid(3, 3)},
		4:  {},
		5:  {Rejected: RejectInvalidScore},
		6:  {Rejected: RejectInvalidScore},
		7:  {Rejected: RejectInvalidScore},
		8:  {Rejected: RejectInvalidScore},
		9:  {Rejected: RejectInvalidScore},
		10: {Rejected: RejectInvalidScore},
	}
	cases := []struct {
		name        string
		methodology dbx.RiskMethodology
		csv         string
		columnMap   map[string]string
		want        map[int]scoreRow
		unmapped    []string
	}{
		{"qualitative 5x5", dbx.RiskMethodologyQualitative5x5, gridHeader + gridRows, nil, gridWant, []string{}},
		{"nist 800-30", dbx.RiskMethodologyNist80030, gridHeader + gridRows, nil, gridWant, []string{}},
		{"the default is nist 800-30", "", gridHeader + gridRows, nil, gridWant, []string{}},
		{
			name:        "bare likelihood and impact are the inherent score",
			methodology: dbx.RiskMethodologyQualitative5x5,
			csv:         "Title,Category,Likelihood,Impact\nA,operational,2,4\n",
			want:        map[int]scoreRow{2: {Inherent: grid(2, 4)}},
			unmapped:    []string{},
		},
		{
			name:        "fair",
			methodology: dbx.RiskMethodologyFair,
			csv: "Title,Category,Loss Event Frequency,Loss Magnitude,Residual LEF,Residual LM\n" +
				"Scored,operational,0.5,250000,0.1,100000\n" + // row 2
				"Inherent only,operational,2,1000,,\n" + // row 3
				"Unscored,operational,,,,\n" + // row 4
				"Half a pair,operational,0.5,,,\n" + // row 5
				"Negative,operational,-1,1000,,\n" + // row 6
				"Currency,operational,0.5,\"$250,000\",,\n" + // row 7
				"Infinite,operational,Inf,1000,,\n" + // row 8
				"Residual without inherent,operational,,,0.1,10\n", // row 9
			want: map[int]scoreRow{
				2: {Inherent: fair(0.5, 250000), Residual: fair(0.1, 100000)},
				// The create path's rule: a FAIR residual left out takes the inherent.
				3: {Inherent: fair(2, 1000), Residual: fair(2, 1000)},
				4: {},
				5: {Rejected: RejectInvalidScore},
				6: {Rejected: RejectInvalidScore},
				7: {Rejected: RejectInvalidScore},
				8: {Rejected: RejectInvalidScore},
				9: {Rejected: RejectInvalidScore},
			},
			unmapped: []string{},
		},
		{
			name:        "another methodology's score columns stay unmapped",
			methodology: dbx.RiskMethodologyFair,
			csv:         "Title,Category,Likelihood,Impact,LEF,LM\nA,operational,9,9,1,10\n",
			want:        map[int]scoreRow{2: {Inherent: fair(1, 10), Residual: fair(1, 10)}},
			unmapped:    []string{"Likelihood", "Impact"},
		},
		{
			name:        "score columns by column map",
			methodology: dbx.RiskMethodologyQualitative5x5,
			csv:         "Title,Category,L,I\nA,operational,1,5\n",
			columnMap:   map[string]string{FieldInherentLikelihood: "L", FieldInherentImpact: "I"},
			want:        map[int]scoreRow{2: {Inherent: grid(1, 5)}},
			unmapped:    []string{},
		},
		{
			name:        "no score columns at all imports unscored",
			methodology: dbx.RiskMethodologyFair,
			csv:         "Title,Category\nA,operational\n",
			want:        map[int]scoreRow{2: {}},
			unmapped:    []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCSVWith([]byte(tc.csv), ParseOptions{ColumnMap: tc.columnMap, Methodology: tc.methodology})
			if err != nil {
				t.Fatalf("ParseCSVWith: %v", err)
			}
			wantM := tc.methodology
			if wantM == "" {
				wantM = dbx.RiskMethodologyNist80030
			}
			if got.Methodology != wantM {
				t.Errorf("methodology = %q, want %q", got.Methodology, wantM)
			}
			if rows := scoreRows(t, got); !reflect.DeepEqual(rows, tc.want) {
				t.Errorf("rows =\n%+v\nwant\n%+v\nrejected: %+v", rows, tc.want, got.Rejected)
			}
			if !reflect.DeepEqual(got.UnmappedColumns, tc.unmapped) {
				t.Errorf("unmapped = %q, want %q", got.UnmappedColumns, tc.unmapped)
			}
		})
	}
}

// TestParseScoresRejectionDetail pins the reason a person reads for each way
// a score is invalid.
func TestParseScoresRejectionDetail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		methodology dbx.RiskMethodology
		header, row string
		want        string
	}{
		{dbx.RiskMethodologyQualitative5x5, "Likelihood,Impact", "3,", "inherent impact is missing: give both inherent likelihood and inherent impact, or neither"},
		{dbx.RiskMethodologyQualitative5x5, "Likelihood,Impact", "7,2", `inherent likelihood "7" is not a whole number from 1 to 5`},
		{dbx.RiskMethodologyNist80030, "Residual Likelihood,Residual Impact", "2,2", "residual scores need inherent scores: give inherent likelihood and inherent impact too"},
		{dbx.RiskMethodologyFair, "LEF,LM", "0.5,\"1,000\"", `inherent loss magnitude "1,000" is not a plain non-negative number such as 0.5 or 250000`},
		{dbx.RiskMethodologyQualitative5x5, "Likelihood,Impact", strings.Repeat("9", 60) + ",2", `inherent likelihood "` + strings.Repeat("9", 40) + `…" is not a whole number from 1 to 5`},
	}
	for _, tc := range cases {
		got, err := ParseCSVWith([]byte("Title,Category,"+tc.header+"\nA,operational,"+tc.row+"\n"), ParseOptions{Methodology: tc.methodology})
		if err != nil {
			t.Fatalf("%s: %v", tc.row, err)
		}
		if len(got.Rejected) != 1 || got.Rejected[0].Detail != tc.want {
			t.Errorf("%s %s: rejected = %+v, want detail %q", tc.methodology, tc.row, got.Rejected, tc.want)
		}
	}
}

func TestParseScoresXLSX(t *testing.T) {
	t.Parallel()
	raw := xlsxOf(t, [][]any{
		{"Title", "Category", "Inherent LEF", "Inherent LM"},
		{"Numeric cells", "operational", 0.25, 40000},
	})
	got, err := Parse("register.xlsx", raw, ParseOptions{Methodology: dbx.RiskMethodologyFair})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rows := scoreRows(t, got)
	want := map[string]any{"loss_event_frequency": 0.25, "loss_magnitude": 40000.0, "annualized_loss_exposure": 10000.0}
	if !reflect.DeepEqual(rows[2].Inherent, want) {
		t.Errorf("row 2 = %+v, want inherent %v", rows[2], want)
	}
}

func TestParseMethodologyErrors(t *testing.T) {
	t.Parallel()
	for _, m := range []dbx.RiskMethodology{dbx.RiskMethodologyCisRam, dbx.RiskMethodologyIso27005, "five_by_five"} {
		if _, err := ParseCSVWith([]byte("Title,Category\nA,operational\n"), ParseOptions{Methodology: m}); !errors.Is(err, ErrInvalidMethodology) {
			t.Errorf("methodology %q: err = %v, want ErrInvalidMethodology", m, err)
		}
	}
	_, err := ParseCSVWith([]byte("Title,Category,L\nA,operational,1\n"), ParseOptions{
		Methodology: dbx.RiskMethodologyFair,
		ColumnMap:   map[string]string{FieldInherentLikelihood: "L"},
	})
	if !errors.Is(err, ErrInvalidColumnMap) {
		t.Errorf("a 5x5 field mapped in a fair batch: err = %v, want ErrInvalidColumnMap", err)
	}
}
