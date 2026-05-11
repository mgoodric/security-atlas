package scope_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/scope"
)

// TestEvaluate covers the JSON-AST evaluator. Each case names the AC it backs.
//
// AC-3 — `environment IN ('prod','staging') AND data_classification IN
//
//	('restricted','confidential')` returns matching cells from the universe.
//
// AC-4 — Empty/nil expression matches every cell.
func TestEvaluate(t *testing.T) {
	cells := []scope.Cell{
		{Label: "prod-restricted", Dimensions: map[string]string{
			"environment": "prod", "data_classification": "restricted",
		}},
		{Label: "prod-public", Dimensions: map[string]string{
			"environment": "prod", "data_classification": "public",
		}},
		{Label: "staging-confidential", Dimensions: map[string]string{
			"environment": "staging", "data_classification": "confidential",
		}},
		{Label: "dev-internal", Dimensions: map[string]string{
			"environment": "dev", "data_classification": "internal",
		}},
	}

	tests := []struct {
		name   string
		exprJS string
		want   []string // labels
	}{
		{
			name:   "AC-4 nil expression matches all",
			exprJS: ``,
			want:   []string{"prod-restricted", "prod-public", "staging-confidential", "dev-internal"},
		},
		{
			name:   "AC-4 empty object matches all",
			exprJS: `{}`,
			want:   []string{"prod-restricted", "prod-public", "staging-confidential", "dev-internal"},
		},
		{
			name:   "AC-4 explicit true matches all",
			exprJS: `{"op":"true"}`,
			want:   []string{"prod-restricted", "prod-public", "staging-confidential", "dev-internal"},
		},
		{
			name:   "AC-3 eq",
			exprJS: `{"op":"eq","dim":"environment","value":"prod"}`,
			want:   []string{"prod-restricted", "prod-public"},
		},
		{
			name:   "AC-3 in single dim",
			exprJS: `{"op":"in","dim":"environment","values":["prod","staging"]}`,
			want:   []string{"prod-restricted", "prod-public", "staging-confidential"},
		},
		{
			name: "AC-3 and over two in-clauses (canvas §5.1 example)",
			exprJS: `{"op":"and","args":[
				{"op":"in","dim":"environment","values":["prod","staging"]},
				{"op":"in","dim":"data_classification","values":["restricted","confidential"]}
			]}`,
			want: []string{"prod-restricted", "staging-confidential"},
		},
		{
			name: "AC-3 or",
			exprJS: `{"op":"or","args":[
				{"op":"eq","dim":"environment","value":"dev"},
				{"op":"eq","dim":"data_classification","value":"public"}
			]}`,
			want: []string{"prod-public", "dev-internal"},
		},
		{
			name:   "AC-3 not",
			exprJS: `{"op":"not","arg":{"op":"eq","dim":"environment","value":"prod"}}`,
			want:   []string{"staging-confidential", "dev-internal"},
		},
		{
			name:   "missing dim on cell is treated as no-match (not panic)",
			exprJS: `{"op":"eq","dim":"product_line","value":"core"}`,
			want:   []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scope.Evaluate([]byte(tc.exprJS), cells)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			gotLabels := labelsOf(got)
			if !equalSlices(gotLabels, tc.want) {
				t.Fatalf("labels = %v; want %v", gotLabels, tc.want)
			}
		})
	}
}

// TestEvaluate_InvalidJSON_RejectsLoudly verifies the anti-criterion: do NOT
// silently drop cells when the expression is malformed.
func TestEvaluate_InvalidJSON_RejectsLoudly(t *testing.T) {
	cells := []scope.Cell{{Label: "x", Dimensions: map[string]string{"environment": "prod"}}}

	cases := []struct {
		name   string
		exprJS string
	}{
		{"unknown op", `{"op":"contains","dim":"environment","value":"prod"}`},
		{"in missing values", `{"op":"in","dim":"environment"}`},
		{"eq missing value", `{"op":"eq","dim":"environment"}`},
		{"missing dim on eq", `{"op":"eq","value":"prod"}`},
		{"and with no args", `{"op":"and"}`},
		{"not with no arg", `{"op":"not"}`},
		{"malformed json", `{op:eq}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scope.Evaluate([]byte(tc.exprJS), cells)
			if err == nil {
				t.Fatalf("expected error for %q", tc.exprJS)
			}
		})
	}
}

func labelsOf(cs []scope.Cell) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Label)
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
