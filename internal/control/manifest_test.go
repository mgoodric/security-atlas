package control

import (
	"strings"
	"testing"
)

func TestValidateStructural_AcceptsMinimal(t *testing.T) {
	t.Parallel()
	m := Manifest{
		BundleSchemaVersion: "1",
		BundleID:            "minimal_control",
		Title:               "Minimal control",
		SCFAnchorID:         "IAC-06",
		ImplementationType:  "automated",
	}
	if err := m.ValidateStructural(); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestValidateStructural_RejectsMissingScfAnchor(t *testing.T) {
	t.Parallel()
	m := Manifest{
		BundleSchemaVersion: "1",
		BundleID:            "no_anchor",
		Title:               "Has no anchor",
		ImplementationType:  "automated",
	}
	err := m.ValidateStructural()
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "scf_anchor_id") {
		t.Fatalf("error must mention scf_anchor_id; got %v", err)
	}
}

func TestValidateStructural_RejectsBadImplementationType(t *testing.T) {
	t.Parallel()
	m := Manifest{
		BundleSchemaVersion: "1",
		BundleID:            "bad_impl",
		Title:               "Bad impl",
		SCFAnchorID:         "IAC-06",
		ImplementationType:  "vibes-based",
	}
	err := m.ValidateStructural()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "implementation_type") {
		t.Fatalf("error must mention implementation_type; got %v", err)
	}
}

func TestValidateStructural_RejectsBadBundleSchemaVersion(t *testing.T) {
	t.Parallel()
	m := Manifest{
		BundleSchemaVersion: "2",
		BundleID:            "future",
		Title:               "Future",
		SCFAnchorID:         "IAC-06",
		ImplementationType:  "automated",
	}
	if err := m.ValidateStructural(); err == nil {
		t.Fatalf("expected error for unknown schema version")
	}
}

func TestValidateStructural_RejectsBadBundleIDPattern(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"Capitalized",
		"with spaces",
		"3starts_with_digit",
		"", // empty
	} {
		m := Manifest{
			BundleSchemaVersion: "1",
			BundleID:            bad,
			Title:               "x",
			SCFAnchorID:         "IAC-06",
			ImplementationType:  "automated",
		}
		if err := m.ValidateStructural(); err == nil {
			t.Errorf("expected rejection for bundle_id=%q", bad)
		}
	}
}

func TestValidateStructural_RejectsDuplicateQueryIDs(t *testing.T) {
	t.Parallel()
	m := Manifest{
		BundleSchemaVersion: "1",
		BundleID:            "dupe",
		Title:               "Dupe",
		SCFAnchorID:         "IAC-06",
		ImplementationType:  "automated",
		EvidenceQueries: []EvidenceQuery{
			{ID: "query_one", Language: "rego", Expression: "package x"},
			{ID: "query_one", Language: "rego", Expression: "package y"},
		},
	}
	err := m.ValidateStructural()
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("expected duplicate-id error; got %v", err)
	}
}

func TestValidateStructural_RejectsUnknownQueryLanguage(t *testing.T) {
	t.Parallel()
	m := Manifest{
		BundleSchemaVersion: "1",
		BundleID:            "lang",
		Title:               "Lang",
		SCFAnchorID:         "IAC-06",
		ImplementationType:  "automated",
		EvidenceQueries: []EvidenceQuery{
			{ID: "query_one", Language: "english", Expression: "is it good"},
		},
	}
	err := m.ValidateStructural()
	if err == nil || !strings.Contains(err.Error(), "language") {
		t.Fatalf("expected language error; got %v", err)
	}
}

func TestApplicabilityExprJSON_NilExpr(t *testing.T) {
	t.Parallel()
	m := Manifest{}
	b, err := m.ApplicabilityExprJSON()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("expected null sentinel; got %s", string(b))
	}
}

func TestEvidenceQueriesJSON_EmptyIsArray(t *testing.T) {
	t.Parallel()
	m := Manifest{}
	b, err := m.EvidenceQueriesJSON()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if string(b) != "[]" {
		t.Fatalf("expected empty array; got %s", string(b))
	}
}

// Issue #1736 — anchor_fit rules.
func TestValidateStructural_AnchorFit(t *testing.T) {
	t.Parallel()
	base := func() Manifest {
		return Manifest{
			BundleSchemaVersion: "1",
			BundleID:            "soc2_cc1_2_board_independence",
			Title:               "Audit committee independence",
			SCFAnchorID:         "GOV-09",
			ImplementationType:  "manual_attested",
		}
	}
	note := "No SCF control covers audit committee independence; GOV-09 is the nearest catch-all."

	cases := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string // empty = valid
	}{
		{"omitted defaults to exact", func(*Manifest) {}, ""},
		{"exact without note", func(m *Manifest) { m.AnchorFit = "exact" }, ""},
		{"partial with note", func(m *Manifest) { m.AnchorFit = "partial"; m.AnchorFitNote = note }, ""},
		{"none with note and anchor", func(m *Manifest) { m.AnchorFit = "none"; m.AnchorFitNote = note }, ""},
		{"none with note, no anchor", func(m *Manifest) {
			m.AnchorFit = "none"
			m.AnchorFitNote = note
			m.SCFAnchorID = ""
		}, ""},
		{"none without note", func(m *Manifest) { m.AnchorFit = "none" }, "anchor_fit_note is required"},
		{"partial with blank note", func(m *Manifest) { m.AnchorFit = "partial"; m.AnchorFitNote = "   " }, "anchor_fit_note is required"},
		{"unknown value", func(m *Manifest) { m.AnchorFit = "approximate"; m.AnchorFitNote = note }, `anchor_fit "approximate" is not one of exact|partial|none`},
		{"partial, no anchor", func(m *Manifest) {
			m.AnchorFit = "partial"
			m.AnchorFitNote = note
			m.SCFAnchorID = ""
		}, "scf_anchor_id is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := base()
			tc.mutate(&m)
			err := m.ValidateStructural()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v; want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestAnchorFitDefaults(t *testing.T) {
	t.Parallel()
	var m Manifest
	if got := m.AnchorFitOrDefault(); got != AnchorFitExact {
		t.Fatalf("AnchorFitOrDefault() = %q; want %q", got, AnchorFitExact)
	}
	if got := m.AnchorFitNotePtr(); got != nil {
		t.Fatalf("AnchorFitNotePtr() = %q; want nil", *got)
	}
	m.AnchorFitNote = "  partial cover  "
	if got := m.AnchorFitNotePtr(); got == nil || *got != "partial cover" {
		t.Fatalf("AnchorFitNotePtr() = %v; want trimmed note", got)
	}
}
