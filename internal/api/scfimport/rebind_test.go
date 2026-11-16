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

// A bundle that declares scf_release, or names its anchor by UUID, is
// pinned and never re-bound (#1770); an unreadable manifest is reported as
// not parsed so the caller leaves it alone too.
func TestIsPinned(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		manifest       string
		pinned, parsed bool
	}{
		{"declared release", "bundle_id: a\nscf_anchor_id: IAC-10\nscf_release: test-2026.1\n", true, true},
		{"anchor uuid", "bundle_id: a\nscf_anchor_id: 2f1c6a52-6c2f-4f4e-9a0b-6f3c2a1d9e01\n", true, true},
		{"code, no release", "bundle_id: a\nscf_anchor_id: IAC-10\n", false, true},
		{"blank release", "bundle_id: a\nscf_anchor_id: IAC-10\nscf_release: '  '\n", false, true},
		{"unparseable", "not: [valid", false, false},
	}
	for _, c := range cases {
		pinned, parsed := IsPinned(c.manifest)
		if pinned != c.pinned || parsed != c.parsed {
			t.Errorf("%s: IsPinned = (%v, %v); want (%v, %v)", c.name, pinned, parsed, c.pinned, c.parsed)
		}
	}
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
		// #1975: CLD-09 and TPM-04 merge into one 2026.3 anchor, so a
		// control on either old code is pointed at TPM-10.
		{"many-to-one remap, CLD-09", legacy("CLD-09", "Cloud Service Provider Risk Management", "", false, ""),
			"CLD-09 -> TPM-10 in test-2026.3 (concept)"},
		{"many-to-one remap, TPM-04", legacy("TPM-04", "Third-Party Risk Assessments", "", false, ""),
			"TPM-04 -> TPM-10 in test-2026.3 (concept)"},
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

// #2176: the upgrade and the tenant read share one classifier, so pinned
// and rebindable controls never reach the dashboard warning.
func TestNeedsReanchoring(t *testing.T) {
	t.Parallel()
	withManifest := func(r dbx.ListControlsOnLegacySCFAnchorsRow, m string) dbx.ListControlsOnLegacySCFAnchorsRow {
		r.BundleManifestYaml = m
		return r
	}
	const code = "bundle_id: a\nscf_anchor_id: IAC-10\n"
	rows := []dbx.ListControlsOnLegacySCFAnchorsRow{
		withManifest(legacy("IAC-01", "Policy", "Policy", true, ""), code),
		withManifest(legacy("IAC-10", "Authenticator Management", "Privileged Account Management (PAM)", true, ""), code),
		withManifest(legacy("IAC-10", "Authenticator Management", "Privileged Account Management (PAM)", true, ""), code+"scf_release: test-2026.1\n"),
		withManifest(legacy("IAC-21", "Privileged Account Management", "", false, ""), "not: [valid"),
	}
	got, err := NeedsReanchoring(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("NeedsReanchoring returned %d rows; want 2 (the changed title and the unparseable manifest): %+v", len(got), got)
	}
	if got[0].LegacySCFID != "IAC-10" || got[0].Note == "" {
		t.Errorf("first row = %+v; want IAC-10 with a note", got[0])
	}
	if got[1].Note != unparseableNote {
		t.Errorf("second row note = %q; want the unparseable note", got[1].Note)
	}
	empty, err := NeedsReanchoring(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("NeedsReanchoring(nil) = %v, %v; want an empty, non-nil slice", empty, err)
	}
}
