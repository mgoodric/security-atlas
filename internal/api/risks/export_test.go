// Slice 136 — unit tests for the export-projection helpers. The
// integration suite (`export_integration_test.go`) exercises the full
// wire surface against Postgres; this file covers the pure functions
// that do not need a DB.

package risks

import (
	"slices"
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
	parentA, parentB := uuid.New(), uuid.New()
	review := now.Add(7 * 24 * time.Hour)
	acceptedUntil := now.Add(30 * 24 * time.Hour)

	row := riskRow{
		ID:             id,
		Title:          "title-X",
		Description:    "desc-X",
		Category:       "operational",
		CategoryName:   "Operational Resilience",
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
		ParentIDs:      []uuid.UUID{parentA, parentB},
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
		"parent_risk_ids":      parentA.String() + ";" + parentB.String(),
		"category_name":        "Operational Resilience",
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

// col returns the index of the named column, failing the test when the
// header lacks it.
func col(t *testing.T, header []string, name string) int {
	t.Helper()
	i := slices.Index(header, name)
	if i < 0 {
		t.Fatalf("export header has no %q column: %v", name, header)
	}
	return i
}

// A risk with no parents renders an empty parent_risk_ids cell (#1748),
// and the column is appended after acceptance_status so the existing
// column positions hold.
func TestRisksToRowIter_NoParentsRendersBlank(t *testing.T) {
	header := riskExportHeader()
	parents := col(t, header, "parent_risk_ids")
	if got := col(t, header, "acceptance_status"); got != parents-1 {
		t.Fatalf("acceptance_status at %d; want just before parent_risk_ids (%d)", got, parents)
	}
	for cells := range risksToRowIter([]riskRow{{ID: uuid.New()}}) {
		if got := cells[parents]; got != "" {
			t.Errorf("parent_risk_ids for a risk with no parents = %q; want empty string", got)
		}
		break
	}
}

// #1814: the export carries both the category slug and the organisation's
// name for it, the name appended after parent_risk_ids so existing column
// positions hold.
func TestRiskExportHeader_CategorySlugAndName(t *testing.T) {
	header := riskExportHeader()
	if got := header[3]; got != "category" {
		t.Errorf("header[3] = %q; want category (slug, position unchanged)", got)
	}
	if got, want := col(t, header, "category_name"), col(t, header, "parent_risk_ids")+1; got != want {
		t.Errorf("category_name at %d; want %d, after parent_risk_ids", got, want)
	}
}

// #2107: the export carries every risk with its status and, for a closed
// risk, its closure reason, appended after category_name.
func TestRiskExportHeader_StatusAndClosureReason(t *testing.T) {
	header := riskExportHeader()
	status, reason := col(t, header, "status"), col(t, header, "closure_reason")
	if want := col(t, header, "category_name") + 1; status != want || reason != want+1 {
		t.Errorf("status, closure_reason at %d, %d; want %d, %d", status, reason, want, want+1)
	}
	rows := []riskRow{
		{ID: uuid.New(), Status: "open"},
		{ID: uuid.New(), Status: "closed", ClosureReason: "merged"},
	}
	var got [][2]string
	for cells := range risksToRowIter(rows) {
		got = append(got, [2]string{cells[status], cells[reason]})
	}
	want := [][2]string{{"open", ""}, {"closed", "merged"}}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d status, closure_reason = %v; want %v", i, got[i], want[i])
		}
	}
}

// #2450: the owner user and their name are appended after closure_reason,
// beside the free-text treatment_owner, so existing positions hold. A risk
// with no owner user leaves both empty.
func TestRiskExportHeader_OwnerUserAndName(t *testing.T) {
	header := riskExportHeader()
	user, name := col(t, header, "owner_user_id"), col(t, header, "owner_name")
	if reason := col(t, header, "closure_reason"); user != reason+1 || name != reason+2 {
		t.Errorf("owner_user_id, owner_name at %d, %d; want %d, %d (after closure_reason)", user, name, reason+1, reason+2)
	}
	owner := uuid.New()
	rows := []riskRow{
		{ID: uuid.New(), OwnerUserID: &owner, OwnerName: "Alice Owner"},
		{ID: uuid.New(), TreatmentOwner: "risk team"},
	}
	text := col(t, header, "treatment_owner")
	var got [][3]string
	for cells := range risksToRowIter(rows) {
		if len(cells) != len(header) {
			t.Fatalf("row has %d cells; header has %d", len(cells), len(header))
		}
		got = append(got, [3]string{cells[text], cells[user], cells[name]})
	}
	want := [][3]string{{"", owner.String(), "Alice Owner"}, {"risk team", "", ""}}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d treatment_owner, owner_user_id, owner_name = %v; want %v", i, got[i], want[i])
		}
	}
}

// #2838: the organisation risk ID is appended last, so existing positions
// hold. A risk without one exports an empty cell.
func TestRiskExportHeader_ExternalRef(t *testing.T) {
	header := riskExportHeader()
	if got := header[len(header)-1]; got != "external_ref" {
		t.Errorf("last column = %q; want external_ref", got)
	}
	rows := []riskRow{{ID: uuid.New(), ExternalRef: "R-014"}, {ID: uuid.New()}}
	var got []string
	for cells := range risksToRowIter(rows) {
		if len(cells) != len(header) {
			t.Fatalf("row has %d cells; header has %d", len(cells), len(header))
		}
		got = append(got, cells[len(header)-1])
	}
	if want := []string{"R-014", ""}; got[0] != want[0] || got[1] != want[1] {
		t.Errorf("external_ref cells = %q; want %q", got, want)
	}
}
