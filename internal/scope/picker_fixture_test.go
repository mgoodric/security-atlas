package scope_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/mgoodric/security-atlas/internal/scope"
)

// TestPickerFixturePredicatesEvaluate is the Go half of the #2103 shared
// fixture: every predicate the web scope-dimension picker emits or loads
// must be one scope.Evaluate accepts, and must select the cells the
// fixture says. web/components/scope/scope-picker-model.test.ts reads the
// same file, so a grammar change in either tier fails one of them.
func TestPickerFixturePredicatesEvaluate(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/picker-predicates.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Universe []struct {
			Label      string            `json:"label"`
			Dimensions map[string]string `json:"dimensions"`
		} `json:"universe"`
		Cases []struct {
			Name      string          `json:"name"`
			Predicate json.RawMessage `json:"predicate"`
			Want      []string        `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	universe := make([]scope.Cell, len(fx.Universe))
	for i, c := range fx.Universe {
		universe[i] = scope.Cell{Label: c.Label, Dimensions: c.Dimensions}
	}
	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			got, err := scope.Evaluate(tc.Predicate, universe)
			if err != nil {
				t.Fatalf("Evaluate(%s): %v", tc.Predicate, err)
			}
			labels := make([]string, len(got))
			for i, c := range got {
				labels[i] = c.Label
			}
			if !slices.Equal(labels, tc.Want) {
				t.Fatalf("Evaluate(%s) = %v, want %v", tc.Predicate, labels, tc.Want)
			}
		})
	}
}
