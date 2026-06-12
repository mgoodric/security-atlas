package grouprole

import (
	"reflect"
	"testing"
)

// TestPlanReconcile is the exhaustive table-driven unit test for the slice 509
// precedence + conflict-resolution + last-admin-guard JUDGMENT. No DB, no I/O —
// pure logic (slice 353 Q-2). Each case maps to an AC / P0.
func TestPlanReconcile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		target         []string
		current        []string
		manual         []string
		adminCount     int
		manualAdmin    bool
		wantGrants     []string
		wantRevokes    []string
		wantSuppressed []string
	}{
		{
			name:       "AC-3 union: two mapped groups grant the union of roles",
			target:     []string{"viewer", "auditor"},
			current:    nil,
			adminCount: 1,
			wantGrants: []string{"auditor", "viewer"},
		},
		{
			name:        "AC-3/P0-509-1 fail-closed: empty target (only unmapped groups) grants nothing and revokes prior derived",
			target:      nil,
			current:     []string{"viewer"},
			adminCount:  1,
			wantRevokes: []string{"viewer"},
		},
		{
			name:       "no-op: target == current",
			target:     []string{"viewer", "control_owner"},
			current:    []string{"viewer", "control_owner"},
			adminCount: 1,
			// nothing to do
		},
		{
			name:        "AC-4 manual survives: a manual role is NOT in current group-derived, so an empty target does not revoke it",
			target:      nil,
			current:     nil, // user holds 'admin' manually only — not group-derived
			manual:      []string{"admin"},
			adminCount:  1,
			manualAdmin: true,
			// no group-derived rows to touch; manual admin untouched
		},
		{
			name:        "AC-4 mixed: re-derivation revokes the group-derived role but the manual role (same user) is untouched because it is not in current",
			target:      []string{"auditor"},
			current:     []string{"viewer"}, // viewer was group-derived, now unmapped
			manual:      []string{"control_owner"},
			adminCount:  1,
			wantGrants:  []string{"auditor"},
			wantRevokes: []string{"viewer"},
		},
		{
			name:           "AC-5/P0-509-3 last-admin guard: only admin, group-derived admin now unmapped -> revoke SUPPRESSED",
			target:         nil,
			current:        []string{"admin"},
			adminCount:     1,
			manualAdmin:    false,
			wantSuppressed: []string{"admin"},
		},
		{
			name:        "AC-5 guard does NOT fire when user also holds admin manually (manual admin survives, tenant safe)",
			target:      nil,
			current:     []string{"admin"},
			manual:      []string{"admin"},
			adminCount:  2, // this user counts once via group-derived + manual is same user; but another admin may exist
			manualAdmin: true,
			wantRevokes: []string{"admin"},
		},
		{
			name:        "AC-5 guard does NOT fire when another admin exists (count > 1)",
			target:      nil,
			current:     []string{"admin"},
			adminCount:  2,
			manualAdmin: false,
			wantRevokes: []string{"admin"},
		},
		{
			name:           "guard suppresses ONLY admin; other now-unmapped roles still revoke in the same plan",
			target:         nil,
			current:        []string{"admin", "viewer"},
			adminCount:     1,
			wantRevokes:    []string{"viewer"},
			wantSuppressed: []string{"admin"},
		},
		{
			name:       "grant admin freely (guard is about REVOKE, not grant)",
			target:     []string{"admin"},
			current:    nil,
			adminCount: 0,
			wantGrants: []string{"admin"},
		},
		{
			name:       "AC-3 duplicate target roles are de-duplicated by the set",
			target:     []string{"viewer", "viewer", "auditor"},
			current:    nil,
			adminCount: 1,
			wantGrants: []string{"auditor", "viewer"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := reconcileState{
				target:               setOf(tc.target),
				current:              setOf(tc.current),
				manual:               setOf(tc.manual),
				tenantAdminCount:     tc.adminCount,
				userHoldsManualAdmin: tc.manualAdmin,
			}
			got := planReconcile(st)
			if !eqSlice(got.grants, tc.wantGrants) {
				t.Errorf("grants = %v, want %v", got.grants, tc.wantGrants)
			}
			if !eqSlice(got.revokes, tc.wantRevokes) {
				t.Errorf("revokes = %v, want %v", got.revokes, tc.wantRevokes)
			}
			if !eqSlice(got.suppressedRevokes, tc.wantSuppressed) {
				t.Errorf("suppressedRevokes = %v, want %v", got.suppressedRevokes, tc.wantSuppressed)
			}
		})
	}
}

func TestSourceValid(t *testing.T) {
	t.Parallel()
	if !SourceOIDC.Valid() || !SourceSCIM.Valid() {
		t.Fatal("oidc and scim must be valid sources")
	}
	if Source("ldap").Valid() {
		t.Fatal("unknown source must be invalid")
	}
}

func TestWouldStrandLastAdmin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		count       int
		manualAdmin bool
		want        bool
	}{
		{"sole admin, no manual -> stranded", 1, false, true},
		{"sole admin but holds manual -> safe", 1, true, false},
		{"two admins -> safe", 2, false, false},
		{"zero admins defensive -> stranded", 0, false, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := wouldStrandLastAdmin(reconcileState{tenantAdminCount: tc.count, userHoldsManualAdmin: tc.manualAdmin})
			if got != tc.want {
				t.Errorf("wouldStrandLastAdmin = %v, want %v", got, tc.want)
			}
		})
	}
}

// eqSlice compares two string slices treating nil and empty as equal.
func eqSlice(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
