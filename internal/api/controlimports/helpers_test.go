package controlimports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/controlimport"
)

// Pure-Go tier (slice-353 Q-2): the handler's input parsing and error
// mapping, no Postgres. The integration suite covers the stored paths.

func TestParseColumnMap(t *testing.T) {
	t.Parallel()
	got, err := parseColumnMap([]string{"id = Ref No.", "tested_under=Audit = Mapping"})
	if err != nil {
		t.Fatalf("parseColumnMap: %v", err)
	}
	if got["id"] != "Ref No." || got["tested_under"] != "Audit = Mapping" {
		t.Errorf("map = %v", got)
	}
	if m, err := parseColumnMap(nil); err != nil || m != nil {
		t.Errorf("empty = %v, %v", m, err)
	}
	for _, bad := range [][]string{{"id"}, {"=Header"}, {"id="}, {"id=A", "id=B"}} {
		if _, err := parseColumnMap(bad); err == nil {
			t.Errorf("parseColumnMap(%q) succeeded, want error", bad)
		}
	}
}

func TestWriteStageErrorStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err      error
		status   int
		contains string
	}{
		{controlimport.ErrUploadTooLarge, http.StatusRequestEntityTooLarge, "5 MiB size cap"},
		{controlimport.ErrWorkbookTooLarge, http.StatusRequestEntityTooLarge, "64 MiB uncompressed cap"},
		{controlimport.ErrTooManyRows, http.StatusBadRequest, "5000-row cap"},
		{controlimport.ErrTooManyColumns, http.StatusBadRequest, "past column 200"},
		{controlimport.ErrFrameworkRequired, http.StatusBadRequest, "framework is required"},
		{fmt.Errorf("%w: %q", controlimport.ErrUnknownFramework, "iso"), http.StatusBadRequest, `"iso"`},
		{controlimport.ErrUnsupportedFormat, http.StatusBadRequest, ".csv or .xlsx"},
		{controlimport.ErrEmptyFile, http.StatusBadRequest, "empty"},
		{controlimport.ErrNoHeaderRow, http.StatusBadRequest, "header"},
		{fmt.Errorf("%w: unknown field", controlimport.ErrInvalidColumnMap), http.StatusBadRequest, "unknown field"},
		{controlimport.ErrMappedColumnAbsent, http.StatusBadRequest, "not in the file"},
		{errors.New("db down"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/controls/imports", nil)
		writeStageError(rec, req, tc.err)
		if rec.Code != tc.status {
			t.Errorf("%v: status = %d, want %d", tc.err, rec.Code, tc.status)
		}
		var body map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if !strings.Contains(body["error"], tc.contains) {
			t.Errorf("%v: error = %q, want it to contain %q", tc.err, body["error"], tc.contains)
		}
		if strings.Contains(body["error"], "controlimport:") || strings.Contains(body["error"], "db down") {
			t.Errorf("%v: error leaks internals: %q", tc.err, body["error"])
		}
	}
}

func TestHandlersRequireTenant(t *testing.T) {
	t.Parallel()
	h := New(nil) // never reached: the tenant guard refuses first
	for _, tc := range []struct {
		name string
		fn   http.HandlerFunc
	}{{"create", h.Create}, {"get", h.Get}} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/controls/imports", nil).WithContext(context.Background())
		tc.fn(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without tenant = %d, want 401", tc.name, rec.Code)
		}
	}
}
