// Issue 1957 — ImportExcel maps a zip-bomb upload to 413. Pure-Go: the
// parser rejects before any store call, so a nil store is safe.

package questionnaires

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

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

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "bomb.xlsx")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write(bomb.Bytes()); err != nil {
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

	h := New(nil)
	w2 := route(h.ImportExcel, r, map[string]string{"id": testRunID})
	if w2.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("zip bomb: got %d, want 413 (body %s)", w2.Code, w2.Body.String())
	}
}
