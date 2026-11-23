package riskimport

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/risk"
)

func TestParseCSV_ParentRiskIDs(t *testing.T) {
	t.Parallel()
	csv := "Risk ID,Title,Category,parent_risk_ids\n" +
		"R-2,Child,operational, 9b0c0e3e-0000-0000-0000-000000000001 ;;9b0c0e3e-0000-0000-0000-000000000002\n"
	got, err := ParseCSVWith([]byte(csv), ParseOptions{})
	if err != nil {
		t.Fatalf("ParseCSVWith: %v", err)
	}
	want := []string{"9b0c0e3e-0000-0000-0000-000000000001", "9b0c0e3e-0000-0000-0000-000000000002"}
	if r := got.Risks[0]; fmt.Sprint(r.ParentRiskIDRefs) != fmt.Sprint(want) || r.ParentRef != "" {
		t.Fatalf("risk = %+v", r)
	}
	// Kept unmapped until staging resolves it, as owner_user_id is.
	if v := got.Risks[0].Unmapped["parent_risk_ids"]; v == "" {
		t.Errorf("unmapped = %v; want the cell kept", got.Risks[0].Unmapped)
	}
}

func TestApplyParentRiskIDs(t *testing.T) {
	t.Parallel()
	a := uuid.MustParse("9b0c0e3e-0000-0000-0000-00000000000a")
	b := uuid.MustParse("9b0c0e3e-0000-0000-0000-00000000000b")
	resolved := map[string]uuid.UUID{a.String(): a, b.String(): b, strings.ToUpper(a.String()): a}
	cases := []struct {
		name         string
		refs         []string
		parentRef    string
		wantIDs      []string
		wantUnmapped string // "" means the key is removed
	}{
		{"all resolve, each once", []string{a.String(), strings.ToUpper(a.String()), b.String()}, "", []string{a.String(), b.String()}, ""},
		{"unresolved ids stay unmapped", []string{a.String(), "x", "9b0c0e3e-0000-0000-0000-0000000000ff"}, "", []string{a.String()}, "x;9b0c0e3e-0000-0000-0000-0000000000ff"},
		{"a parent ref wins", []string{a.String()}, "R-1", nil, "as written"},
	}
	for _, c := range cases {
		row := BatchRow{Unmapped: map[string]string{"parent_risk_ids": "as written"}}
		applyParentRiskIDs(&row, StagedRisk{ParentRiskIDRefs: c.refs, ParentRef: c.parentRef}, resolved, "parent_risk_ids")
		if fmt.Sprint(row.ParentRiskIDs) != fmt.Sprint(c.wantIDs) {
			t.Errorf("%s: ids = %v; want %v", c.name, row.ParentRiskIDs, c.wantIDs)
		}
		v, ok := row.Unmapped["parent_risk_ids"]
		if (c.wantUnmapped == "") == ok || v != c.wantUnmapped {
			t.Errorf("%s: unmapped = %q, %v; want %q", c.name, v, ok, c.wantUnmapped)
		}
	}
}

func TestLinkAudit(t *testing.T) {
	t.Parallel()
	own := linkAudit("B1", "row-1", decision{"u", "B1", "row-1", 3})
	if _, ok := own.Detail["note"]; ok || own.Actor != "u" || own.Detail["import_batch_id"] != "B1" {
		t.Errorf("own link audit = %+v", own)
	}
	same := linkAudit("B1", "row-1", decision{"u", "B1", "row-2", 4})
	if same.Detail["settled_by_import_row_id"] != "row-2" || same.Detail["settled_by_import_batch_id"] != nil ||
		!strings.Contains(fmt.Sprint(same.Detail["note"]), "import row 4") {
		t.Errorf("same-batch settle audit = %+v", same)
	}
	cross := linkAudit("B1", "row-1", decision{"u", "B2", "row-9", 7})
	if cross.Detail["import_batch_id"] != "B1" || cross.Detail["settled_by_import_batch_id"] != "B2" ||
		!strings.Contains(fmt.Sprint(cross.Detail["note"]), "another import") {
		t.Errorf("cross-batch settle audit = %+v", cross)
	}
}

func TestStrandedParent(t *testing.T) {
	t.Parallel()
	got := strandedParent("R-1", "completed", unknownParent("R-1"))
	if !strings.Contains(got, `"R-1" when this import completed`) || !strings.Contains(got, "no pending row of any import") {
		t.Errorf("strandedParent = %q", got)
	}
	if got := strandedParent("R-1", "was closed", "x"); got != `still waiting on parent risk ID "R-1" when this import was closed: x` {
		t.Errorf("strandedParent closed = %q", got)
	}
}

func TestParseCSV_ParentRef(t *testing.T) {
	t.Parallel()
	csv := "Risk ID,Title,Category,Parent Risk ID,parent_risk_ids\n" +
		"R-1,Parent,operational,,\n" +
		"R-2,Child,operational, r-1 ,9b0c0e3e-0000-0000-0000-000000000000\n"
	got, err := ParseCSVWith([]byte(csv), ParseOptions{})
	if err != nil {
		t.Fatalf("ParseCSVWith: %v", err)
	}
	if len(got.Risks) != 2 || got.Risks[0].ParentRef != "" || got.Risks[1].ParentRef != "r-1" {
		t.Fatalf("risks = %+v", got.Risks)
	}
	// The export's parent_risk_ids holds uuids of the exporting tenant and
	// is never read as a parent risk ID.
	if len(got.UnmappedColumns) != 1 || got.UnmappedColumns[0] != "parent_risk_ids" {
		t.Errorf("unmapped = %q", got.UnmappedColumns)
	}
}

func TestParseCSV_ParentRefColumnMap(t *testing.T) {
	t.Parallel()
	csv := "Title,Category,Rolls up to\nChild,operational,R-9\n"
	got, err := ParseCSVWith([]byte(csv), ParseOptions{ColumnMap: map[string]string{FieldParentRef: "Rolls up to"}})
	if err != nil {
		t.Fatalf("ParseCSVWith: %v", err)
	}
	if len(got.Risks) != 1 || got.Risks[0].ParentRef != "R-9" {
		t.Fatalf("risks = %+v", got.Risks)
	}
}

func TestLinkRefusal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err     error
		refused bool
		want    string
	}{
		{fmt.Errorf("%w: x", risk.ErrAggregationCycle), true, "cycle"},
		{fmt.Errorf("%w: x", risk.ErrChildLevel), true, "level must be below"},
		{fmt.Errorf("%w: x", risk.ErrIncompatibleMethodology), true, "5x5"},
		{risk.ErrNotFound, true, "matches no risk"},
		{risk.ErrChildrenNotFound, true, "matches no risk"},
		{errors.New("connection reset"), false, ""},
	}
	for _, c := range cases {
		reason, refused := linkRefusal(c.err, "R-1")
		if refused != c.refused || !strings.Contains(reason, c.want) {
			t.Errorf("linkRefusal(%v) = %q, %v; want %v containing %q", c.err, reason, refused, c.refused, c.want)
		}
		if refused && !strings.Contains(reason, `"R-1"`) {
			t.Errorf("linkRefusal(%v) = %q; want the parent named", c.err, reason)
		}
	}
}
