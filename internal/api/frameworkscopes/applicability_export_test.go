package frameworkscopes

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

func TestSoAFilename(t *testing.T) {
	t.Parallel()
	iso := frameworkscope.SoAExport{FrameworkSlug: "iso27001", FrameworkVersion: "2022"}
	if got := soaFilename(iso, "xlsx"); got != "statement-of-applicability-iso27001-2022.xlsx" {
		t.Errorf("iso filename = %q", got)
	}
	other := frameworkscope.SoAExport{FrameworkSlug: "nist_csf", FrameworkVersion: "2.0 \"final\""}
	if got := soaFilename(other, "csv"); got != "applicability-nist-csf-2-0-final.csv" {
		t.Errorf("other filename = %q", got)
	}
}

// roundTripExport builds an export with one excluded requirement per cell,
// the cell used as both justification and implementation reference.
func roundTripExport(cells []string) frameworkscope.SoAExport {
	var reqs []frameworkscope.RequirementApplicability
	for i, c := range cells {
		reqs = append(reqs, frameworkscope.RequirementApplicability{
			RequirementID: uuid.New(),
			Code:          "R-" + string(rune('A'+i)),
			Decision:      &frameworkscope.Decision{Decision: frameworkscope.DecisionExcluded, Justification: c, ImplementationRef: c},
		})
	}
	return frameworkscope.SoAExport{Applicability: frameworkscope.Applicability{Requirements: reqs}}
}

// Export, re-import, export again: the imported text equals the stored text
// and the second CSV equals the first, so a justification starting with a
// formula character is neither lost nor turned into a formula (#2048).
func TestSoACSVRoundTrip(t *testing.T) {
	t.Parallel()
	cells := []string{"plain", "-not a formula", "=SUM(A1)", "+1", "@cmd", "'quoted by hand", "'=already quoted", "''-twice"}
	var first bytes.Buffer
	if err := writeSoACSV(&first, roundTripExport(cells)); err != nil {
		t.Fatalf("export: %v", err)
	}
	rows, rejected, err := frameworkscope.ParseDecisionCSV(bytes.NewReader(first.Bytes()))
	if err != nil || len(rejected) != 0 {
		t.Fatalf("import: rows %v rejected %v err %v", rows, rejected, err)
	}
	var got []string
	for i, r := range rows {
		got = append(got, r.Justification)
		if r.ImplementationRef != cells[i] {
			t.Errorf("implementation_ref %q re-imported as %q", cells[i], r.ImplementationRef)
		}
	}
	if !reflect.DeepEqual(got, cells) {
		t.Fatalf("re-imported justifications:\n got %q\nwant %q", got, cells)
	}
	var second bytes.Buffer
	if err := writeSoACSV(&second, roundTripExport(got)); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("re-export differs:\n%s\n---\n%s", first.String(), second.String())
	}
}

// A past revision's xlsx states that contradictions reflect current control
// results, on the row above the header; the activated revision's does not.
func TestSoAXLSXContradictionNote(t *testing.T) {
	t.Parallel()
	for _, past := range []bool{false, true} {
		e := roundTripExport([]string{"x"})
		e.PastRevision = past
		var buf bytes.Buffer
		if err := writeSoAXLSX(&buf, e); err != nil {
			t.Fatalf("write: %v", err)
		}
		f, err := excelize.OpenReader(&buf)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		rows, err := f.GetRows(e.Title())
		_ = f.Close()
		if err != nil {
			t.Fatalf("rows: %v", err)
		}
		header := 4
		if past {
			header = 5
			if rows[4][0] != frameworkscope.ContradictionNote {
				t.Fatalf("past revision row 5 = %q, want the contradiction note", rows[4])
			}
		}
		if !reflect.DeepEqual(rows[header], frameworkscope.SoAHeader) {
			t.Fatalf("past=%v: row %d = %q, want the header", past, header+1, rows[header])
		}
	}
}
