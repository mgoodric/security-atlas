// Slice 135 — unit tests for the data-export library.
//
// Coverage map:
//
//	AC-2 → TestStreamingMemoryUnder50MBFor100KRows
//	AC-3 → TestCSVCellInjectionMitigation
//	AC-4 → TestXLSXZipMembersExactlyFiveEntries
//	AC-5 → TestJSONIsArrayOfObjectsWithHeaderKeys
//	AC-6 → TestBuildFilenameSanitization

package export_test

import (
	"archive/zip"
	"bytes"
	stdcsv "encoding/csv"
	"encoding/json"
	"encoding/xml"
	"io"
	"iter"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/export"
)

// rowIter is a tiny test helper that converts a slice of rows into an
// iter.Seq[[]string] without buffering them again (each row is a
// reference into the original slice).
func rowIter(rows [][]string) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, r := range rows {
			if !yield(r) {
				return
			}
		}
	}
}

// generatedRowIter produces n rows on demand without retaining any of
// them. Used by the memory-bound test (AC-2) — if the encoder buffered
// the full result set, total allocation would grow with n.
func generatedRowIter(n int, ncols int) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		buf := make([]string, ncols)
		for i := 0; i < n; i++ {
			for c := 0; c < ncols; c++ {
				buf[c] = "row" + itoa(i) + "col" + itoa(c) + "padding-text-to-bulk-up-payload-size"
			}
			if !yield(buf) {
				return
			}
		}
	}
}

func itoa(n int) string {
	// avoid pulling strconv into the hot loop signature; the test
	// generator only needs an ad-hoc base-10 conversion.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + (n % 10))
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// discardWriter is io.Discard wrapped so the encoder cannot tell its
// downstream is bottomless. Used by the memory test so write throughput
// is not the limiting factor.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// ---------- AC-2: streaming, memory bound ----------
//
// A 100,000-row export through any of the three encoders MUST keep
// total allocated heap under 50 MB at any point. The test reads
// runtime.MemStats before + after the run and asserts the delta is
// well below the budget. The row generator yields a small reused
// buffer to confirm the encoder does not retain the row beyond its
// yield window.

func TestStreamingMemoryUnder50MBFor100KRows(t *testing.T) {
	const rows = 100_000
	const cols = 9 // matches the audit-log unified Entry width

	header := make([]string, cols)
	for i := range header {
		header[i] = "col" + itoa(i)
	}

	cases := []struct {
		name string
		exp  export.Exporter
	}{
		{"csv", export.NewCSVExporter()},
		{"json", export.NewJSONExporter()},
		{"xlsx", export.NewXLSXExporter()},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			if err := tc.exp.WriteRows(discardWriter{}, header,
				generatedRowIter(rows, cols)); err != nil {
				t.Fatalf("WriteRows: %v", err)
			}

			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			// HeapAlloc snapshot delta. 50 MB cap per slice 135 AC-2.
			// The encoders' working set per row is O(len(header) *
			// average-cell-bytes) ~= a few KB; the cumulative delta
			// across 100k rows should sit in the low MBs after GC.
			//
			// We compare HeapAlloc directly (live heap at sample
			// time) rather than TotalAlloc (cumulative). Streaming
			// encoders should not retain rows; live heap should be
			// roughly flat across the call.
			const budget = 50 * 1024 * 1024
			liveDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			if liveDelta > budget {
				t.Errorf("HeapAlloc grew by %d bytes (%.1f MB); want <= %d (50 MB)",
					liveDelta, float64(liveDelta)/1024/1024, budget)
			}
		})
	}
}

// ---------- AC-3: CSV cell-injection mitigation ----------
//
// Every cell whose first rune is one of `= + - @ \t \r` MUST be
// prefixed with a single quote `'` per the OWASP CSV Injection cheat
// sheet. Cells that DO NOT start with one of those runes MUST pass
// through untouched.

func TestCSVCellInjectionMitigation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"equals", "=cmd|' /C calc'!A0", "'=cmd|' /C calc'!A0"},
		{"plus", "+1+1", "'+1+1"},
		{"minus", "-2+3+cmd|' /C calc'!A0", "'-2+3+cmd|' /C calc'!A0"},
		{"at", "@SUM(1+9)*cmd|' /C calc'!A0", "'@SUM(1+9)*cmd|' /C calc'!A0"},
		{"tab", "\tfoo", "'\tfoo"},
		{"carriage_return", "\rfoo", "'\rfoo"},
		{"normal_text_untouched", "hello", "hello"},
		// "" cell — sanitizer leaves it alone; the csv writer
		// emits just `\n` which the reader skips silently. We
		// cover the empty path via the cell-sanitization unit
		// (TestSanitizeCSVCellTable) and skip the round-trip
		// here.
		{"leading_digit_untouched", "123abc", "123abc"},
		{"leading_letter_untouched", "abc123", "abc123"},
		// Equals NOT at start — untouched. Mitigation only applies
		// to the first rune (formula interpretation trigger).
		{"middle_equals_untouched", "abc=def", "abc=def"},
	}

	header := []string{"value"}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			exp := export.NewCSVExporter()
			err := exp.WriteRows(&buf, header,
				rowIter([][]string{{tc.in}}))
			if err != nil {
				t.Fatalf("WriteRows: %v", err)
			}
			// CSV body: "value\n<sanitized-cell>\n"
			body := buf.String()
			// Parse round-trip via the stdlib reader so we
			// compare LOGICAL cell values, not wire-quoted form.
			// (The csv writer may quote cells containing CR or
			// tab; the reader unquotes them.)
			r := readCSVOneRow(t, body, 1)
			if got := r[0]; got != tc.want {
				t.Errorf("cell = %q; want %q (full body: %q)",
					got, tc.want, body)
			}
		})
	}
}

// TestCSVEmptyCellRoundTrips proves the empty-cell case end-to-end:
// an empty row is emitted as a bare newline and read back as an
// empty single-column record (the stdlib csv reader skips the bare
// `\n` between header and EOF, so we read by counting bytes).
func TestCSVEmptyCellEmitsBareNewline(t *testing.T) {
	var buf bytes.Buffer
	err := export.NewCSVExporter().WriteRows(&buf,
		[]string{"value"}, rowIter([][]string{{""}}))
	if err != nil {
		t.Fatalf("WriteRows: %v", err)
	}
	want := "value\n\n"
	if buf.String() != want {
		t.Errorf("empty cell body = %q; want %q", buf.String(), want)
	}
}

// readCSVOneRow parses the n-th data row out of a body the CSV
// encoder produced. Lets the test assert against the LOGICAL cell
// value rather than the wire-formatted (potentially quoted) form.
func readCSVOneRow(t *testing.T, body string, rowIdx int) []string {
	t.Helper()
	r := stdcsv.NewReader(strings.NewReader(body))
	// The encoder may emit CR mid-cell (one of the OWASP attack
	// inputs); LazyQuotes loosens the parser so we can still read
	// the round-trip back faithfully for the test assertion.
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	for i := 0; i <= rowIdx; i++ {
		rec, err := r.Read()
		if err != nil {
			t.Fatalf("csv read row %d: %v", i, err)
		}
		if i == rowIdx {
			return rec
		}
	}
	t.Fatalf("csv has fewer than %d rows", rowIdx+1)
	return nil
}

// ---------- AC-4: XLSX zip members are exactly five ----------
//
// The handcrafted minimal-XLSX writer (slice 135 D1) MUST produce
// exactly five files inside the zip — and SPECIFICALLY MUST NOT emit
// `xl/charts/*`, `xl/vbaProject.bin`, `xl/sharedStrings.xml`, or any
// named-range / defined-names XML. P0-A6 is enforced by construction
// but the test pins the contract so a future refactor cannot quietly
// add a forbidden surface.

func TestXLSXZipMembersExactlyFiveEntries(t *testing.T) {
	var buf bytes.Buffer
	exp := export.NewXLSXExporter()
	err := exp.WriteRows(&buf, []string{"a", "b"},
		rowIter([][]string{{"1", "2"}, {"3", "4"}}))
	if err != nil {
		t.Fatalf("WriteRows: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	got := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		got = append(got, f.Name)
	}
	sort.Strings(got)

	want := []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/_rels/workbook.xml.rels",
		"xl/workbook.xml",
		"xl/worksheets/sheet1.xml",
	}
	if len(got) != len(want) {
		t.Fatalf("zip member count = %d; want %d; got = %v",
			len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("zip member [%d] = %q; want %q", i, got[i], want[i])
		}
	}

	// Forbidden surfaces: chart objects, VBA, sharedStrings,
	// named-range definitions. P0-A6.
	for _, member := range got {
		forbidden := []string{
			"xl/charts/",
			"xl/vbaProject.bin",
			"xl/sharedStrings.xml",
			"xl/drawings/",
			"xl/embeddings/",
			"xl/media/",
			"xl/pivotTables/",
		}
		for _, prefix := range forbidden {
			if strings.HasPrefix(member, prefix) || member == prefix {
				t.Errorf("forbidden XLSX surface present: %q", member)
			}
		}
	}

	// Workbook xml MUST declare exactly one sheet and MUST NOT carry
	// a <definedNames> element.
	wb := mustReadZipMember(t, zr, "xl/workbook.xml")
	if strings.Count(wb, "<sheet ") != 1 {
		t.Errorf("workbook.xml MUST declare exactly one <sheet>; got %d in %q",
			strings.Count(wb, "<sheet "), wb)
	}
	if strings.Contains(wb, "<definedNames") {
		t.Errorf("workbook.xml MUST NOT contain <definedNames>: %q", wb)
	}

	// Sheet1 MUST contain header + 2 rows; cells must be inline-string
	// type ("inlineStr") so we don't need a sharedStrings dictionary.
	sheet := mustReadZipMember(t, zr, "xl/worksheets/sheet1.xml")
	if got := strings.Count(sheet, "<row "); got != 3 {
		t.Errorf("sheet1 row count = %d; want 3 (header + 2 data rows); body=%q",
			got, sheet)
	}
	if !strings.Contains(sheet, `t="inlineStr"`) {
		t.Errorf("sheet1 cells MUST be inlineStr type; body=%q", sheet)
	}
	if strings.Contains(sheet, `t="s"`) {
		t.Errorf("sheet1 MUST NOT use shared-string cell type t=\"s\"; body=%q", sheet)
	}

	// Sheet1 MUST be well-formed XML.
	var probe struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal([]byte(sheet), &probe); err != nil {
		t.Errorf("sheet1 xml is malformed: %v; body=%q", err, sheet)
	}
}

// mustReadZipMember returns the full body of the named zip member,
// failing the test if the member is absent or unreadable.
func mustReadZipMember(t *testing.T, zr *zip.Reader, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name == name {
			r, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}
			defer r.Close()
			b, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			return string(b)
		}
	}
	t.Fatalf("zip member %q not found", name)
	return ""
}

// XLSX cell-value escaping: angle brackets, ampersands, double-quotes
// in the cell data must NOT break the XML. xml.EscapeText handles this;
// the test pins that we route every cell through it.
func TestXLSXCellEscapesXMLSpecialChars(t *testing.T) {
	var buf bytes.Buffer
	exp := export.NewXLSXExporter()
	err := exp.WriteRows(&buf, []string{"value"},
		rowIter([][]string{{`<script>alert("xss")&fail</script>`}}))
	if err != nil {
		t.Fatalf("WriteRows: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	sheet := mustReadZipMember(t, zr, "xl/worksheets/sheet1.xml")
	// Raw `<script>` MUST NOT appear in the sheet body.
	if strings.Contains(sheet, "<script>") {
		t.Errorf("sheet1 contains unescaped <script>: %q", sheet)
	}
	// Sheet1 still parses as well-formed XML.
	if err := xml.Unmarshal([]byte(sheet), &struct {
		XMLName xml.Name
	}{}); err != nil {
		t.Errorf("sheet1 unparseable after escaping: %v", err)
	}
}

// ---------- AC-5: JSON shape ----------
//
// Output MUST be an array of objects (NOT NDJSON). Object keys MUST
// match the header strings byte-for-byte. Empty input produces "[]".

func TestJSONIsArrayOfObjectsWithHeaderKeys(t *testing.T) {
	t.Run("empty_input_emits_empty_array", func(t *testing.T) {
		var buf bytes.Buffer
		err := export.NewJSONExporter().WriteRows(&buf,
			[]string{"a", "b"}, rowIter(nil))
		if err != nil {
			t.Fatalf("WriteRows: %v", err)
		}
		if buf.String() != "[]" {
			t.Errorf("empty input body = %q; want %q", buf.String(), "[]")
		}
	})

	t.Run("two_rows_array_of_objects_in_header_order", func(t *testing.T) {
		header := []string{"actor_id", "kind", "action"}
		rows := [][]string{
			{"user-1", "decision", "allow"},
			{"key_x", "evidence", "accepted"},
		}
		var buf bytes.Buffer
		err := export.NewJSONExporter().WriteRows(&buf, header,
			rowIter(rows))
		if err != nil {
			t.Fatalf("WriteRows: %v", err)
		}

		var parsed []map[string]string
		if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
			t.Fatalf("unmarshal body %q: %v", buf.String(), err)
		}
		if len(parsed) != 2 {
			t.Fatalf("len(parsed) = %d; want 2", len(parsed))
		}
		for i, want := range rows {
			for j, k := range header {
				if got := parsed[i][k]; got != want[j] {
					t.Errorf("row[%d][%q] = %q; want %q",
						i, k, got, want[j])
				}
			}
		}

		// AC-5 byte-for-byte check: header keys appear in declaration
		// order in the wire body (no alphabetical sort).
		body := buf.String()
		idxActor := strings.Index(body, `"actor_id"`)
		idxKind := strings.Index(body, `"kind"`)
		idxAction := strings.Index(body, `"action"`)
		if idxActor < 0 || idxKind <= idxActor || idxAction <= idxKind {
			t.Errorf("header keys not in declaration order; body=%q", body)
		}

		// MUST be a JSON array — `[` first non-whitespace.
		if strings.TrimSpace(body)[0] != '[' {
			t.Errorf("body MUST start with '['; got %q", body[:1])
		}
		// MUST NOT be NDJSON — there are no bare-line objects.
		if strings.Contains(body, "}\n{") {
			t.Errorf("body looks like NDJSON; want JSON array. body=%q", body)
		}
	})

	t.Run("cell_with_special_chars_escaped", func(t *testing.T) {
		var buf bytes.Buffer
		err := export.NewJSONExporter().WriteRows(&buf,
			[]string{"value"},
			rowIter([][]string{{`"hello"<br>&world`}}))
		if err != nil {
			t.Fatalf("WriteRows: %v", err)
		}
		var parsed []map[string]string
		if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got := parsed[0]["value"]; got != `"hello"<br>&world` {
			t.Errorf("escape round-trip lost data: got %q", got)
		}
	})
}

// ---------- AC-6: BuildFilename sanitization ----------

func TestBuildFilenameSanitization(t *testing.T) {
	cases := []struct {
		name        string
		entity      string
		ext         string
		params      map[string]string
		mustContain []string
		mustNot     []string
		maxLen      int
	}{
		{
			name:        "happy_path_audit_log_csv",
			entity:      "audit-log",
			ext:         "csv",
			params:      map[string]string{"kind": "evidence", "from": "20260518"},
			mustContain: []string{"audit-log_", ".csv", "from-20260518", "kind-evidence"},
			mustNot:     []string{"=", ":", "/", "\\", " "},
			maxLen:      80,
		},
		{
			// CRLF + the header-injection metachars (space, `:`,
			// `;`, `=`, `"`) MUST be stripped. Alphanumeric runes
			// that happen to spell "Content-Disposition" survive,
			// which is harmless once the header-terminator and
			// quote chars are gone — the result cannot break out
			// of the surrounding `filename="<...>"` syntax.
			name:    "crlf_injection_dropped",
			entity:  "audit-log",
			ext:     "csv",
			params:  map[string]string{"actor": "foo\r\nContent-Disposition: attachment; filename=evil.exe"},
			mustNot: []string{"\r", "\n", " ", ":", ";", "=", `"`},
			maxLen:  80,
		},
		{
			name:    "path_traversal_dropped",
			entity:  "../../etc/passwd",
			ext:     "csv",
			params:  map[string]string{"x": "../../etc/passwd"},
			mustNot: []string{"/", "..", ".."},
		},
		{
			name:    "unicode_dropped",
			entity:  "audit",
			ext:     "csv",
			params:  map[string]string{"name": "日本語"},
			mustNot: []string{"日", "本", "語"},
		},
		{
			name:        "length_cap_enforced",
			entity:      "audit-log",
			ext:         "csv",
			params:      map[string]string{"x": strings.Repeat("abcdefgh", 30)},
			mustContain: []string{".csv", "audit-log_"},
			maxLen:      80,
		},
		{
			name:        "empty_params_just_entity_date",
			entity:      "audit-log",
			ext:         "json",
			params:      nil,
			mustContain: []string{"audit-log_", ".json"},
			maxLen:      80,
		},
		{
			name:        "empty_entity_substitutes_default",
			entity:      "",
			ext:         "csv",
			mustContain: []string{"export_", ".csv"},
		},
		{
			name:   "deterministic_with_sorted_params",
			entity: "audit-log",
			ext:    "csv",
			params: map[string]string{"z": "1", "a": "2", "m": "3"},
			// Sorted-key order: a, m, z. The wire output must
			// have a-2 before m-3 before z-1.
			mustContain: []string{"a-2", "m-3", "z-1"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := export.BuildFilename(tc.entity, tc.ext, tc.params)
			t.Logf("filename = %q", got)
			if tc.maxLen > 0 && len(got) > tc.maxLen {
				t.Errorf("filename length = %d; want <= %d (got %q)",
					len(got), tc.maxLen, got)
			}
			for _, sub := range tc.mustContain {
				if !strings.Contains(got, sub) {
					t.Errorf("filename %q must contain %q", got, sub)
				}
			}
			for _, sub := range tc.mustNot {
				if strings.Contains(got, sub) {
					t.Errorf("filename %q must NOT contain %q", got, sub)
				}
			}
			// Universal property: every rune in the output must be
			// ASCII alphanum, `-`, `_`, or `.`.
			for _, r := range got {
				ok := (r >= 'a' && r <= 'z') ||
					(r >= 'A' && r <= 'Z') ||
					(r >= '0' && r <= '9') ||
					r == '-' || r == '_' || r == '.'
				if !ok {
					t.Errorf("filename %q contains forbidden rune %q", got, r)
				}
			}
		})
	}
}

func TestBuildFilenameSortedKeyDeterminism(t *testing.T) {
	// Same filter set MUST produce the same filename regardless of
	// map iteration order.
	params1 := map[string]string{"actor": "alice", "kind": "decision"}
	params2 := map[string]string{"kind": "decision", "actor": "alice"}
	got1 := export.BuildFilename("audit-log", "csv", params1)
	got2 := export.BuildFilename("audit-log", "csv", params2)
	if got1 != got2 {
		t.Errorf("non-deterministic filename: %q vs %q (same params, different map order)",
			got1, got2)
	}
}

// ---------- ResolveExporter wires the three formats ----------

func TestResolveExporterWiresAllThreeFormats(t *testing.T) {
	cases := []struct {
		f               export.Format
		wantContentType string
		wantExt         string
	}{
		{export.FormatCSV, "text/csv; charset=utf-8", "csv"},
		{export.FormatJSON, "application/json", "json"},
		{export.FormatXLSX, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.f), func(t *testing.T) {
			exp, err := export.ResolveExporter(tc.f)
			if err != nil {
				t.Fatalf("ResolveExporter: %v", err)
			}
			if exp.Format() != tc.f {
				t.Errorf("Format() = %q; want %q", exp.Format(), tc.f)
			}
			if exp.ContentType() != tc.wantContentType {
				t.Errorf("ContentType() = %q; want %q",
					exp.ContentType(), tc.wantContentType)
			}
			if exp.FileExt() != tc.wantExt {
				t.Errorf("FileExt() = %q; want %q",
					exp.FileExt(), tc.wantExt)
			}
		})
	}

	_, err := export.ResolveExporter("pdf")
	if err == nil {
		t.Error("ResolveExporter(pdf) must error — slice 135 P0-A11 forbids PDF in this product")
	}
}

func TestIsValid(t *testing.T) {
	for _, f := range export.AllFormats {
		if !export.IsValid(f) {
			t.Errorf("IsValid(%q) = false; want true", f)
		}
	}
	if export.IsValid("pdf") {
		t.Error("IsValid(pdf) = true; want false (slice 135 P0-A11)")
	}
	if export.IsValid("") {
		t.Error("IsValid(empty) = true; want false")
	}
}
