package demoseed

import (
	"testing"
	"time"
)

// TestBuildFixtures_ExceptionsCarryTheirLifecycleStamps checks every demo
// exception against the #3056 CHECKs (an active, expired or revoked row
// carries activated_at; an expired row carries expired_at) and that its
// audit trail ends in the state the row is in, one entry per transition.
func TestBuildFixtures_ExceptionsCarryTheirLifecycleStamps(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := &Seeder{scale: DefaultScale, clock: func() time.Time { return now }}
	fs := s.buildFixtures("demo-exception-stamps")
	wantTrail := map[string][]string{
		"requested": {"requested"},
		"denied":    {"requested"},
		"approved":  {"requested", "approved"},
		"active":    {"requested", "approved", "activated"},
		"expired":   {"requested", "approved", "activated", "expired"},
	}
	seen := map[string]bool{}
	for _, e := range fs.exceptions {
		seen[e.Status] = true
		switch e.Status {
		case "active", "expired", "revoked":
			if e.ActivatedAt == nil {
				t.Errorf("%s exception %s: activated_at is nil", e.Status, e.ID)
			}
		}
		if e.Status == "expired" && e.ExpiredAt == nil {
			t.Errorf("expired exception %s: expired_at is nil", e.ID)
		}
		want, ok := wantTrail[e.Status]
		if !ok {
			t.Fatalf("exception %s: unexpected status %q", e.ID, e.Status)
		}
		trail := exceptionAuditTrail(e)
		if len(trail) != len(want) {
			t.Fatalf("%s exception %s: trail = %+v, want actions %v", e.Status, e.ID, trail, want)
		}
		for i, a := range trail {
			if a.action != want[i] {
				t.Errorf("%s exception %s: trail[%d] = %q, want %q", e.Status, e.ID, i, a.action, want[i])
			}
			if i > 0 && a.at.Before(trail[i-1].at) {
				t.Errorf("%s exception %s: trail[%d] at %s is before trail[%d]", e.Status, e.ID, i, a.at, i-1)
			}
		}
	}
	if !seen["expired"] || !seen["active"] {
		t.Fatalf("fixture has no expired or no active exception: %v", seen)
	}
}
