// Slice 108: unit tests for the userprefs whitelist + default-matrix logic.
// DB-touching paths (Get / Upsert against a live Postgres) are covered by the
// internal/api/me/*_integration_test.go suite.
package userprefs

import (
	"testing"
)

func TestDefaultMatrix_AllCellsEnabled(t *testing.T) {
	m := DefaultMatrix()
	if got, want := len(m), len(Events); got != want {
		t.Fatalf("DefaultMatrix has %d events; want %d", got, want)
	}
	for _, ev := range Events {
		row, ok := m[ev]
		if !ok {
			t.Errorf("DefaultMatrix missing event %q", ev)
			continue
		}
		if got, want := len(row), len(Channels); got != want {
			t.Errorf("DefaultMatrix[%q] has %d channels; want %d", ev, got, want)
		}
		for _, ch := range Channels {
			if !row[ch] {
				t.Errorf("DefaultMatrix[%q][%q] = false; want true", ev, ch)
			}
		}
	}
}

func TestIsAllowedEvent(t *testing.T) {
	for _, ev := range Events {
		if !isAllowedEvent(ev) {
			t.Errorf("isAllowedEvent(%q) = false; want true", ev)
		}
	}
	for _, bad := range []string{"", "Audit_Period_Assignment", "unknown_event", "policy_ack_due_typo"} {
		if isAllowedEvent(bad) {
			t.Errorf("isAllowedEvent(%q) = true; want false", bad)
		}
	}
}

func TestIsAllowedChannel(t *testing.T) {
	for _, ch := range Channels {
		if !isAllowedChannel(ch) {
			t.Errorf("isAllowedChannel(%q) = false; want true", ch)
		}
	}
	for _, bad := range []string{"", "IN_APP", "sms", "push", "webhook"} {
		if isAllowedChannel(bad) {
			t.Errorf("isAllowedChannel(%q) = true; want false", bad)
		}
	}
}

// TestPreferencesTypeShape is a compile-time guard: Preferences must be a map
// of map of bool so the JSON encoder produces the documented wire shape.
func TestPreferencesTypeShape(t *testing.T) {
	var _ Preferences = Preferences{
		"audit_period_assignment": {"in_app": true, "email": false},
	}
}
