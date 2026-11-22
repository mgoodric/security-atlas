package riskimport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk"
)

func TestSplitThemes(t *testing.T) {
	t.Parallel()
	for cell, want := range map[string]string{
		"":                               "",
		"vendor-risk":                    "vendor-risk",
		"vendor-risk,availability":       "vendor-risk|availability",
		" Vendors ; availability ,, ;":   "Vendors|availability",
		"Vendors, vendors,VENDORS, Ops ": "Vendors|Ops",
	} {
		if got := strings.Join(splitThemes(cell), "|"); got != want {
			t.Errorf("splitThemes(%q) = %q, want %q", cell, got, want)
		}
	}
}

func TestParseThemesColumn(t *testing.T) {
	t.Parallel()
	got, err := ParseCSVWith([]byte("Title,Category,Risk Themes\nOutage,Ops,\"Vendors; availability\"\nGap,ops,\n"), ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Risks) != 2 || len(got.UnmappedColumns) != 0 {
		t.Fatalf("parsed %+v", got)
	}
	if r := got.Risks[0]; r.Category != "Ops" || strings.Join(r.Themes, "|") != "Vendors|availability" {
		t.Errorf("row 1 = %q %v", r.Category, r.Themes)
	}
	if r := got.Risks[1]; r.Category != "ops" || len(r.Themes) != 0 {
		t.Errorf("row 2 = %q %v", r.Category, r.Themes)
	}
}

// TestDistinctValues: each value is listed once, values differing only in
// case counted together under the first spelling, mapped where it already
// names an active category or a theme, and unmapped otherwise.
func TestDistinctValues(t *testing.T) {
	t.Parallel()
	cats := []risk.Category{
		{Slug: "operational", Name: "Operational"},
		{Slug: "legacy", Name: "Legacy", Archived: true},
	}
	themes := []risk.Theme{{Name: "availability"}, {Name: "vendor-risk"}}
	got := distinctValues([]StagedRisk{
		{Category: "Ops", Themes: []string{"Vendors", "Availability"}},
		{Category: "Operational", Themes: []string{"vendors"}},
		{Category: "OPS"},
		{Category: "legacy", Themes: []string{"vendor-risk"}},
	}, cats, themes)
	var out []string
	for _, m := range got {
		out = append(out, fmt.Sprintf("%s:%s=%s/%d", m.Kind, m.Value, m.Target, m.Rows))
	}
	want := "category:Ops=/2 theme:Vendors=/2 theme:Availability=availability/1 " +
		"category:Operational=operational/1 category:legacy=/1 theme:vendor-risk=vendor-risk/1"
	if s := strings.Join(out, " "); s != want {
		t.Errorf("distinctValues =\n%s\nwant\n%s", s, want)
	}
}

func TestResolveRow(t *testing.T) {
	t.Parallel()
	targets := valueTargets{
		ValueKindCategory: {"ops": "operational", "operations": ""},
		ValueKindTheme:    {"vendors": "vendor-risk", "availability": "availability", "people": ""},
	}
	cat, themes, unmapped := targets.resolveRow("OPS", []string{"Vendors", "availability"})
	if cat != "operational" || strings.Join(themes, ",") != "vendor-risk,availability" || len(unmapped) != 0 {
		t.Errorf("mapped row = %q %v %v", cat, themes, unmapped)
	}
	cat, themes, unmapped = targets.resolveRow("operations", []string{"People", "vendors", "Unknown"})
	if cat != "" || strings.Join(themes, ",") != "vendor-risk" ||
		strings.Join(unmapped, "; ") != `category "operations"; theme "People"; theme "Unknown"` {
		t.Errorf("unmapped row = %q %v %v", cat, themes, unmapped)
	}
}

func TestUnknownTargetError(t *testing.T) {
	t.Parallel()
	cat := (&UnknownTargetError{Kind: ValueKindCategory, Target: "x", Valid: []string{"a", "b"}}).Error()
	theme := (&UnknownTargetError{Kind: ValueKindTheme, Target: "y", Valid: []string{"c"}}).Error()
	if !strings.Contains(cat, "active category slug") || !strings.Contains(cat, "valid: a, b") ||
		!strings.Contains(theme, "a theme name") || !strings.Contains(theme, "valid: c") {
		t.Errorf("errors = %q / %q", cat, theme)
	}
}

// TestBlankCategoryIsMissing: a blank or whitespace-only category cell is a
// missing required field at parse, so it never becomes a value of the
// batch (risk_import_value_maps_value_chk refuses an empty value, which
// would fail the whole upload). A theme cell of blanks and separators
// yields no theme value.
func TestBlankCategoryIsMissing(t *testing.T) {
	t.Parallel()
	got, err := ParseCSVWith([]byte("Title,Category,Themes\nBlank,,x\nSpaces,   ,\nThemes only separators,ops, ; ,\n"), ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Rejected) != 2 {
		t.Fatalf("rejected = %+v, want the two blank-category rows", got.Rejected)
	}
	for _, r := range got.Rejected {
		if r.Reason != RejectMissingField || r.Detail != "missing category" {
			t.Errorf("row %d rejected as %s %q", r.RowNumber, r.Reason, r.Detail)
		}
	}
	if len(got.Risks) != 1 || len(got.Risks[0].Themes) != 0 {
		t.Fatalf("staged = %+v", got.Risks)
	}
	for _, v := range distinctValues(got.Risks, nil, nil) {
		if strings.TrimSpace(v.Value) == "" {
			t.Errorf("blank %s value listed", v.Kind)
		}
	}
}
