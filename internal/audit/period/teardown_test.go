package period

import (
	"testing"
	"time"
)

// A positive lockTimeout never formats as 0ms, which Postgres reads as no
// timeout at all (#3094).
func TestLockTimeoutSetting_RoundsUp(t *testing.T) {
	t.Parallel()
	for d, want := range map[time.Duration]string{
		time.Nanosecond:                    "1ms",
		500 * time.Microsecond:             "1ms",
		time.Millisecond:                   "1ms",
		time.Millisecond + time.Nanosecond: "2ms",
		200 * time.Millisecond:             "200ms",
		2 * time.Second:                    "2000ms",
	} {
		if got := lockTimeoutSetting(d); got != want {
			t.Errorf("lockTimeoutSetting(%v) = %q, want %q", d, got, want)
		}
	}
}
