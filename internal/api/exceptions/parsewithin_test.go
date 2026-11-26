package exceptions

import (
	"testing"
	"time"
)

// #1914: a num large enough to overflow time.Duration is refused instead
// of wrapping negative under the MaxExpiringWindow check.
func TestParseWithin_RefusesOverflow(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"4611686018427387904d", "9223372036854775807m", "9223372036854775807h", "106752d"} {
		if d, err := parseWithin(s); err == nil {
			t.Fatalf("parseWithin(%q) = %v, want an error", s, d)
		}
	}
	got, err := parseWithin("400d")
	if err != nil || got != 400*24*time.Hour {
		t.Fatalf("parseWithin(400d) = %v, %v; want 400d so the caller's max check names the cap", got, err)
	}
}
