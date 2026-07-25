package email

import "github.com/mgoodric/security-atlas/internal/notify"

// Per-notification-kind email filtering (slice 542; generalized in slice 583).
//
// Slice 445 shipped a MASTER email opt-in (one toggle: email delivery on/off
// for the whole user) and deferred per-kind filtering (445 D7). Slice 542
// layered the slice-108 per-event `email` channel ON TOP of that master gate:
// the digest includes a notification kind only if (master opt-in is ON) AND
// (that kind's `email` channel pref is enabled).
//
// Slice 583 lifted the implementation into the shared internal/notify package
// (notify.EnabledForKind / notify.FilterCountsByChannelPref /
// notify.KindToEvent) so all three channels (email, slack, webhook) share ONE
// filter. The functions below are thin email-channel-specialized delegates that
// keep email's call sites byte-identical (slice 543 D1) while routing through
// the single source of truth.
//
// Composition semantics (slice 542 JUDGMENT, unchanged): master AND per-kind.
// The master switch is the OUTER gate (default off, enforced in DeliverDigest
// BEFORE this filter runs); the per-kind prefs are the INNER filter.
//
// Default-on-missing-row (slice 542 JUDGMENT, inheriting slice-108 D3): a kind
// with NO `email`-channel pref row is INCLUDED. Backward-compatible; the filter
// never SILENTLY suppresses a kind.

// emailEnabledForKind decides whether a single notification kind should appear
// in the digest, GIVEN the master opt-in is already ON. It delegates to the
// shared notify.EnabledForKind (slice 583); behavior is identical to the
// slice-542 implementation it replaced.
func emailEnabledForKind(kind string, emailChannelByEvent map[string]bool) bool {
	return notify.EnabledForKind(kind, emailChannelByEvent)
}

// filterCountsByEmailPref drops, from the in-memory type-count map, every kind
// whose per-kind `email` preference is disabled, returning the filtered map and
// recomputed total. Delegates to the shared notify.FilterCountsByChannelPref
// (slice 583). It NEVER mutates recipient resolution or tenant scoping
// (P0-542-2): it operates purely on the already-RLS-scoped count map. The
// master opt-in must already be confirmed ON by the caller (P0-542-1).
func filterCountsByEmailPref(counts map[string]int, emailChannelByEvent map[string]bool) (map[string]int, int) {
	return notify.FilterCountsByChannelPref(counts, emailChannelByEvent)
}

// kindToEvent is retained as a package-local view over the shared
// notify.KindToEvent map so the existing slice-542 unit test
// (TestKindToEventMapping) keeps pinning the taxonomy. It is rebuilt from the
// shared source of truth, so it cannot drift from the canonical map.
var kindToEvent = buildKindToEvent()

func buildKindToEvent() map[string]string {
	out := make(map[string]string)
	for _, kind := range []string{
		"audit_period_assignment",
		"policy_ack_due",
		"risk_review_overdue",
		"control.drift",
		"audit_note.reply",
		"evidence.staleness",
	} {
		if ev, ok := notify.KindToEvent(kind); ok {
			out[kind] = ev
		}
	}
	return out
}
