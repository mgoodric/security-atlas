// Package notify holds the cross-channel pieces shared by the
// notification delivery sinks (slice 543). Slice 445 shipped EMAIL as the
// first channel; this package generalizes the parts that every channel
// needs without disturbing email's byte-identical behavior:
//
//   - Summary: the minimum-disclosure digest value object (counts + a deep
//     link only; NEVER notification details — P0-543-1 / threat-model I).
//   - DeepLink: the notifications deep-link from the public base URL.
//   - TypeLabel: the CLOSED type-constant -> human-label map (the digest
//     never echoes a raw type string from a row — minimum disclosure +
//     injection defense).
//   - secret redaction + SSRF host guard live in their sibling files
//     (secret.go, ssrf.go).
//
// Email predates this package and keeps its OWN copies of these helpers so
// its wire output stays byte-identical (slice 543 D1); the values here are
// kept intentionally consistent with email's so Slack/webhook render the
// same labels.
package notify

import (
	"sort"
	"strings"
)

// Summary is the minimum-disclosure digest a channel renders. It carries
// COUNTS keyed by notification type plus the total and a deep link — never
// notification contents (P0-543-1 / threat-model I). It is the shared input
// every non-email channel builds its payload from.
type Summary struct {
	// TypeCounts maps a notification `type` constant to the number unread of
	// that type. Only positive counts are meaningful.
	TypeCounts map[string]int
	// TotalUnread is the sum of all (un-muted) unread counts.
	TotalUnread int
	// DeepLink is the absolute (or relative) URL to the in-app notifications
	// view. It is the ONLY place a recipient is sent to see details.
	DeepLink string
}

// SortedTypes returns the type keys of the summary in deterministic order
// (stable payloads / tests).
func (s Summary) SortedTypes() []string {
	out := make([]string, 0, len(s.TypeCounts))
	for k := range s.TypeCounts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DeepLink composes the notifications deep-link from the public base URL. A
// missing base URL yields a relative path (mirrors email.buildDeepLink).
func DeepLink(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "/notifications"
	}
	return base + "/notifications"
}

// typeLabels is the closed map from notification `type` constant to a
// human-readable label. Kept in sync with email.typeLabels so every channel
// renders the same wording. Closed by design: the digest never echoes a raw
// type string from a row (minimum disclosure + type-taxonomy injection
// defense).
var typeLabels = map[string]string{
	"audit_note.reply":        "Audit-note replies",
	"control.drift":           "Control-drift alerts",
	"policy_ack_due":          "Policy acknowledgments due",
	"risk_review_overdue":     "Overdue risk reviews",
	"audit_period_assignment": "Audit-period assignments",
	"evidence.staleness":      "Stale-evidence digests",
}

// TypeLabel maps a notification type constant to its closed human label.
// Unknown types fall back to a generic label (never the raw type string).
func TypeLabel(t string) string {
	if l, ok := typeLabels[t]; ok {
		return l
	}
	return "Other notifications"
}

// DigestKeyForDay returns the deterministic idempotency / rate-limit key
// for an instant: one digest per channel per user per UTC day. Same shape
// as email.DigestKeyForDay; the channel name is folded into the key by the
// caller so the slack + webhook + email claims never collide with each
// other.
func DigestKeyForDay(channel, ymdUTC string) string {
	return channel + ":digest:" + ymdUTC
}

// Plural returns "s" for any count != 1 (English digest copy helper).
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
