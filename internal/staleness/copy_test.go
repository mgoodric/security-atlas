package staleness

import (
	"strings"
	"testing"
	"time"
)

// AC-6: the recompute interval + digest cadence are surfaced as honest named
// intervals — and NEVER as "continuous monitoring" / "real-time" (P0-439-1).
func TestHonestIntervalCopy(t *testing.T) {
	t.Parallel()

	alert := alertMessage("monthly")
	if !strings.Contains(alert, RecomputeIntervalText) {
		t.Errorf("alert copy must name the recompute interval %q; got %q", RecomputeIntervalText, alert)
	}
	digest := digestMessage(3, 5)
	if !strings.Contains(digest, DigestCadenceText) {
		t.Errorf("digest copy must name the cadence %q; got %q", DigestCadenceText, digest)
	}

	banned := []string{"continuous monitoring", "real-time", "real time", "live monitoring"}
	for _, phrase := range []string{alert, digest} {
		low := strings.ToLower(phrase)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("copy contains banned phrase %q (canvas anti-pattern): %q", b, phrase)
			}
		}
	}
}

func TestAlertMessage_ClassPhrasing(t *testing.T) {
	t.Parallel()
	withClass := alertMessage("quarterly")
	if !strings.Contains(withClass, "quarterly-class evidence") {
		t.Errorf("expected class-specific subject; got %q", withClass)
	}
	noClass := alertMessage("")
	if !strings.HasPrefix(noClass, "Evidence for a control") {
		t.Errorf("expected generic subject for empty class; got %q", noClass)
	}
}

// AC-4 / threat-model D: the digest caps the enumerated list at DigestTopN,
// sorts stale before approaching, and flags truncation.
func TestBuildDigestPayload_CapAndOrder(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)

	// 12 stale items -> exceeds the cap of 10; expect truncation to 10.
	items := make([]DigestItem, 0, 14)
	for i := 0; i < 12; i++ {
		items = append(items, DigestItem{
			ControlID:      "ctrl-stale-" + string(rune('a'+i)),
			FreshnessClass: "monthly",
			Band:           BandStale.String(),
		})
	}
	// 2 approaching items -> must sort AFTER stale.
	items = append(items,
		DigestItem{ControlID: "ctrl-appr-1", Band: BandApproaching.String()},
		DigestItem{ControlID: "ctrl-appr-2", Band: BandApproaching.String()},
	)

	got := BuildDigestPayload(items, 12, 2, start, end, "/freshness")

	if got.StaleCount != 12 || got.ApproachingCount != 2 {
		t.Errorf("counts = (%d,%d), want (12,2)", got.StaleCount, got.ApproachingCount)
	}
	if len(got.TopStale) != DigestTopN {
		t.Fatalf("TopStale len = %d, want capped at %d", len(got.TopStale), DigestTopN)
	}
	if !got.Truncated {
		t.Error("expected Truncated=true when more than DigestTopN items exist")
	}
	for _, it := range got.TopStale {
		if it.Band != BandStale.String() {
			t.Errorf("cap should keep stale items first; saw band %q", it.Band)
		}
	}
	if got.PeriodStart != start.Format(time.RFC3339) || got.PeriodEnd != end.Format(time.RFC3339) {
		t.Errorf("digest must state the period it covers; got start=%q end=%q", got.PeriodStart, got.PeriodEnd)
	}
	if got.FreshnessViewURL != "/freshness" {
		t.Errorf("digest must link to the freshness view (AC-9); got %q", got.FreshnessViewURL)
	}
}

func TestBuildDigestPayload_NoTruncationUnderCap(t *testing.T) {
	t.Parallel()
	start := time.Now().UTC()
	items := []DigestItem{
		{ControlID: "c1", Band: BandStale.String()},
		{ControlID: "c2", Band: BandApproaching.String()},
	}
	got := BuildDigestPayload(items, 1, 1, start, start.Add(time.Hour), "/freshness")
	if got.Truncated {
		t.Error("Truncated must be false when item count <= cap")
	}
	if len(got.TopStale) != 2 {
		t.Errorf("TopStale len = %d, want 2", len(got.TopStale))
	}
}
