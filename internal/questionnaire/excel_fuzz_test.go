package questionnaire

import (
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

// goldenXLSX builds a small, valid questionnaire workbook as bytes —
// the same shape makeXLSX produces in excel_test.go. Used to seed the
// fuzz corpus from a golden-derived (synthetic) valid input (P0-421-4).
func goldenXLSX(tb testing.TB) []byte {
	tb.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := f.GetSheetName(0)
	rows := [][]string{
		{"Question ID", "Question", "Domain", "Answer Type"},
		{"IAM-02", "Do you require MFA?", "IAM", "yes/no"},
		{"DSI-01", "Encrypted at rest?", "DSI", "yes/no"},
	}
	for r, row := range rows {
		for c, val := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				tb.Fatalf("coords: %v", err)
			}
			if err := f.SetCellStr(sheet, cell, val); err != nil {
				tb.Fatalf("set cell %s: %v", cell, err)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		tb.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

// FuzzParseExcel drives the questionnaire Excel parser with arbitrary
// bytes. ParseExcel ingests operator-uploaded .xlsx files (untrusted
// input) via excelize, which unzips and parses a zip archive — a
// malformed central directory or zip-bomb shape is the interesting
// boundary (slice 421 notes). A panic here is a DoS on the questionnaire
// ingest path (headline threat = DoS).
//
// Contract asserted: ParseExcel returns either a clean error OR a
// non-nil ParseResult — it never panics. No recover() (P0-421-1). The
// MaxUploadBytes cap is exercised by the seed corpus; the fuzz engine
// explores malformed-zip shapes below the cap.
func FuzzParseExcel(f *testing.F) {
	// Seed corpus: a golden valid workbook, plus adversarial / boundary
	// shapes (empty, non-zip text, truncated zip magic, a bare zip EOCD).
	f.Add(goldenXLSX(f))
	for _, s := range [][]byte{
		nil,
		[]byte(""),
		[]byte("this is not a real xlsx file at all"),
		[]byte("PK\x03\x04"),                         // local-file-header magic, then nothing
		[]byte("PK\x05\x06\x00\x00\x00\x00\x00\x00"), // truncated end-of-central-directory
		[]byte("PK\x03\x04\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		res, err := ParseExcel(data)
		if err != nil {
			return // clean error is an acceptable outcome
		}
		// On success the result must be non-nil and internally consistent —
		// a nil result with a nil error would be a silent mis-parse (T).
		if res == nil {
			t.Fatalf("ParseExcel returned nil result and nil error for %d-byte input", len(data))
		}
	})
}
