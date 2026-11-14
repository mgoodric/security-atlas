package frameworkscope

import (
	"errors"
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
