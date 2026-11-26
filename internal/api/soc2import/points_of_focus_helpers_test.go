package soc2import

import "testing"

// marshalPointsOfFocus writes [] for none, so the NOT NULL JSONB column is
// always an array, and pointsOfFocusEqual reads the stored value back the way
// the importer's unchanged check needs (#1731).
func TestPointsOfFocusJSONRoundTrip(t *testing.T) {
	t.Parallel()
	empty, err := marshalPointsOfFocus(nil)
	if err != nil || string(empty) != "[]" {
		t.Fatalf("marshal(nil) = %q, %v; want []", empty, err)
	}
	pofs := []PointOfFocus{{Code: "CC1.1-POF1", SCFAnchors: []string{"GOV-01", "IAC-01"}}}
	enc, err := marshalPointsOfFocus(pofs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, tc := range []struct {
		name     string
		stored   string
		incoming []PointOfFocus
		want     bool
	}{
		{"empty array vs none", "[]", nil, true},
		{"empty column vs none", "", nil, true},
		{"round trip", string(enc), pofs, true},
		{"postgres spacing", `[{"code": "CC1.1-POF1", "scf_anchors": ["GOV-01", "IAC-01"]}]`, pofs, true},
		{"none vs some", "[]", pofs, false},
		{"different anchors", `[{"code":"CC1.1-POF1","scf_anchors":["GOV-01"]}]`, pofs, false},
		{"different code", `[{"code":"CC1.1-POF2","scf_anchors":["GOV-01","IAC-01"]}]`, pofs, false},
		{"undecodable", `{`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := pointsOfFocusEqual([]byte(tc.stored), tc.incoming); got != tc.want {
				t.Fatalf("pointsOfFocusEqual(%q) = %v; want %v", tc.stored, got, tc.want)
			}
		})
	}
}

func TestParsePointOfFocus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code   string
		parent string
		n      int
		ok     bool
	}{
		{"CC1.1-POF3", "CC1.1", 3, true},
		{"A1.2-POF12", "A1.2", 12, true},
		{"CC1.1", "", 0, false},
		{"-POF1", "", 0, false},
		{"CC1.1-POFx", "", 0, false},
		{"AC-2(1)", "", 0, false},
	} {
		parent, n, ok := parsePointOfFocus(tc.code)
		if parent != tc.parent || n != tc.n || ok != tc.ok {
			t.Fatalf("parsePointOfFocus(%q) = %q, %d, %v; want %q, %d, %v", tc.code, parent, n, ok, tc.parent, tc.n, tc.ok)
		}
	}
}
