// Slice 136 — unit tests for the export-projection helpers. The
// integration suite (`export_integration_test.go`) exercises the full
// wire surface against Postgres; this file covers the pure functions
// that do not need a DB.

package risks

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// risksToRowIter must emit rows in canonical column order. The unit
// test guards against a future contributor reshuffling
// riskExportHeader without updating risksToRowIter (or vice versa).
func TestRisksToRowIter_ColumnOrderMatchesHeader(t *testing.T) {
	header := riskExportHeader()
	now := time.Now().UTC()
	id := uuid.New()
	ou := uuid.New()
	review := now.Add(7 * 24 * time.Hour)
	acceptedUntil := now.Add(30 * 24 * time.Hour)

	row := riskRow{
		ID:             id,
		Title:          "title-X",
		Description:    "desc-X",
		Category:       "operational",
		Methodology:    "nist_800_30",
		InherentScore:  []byte(`{"likelihood":3,"impact":4}`),
		Treatment:      "mitigate",
		TreatmentOwner: "owner-A",
		ResidualScore:  []byte(`{"likelihood":2,"impact":3}`),
		ReviewDueAt:    &review,
		AcceptedUntil:  &acceptedUntil,
		Accepter:       "accepter-X",
		InstrumentRef:  "pol-001",
		OrgUnitID:      &ou,
		Themes:         []string{"data-protection", "access"},
		Severity:       12,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	iter := risksToRowIter([]riskRow{row})
	var cells []string
	for r := range iter {
		cells = r
		break
	}
	if len(cells) != len(header) {
		t.Fatalf("row cell count = %d; want %d (header)", len(cells), len(header))
	}

	// Spot-check the cells at known positions.
	checks := map[string]string{
		"id":                   id.String(),
		"title":                "title-X",
		"description":          "desc-X",
		"category":             "operational",
		"methodology":          "nist_800_30",
		"treatment":            "mitigate",
		"treatment_owner":      "owner-A",
		"accepter":             "accepter-X",
		"instrument_reference": "pol-001",
		"severity":             "12",
		"org_unit_id":          ou.String(),
		"themes":               "data-protection,access",
	}
	for col, want := range checks {
		idx := -1
		for i, h := range header {
			if h == col {
				idx = i
				break
			}
		}
		if idx == -1 {
			t.Errorf("header missing column %q", col)
			continue
		}
		if cells[idx] != want {
			t.Errorf("cell[%q] = %q; want %q", col, cells[idx], want)
		}
	}
	// inherent_score / residual_score round-trip as raw JSON.
	for col, want := range map[string]string{
		"inherent_score": `{"likelihood":3,"impact":4}`,
		"residual_score": `{"likelihood":2,"impact":3}`,
	} {
		idx := -1
		for i, h := range header {
			if h == col {
				idx = i
				break
			}
		}
		if cells[idx] != want {
			t.Errorf("cell[%q] = %q; want %q", col, cells[idx], want)
		}
	}
}

// riskExportHeader must NOT contain treatment_narrative — P0-A-Risk-1
// invariant. Guarded by unit test so a future column-set widening is
// a deliberate slice-doc + decisions-log edit, not an accidental
// addition.
func TestRiskExportHeader_ExcludesTreatmentNarrative(t *testing.T) {
	for _, col := range riskExportHeader() {
		if col == "treatment_narrative" {
			t.Errorf("riskExportHeader contains treatment_narrative — slice 136 P0-A-Risk-1 violation; widen via slice-doc + decisions-log only")
		}
	}
}

// riskExportHeader must contain org_unit_id — P0-A-Risk-2 invariant.
func TestRiskExportHeader_IncludesOrgUnitID(t *testing.T) {
	found := false
	for _, col := range riskExportHeader() {
		if col == "org_unit_id" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("riskExportHeader missing org_unit_id — slice 136 P0-A-Risk-2 violation; the column is load-bearing for slice-053 hierarchy preservation")
	}
}

// Empty themes list renders as the empty string (not "null" or "[]").
// CSV / XLSX consumers parse an empty cell cleanly; the rendering is
// stable across formats via the encoder library.
func TestRisksToRowIter_EmptyThemesRendersBlank(t *testing.T) {
	header := riskExportHeader()
	themesIdx := -1
	for i, h := range header {
		if h == "themes" {
			themesIdx = i
			break
		}
	}
	row := riskRow{
		ID:     uuid.New(),
		Themes: nil,
	}
	iter := risksToRowIter([]riskRow{row})
	for cells := range iter {
		if cells[themesIdx] != "" {
			t.Errorf("themes cell for nil themes = %q; want empty string", cells[themesIdx])
		}
		break
	}
}

// Filename builder strips tenant-identifying characters.
func TestRiskExportFilenamePrefix(t *testing.T) {
	// Cross-check that the entity string survives BuildFilename
	// (sanitization keeps ASCII alphanum + - + _).
	// We can't import internal/export here directly without
	// duplicating that test surface; instead we verify the
	// expectation at the integration layer. This unit test only
	// asserts the prefix shape of the constant.
	if !strings.HasPrefix(riskExportEntity, "risk-register") {
		t.Errorf("riskExportEntity = %q; want prefix risk-register", riskExportEntity)
	}
}
