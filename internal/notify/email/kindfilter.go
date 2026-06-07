package email

// Per-notification-kind email filtering (slice 542).
//
// Slice 445 shipped a MASTER email opt-in (one toggle: email delivery on/off
// for the whole user) and deferred per-kind filtering (445 D7). This file
// layers the slice-108 per-event `email` channel ON TOP of that master gate:
// the digest includes a notification kind only if (master opt-in is ON) AND
// (that kind's `email` channel pref is enabled).
//
// Composition semantics (slice 542 JUDGMENT): master AND per-kind. The master
// switch is the OUTER gate (default off, enforced in DeliverDigest BEFORE this
// filter runs); the per-kind prefs are the INNER filter. A user must opt in
// once at the master level before any per-kind pref matters. This is the safe
// composition: it cannot start delivering email to a user who never opted in.
//
// Default-on-missing-row (slice 542 JUDGMENT, inheriting slice-108 D3): when a
// user has NO preference row for a kind's mapped event, the kind is INCLUDED
// (the master opt-in governs). This is backward-compatible: an opted-in user
// keeps receiving every kind they get today unless they set an explicit
// per-kind `email=false` opt-out. The filter never SILENTLY suppresses a kind.

// kindToEvent maps a notification `type` constant (as it appears on the
// notifications row and in BuildDigest's TypeCounts) to the slice-108
// `user_notification_preferences.event` key. The two taxonomies are NOT 1:1:
//
//   - The names differ by punctuation: notification `control.drift` (dot) maps
//     to slice-108 event `control_drift` (underscore).
//   - Some notification kinds have NO slice-108 event row at all
//     (`audit_note.reply`, `evidence.staleness`). These are UNMAPPED.
//
// An UNMAPPED kind (absent from this map) defaults to included-when-master-on
// (it has no per-kind opt-out surface yet — a future slice that adds a
// slice-108 event row for it MUST also add the mapping here, mirroring the
// schema-CHECK-and-whitelist-move-together discipline from slice 108).
//
// The slice-108 event whitelist (internal/auth/userprefs.Events) is:
//
//	audit_period_assignment, policy_ack_due, risk_review_overdue, control_drift
var kindToEvent = map[string]string{
	"audit_period_assignment": "audit_period_assignment",
	"policy_ack_due":          "policy_ack_due",
	"risk_review_overdue":     "risk_review_overdue",
	"control.drift":           "control_drift",
	// audit_note.reply  -> (no slice-108 event) UNMAPPED -> included-when-master-on
	// evidence.staleness -> (no slice-108 event) UNMAPPED -> included-when-master-on
}

// emailEnabledForKind decides whether a single notification kind should appear
// in the digest, GIVEN the master opt-in is already ON (the caller has gated on
// that). It consults the per-event `email` channel preference:
//
//   - kind is UNMAPPED (no slice-108 event)        -> true  (default-on)
//   - mapped event has NO row for the email channel -> true  (default-on, 108 D3)
//   - mapped event's email channel pref is true     -> true  (explicit opt-in)
//   - mapped event's email channel pref is false    -> false (explicit opt-out)
//
// emailChannelByEvent is event -> (email-channel enabled?), with presence
// indicating an explicit row exists. An absent event means default-on.
func emailEnabledForKind(kind string, emailChannelByEvent map[string]bool) bool {
	event, mapped := kindToEvent[kind]
	if !mapped {
		// No per-kind opt-out surface for this kind yet: master governs.
		return true
	}
	enabled, hasRow := emailChannelByEvent[event]
	if !hasRow {
		// Default-on-missing-row (slice-108 D3): master governs.
		return true
	}
	return enabled
}

// filterCountsByEmailPref drops, from the in-memory type-count map, every kind
// whose per-kind `email` preference is disabled. It returns the filtered counts
// map and the recomputed total. It NEVER mutates recipient resolution or tenant
// scoping (P0-542-2): it operates purely on the already-RLS-scoped count map
// (threat-model I mitigation). The master opt-in must already be confirmed ON
// by the caller (P0-542-1) — this function only narrows WHICH kinds appear.
func filterCountsByEmailPref(counts map[string]int, emailChannelByEvent map[string]bool) (map[string]int, int) {
	out := make(map[string]int, len(counts))
	total := 0
	for kind, n := range counts {
		if n <= 0 {
			continue
		}
		if !emailEnabledForKind(kind, emailChannelByEvent) {
			continue // muted: per-kind email pref is explicitly off
		}
		out[kind] = n
		total += n
	}
	return out, total
}
