package scfimport

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func legacy(scfID, title, currentTitle string, current bool, successor string) dbx.ListControlsOnLegacySCFAnchorsRow {
	r := dbx.ListControlsOnLegacySCFAnchorsRow{
		LegacyScfID:       scfID,
		LegacyAnchorTitle: title,
		LegacyRelease:     "test-2026.1",
		CurrentRelease:    "test-2026.3",
		SuccessorScfID:    successor,
	}
	if current {
		r.CurrentAnchorID = pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
		r.CurrentAnchorTitle = &currentTitle
	}
	return r
}

// Matt's #1971 D3 rule: re-bind only when the current release has the same
// code with the same title.
func TestRebindable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		row  dbx.ListControlsOnLegacySCFAnchorsRow
		want bool
	}{
		{"same code, same title", legacy("IAC-01", "Policy", "Policy", true, ""), true},
		{"same code, new title", legacy("IAC-10", "Authenticator Management", "Privileged Account Management (PAM)", true, ""), false},
		{"code gone", legacy("IAC-21", "Privileged Account Management", "", false, ""), false},
	}
	for _, c := range cases {
		if got := Rebindable(c.row); got != c.want {
			t.Errorf("%s: Rebindable = %v; want %v", c.name, got, c.want)
		}
	}
}

func TestReanchorNote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		row  dbx.ListControlsOnLegacySCFAnchorsRow
		want string
	}{
		{"remap table", legacy("IAC-21", "Privileged Account Management", "", false, ""),
			"IAC-21 -> IAC-10 in test-2026.3 (concept)"},
		{"SCF legacy id", legacy("ZZZ-01", "Old", "", false, "ZZZ-09"),
			"ZZZ-01 -> ZZZ-09 in test-2026.3 (SCF lists ZZZ-01 as its legacy ID)"},
		{"title changed", legacy("ZZZ-02", "Old meaning", "New meaning", true, ""),
			`ZZZ-02 is "New meaning" in test-2026.3, was "Old meaning": check the control still means it`},
		{"code gone", legacy("ZZZ-03", "Old", "", false, ""),
			"ZZZ-03 is not in test-2026.3"},
	}
	for _, c := range cases {
		got, err := ReanchorNote(c.row)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: note = %q; want %q", c.name, got, c.want)
		}
	}
}
