// Issue 1957 — decompression and row bounds on the Excel parser.
//
// Fixture-free like excel_test.go: the zip bomb is synthesized in-test
// from a real workbook plus one highly compressible padding part.
package questionnaire

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

// zipBomb rebuilds a valid workbook with one extra part of padBytes
// zeros. Zeros deflate roughly 1000:1, so the upload stays far under
// MaxUploadBytes while the declared uncompressed size does not.
func zipBomb(t *testing.T, workbook []byte, padBytes int) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(workbook), int64(len(workbook)))
	if err != nil {
		t.Fatalf("read workbook zip: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, zf := range zr.File {
		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatalf("create %s: %v", zf.Name, err)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open %s: %v", zf.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatalf("copy %s: %v", zf.Name, err)
		}
		_ = rc.Close()
	}
	w, err := zw.Create("xl/media/pad.bin")
	if err != nil {
		t.Fatalf("create pad: %v", err)
	}
	if _, err := w.Write(make([]byte, padBytes)); err != nil {
		t.Fatalf("write pad: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func minimalWorkbook(t *testing.T) []byte {
	t.Helper()
	return makeXLSX(t, [][]string{
		{"Question ID", "Question"},
		{"IAM-02", "Do you require MFA?"},
	})
}

func TestParseExcel_ZipBombRejected(t *testing.T) {
	raw := zipBomb(t, minimalWorkbook(t), MaxDecompressedBytes+1)
	if len(raw) > MaxUploadBytes {
		t.Fatalf("fixture is %d bytes; it must fit under the upload cap to prove the decompression cap", len(raw))
	}
	_, err := ParseExcel(raw)
	if !errors.Is(err, ErrDecompressedTooLarge) {
		t.Fatalf("expected ErrDecompressedTooLarge, got %v", err)
	}
}

func TestParseExcel_PaddedUnderCapStillParses(t *testing.T) {
	// The same shape well under the cap is a legitimate workbook.
	got, err := ParseExcel(zipBomb(t, minimalWorkbook(t), 1<<20))
	if err != nil {
		t.Fatalf("ParseExcel: %v", err)
	}
	if len(got.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(got.Questions))
	}
}

func TestParseExcel_RowCap(t *testing.T) {
	build := func(dataRows int) []byte {
		rows := make([][]string, 0, dataRows+1)
		rows = append(rows, []string{"Question ID", "Question"})
		for i := 0; i < dataRows; i++ {
			rows = append(rows, []string{fmt.Sprintf("Q-%d", i), "text"})
		}
		return makeXLSX(t, rows)
	}
	// Header + MaxRows-1 data rows = MaxRows rows: accepted.
	got, err := ParseExcel(build(MaxRows - 1))
	if err != nil {
		t.Fatalf("at cap: %v", err)
	}
	if len(got.Questions) != MaxRows-1 {
		t.Fatalf("at cap: got %d questions, want %d", len(got.Questions), MaxRows-1)
	}
	// One row past the cap: rejected.
	if _, err := ParseExcel(build(MaxRows)); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("past cap: expected ErrTooManyRows, got %v", err)
	}
}

func TestCheckDecompressedSize_NonZipFallsThrough(t *testing.T) {
	if err := checkDecompressedSize([]byte("not a zip")); err != nil {
		t.Fatalf("non-zip input should defer to excelize, got %v", err)
	}
}
