package controlimport

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
)

// Pure-Go tier (slice-353 Q-2) for approval: input validation, the bundle ID
// slug and the bundle a row becomes. The integration suite in
// internal/api/controlimports covers the stored paths.

func TestBundleIDFor(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"ORG-AC-01":             "org-ac-01",
		"  Access Review 2 ":    "access-review-2",
		"AC 01 / v2":            "ac-01-v2",
		"1.2.3":                 "c-1.2.3",
		"x":                     "x--",
		"--A--":                 "a--",
		"":                      "c--",
		"ünïcode":               "n-code",
		"Ctl_1.a":               "ctl_1.a",
		strings.Repeat("a", 80): strings.Repeat("a", 64),
	}
	for in, want := range cases {
		got := BundleIDFor(in)
		if got != want {
			t.Errorf("BundleIDFor(%q) = %q, want %q", in, got, want)
		}
		m := control.Manifest{
			BundleSchemaVersion: control.BundleSchemaVersion, BundleID: got, Title: "t",
			SCFAnchorID: "IAC-06", ImplementationType: "manual_attested",
		}
		if err := m.ValidateStructural(); err != nil {
			t.Errorf("BundleIDFor(%q) = %q fails the bundle pattern: %v", in, got, err)
		}
	}
}

func TestApproveRowInputValidate(t *testing.T) {
	t.Parallel()
	ok := ApproveRowInput{Approver: "u", CandidateRank: 1}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	cases := []struct {
		name string
		in   ApproveRowInput
		want error
	}{
		{"no approver", ApproveRowInput{CandidateRank: 1}, ErrApproverRequired},
		{"blank approver", ApproveRowInput{Approver: "  ", SCFID: "IAC-06"}, ErrApproverRequired},
		{"no anchor", ApproveRowInput{Approver: "u"}, ErrAnchorChoice},
		{"both anchors", ApproveRowInput{Approver: "u", CandidateRank: 1, SCFID: "IAC-06"}, ErrAnchorChoice},
		{"negative rank", ApproveRowInput{Approver: "u", CandidateRank: -1, SCFID: "IAC-06"}, ErrAnchorChoice},
		{"bad implementation type", ApproveRowInput{Approver: "u", SCFID: "IAC-06", ImplementationType: "manual"}, ErrInvalidApproval},
	}
	for _, tc := range cases {
		if err := tc.in.validate(); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestRejectRowInputValidate(t *testing.T) {
	t.Parallel()
	if err := (RejectRowInput{Approver: "u", Reason: "out of scope"}).validate(); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	for _, tc := range []struct {
		in   RejectRowInput
		want error
	}{
		{RejectRowInput{Reason: "x"}, ErrApproverRequired},
		{RejectRowInput{Approver: "u", Reason: " \n"}, ErrReasonRequired},
		{RejectRowInput{Approver: "u", Reason: strings.Repeat("x", maxRejectReason+1)}, ErrInvalidApproval},
	} {
		if err := tc.in.validate(); !errors.Is(err, tc.want) {
			t.Errorf("%+v: err = %v, want %v", tc.in, err, tc.want)
		}
	}
}

func stagedRow() stagedForDecision {
	return stagedForDecision{
		BatchID: "b", RowNumber: 2, ExternalID: "ORG-AC-01",
		Title: "MFA for workforce", Description: "All workforce accounts require MFA.",
		TestedUnder:   []Requirement{{Code: "CC6.1"}, {Code: "CC6.2"}},
		FrameworkSlug: "soc2", SCFRelease: "2025.2",
	}
}

func TestBundleForRow_DefaultsAndRefs(t *testing.T) {
	t.Parallel()
	b, err := bundleForRow(stagedRow(), "IAC-06", ApproveRowInput{})
	if err != nil {
		t.Fatalf("bundleForRow: %v", err)
	}
	m := b.Manifest
	if m.BundleID != "org-ac-01" || m.Title != "MFA for workforce" || m.Description != "All workforce accounts require MFA." {
		t.Errorf("identity = %q %q %q", m.BundleID, m.Title, m.Description)
	}
	if m.SCFAnchorID != "IAC-06" || m.SCFRelease != "2025.2" {
		t.Errorf("anchor = %s in %q", m.SCFAnchorID, m.SCFRelease)
	}
	if m.ImplementationType != DefaultImplementationType || len(m.ApplicabilityExpr) != 0 {
		t.Errorf("defaults = %q %v, want manual_attested and no applicability (all cells)", m.ImplementationType, m.ApplicabilityExpr)
	}
	want := []control.ExternalRef{
		{System: OrgControlRefSystem, Kind: control.ExternalRefKindReference, ID: "ORG-AC-01"},
		{System: "soc2", Kind: control.ExternalRefKindTestedUnder, ID: "CC6.1"},
		{System: "soc2", Kind: control.ExternalRefKindTestedUnder, ID: "CC6.2"},
	}
	if len(m.ExternalRefs) != len(want) {
		t.Fatalf("refs = %+v", m.ExternalRefs)
	}
	for i := range want {
		if m.ExternalRefs[i] != want[i] {
			t.Errorf("refs[%d] = %+v, want %+v", i, m.ExternalRefs[i], want[i])
		}
	}
	if b.ManifestHashHex == "" || !strings.Contains(string(b.ManifestYAMLRaw), "scf_release: \"2025.2\"") {
		t.Errorf("bundle yaml = %s", b.ManifestYAMLRaw)
	}
}

func TestBundleForRow_RowThenApproverValues(t *testing.T) {
	t.Parallel()
	row := stagedRow()
	row.ImplementationType = "manual_periodic"
	row.Applicability = `{"op":"eq","dim":"env","value":"prod"}`

	b, err := bundleForRow(row, "IAC-06", ApproveRowInput{})
	if err != nil {
		t.Fatalf("bundleForRow: %v", err)
	}
	if b.Manifest.ImplementationType != "manual_periodic" || b.Manifest.ApplicabilityExpr["dim"] != "env" {
		t.Errorf("row values not used: %q %v", b.Manifest.ImplementationType, b.Manifest.ApplicabilityExpr)
	}

	b, err = bundleForRow(row, "IAC-06", ApproveRowInput{
		ImplementationType: "automated",
		ApplicabilityExpr:  map[string]any{"op": "eq", "dim": "geo", "value": "eu"},
	})
	if err != nil {
		t.Fatalf("bundleForRow: %v", err)
	}
	if b.Manifest.ImplementationType != "automated" || b.Manifest.ApplicabilityExpr["dim"] != "geo" {
		t.Errorf("approver values not used: %q %v", b.Manifest.ImplementationType, b.Manifest.ApplicabilityExpr)
	}
}

func TestBundleForRow_Refusals(t *testing.T) {
	t.Parallel()
	row := stagedRow()
	row.Applicability = "production only"
	if _, err := bundleForRow(row, "IAC-06", ApproveRowInput{}); !errors.Is(err, ErrInvalidApproval) ||
		!strings.Contains(err.Error(), "applicability_expr") {
		t.Errorf("free-text applicability: err = %v", err)
	}

	row = stagedRow()
	if _, err := bundleForRow(row, "IAC-06", ApproveRowInput{
		ApplicabilityExpr: map[string]any{"op": "no-such-op"},
	}); !errors.Is(err, ErrInvalidApproval) {
		t.Errorf("bad applicability expr: err = %v", err)
	}

	row.Title = strings.Repeat("t", 201)
	if _, err := bundleForRow(row, "IAC-06", ApproveRowInput{}); !errors.Is(err, ErrInvalidApproval) ||
		strings.Contains(err.Error(), "control bundle:") {
		t.Errorf("long title: err = %v", err)
	}
}
