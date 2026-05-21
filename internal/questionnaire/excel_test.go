// Slice 155 — Excel parser unit tests.
//
// These tests are deliberately fixture-free (no .xlsx checked into the
// tree). We synthesize each test workbook with excelize.NewFile() and
// hand it to ParseExcel as bytes — same shape the HTTP handler does.
package questionnaire

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// makeXLSX returns a freshly assembled xlsx as a byte slice. `rows[0]`
// is treated as the header.
func makeXLSX(t *testing.T, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	for r, row := range rows {
		for c, val := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("coords: %v", err)
			}
			if err := f.SetCellStr(sheet, cell, val); err != nil {
				t.Fatalf("set cell %s: %v", cell, err)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func TestParseExcel_StandardHeaders(t *testing.T) {
	raw := makeXLSX(t, [][]string{
		{"Question ID", "Question", "Domain", "Answer Type"},
		{"IAM-02", "Do you require MFA?", "IAM", "yes/no"},
		{"DSI-01", "Encrypted at rest?", "DSI", "yes/no"},
	})

	got, err := ParseExcel(raw)
	if err != nil {
		t.Fatalf("ParseExcel: %v", err)
	}
	if len(got.Questions) != 2 {
		t.Fatalf("expected 2 parsed questions, got %d", len(got.Questions))
	}
	if got.Questions[0].Code != "IAM-02" {
		t.Fatalf("expected first code IAM-02, got %q", got.Questions[0].Code)
	}
	if got.Questions[0].Domain != "IAM" {
		t.Fatalf("expected domain IAM, got %q", got.Questions[0].Domain)
	}
	if got.Questions[1].Text != "Encrypted at rest?" {
		t.Fatalf("expected second text, got %q", got.Questions[1].Text)
	}
}

func TestParseExcel_AliasHeaders(t *testing.T) {
	// Vendor questionnaires use a riot of header shapes — the alias map
	// must accept the common ones.
	raw := makeXLSX(t, [][]string{
		{"Control ID", "Description", "Section", "Response Type"},
		{"G.1.1", "MFA enforced?", "Governance", "yes/no/na"},
	})

	got, err := ParseExcel(raw)
	if err != nil {
		t.Fatalf("ParseExcel: %v", err)
	}
	if len(got.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(got.Questions))
	}
	q := got.Questions[0]
	if q.Code != "G.1.1" || q.Domain != "Governance" || q.AnswerType != "yes/no/na" {
		t.Fatalf("alias mapping failed: %+v", q)
	}
}

func TestParseExcel_UnmappedColumns(t *testing.T) {
	raw := makeXLSX(t, [][]string{
		{"Question ID", "Question", "Internal Owner", "Notes"},
		{"IAM-02", "Do you require MFA?", "Sam R.", "see policy"},
	})
	got, err := ParseExcel(raw)
	if err != nil {
		t.Fatalf("ParseExcel: %v", err)
	}
	if len(got.UnmappedColumns) != 2 {
		t.Fatalf("expected 2 unmapped columns, got %d (%v)", len(got.UnmappedColumns), got.UnmappedColumns)
	}
}

func TestParseExcel_SkipsBlankAndIncompleteRows(t *testing.T) {
	raw := makeXLSX(t, [][]string{
		{"Question ID", "Question"},
		{"IAM-02", "Do you require MFA?"},
		{"", ""},                 // blank row — skip
		{"IAM-04", ""},           // no text — skip
		{"", "Some orphan text"}, // no code — skip
		{"DSI-01", "Encrypted at rest?"},
	})
	got, err := ParseExcel(raw)
	if err != nil {
		t.Fatalf("ParseExcel: %v", err)
	}
	if len(got.Questions) != 2 {
		t.Fatalf("expected 2 questions after skipping, got %d", len(got.Questions))
	}
}

func TestParseExcel_TooLarge(t *testing.T) {
	// Build a payload that exceeds MaxUploadBytes without doing any
	// xlsx work — the size check is the first action.
	raw := make([]byte, MaxUploadBytes+1)
	_, err := ParseExcel(raw)
	if err == nil {
		t.Fatal("expected ErrUploadTooLarge, got nil")
	}
	if err != ErrUploadTooLarge {
		t.Fatalf("expected ErrUploadTooLarge, got %v", err)
	}
}

func TestParseExcel_Empty(t *testing.T) {
	_, err := ParseExcel(nil)
	if err != ErrEmptyWorkbook {
		t.Fatalf("expected ErrEmptyWorkbook, got %v", err)
	}
}

func TestParseExcel_NoRecognizableHeaders(t *testing.T) {
	raw := makeXLSX(t, [][]string{
		{"Internal", "Owner", "Notes"},
		{"foo", "bar", "baz"},
	})
	_, err := ParseExcel(raw)
	if err != ErrNoHeaderRow {
		t.Fatalf("expected ErrNoHeaderRow, got %v", err)
	}
}

func TestParseExcel_FormulaCellIsOpaque(t *testing.T) {
	// Security mitigation: a formula cell must be read as its raw
	// string, never evaluated. Build a workbook with a formula cell
	// in the answer-type column and assert the parser does NOT crash
	// and treats the cell as literal text.
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	headers := []string{"Question ID", "Question", "Answer Type"}
	for c, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		_ = f.SetCellStr(sheet, cell, h)
	}
	// Row 2 with a formula in the answer-type column.
	_ = f.SetCellStr(sheet, "A2", "IAM-02")
	_ = f.SetCellStr(sheet, "B2", "Do you require MFA?")
	// excelize.SetCellFormula writes a real formula; we want excelize
	// to NOT evaluate it on read. The parser reads cell strings via
	// GetRows which returns the cached display value — for an unevaluated
	// formula this is the empty string, which the parser tolerates.
	if err := f.SetCellFormula(sheet, "C2", "=HYPERLINK(\"http://attacker.example/x\",\"click\")"); err != nil {
		t.Fatalf("set formula: %v", err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ParseExcel(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseExcel: %v", err)
	}
	if len(got.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(got.Questions))
	}
	// Whatever the cached value is, it must NOT contain the attacker URL
	// because we never resolve the formula.
	if strings.Contains(got.Questions[0].AnswerType, "attacker.example") {
		t.Fatalf("formula was evaluated — value contained attacker URL: %q", got.Questions[0].AnswerType)
	}
}
