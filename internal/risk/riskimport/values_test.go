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
