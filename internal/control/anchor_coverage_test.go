package control

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestParentSCFCode(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"NET-26.5": "NET-26",
		"GOV-03.1": "GOV-03",
		"IRO-17":   "",
		"":         "",
		"A.B.C":    "A.B",
	}
	for in, want := range cases {
		if got := parentSCFCode(in); got != want {
			t.Errorf("parentSCFCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func fvID(b byte) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte{b}, Valid: true}
}

func TestEnabledFromScopes(t *testing.T) {
	t.Parallel()
	none := enabledFromScopes([]dbx.FrameworkScope{
		{FrameworkVersionID: fvID(1), State: "draft"},
		{FrameworkVersionID: fvID(2), State: "superseded"},
	})
	if none.scoped {
		t.Fatal("no activated scope: want unscoped (every current version)")
	}
	some := enabledFromScopes([]dbx.FrameworkScope{
		{FrameworkVersionID: fvID(1), State: "activated"},
		{FrameworkVersionID: fvID(2), State: "draft"},
	})
	if !some.scoped || !some.versions[fvID(1).Bytes] || some.versions[fvID(2).Bytes] {
		t.Fatalf("activated scope set = %+v, want only fv 1", some)
	}
}

func TestReachesEnabledFramework(t *testing.T) {
	t.Parallel()
	current := dbx.ListRequirementsForAnchorRow{FrameworkVersionID: fvID(1), FrameworkVersionStatus: dbx.FrameworkVersionStatusCurrent}
	legacy := dbx.ListRequirementsForAnchorRow{FrameworkVersionID: fvID(2), FrameworkVersionStatus: dbx.FrameworkVersionStatus("legacy")}
	unscoped := enabledSet{}
	scopedToLegacy := enabledSet{scoped: true, versions: map[[16]byte]bool{fvID(2).Bytes: true}}

	tests := []struct {
		name    string
		reqs    []dbx.ListRequirementsForAnchorRow
		enabled enabledSet
		want    bool
	}{
		{"no requirements", nil, unscoped, false},
		{"unscoped, current version", []dbx.ListRequirementsForAnchorRow{current}, unscoped, true},
		{"unscoped, legacy only", []dbx.ListRequirementsForAnchorRow{legacy}, unscoped, false},
		{"scoped, version not activated", []dbx.ListRequirementsForAnchorRow{current}, scopedToLegacy, false},
		{"scoped, version activated", []dbx.ListRequirementsForAnchorRow{current, legacy}, scopedToLegacy, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := reachesEnabledFramework(tc.reqs, tc.enabled); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAnchorCoverageMessage(t *testing.T) {
	t.Parallel()
	withParent := anchorCoverageMessage("NET-26.5", false, "NET-26", true)
	for _, want := range []string{"NET-26.5", "any current framework", "parent anchor NET-26 does"} {
		if !strings.Contains(withParent, want) {
			t.Errorf("message %q missing %q", withParent, want)
		}
	}
	noParent := anchorCoverageMessage("IRO-17", true, "", false)
	if !strings.Contains(noParent, "activated scope") || strings.Contains(noParent, "parent") {
		t.Errorf("message %q: want scoped wording and no parent suggestion", noParent)
	}
	unmappedParent := anchorCoverageMessage("X-01.1", false, "X-01", false)
	if strings.Contains(unmappedParent, "parent anchor") {
		t.Errorf("message %q: must not suggest an unmapped parent", unmappedParent)
	}
}
