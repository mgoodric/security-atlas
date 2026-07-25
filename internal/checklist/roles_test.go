package checklist

import "testing"

// TestAssignRole exhaustively exercises the DETERMINISTIC control->role split
// (AC-1, AC-13). The split is the load-bearing "never LLM-guessed" property of
// the slice, so the map is tested by enumeration: exact aliases, the substring
// heuristic + its precedence order, the applicability fallback, and the
// explicit unassigned bucket. Pure-Go, table-driven, fast (slice-353 pattern).
func TestAssignRole(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		ownerRole string
		applic    string
		want      Role
	}{
		// --- exact aliases: infra ---
		{"infra exact", "infra", "true", RoleInfra},
		{"infrastructure", "infrastructure", "true", RoleInfra},
		{"platform team", "platform team", "true", RoleInfra},
		{"devops", "devops", "true", RoleInfra},
		{"sre", "sre", "true", RoleInfra},
		{"operations", "operations", "true", RoleInfra},

		// --- exact aliases: engineering ---
		{"engineering exact", "engineering", "true", RoleEngineering},
		{"developer", "developer", "true", RoleEngineering},
		{"application", "application", "true", RoleEngineering},
		{"backend", "backend", "true", RoleEngineering},

		// --- exact aliases: security ---
		{"security exact", "security", "true", RoleSecurity},
		{"infosec", "infosec", "true", RoleSecurity},
		{"grc engineer", "grc engineer", "true", RoleSecurity},
		{"compliance", "compliance", "true", RoleSecurity},
		{"appsec", "appsec", "true", RoleSecurity},

		// --- normalization: case / separators collapse to the same alias ---
		{"upper+underscore", "INFRA_TEAM", "true", RoleInfra},
		{"dash", "infra-team", "true", RoleInfra},
		{"padded", "  Infra  Team  ", "true", RoleInfra},
		{"slash", "security/grc", "true", RoleSecurity},
		{"mixed sep eng", "Software.Engineering", "true", RoleEngineering},

		// --- substring heuristic (non-exact token) ---
		{"substr security ops -> security not infra", "security operations group", "true", RoleSecurity},
		{"substr appsec engineer -> security", "appsec engineer", "true", RoleSecurity},
		{"substr platform reliability -> infra", "platform reliability guild", "true", RoleInfra},
		{"substr backend developers -> engineering", "backend developers pod", "true", RoleEngineering},
		{"substr cloud team -> infra", "cloud team", "true", RoleInfra},

		// --- precedence: security terms beat ops/eng terms ---
		{"security ops beats ops", "secops on-call", "true", RoleSecurity},
		{"compliance engineering -> security (compliance first)", "compliance engineering", "true", RoleSecurity},
		{"risk operations -> security", "risk operations", "true", RoleSecurity},

		// --- applicability fallback (owner_role blank / non-indicative) ---
		{"blank owner, data_class -> security", "", "data_class = 'pci'", RoleSecurity},
		{"blank owner, cloud -> infra", "", "cloud = 'aws'", RoleInfra},
		{"blank owner, env prod -> infra", "", "env = 'prod'", RoleInfra},
		{"blank owner, product -> engineering", "", "product = 'checkout'", RoleEngineering},
		{"blank owner, pci scope -> security", "", "scope_pci", RoleSecurity},
		{"non-indicative owner falls to applic", "team-alpha", "cloud = 'gcp'", RoleInfra},

		// --- unassigned bucket (honest, never dropped) ---
		{"blank owner, true expr -> unassigned", "", "true", RoleUnassigned},
		{"blank owner, blank expr -> unassigned", "", "", RoleUnassigned},
		{"opaque owner, no signal -> unassigned", "team-alpha", "true", RoleUnassigned},
		{"gibberish -> unassigned", "qzxw", "", RoleUnassigned},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := AssignRole(tc.ownerRole, tc.applic)
			if got != tc.want {
				t.Errorf("AssignRole(%q, %q) = %q, want %q", tc.ownerRole, tc.applic, got, tc.want)
			}
		})
	}
}

// TestAssignRole_AlwaysValid proves the split NEVER returns an out-of-taxonomy
// role for any input — a deterministic total function over the fixed v0 set.
func TestAssignRole_AlwaysValid(t *testing.T) {
	t.Parallel()
	inputs := []struct{ owner, applic string }{
		{"infra", "true"}, {"", ""}, {"weird role", "made up = 'expr'"},
		{"SECURITY", "data_class"}, {"dev", "product"}, {"123", "456"},
	}
	for _, in := range inputs {
		r := AssignRole(in.owner, in.applic)
		if !ValidRole(r) {
			t.Errorf("AssignRole(%q,%q) = %q is not a valid v0 role", in.owner, in.applic, r)
		}
	}
}

func TestValidRole(t *testing.T) {
	t.Parallel()
	for _, r := range []Role{RoleInfra, RoleEngineering, RoleSecurity, RoleUnassigned} {
		if !ValidRole(r) {
			t.Errorf("ValidRole(%q) = false, want true", r)
		}
	}
	for _, r := range []Role{"", "ciso", "ops", Role("INFRA")} {
		if ValidRole(r) {
			t.Errorf("ValidRole(%q) = true, want false", r)
		}
	}
}

// TestAIRoles asserts the AI-authored role set excludes unassigned and is in
// stable order — the section persistence + review view depend on this order.
func TestAIRoles(t *testing.T) {
	t.Parallel()
	want := []Role{RoleInfra, RoleEngineering, RoleSecurity}
	if len(AIRoles) != len(want) {
		t.Fatalf("AIRoles len = %d, want %d", len(AIRoles), len(want))
	}
	for i := range want {
		if AIRoles[i] != want[i] {
			t.Errorf("AIRoles[%d] = %q, want %q", i, AIRoles[i], want[i])
		}
	}
	for _, r := range AIRoles {
		if r == RoleUnassigned {
			t.Error("AIRoles must not contain unassigned")
		}
	}
}

func TestNormalizeOwnerRole(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Infra_Team":      "infra team",
		"  SRE  ":         "sre",
		"security/grc":    "security grc",
		"dev-ops":         "dev ops",
		"App.Engineering": "app engineering",
		"":                "",
	}
	for in, want := range cases {
		if got := normalizeOwnerRole(in); got != want {
			t.Errorf("normalizeOwnerRole(%q) = %q, want %q", in, got, want)
		}
	}
}
