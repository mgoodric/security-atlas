// Issue 1957 — ImportExcel maps a zip-bomb upload to 413. Issue 1969 adds
// the zip64 wrap shape (413) and the column cap (400). Pure-Go: the
// parser rejects before any store call, so a nil store is safe.

package questionnaires

import (
	"archive/zip"
	"bytes"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/questionnaire"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestImportExcel_ZipBombIs413(t *testing.T) {
	t.Parallel()
	// One part of zeros declaring more than the decompression cap; it
	// deflates to well under the 5 MB upload cap.
	var bomb bytes.Buffer
	zw := zip.NewWriter(&bomb)
	w, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(make([]byte, questionnaire.MaxDecompressedBytes+1)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	if code, body := postXLSX(t, bomb.Bytes()); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("zip bomb: got %d, want 413 (body %s)", code, body)
	}
}

func TestImportExcel_Zip64WrapIs413(t *testing.T) {
	t.Parallel()
	// Two entries whose declared sizes sum to exactly 2^64: a naive
	// running sum wraps to 0 and the upload fell through to a 400.
	var raw bytes.Buffer
	zw := zip.NewWriter(&raw)
	for _, n := range []uint64{10, math.MaxUint64 - 9} {
		if _, err := zw.CreateRaw(&zip.FileHeader{Name: "xl/media/pad.bin", Method: zip.Store, UncompressedSize64: n}); err != nil {
			t.Fatalf("zip create: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	if code, body := postXLSX(t, raw.Bytes()); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("zip64 wrap: got %d, want 413 (body %s)", code, body)
	}
}

func TestImportExcel_FarColumnIs400(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := f.GetSheetName(0)
	for cell, v := range map[string]string{"A1": "Question ID", "B1": "Question", "XFD2": "x"} {
		if err := f.SetCellStr(sheet, cell, v); err != nil {
			t.Fatalf("set %s: %v", cell, err)
		}
	}
	var raw bytes.Buffer
	if err := f.Write(&raw); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}

	code, body := postXLSX(t, raw.Bytes())
	if code != http.StatusBadRequest || !strings.Contains(body, questionnaire.ErrTooManyColumns.Error()) {
		t.Fatalf("far column: got %d, want 400 column cap (body %s)", code, body)
	}
}

func postXLSX(t *testing.T, xlsx []byte) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "upload.xlsx")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write(xlsx); err != nil {
		t.Fatalf("form write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/x", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	ctx, err := tenancy.WithTenant(r.Context(), testTenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	r = r.WithContext(ctx)

	w := route(New(nil).ImportExcel, r, map[string]string{"id": testRunID})
	return w.Code, w.Body.String()
}
