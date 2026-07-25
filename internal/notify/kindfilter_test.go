package notify

import (
	"reflect"
	"testing"
)

// TestEnabledForKind covers the per-kind composition decision GIVEN the master
// opt-in for the channel is already ON (each channel's DeliverDigest gates the
// master switch before this runs). The four load-bearing cases (slice 583,
// generalizing slice 542) — channel-agnostic, since the projected
// channelByEvent map already encodes the channel:
//
//	master on + kind pref on   -> deliver (explicit opt-in)
//	master on + kind pref off  -> mute    (explicit opt-out)
//	master on + no row         -> deliver (default-on-missing-row, 108 D3)
//	master on + unmapped kind  -> deliver (no per-kind surface; master governs)
func TestEnabledForKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		kind           string
		channelByEvent map[string]bool
		want           bool
	}{
		{"mapped, pref on -> deliver", "control.drift", map[string]bool{"control_drift": true}, true},
		{"mapped, pref off -> mute", "control.drift", map[string]bool{"control_drift": false}, false},
		{"mapped, no row -> default-on", "control.drift", map[string]bool{}, true},
		{"mapped, only OTHER event has row -> default-on", "risk_review_overdue", map[string]bool{"control_drift": false}, true},
		{"slice-566 kind (audit_note.reply) off -> mute", "audit_note.reply", map[string]bool{"audit_note_reply": false}, false},
		{"slice-566 kind (evidence.staleness) on -> deliver", "evidence.staleness", map[string]bool{"evidence_staleness": true}, true},
		{"unmapped kind -> default-on (master governs)", "some.future.kind", map[string]bool{"some_future_kind": false}, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := EnabledForKind(tc.kind, tc.channelByEvent); got != tc.want {
				t.Fatalf("EnabledForKind(%q, %v) = %v, want %v", tc.kind, tc.channelByEvent, got, tc.want)
			}
		})
	}
}

// TestFilterCountsByChannelPref proves the count map is narrowed and the total
// recomputed. Muted kinds drop out; others are preserved unchanged. The map is
// channel-agnostic: the channelByEvent argument is already projected to one
// channel by ChannelPrefMap, so this single test covers email/slack/webhook.
func TestFilterCountsByChannelPref(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		counts         map[string]int
		channelByEvent map[string]bool
		wantCounts     map[string]int
		wantTotal      int
	}{
		{
			name:           "no prefs -> everything passes (default-on)",
			counts:         map[string]int{"control.drift": 3, "audit_note.reply": 2},
			channelByEvent: map[string]bool{},
			wantCounts:     map[string]int{"control.drift": 3, "audit_note.reply": 2},
			wantTotal:      5,
		},
		{
			name:           "one mapped kind muted -> its count removed, others kept",
			counts:         map[string]int{"control.drift": 3, "policy_ack_due": 4, "audit_note.reply": 2},
			channelByEvent: map[string]bool{"control_drift": false},
			wantCounts:     map[string]int{"policy_ack_due": 4, "audit_note.reply": 2},
			wantTotal:      6,
		},
		{
			name:           "everything muted -> empty map, zero total (digest skips)",
			counts:         map[string]int{"control.drift": 2, "policy_ack_due": 1},
			channelByEvent: map[string]bool{"control_drift": false, "policy_ack_due": false},
			wantCounts:     map[string]int{},
			wantTotal:      0,
		},
		{
			name:           "zero/negative counts dropped defensively",
			counts:         map[string]int{"control.drift": 0, "policy_ack_due": 2},
			channelByEvent: map[string]bool{},
			wantCounts:     map[string]int{"policy_ack_due": 2},
			wantTotal:      2,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotCounts, gotTotal := FilterCountsByChannelPref(tc.counts, tc.channelByEvent)
			if gotTotal != tc.wantTotal {
				t.Fatalf("total = %d, want %d", gotTotal, tc.wantTotal)
			}
			if !reflect.DeepEqual(gotCounts, tc.wantCounts) {
				t.Fatalf("counts = %v, want %v", gotCounts, tc.wantCounts)
			}
		})
	}
}

// TestChannelPrefMap proves rows are projected to ONE channel: only rows whose
// Channel matches are kept; other channels' rows are ignored. This is the key
// generalization vs slice 542's email-only projector — the same projector now
// serves slack + webhook by name.
func TestChannelPrefMap(t *testing.T) {
	t.Parallel()
	rows := []ChannelPrefRow{
		{Event: "control_drift", Channel: "email", Enabled: false},
		{Event: "control_drift", Channel: "slack", Enabled: true},
		{Event: "control_drift", Channel: "webhook", Enabled: false},
		{Event: "control_drift", Channel: "in_app", Enabled: true},
		{Event: "policy_ack_due", Channel: "slack", Enabled: false},
		{Event: "risk_review_overdue", Channel: "webhook", Enabled: true},
	}
	cases := []struct {
		channel string
		want    map[string]bool
	}{
		{"email", map[string]bool{"control_drift": false}},
		{"slack", map[string]bool{"control_drift": true, "policy_ack_due": false}},
		{"webhook", map[string]bool{"control_drift": false, "risk_review_overdue": true}},
		{"in_app", map[string]bool{"control_drift": true}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.channel, func(t *testing.T) {
			t.Parallel()
			got := ChannelPrefMap(rows, tc.channel)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ChannelPrefMap(_, %q) = %v, want %v", tc.channel, got, tc.want)
			}
		})
	}
}

// TestKindToEvent pins the shared type->event mapping so a taxonomy change is a
// deliberate edit. The dot-vs-underscore normalization is the load-bearing case.
func TestKindToEvent(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"audit_period_assignment": "audit_period_assignment",
		"policy_ack_due":          "policy_ack_due",
		"risk_review_overdue":     "risk_review_overdue",
		"control.drift":           "control_drift",
		"audit_note.reply":        "audit_note_reply",
		"evidence.staleness":      "evidence_staleness",
	}
	for kind, wantEvent := range want {
		got, ok := KindToEvent(kind)
		if !ok || got != wantEvent {
			t.Fatalf("KindToEvent(%q) = (%q,%v), want (%q,true)", kind, got, ok, wantEvent)
		}
	}
	if _, ok := KindToEvent("unmapped.kind"); ok {
		t.Fatalf("KindToEvent(unmapped.kind) returned mapped=true; want false")
	}
}
