package frameworkscope

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDecision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		decision      string
		justification string
		want          error
	}{
		{"applicable without justification", DecisionApplicable, "", nil},
		{"excluded with justification", DecisionExcluded, "No in-house development", nil},
		{"excluded without justification", DecisionExcluded, "", ErrJustificationRequired},
		{"excluded with blank justification", DecisionExcluded, " \t\n", ErrJustificationRequired},
		{"unknown decision", "maybe", "because", ErrInvalidDecision},
		{"empty decision", "", "", ErrInvalidDecision},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidateDecision(tc.decision, tc.justification); !errors.Is(got, tc.want) {
				t.Fatalf("ValidateDecision(%q, %q) = %v, want %v", tc.decision, tc.justification, got, tc.want)
			}
		})
	}
}

func TestApplicabilityFilter(t *testing.T) {
	t.Parallel()
	excl := &Decision{Decision: DecisionExcluded}
	appl := &Decision{Decision: DecisionApplicable}
	a := Applicability{
		Requirements: []RequirementApplicability{
			{Code: "R-01", Decision: excl, Contradiction: true},
			{Code: "R-02", Decision: excl},
			{Code: "R-03"},
			{Code: "R-04", Decision: appl},
		},
		Counts: ApplicabilityCounts{Total: 4, Applicable: 1, Excluded: 2, Undecided: 1, Contradictions: 1},
	}
	yes, no := true, false
	cases := []struct {
		name   string
		filter ApplicabilityFilter
		want   []string
	}{
		{"none", ApplicabilityFilter{}, []string{"R-01", "R-02", "R-03", "R-04"}},
		{"excluded", ApplicabilityFilter{Decision: DecisionExcluded}, []string{"R-01", "R-02"}},
		{"applicable", ApplicabilityFilter{Decision: DecisionApplicable}, []string{"R-04"}},
		{"undecided", ApplicabilityFilter{Decision: FilterUndecided}, []string{"R-03"}},
		{"contradiction", ApplicabilityFilter{Contradiction: &yes}, []string{"R-01"}},
		{"excluded without contradiction", ApplicabilityFilter{Decision: DecisionExcluded, Contradiction: &no}, []string{"R-02"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.filter.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got := a.Filter(tc.filter)
			codes := make([]string, len(got.Requirements))
			for i, r := range got.Requirements {
				codes[i] = r.Code
			}
			if strings.Join(codes, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("Filter(%+v) = %v, want %v", tc.filter, codes, tc.want)
			}
			if got.Counts != a.Counts {
				t.Fatalf("Filter changed counts: %+v", got.Counts)
			}
		})
	}
	if err := (ApplicabilityFilter{Decision: "maybe"}).Validate(); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("Validate(maybe) = %v, want ErrInvalidFilter", err)
	}
}
