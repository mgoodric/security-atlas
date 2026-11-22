package riskimport

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk"
)

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
