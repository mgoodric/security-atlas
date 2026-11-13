package control

import (
	"strings"
	"testing"
)

// #1737 — external_refs on control.yaml.

const externalRefsYAML = `bundle_schema_version: "1"
bundle_id: risk_assessment_annual
title: Annual risk assessment
scf_anchor_id: RSK-04
implementation_type: manual_periodic
external_refs:
  - system: acme-audit
    id: RA-03
    note: "2025 SOC 2 report, Section 4"
  - system: soc2
    kind: tested_under
    id: CC3.1
    note: "FY25 report, section 4"
`

func TestFinalizeBundle_ParsesExternalRefs(t *testing.T) {
	t.Parallel()
	b, err := FinalizeBundleForHTTP([]byte(externalRefsYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := b.Manifest.ExternalRefs
	want := []ExternalRef{
		{System: "acme-audit", Kind: ExternalRefKindReference, ID: "RA-03", Note: "2025 SOC 2 report, Section 4"},
		{System: "soc2", Kind: ExternalRefKindTestedUnder, ID: "CC3.1", Note: "FY25 report, section 4"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d refs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	js, err := b.Manifest.ExternalRefsJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const wantJSON = `[{"system":"acme-audit","kind":"reference","id":"RA-03","note":"2025 SOC 2 report, Section 4"},{"system":"soc2","kind":"tested_under","id":"CC3.1","note":"FY25 report, section 4"}]`
	if string(js) != wantJSON {
		t.Errorf("ExternalRefsJSON = %s\nwant %s", js, wantJSON)
	}
}

func TestExternalRefsJSON_EmptyIsArray(t *testing.T) {
	t.Parallel()
	var m Manifest
	js, err := m.ExternalRefsJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(js) != "[]" {
		t.Fatalf("ExternalRefsJSON = %s, want []", js)
	}
}

func TestValidateStructural_RejectsBadExternalRefs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		refs []ExternalRef
		want string
	}{
		{"missing system", []ExternalRef{{ID: "X-1"}}, "external_refs[0].system is required"},
		{"bad system", []ExternalRef{{System: "SOC 2", ID: "X-1"}}, "external_refs[0].system"},
		{"missing id", []ExternalRef{{System: "soc2", ID: "  "}}, "external_refs[0].id is required"},
		{"bad kind", []ExternalRef{{System: "soc2", Kind: "mapped", ID: "CC1.1"}}, "kind \"mapped\""},
		{"long id", []ExternalRef{{System: "soc2", ID: strings.Repeat("x", 201)}}, "id exceeds 200"},
		{"long note", []ExternalRef{{System: "soc2", ID: "CC1.1", Note: strings.Repeat("x", 501)}}, "note exceeds 500"},
		{
			"duplicate after default kind",
			[]ExternalRef{{System: "soc2", ID: "CC1.1"}, {System: "soc2", Kind: "reference", ID: "CC1.1"}},
			"external_refs[1] duplicates",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := Manifest{
				BundleSchemaVersion: "1",
				BundleID:            "refs_control",
				Title:               "Refs",
				SCFAnchorID:         "IAC-06",
				ImplementationType:  "automated",
				ExternalRefs:        tc.refs,
			}
			err := m.ValidateStructural()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateStructural_SameIDDifferentKindAllowed(t *testing.T) {
	t.Parallel()
	m := Manifest{
		BundleSchemaVersion: "1",
		BundleID:            "refs_control",
		Title:               "Refs",
		SCFAnchorID:         "IAC-06",
		ImplementationType:  "automated",
		ExternalRefs: []ExternalRef{
			{System: "soc2", ID: "CC1.1"},
			{System: "soc2", Kind: ExternalRefKindTestedUnder, ID: "CC1.1"},
		},
	}
	if err := m.ValidateStructural(); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}
