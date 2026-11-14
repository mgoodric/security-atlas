package frameworkscope

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSoAExportTitle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ slug, want string }{
		{"iso27001", TitleSoA},
		{"soc2", TitleApplicability},
		{"iso27001-draft", TitleApplicability},
	} {
		e := SoAExport{FrameworkSlug: tc.slug, FrameworkName: "F", FrameworkVersion: "1"}
		if got := e.Title(); got != tc.want {
			t.Errorf("Title(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
	if got := (SoAExport{FrameworkName: "ISO/IEC 27001", FrameworkVersion: "2022"}).Subtitle(); got != "ISO/IEC 27001 2022" {
		t.Errorf("Subtitle = %q", got)
	}
}

func TestSoAExportRows(t *testing.T) {
	t.Parallel()
	r1, r2 := uuid.New(), uuid.New()
	scopeID := uuid.New()
	approvedAt := time.Date(2026, 11, 1, 9, 30, 0, 0, time.FixedZone("x", 3600))

	e := SoAExport{
		Approver: "key_approver",
		Applicability: Applicability{
			Scope: &FrameworkScope{ID: scopeID, ApprovedAt: &approvedAt},
			Requirements: []RequirementApplicability{
				{RequirementID: r1, Code: "A.1", Title: "One", Contradiction: true,
					Decision: &Decision{Decision: DecisionExcluded, Justification: "why"}},
				{RequirementID: r2, Code: "A.2", Title: "Two"},
			},
		},
		LinkedControls: map[uuid.UUID][]string{r1: {"c_a", "c_b"}},
	}
	want := [][]string{
		{"A.1", "One", "excluded", "why", "", "c_a; c_b", "true", scopeID.String(), "key_approver", "2026-11-01T08:30:00Z"},
		{"A.2", "Two", "undecided", "", "", "", "false", scopeID.String(), "key_approver", "2026-11-01T08:30:00Z"},
	}
	if got := e.Rows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Rows:\n got %q\nwant %q", got, want)
	}
	for _, row := range want {
		if len(row) != len(SoAHeader) {
			t.Fatalf("row width %d, header width %d", len(row), len(SoAHeader))
		}
	}

	// No revision in force: the revision and approval columns are empty.
	e.Applicability.Scope = nil
	if got := e.Rows()[1]; got[7] != "" || got[8] != "" || got[9] != "" {
		t.Fatalf("no-scope row = %q, want empty revision and approval", got)
	}
}
