package staleness

import (
	"fmt"
	"sort"
	"time"
)

// HONEST-INTERVAL COPY (P0-439-1 / AC-6).
//
// Every recompute cadence the operator can see is named here, as a literal
// interval — never "continuous monitoring" / "real-time" / "live." The canvas
// (§1.6) bans dressing polling up as real-time; this file is the single place
// the named intervals live so the copy stays honest everywhere it surfaces
// (notification payload + UI + operator docs all read from these constants /
// helpers).

const (
	// RecomputeIntervalText is the human phrasing of how often the rollup runs.
	// MUST match DefaultRecomputeInterval (scheduler.go). Stated verbatim in
	// the alert copy so the operator knows the alert is periodic, not live.
	RecomputeIntervalText = "every 6 hours"

	// DigestCadenceText is the human phrasing of the weekly digest cadence.
	// MUST match the scheduler's weekly Monday 09:00 UTC trigger.
	DigestCadenceText = "every Monday at 09:00 UTC"

	// DigestTopN is the cap on how many individual stale controls the digest
	// body enumerates (threat-model D / P0-439-6). Beyond this the digest
	// summarizes a count and links to the freshness view rather than listing
	// every record unboundedly.
	DigestTopN = 10

	// FreshnessViewPath is the in-app deep-link target the digest + alert point
	// at for the full stale list (AC-9). The freshness read model is surfaced
	// by the dashboard's EvidenceFreshnessPanel (slice 040); the anchor jumps
	// to that panel. Relative path; the UI composes the absolute URL from its
	// base. MUST match the TS staleness presentation helper
	// (web/lib/api/staleness-notification.ts).
	FreshnessViewPath = "/dashboard#evidence-freshness"
)

// AlertPayload is the JSON shape carried by a per-control `evidence.staleness`
// alert notification (notification.type = "evidence.staleness", subtype =
// "alert"). It carries control identity + the band + the honest interval — no
// raw evidence payloads, no S3 URLs, no cross-tenant IDs (threat-model I).
type AlertPayload struct {
	Subtype          string  `json:"subtype"` // always "alert"
	ControlID        string  `json:"control_id"`
	FreshnessClass   string  `json:"freshness_class,omitempty"`
	Band             string  `json:"band"` // "stale"
	ValidUntil       *string `json:"valid_until,omitempty"`
	RecomputeMessage string  `json:"recompute_message"` // honest-interval copy
	Message          string  `json:"message"`
	FreshnessViewURL string  `json:"freshness_view_url"`
}

// DigestPayload is the JSON shape carried by the weekly digest
// `evidence.staleness` notification (subtype = "digest"). It summarizes counts
// + a capped top-N list and names the period it covers + the cadence.
type DigestPayload struct {
	Subtype          string       `json:"subtype"` // always "digest"
	PeriodStart      string       `json:"period_start"`
	PeriodEnd        string       `json:"period_end"`
	StaleCount       int          `json:"stale_count"`
	ApproachingCount int          `json:"approaching_count"`
	TopStale         []DigestItem `json:"top_stale"`
	Truncated        bool         `json:"truncated"`
	CadenceMessage   string       `json:"cadence_message"` // honest-interval copy
	Message          string       `json:"message"`
	FreshnessViewURL string       `json:"freshness_view_url"`
}

// DigestItem is one capped entry in the digest's top-N stale list. Control
// identity + class only — minimum disclosure.
type DigestItem struct {
	ControlID      string `json:"control_id"`
	FreshnessClass string `json:"freshness_class,omitempty"`
	Band           string `json:"band"`
}

// alertMessage builds the plain, factual one-line alert copy. Measured tone
// (the project's tone discipline applies even with no LLM): state the fact and
// the honest cadence, no superlatives, no "continuous" framing.
func alertMessage(class string) string {
	subject := "Evidence"
	if class != "" {
		subject = fmt.Sprintf("%s-class evidence", class)
	}
	return fmt.Sprintf(
		"%s for a control has crossed its freshness threshold and is now stale. "+
			"Staleness is recomputed on a schedule (%s).",
		subject, RecomputeIntervalText,
	)
}

// digestMessage builds the weekly digest's plain summary line. Names the
// counts and the honest cadence; points at the freshness view for the full
// list (the digest body is capped — threat-model D).
func digestMessage(stale, approaching int) string {
	return fmt.Sprintf(
		"%d stale and %d approaching-stale control%s this week. "+
			"This digest is generated %s; see the freshness view for the full list.",
		stale, approaching, plural(stale+approaching), DigestCadenceText,
	)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// BuildDigestPayload assembles the digest payload from the classified cells.
// Caps the enumerated list at DigestTopN (threat-model D / P0-439-6), sorts
// stale-before-approaching then by control id for a stable body, and sets
// Truncated when more stale controls exist than the cap shows.
func BuildDigestPayload(items []DigestItem, staleCount, approachingCount int, periodStart, periodEnd time.Time, freshnessViewURL string) DigestPayload {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Band != items[j].Band {
			// stale sorts before approaching (worst first)
			return items[i].Band == BandStale.String()
		}
		return items[i].ControlID < items[j].ControlID
	})
	truncated := len(items) > DigestTopN
	if truncated {
		items = items[:DigestTopN]
	}
	return DigestPayload{
		Subtype:          "digest",
		PeriodStart:      periodStart.UTC().Format(time.RFC3339),
		PeriodEnd:        periodEnd.UTC().Format(time.RFC3339),
		StaleCount:       staleCount,
		ApproachingCount: approachingCount,
		TopStale:         items,
		Truncated:        truncated,
		CadenceMessage:   fmt.Sprintf("Generated %s.", DigestCadenceText),
		Message:          digestMessage(staleCount, approachingCount),
		FreshnessViewURL: freshnessViewURL,
	}
}
