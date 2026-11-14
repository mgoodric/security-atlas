package freshnessdrift

import "testing"

// #1914: the largest int32 day count overflows time.Duration when
// multiplied by 24h; it now clamps to maxDriftWindow instead of wrapping.
func TestParseSinceDays_ClampsBeforeOverflow(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"2147483647d", "106752d", "401d"} {
		got, err := parseSinceDays(s)
		if err != nil || got != maxDriftWindow {
			t.Fatalf("parseSinceDays(%q) = %v, %v; want %v", s, got, err, maxDriftWindow)
		}
	}
}
