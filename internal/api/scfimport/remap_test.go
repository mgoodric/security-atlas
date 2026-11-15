package scfimport

import (
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #1775: RemapClassFor reads the current release's legacy IDs to name the
// successors of a legacy anchor and classify the proposal.
func TestRemapClassFor(t *testing.T) {
	t.Parallel()
	row := func(sameCodeTitle *string, sameCodeLegacy string, succ, succTitles []string) dbx.ListControlsOnLegacySCFAnchorsRow {
		r := dbx.ListControlsOnLegacySCFAnchorsRow{
			LegacyScfID:                "IAC-06",
			LegacyAnchorTitle:          "Multi-Factor Authentication",
			CurrentAnchorLegacyScfID:   sameCodeLegacy,
			LegacySuccessorScfIds:      succ,
			LegacySuccessorTitles:      succTitles,
			CurrentReleaseHasLegacyIds: true,
		}
		if sameCodeTitle != nil {
			r.CurrentAnchorID = pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
			r.CurrentAnchorTitle = sameCodeTitle
		}
		return r
	}
	same, other := "Multi-Factor Authentication", "Strong Authentication"
	noLegacy := row(&same, "", nil, nil)
	noLegacy.CurrentReleaseHasLegacyIds = false
	cases := []struct {
		name  string
		row   dbx.ListControlsOnLegacySCFAnchorsRow
		class RemapClass
		succ  []string
	}{
		{"same code, no legacy id, same title", row(&same, "", nil, nil), RemapUnchanged, []string{"IAC-06"}},
		{"same code lists itself", row(&same, "IAC-06", []string{"IAC-06"}, []string{same}), RemapUnchanged, []string{"IAC-06"}},
		{"same code, new title", row(&other, "", nil, nil), RemapRenamed, []string{"IAC-06"}},
		{"moved code, same title", row(nil, "", []string{"IAC-37"}, []string{same}), RemapUnchanged, []string{"IAC-37"}},
		{"moved code, new title", row(nil, "", []string{"IAC-37"}, []string{other}), RemapRenamed, []string{"IAC-37"}},
		{"split across two codes", row(nil, "", []string{"IAC-37", "IAC-38"}, []string{same, other}), RemapSplit, []string{"IAC-37", "IAC-38"}},
		{"same code kept plus a new one", row(&same, "", []string{"IAC-40"}, []string{other}), RemapSplit, []string{"IAC-06", "IAC-40"}},
		{"code reused by another lineage", row(&same, "IAC-99", []string{"IAC-37"}, []string{same}), RemapReusedID, []string{"IAC-37"}},
		{"code reused, nothing continues it", row(&other, "IAC-99", nil, nil), RemapReusedID, []string{}},
		{"withdrawn", row(nil, "", nil, nil), RemapNoSuccessor, []string{}},
		{"release without legacy ids", noLegacy, RemapNoSuccessor, []string{}},
	}
	for _, c := range cases {
		class, succ := RemapClassFor(c.row)
		if class != c.class || !slices.Equal(succ, c.succ) {
			t.Errorf("%s: got %s %v; want %s %v", c.name, class, succ, c.class, c.succ)
		}
	}
}
