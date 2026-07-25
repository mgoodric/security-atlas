// framework_label_test.go — pure-Go coverage for the slice-680
// framework-label helper (ATLAS-033).
//
// Load-bearing function + branches covered:
//
//   - frameworkLabel (period.go) — composes "<name> <version>" from the
//     nullable catalog columns surfaced by the LIST join. Branches:
//     both present (happy), nil name, nil version, empty-after-trim name,
//     empty-after-trim version, and surrounding-whitespace trim. A ""
//     return is the signal the wire omits framework_label and the
//     frontend falls back to the framework_version_id UUID.
//
// No Postgres, no build tag — fast pure-Go branch coverage per the
// CLAUDE.md "Pure-Go pre-DB unit convention" (Q-2).

package period

import "testing"

func strptr(s string) *string { return &s }

func TestFrameworkLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		fwName  *string
		version *string
		want    string
	}{
		{"both present", strptr("SCF"), strptr("2025.2"), "SCF 2025.2"},
		{"multiword name", strptr("SOC 2"), strptr("2017"), "SOC 2 2017"},
		{"nil name", nil, strptr("2025.2"), ""},
		{"nil version", strptr("SCF"), nil, ""},
		{"both nil", nil, nil, ""},
		{"empty name after trim", strptr("   "), strptr("2025.2"), ""},
		{"empty version after trim", strptr("SCF"), strptr("  "), ""},
		{"trims surrounding whitespace", strptr("  SCF "), strptr(" 2025.2 "), "SCF 2025.2"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := frameworkLabel(tc.fwName, tc.version); got != tc.want {
				t.Errorf("frameworkLabel(%v, %v) = %q; want %q",
					deref(tc.fwName), deref(tc.version), got, tc.want)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
