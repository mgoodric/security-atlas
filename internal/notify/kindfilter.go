package notify

// Per-notification-kind channel filtering (slice 583 — generalizes slice 542).
//
// Slice 542 added per-kind filtering for the EMAIL channel: on top of the
// master email opt-in, a user can mute individual notification kinds via the
// slice-108 `user_notification_preferences` per-event `email` channel column.
// Slice 543 shipped Slack + webhook with the MASTER opt-in only. This file
// lifts the slice-542 filter out of internal/notify/email and generalizes it
// over an arbitrary channel name so all three delivery sinks (email, slack,
// webhook) share ONE filter implementation.
//
// Composition semantics (inherited from slice 542 JUDGMENT): master AND
// per-kind. The master opt-in is the OUTER gate (default OFF for every channel,
// enforced in each channel's DeliverDigest BEFORE this filter runs); the
// per-kind prefs are the INNER filter. A user must opt in once at the master
// level before any per-kind pref matters. This composition cannot start
// delivering on a channel a user never opted into.
//
// Default-on-missing-row (inherited from slice 542 JUDGMENT / slice-108 D3):
// when a user has NO preference row for a kind's mapped event+channel, the kind
// is INCLUDED (the master opt-in governs). Backward-compatible: an opted-in
// user keeps receiving every kind they get today unless they set an explicit
// per-kind opt-out for that channel. The filter never SILENTLY suppresses a
// kind — suppression requires an explicit `enabled=false` row.

// kindToEvent maps a notification `type` constant (as it appears on the
// notifications row and in a channel's TypeCounts) to the slice-108
// `user_notification_preferences.event` key. The two taxonomies are NOT 1:1:
// the names differ by punctuation (notification `control.drift` (dot) maps to
// slice-108 event `control_drift` (underscore); `audit_note.reply` →
// `audit_note_reply`; `evidence.staleness` → `evidence_staleness`).
//
// A kind absent from this map is UNMAPPED and defaults to included-when-
// master-on (no per-kind opt-out surface yet). This map is the SINGLE source of
// truth shared by every channel; the email package's kindToEvent delegates here
// (slice 583). A future slice that adds a slice-108 event row for a new kind
// MUST also add the mapping here, mirroring the schema-CHECK-and-whitelist-
// move-together discipline from slice 108.
//
// The slice-108 event whitelist (internal/auth/userprefs.Events) is:
//
//	audit_period_assignment, policy_ack_due, risk_review_overdue, control_drift,
//	audit_note_reply, evidence_staleness
var kindToEvent = map[string]string{
	"audit_period_assignment": "audit_period_assignment",
	"policy_ack_due":          "policy_ack_due",
	"risk_review_overdue":     "risk_review_overdue",
	"control.drift":           "control_drift",
	"audit_note.reply":        "audit_note_reply",
	"evidence.staleness":      "evidence_staleness",
}

// KindToEvent exposes the canonical kind→event mapping for callers (e.g. the
// email package) that delegate to this shared map rather than re-authoring it.
// The returned (event, mapped) shape lets the caller distinguish an unmapped
// kind from a mapped one whose value happens to equal its key.
func KindToEvent(kind string) (string, bool) {
	event, mapped := kindToEvent[kind]
	return event, mapped
}

// EnabledForKind decides whether a single notification kind should appear in a
// channel's digest, GIVEN the master opt-in for that channel is already ON (the
// caller has gated on that). It consults the per-event preference for the
// caller's channel (already projected to event -> enabled via ChannelPrefMap):
//
//   - kind is UNMAPPED (no slice-108 event)         -> true  (default-on)
//   - mapped event has NO row for this channel       -> true  (default-on, 108 D3)
//   - mapped event's channel pref is true            -> true  (explicit opt-in)
//   - mapped event's channel pref is false           -> false (explicit opt-out)
//
// channelByEvent is event -> (channel enabled?), with presence indicating an
// explicit row exists for that channel. An absent event means default-on.
func EnabledForKind(kind string, channelByEvent map[string]bool) bool {
	event, mapped := kindToEvent[kind]
	if !mapped {
		// No per-kind opt-out surface for this kind yet: master governs.
		return true
	}
	enabled, hasRow := channelByEvent[event]
	if !hasRow {
		// Default-on-missing-row (slice-108 D3): master governs.
		return true
	}
	return enabled
}

// FilterCountsByChannelPref drops, from the in-memory type-count map, every
// kind whose per-kind preference for this channel is explicitly disabled. It
// returns the filtered counts map and the recomputed total. It NEVER mutates
// recipient resolution or tenant scoping (slice 542 P0-542-2, inherited by
// slice 583): it operates purely on the already-RLS-scoped count map (threat-
// model I mitigation). The master opt-in must already be confirmed ON by the
// caller — this function only narrows WHICH kinds appear.
func FilterCountsByChannelPref(counts map[string]int, channelByEvent map[string]bool) (map[string]int, int) {
	out := make(map[string]int, len(counts))
	total := 0
	for kind, n := range counts {
		if n <= 0 {
			continue
		}
		if !EnabledForKind(kind, channelByEvent) {
			continue // muted: per-kind channel pref is explicitly off
		}
		out[kind] = n
		total += n
	}
	return out, total
}

// ChannelPrefRow is the minimal shape FilterCountsByChannelPref's projector
// needs from a slice-108 preference row: the event key, the channel, and the
// enabled flag. The dbx model (internal/db/dbx.UserNotificationPreference)
// satisfies this implicitly via ChannelPrefMap's generic projector below; this
// type documents the contract without coupling internal/notify to dbx.
type ChannelPrefRow struct {
	Event   string
	Channel string
	Enabled bool
}

// ChannelPrefMap projects slice-108 preference rows down to ONE channel, as
// event -> enabled. Presence of an event key means an explicit row exists for
// (event, channel) so default-on-missing-row can distinguish "no row" from "row
// says false". Rows for other channels are ignored — this filter governs only
// the named channel's digest. The rows are supplied as the lightweight
// ChannelPrefRow shape; callers holding dbx rows project via a one-line adapter.
func ChannelPrefMap(rows []ChannelPrefRow, channel string) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		if r.Channel != channel {
			continue
		}
		out[r.Event] = r.Enabled
	}
	return out
}
