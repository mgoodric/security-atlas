// Slice 145 — per-encoder unit tests for the
// [export.WriteOpts.NullForEmpty] hardening hook.
//
// Coverage map (slice 145):
//
//	AC-2 → TestCSVEmptyCellForNullableColumn
//	AC-2 → TestJSONNullForNullableColumnWhenEmpty
//	AC-2 → TestJSONStringForNullableColumnWhenSet
//	AC-2 → TestXLSXEmptyCellForNullableColumn
//	AC-2 → TestNullableOptsIgnoredByCSVAndXLSXForNonEmpty
//	-    → TestWriteRowsMatchesWriteRowsWithOptsZero (backwards-compat)

package export_test

import (
	"archive/zip"
	"bytes"
	stdcsv "encoding/csv"
	"encoding/json"
	"io"
	"iter"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/export"
)

// CSV: a header column listed in NullForEmpty whose row value is the
// empty string MUST render as an empty cell. (The CSV encoder ignores
// NullForEmpty — empty cell is what an external auditor expects from
// a redacted CSV.)
func TestCSVEmptyCellForNullableColumn(t *testing.T) {
	enc := export.NewCSVExporter()
	header := []string{"id", "payload_json"}
	rows := rowIter([][]string{
		{"row-1", ""},
		{"row-2", `{"k":"v"}`},
	})
	var buf bytes.Buffer
	if err := enc.WriteRowsWithOpts(&buf, header, rows, export.WriteOpts{
		NullForEmpty: map[string]bool{"payload_json": true},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := stdcsv.NewReader(strings.NewReader(buf.String()))
	r.LazyQuotes = true
	parsed, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v; body=%q", err, buf.String())
	}
	if len(parsed) != 3 {
		t.Fatalf("expected 3 rows (header + 2 data); got %d", len(parsed))
	}
	// Header row.
	if parsed[0][1] != "payload_json" {
		t.Errorf("header[1] = %q; want payload_json", parsed[0][1])
	}
	// Row 1: empty payload renders as empty cell.
	if parsed[1][1] != "" {
		t.Errorf("redacted row payload_json = %q; want empty", parsed[1][1])
	}
	// Row 2: non-empty payload still renders verbatim.
	if parsed[2][1] != `{"k":"v"}` {
		t.Errorf("non-redacted row payload_json = %q; want JSON blob", parsed[2][1])
	}
}

// JSON: an empty cell on a NullForEmpty column MUST render as the
// literal `null` token, not `""`. This is the load-bearing contract
// for the slice 145 redaction workflow: `jq '.[0].payload_json'`
// returns `null` (field absent) rather than the empty string `""`
// (field present but blank).
func TestJSONNullForNullableColumnWhenEmpty(t *testing.T) {
	enc := export.NewJSONExporter()
	header := []string{"id", "payload_json"}
	rows := rowIter([][]string{{"row-1", ""}})
	var buf bytes.Buffer
	if err := enc.WriteRowsWithOpts(&buf, header, rows, export.WriteOpts{
		NullForEmpty: map[string]bool{"payload_json": true},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	body := buf.String()
	// Cheap textual check before deep-parse: the literal `:null` MUST
	// appear and the literal `:""` for payload_json must NOT.
	if !strings.Contains(body, `"payload_json":null`) {
		t.Errorf("JSON body missing `\"payload_json\":null`; got %q", body)
	}
	if strings.Contains(body, `"payload_json":""`) {
		t.Errorf("JSON body contains `\"payload_json\":\"\"` — redaction MUST emit null, not empty string; got %q", body)
	}

	// Deep parse: payload_json must decode to nil interface (the
	// stdlib representation of JSON null).
	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v; body=%q", err, body)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 row; got %d", len(parsed))
	}
	got, ok := parsed[0]["payload_json"]
	if !ok {
		t.Fatalf("payload_json key missing")
	}
	if got != nil {
		t.Errorf("payload_json = %v (%T); want nil", got, got)
	}
}

// JSON: a NullForEmpty column with a non-empty value renders normally.
// Proves NullForEmpty is empty-string-conditional, not "always null".
func TestJSONStringForNullableColumnWhenSet(t *testing.T) {
	enc := export.NewJSONExporter()
	header := []string{"id", "payload_json"}
	rows := rowIter([][]string{{"row-1", `{"k":"v"}`}})
	var buf bytes.Buffer
	if err := enc.WriteRowsWithOpts(&buf, header, rows, export.WriteOpts{
		NullForEmpty: map[string]bool{"payload_json": true},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v; body=%q", err, buf.String())
	}
	got := parsed[0]["payload_json"]
	if got != `{"k":"v"}` {
		t.Errorf("payload_json = %v; want JSON-as-string", got)
	}
}

// XLSX: a redacted column renders as an empty inline-string cell
// (visually blank in Excel). NullForEmpty is ignored by the XLSX
// encoder — same posture as CSV.
func TestXLSXEmptyCellForNullableColumn(t *testing.T) {
	enc := export.NewXLSXExporter()
	header := []string{"id", "payload_json"}
	rows := rowIter([][]string{
		{"row-1", ""},
		{"row-2", `{"k":"v"}`},
	})
	var buf bytes.Buffer
	if err := enc.WriteRowsWithOpts(&buf, header, rows, export.WriteOpts{
		NullForEmpty: map[string]bool{"payload_json": true},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read sheet1.xml back out of the zip.
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	var sheet string
	for _, f := range zr.File {
		if f.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("sheet open: %v", err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("sheet read: %v", err)
		}
		sheet = string(b)
		break
	}
	if sheet == "" {
		t.Fatalf("sheet1.xml missing from xlsx zip")
	}

	// Row 1 (data row at index r="2" after header at r="1") MUST have
	// the second cell rendered as an empty inline-string.
	// The canonical writer emits `<c r="B2" t="inlineStr"><is><t xml:space="preserve"></t></is></c>`
	// for an empty cell.
	wantEmpty := `<c r="B2" t="inlineStr"><is><t xml:space="preserve"></t></is></c>`
	if !strings.Contains(sheet, wantEmpty) {
		t.Errorf("xlsx sheet missing empty B2 cell; got %q", sheet)
	}
	// Row 2's payload cell at r="3" MUST contain the JSON blob.
	if !strings.Contains(sheet, `{&#34;k&#34;:&#34;v&#34;}`) && !strings.Contains(sheet, `{"k":"v"}`) {
		t.Errorf("xlsx sheet missing row 2 payload; got %q", sheet)
	}
}

// CSV + XLSX ignore NullForEmpty when the cell is non-empty: nullable
// columns with content render normally, no special-casing. Same JSON
// case covered above.
func TestNullableOptsIgnoredByCSVAndXLSXForNonEmpty(t *testing.T) {
	header := []string{"id", "payload_json"}
	rows := func() iter.Seq[[]string] {
		return rowIter([][]string{{"row-1", "non-empty-payload"}})
	}
	opts := export.WriteOpts{NullForEmpty: map[string]bool{"payload_json": true}}

	t.Run("csv", func(t *testing.T) {
		var buf bytes.Buffer
		if err := export.NewCSVExporter().WriteRowsWithOpts(&buf, header, rows(), opts); err != nil {
			t.Fatalf("csv: %v", err)
		}
		if !strings.Contains(buf.String(), "non-empty-payload") {
			t.Errorf("csv body missing non-empty payload; got %q", buf.String())
		}
	})

	t.Run("xlsx", func(t *testing.T) {
		var buf bytes.Buffer
		if err := export.NewXLSXExporter().WriteRowsWithOpts(&buf, header, rows(), opts); err != nil {
			t.Fatalf("xlsx: %v", err)
		}
		// XLSX is a zip — read sheet1 back.
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("zip open: %v", err)
		}
		var sheet string
		for _, f := range zr.File {
			if f.Name != "xl/worksheets/sheet1.xml" {
				continue
			}
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			sheet = string(b)
		}
		if !strings.Contains(sheet, "non-empty-payload") {
			t.Errorf("xlsx sheet missing non-empty payload; got %q", sheet)
		}
	})
}

// Backwards-compat: WriteRows MUST behave identically to
// WriteRowsWithOpts with a zero-valued WriteOpts. Slice 135 callers
// that have not yet adopted the opts variant see no change in
// output.
func TestWriteRowsMatchesWriteRowsWithOptsZero(t *testing.T) {
	header := []string{"a", "b"}
	rows := func() iter.Seq[[]string] {
		return rowIter([][]string{{"1", ""}, {"2", "x"}})
	}

	for _, enc := range []export.Exporter{
		export.NewCSVExporter(),
		export.NewJSONExporter(),
		export.NewXLSXExporter(),
	} {
		enc := enc
		t.Run(string(enc.Format()), func(t *testing.T) {
			var a, b bytes.Buffer
			if err := enc.WriteRows(&a, header, rows()); err != nil {
				t.Fatalf("WriteRows: %v", err)
			}
			if err := enc.WriteRowsWithOpts(&b, header, rows(), export.WriteOpts{}); err != nil {
				t.Fatalf("WriteRowsWithOpts: %v", err)
			}
			if !bytes.Equal(a.Bytes(), b.Bytes()) {
				t.Errorf("format %s: WriteRows / WriteRowsWithOpts({}) differ:\n  a=%q\n  b=%q",
					enc.Format(), a.String(), b.String())
			}
		})
	}
}
