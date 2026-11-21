package exception

import "testing"

// #2714: a recorded key matches the current hash, or the legacy one when
// the caller supplied it; an empty legacy hash never matches.
func TestRequestHashMatches(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		recorded string
		in       CreateInput
		want     bool
	}{
		{"current", "h1", CreateInput{RequestHash: "h1", LegacyRequestHash: "l1"}, true},
		{"legacy", "l1", CreateInput{RequestHash: "h1", LegacyRequestHash: "l1"}, true},
		{"neither", "x", CreateInput{RequestHash: "h1", LegacyRequestHash: "l1"}, false},
		{"empty legacy does not match an empty record", "", CreateInput{RequestHash: "h1"}, false},
	} {
		if got := requestHashMatches(c.recorded, c.in); got != c.want {
			t.Errorf("%s: requestHashMatches = %v, want %v", c.name, got, c.want)
		}
	}
}
