package risks_test

import (
	"testing"
	"time"
)

// orderedTime turns an RFC3339Nano wire timestamp into a fixed-width UTC
// string that sorts as the instant does. RFC3339Nano drops trailing zeros,
// so a whole-millisecond "…40.743Z" sorts after "…40.743964Z" as raw text
// (#2557). It panics on a value that is not a timestamp, as sortValue does
// on an unknown key.
func orderedTime(s string) string {
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic("not an RFC3339Nano timestamp: " + s)
	}
	return ts.UTC().Format("2006-01-02T15:04:05.000000000")
}

func TestOrderedTime_WholeMillisecondSortsBeforeLaterInstant(t *testing.T) {
	t.Parallel()
	whole := "2026-11-17T21:30:40.743Z"
	sub := "2026-11-17T21:30:40.743964Z"
	if !(orderedTime(whole) < orderedTime(sub)) {
		t.Fatalf("orderedTime(%q) = %q does not sort before orderedTime(%q) = %q",
			whole, orderedTime(whole), sub, orderedTime(sub))
	}
	// An offset is the same instant as its UTC form.
	if a, b := orderedTime("2026-11-17T22:30:40.5+01:00"), orderedTime("2026-11-17T21:30:40.5Z"); a != b {
		t.Fatalf("same instant, different keys: %q vs %q", a, b)
	}
}
