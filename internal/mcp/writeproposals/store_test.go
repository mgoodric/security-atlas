// Unit-level tests for the writeproposals package. Integration tests
// (real Postgres + RLS) live in integration_test.go behind the
// `integration` build tag.

package writeproposals

import "testing"

// TestAllowedTools_Exhaustive guards against silent drift between the
// AllowedTools map and the DB CHECK constraint. The list MUST match
// migrations/sql/20260520030000_mcp_write_proposals.sql's
// `mcp_wp_tool_name_check` enum verbatim.
func TestAllowedTools_Exhaustive(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"create_risk":           true,
		"update_control_state":  true,
		"push_evidence":         true,
		"update_risk_treatment": true,
	}
	if len(AllowedTools) != len(want) {
		t.Fatalf("AllowedTools size %d != %d", len(AllowedTools), len(want))
	}
	for name := range want {
		if !AllowedTools[name] {
			t.Errorf("AllowedTools missing %q", name)
		}
	}
	for name := range AllowedTools {
		if !want[name] {
			t.Errorf("AllowedTools contains unexpected %q (must match DB CHECK)", name)
		}
	}
}

// TestStateConstants_MatchMigration documents the lock between Go state
// constants and the DB CHECK enum. A failure here means either the
// migration drifted or the Go constants did; fix both.
func TestStateConstants_MatchMigration(t *testing.T) {
	t.Parallel()
	got := []string{StateAIProposed, StateApplied, StateRejected}
	want := []string{"ai_proposed", "applied", "rejected"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("state[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestDefaultPendingCap_NonZero protects against an accidental zero
// initialization that would silently disable the P0-A5 quota.
func TestDefaultPendingCap_NonZero(t *testing.T) {
	t.Parallel()
	if DefaultPendingCap <= 0 {
		t.Fatalf("DefaultPendingCap must be > 0 to honor P0-A5; got %d", DefaultPendingCap)
	}
}
