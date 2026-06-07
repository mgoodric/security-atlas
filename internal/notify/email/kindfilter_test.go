package email

import (
	"reflect"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestEmailEnabledForKind covers the per-kind composition decision GIVEN the
// master opt-in is already ON (DeliverDigest gates the master switch before
// this runs). The four load-bearing cases from the slice 542 spec:
//
//	master on + kind email pref on   -> send  (explicit opt-in)
//	master on + kind email pref off  -> mute  (explicit opt-out)
//	master on + no row for kind      -> send  (default-on-missing-row, 108 D3)
//	master on + unmapped kind        -> send  (no per-kind surface; master governs)
//
// (The "master off -> never send" case lives one layer up in DeliverDigest and
// is covered by the integration test TestDeliverDigest_DefaultOptedOut.)
func TestEmailEnabledForKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		kind                string
		emailChannelByEvent map[string]bool
		want                bool
	}{
		{
			name:                "mapped kind, email pref explicitly on -> send",
			kind:                "control.drift",
			emailChannelByEvent: map[string]bool{"control_drift": true},
			want:                true,
		},
		{
			name:                "mapped kind, email pref explicitly off -> mute",
			kind:                "control.drift",
			emailChannelByEvent: map[string]bool{"control_drift": false},
			want:                false,
		},
		{
			name:                "mapped kind, no row -> default-on (send)",
			kind:                "control.drift",
			emailChannelByEvent: map[string]bool{},
			want:                true,
		},
		{
			name:                "mapped kind, only OTHER event has a row -> default-on (send)",
			kind:                "risk_review_overdue",
			emailChannelByEvent: map[string]bool{"control_drift": false},
			want:                true,
		},
		{
			name:                "unmapped kind (audit_note.reply) -> included-when-master-on",
			kind:                "audit_note.reply",
			emailChannelByEvent: map[string]bool{"control_drift": false},
			want:                true,
		},
		{
			name:                "unmapped kind (evidence.staleness) -> included-when-master-on",
			kind:                "evidence.staleness",
			emailChannelByEvent: map[string]bool{},
			want:                true,
		},
		{
			name:                "1:1 mapped kind (policy_ack_due) pref off -> mute",
			kind:                "policy_ack_due",
			emailChannelByEvent: map[string]bool{"policy_ack_due": false},
			want:                false,
		},
		{
			name:                "1:1 mapped kind (audit_period_assignment) pref on -> send",
			kind:                "audit_period_assignment",
			emailChannelByEvent: map[string]bool{"audit_period_assignment": true},
			want:                true,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := emailEnabledForKind(tc.kind, tc.emailChannelByEvent); got != tc.want {
				t.Fatalf("emailEnabledForKind(%q, %v) = %v, want %v",
					tc.kind, tc.emailChannelByEvent, got, tc.want)
			}
		})
	}
}

// TestFilterCountsByEmailPref proves the count map is narrowed correctly and
// the total is recomputed. It also asserts the muted-kind threat-model
// invariant at the map level: a muted kind's count is removed, the others are
// preserved unchanged.
func TestFilterCountsByEmailPref(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		counts              map[string]int
		emailChannelByEvent map[string]bool
		wantCounts          map[string]int
		wantTotal           int
	}{
		{
			name:                "no prefs -> everything passes (default-on)",
			counts:              map[string]int{"control.drift": 3, "audit_note.reply": 2},
			emailChannelByEvent: map[string]bool{},
			wantCounts:          map[string]int{"control.drift": 3, "audit_note.reply": 2},
			wantTotal:           5,
		},
		{
			name:                "one mapped kind muted -> its count removed, others kept",
			counts:              map[string]int{"control.drift": 3, "policy_ack_due": 4, "audit_note.reply": 2},
			emailChannelByEvent: map[string]bool{"control_drift": false},
			wantCounts:          map[string]int{"policy_ack_due": 4, "audit_note.reply": 2},
			wantTotal:           6,
		},
		{
			name:                "all mapped kinds muted, unmapped survives",
			counts:              map[string]int{"control.drift": 1, "risk_review_overdue": 1, "evidence.staleness": 5},
			emailChannelByEvent: map[string]bool{"control_drift": false, "risk_review_overdue": false},
			wantCounts:          map[string]int{"evidence.staleness": 5},
			wantTotal:           5,
		},
		{
			name:                "everything muted -> empty map, zero total (digest skips)",
			counts:              map[string]int{"control.drift": 2, "policy_ack_due": 1},
			emailChannelByEvent: map[string]bool{"control_drift": false, "policy_ack_due": false},
			wantCounts:          map[string]int{},
			wantTotal:           0,
		},
		{
			name:                "zero/negative counts are dropped defensively",
			counts:              map[string]int{"control.drift": 0, "policy_ack_due": 2},
			emailChannelByEvent: map[string]bool{},
			wantCounts:          map[string]int{"policy_ack_due": 2},
			wantTotal:           2,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotCounts, gotTotal := filterCountsByEmailPref(tc.counts, tc.emailChannelByEvent)
			if gotTotal != tc.wantTotal {
				t.Fatalf("total = %d, want %d", gotTotal, tc.wantTotal)
			}
			if !reflect.DeepEqual(gotCounts, tc.wantCounts) {
				t.Fatalf("counts = %v, want %v", gotCounts, tc.wantCounts)
			}
		})
	}
}

// TestKindToEventMapping pins the type->event mapping so a future taxonomy
// change is a deliberate edit, not an accident. The dot-vs-underscore name
// difference (control.drift -> control_drift) is the load-bearing case.
func TestKindToEventMapping(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"audit_period_assignment": "audit_period_assignment",
		"policy_ack_due":          "policy_ack_due",
		"risk_review_overdue":     "risk_review_overdue",
		"control.drift":           "control_drift",
	}
	if !reflect.DeepEqual(kindToEvent, want) {
		t.Fatalf("kindToEvent = %v, want %v", kindToEvent, want)
	}
	// The two unmapped kinds must NOT have entries (they default-on).
	for _, unmapped := range []string{"audit_note.reply", "evidence.staleness"} {
		if _, ok := kindToEvent[unmapped]; ok {
			t.Fatalf("kind %q should be UNMAPPED (default-on), but has an event row", unmapped)
		}
	}
}

// TestEmailChannelPrefMap proves only `email`-channel rows are projected;
// `in_app` rows are ignored. (Uses the dbx model directly — this is the bridge
// between the slice-108 rows and the pure filter.)
func TestEmailChannelPrefMap(t *testing.T) {
	t.Parallel()
	prefs := []dbx.UserNotificationPreference{
		{Event: "control_drift", Channel: "email", Enabled: false},
		{Event: "control_drift", Channel: "in_app", Enabled: true},
		{Event: "policy_ack_due", Channel: "email", Enabled: true},
		{Event: "risk_review_overdue", Channel: "in_app", Enabled: false},
	}
	got := emailChannelPrefMap(prefs)
	want := map[string]bool{"control_drift": false, "policy_ack_due": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emailChannelPrefMap = %v, want %v", got, want)
	}
}
