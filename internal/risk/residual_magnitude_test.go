// Unit tests for residualMagnitude, the residual,age sort key (#2058).
//
// Pure function — no DB, no build tag.
package risk

import "testing"

func TestResidualMagnitudeReadsEveryShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want float64
	}{
		{"deriver blob", `{"score":12,"inherent_score":20,"weighted_control_effectiveness":0.4,"breakdown":[]}`, 12},
		{"score wins over likelihood/impact", `{"score":3,"likelihood":5,"impact":5}`, 3},
		{"precomputed severity", `{"severity":16}`, 16},
		{"5x5 pair", `{"likelihood":3,"impact":4}`, 12},
		{"empty object", `{}`, 0},
		{"partial pair", `{"likelihood":3}`, 0},
		{"not JSON", `nope`, 0},
		{"empty", ``, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := residualMagnitude([]byte(tc.raw)); got != tc.want {
				t.Fatalf("residualMagnitude(%s) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
