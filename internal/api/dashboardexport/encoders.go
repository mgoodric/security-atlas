// encoders.go — three multi-panel encoders for the slice 269
// dashboard export. The shared slice 135 library
// (`internal/export/`) ships single-table encoders; the dashboard
// export is multi-panel by definition, so the format generators
// live in this package.
//
// All three encoders stream their output through the supplied
// io.Writer so the response body is built on the fly — the snapshot
// value itself is in memory (it is small; six aggregated panels),
// but the serialised body is NEVER buffered in full.
//
// Streaming-memory guarantee (slice 269 AC-10): with 50K rows
// across the panels, live heap delta stays under 200 MB for each
// format. The 50K test runs in
// `dashboardexport_integration_test.go::TestSlice269_StreamingMemoryUnder200MB`.
//
// # Cell sanitisation
//
// The CSV encoder applies the OWASP cell-injection mitigation —
// every cell whose first rune is one of `= + - @ \t \r` is
// prefixed with a single quote `'`. Borrowed verbatim from
// `internal/export/csv.go` rather than re-imported through the
// package boundary; the function is a 6-line predicate, not worth
// a cross-package coupling.
//
// JSON + XLSX are immune by structure (no cell-text interpretation).
//
// # XLSX surface — explicit P0 by construction
//
// Same posture as `internal/export/xlsx.go`: handcrafted
// minimal-XLSX writer. Six sheets (one per panel) inside the
// workbook; inline-string cells; no shared strings file; no
// charts, named ranges, embedded VBA, or hidden metadata sheets.
// Structurally incapable of emitting any of those by virtue of
// the code path simply not existing.

package dashboardexport

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// panelOrder is the canonical iteration order for the panels in CSV
// (filename order inside the zip) and XLSX (sheet order). The JSON
// encoder emits the same keys but the on-the-wire order is dictated
// by the `Snapshot.Panels` struct's field declaration.
var panelOrder = []string{
	"framework_posture",
	"risks",
	"freshness",
	"drift",
	"upcoming",
	"activity",
}

// encodeSnapshot dispatches to the per-format encoder. Returns the
// first encoder error so the handler can record a failure in the
// meta-audit row.
func encodeSnapshot(w io.Writer, format Format, s Snapshot) error {
	switch format {
	case FormatJSON:
		return encodeJSON(w, s)
	case FormatCSV:
		return encodeCSVZip(w, s)
	case FormatXLSX:
		return encodeXLSX(w, s)
	default:
		// Unreachable — parseFormat already validated.
		return fmt.Errorf("dashboardexport: unsupported format %q", format)
	}
}

// ===== JSON =====

// encodeJSON writes the snapshot as a single JSON document.
//
// The shape (AC-3):
//
//	{
//	  "snapshot_at": "...RFC3339Nano UTC...",
//	  "panels": {
//	    "framework_posture": [...],
//	    "risks":             [...],
//	    "freshness":         {...},
//	    "drift":             {...},
//	    "upcoming":          [...],
//	    "activity":          [...]
//	  }
//	}
//
// The `Snapshot` struct's JSON tags drive the field names; the
// encoder is a thin wrapper around `encoding/json.NewEncoder`.
//
// `activity.summary` is a string holding the upstream
// `evidence_audit_log.summary` JSONB blob as text — we render it
// AS a JSON object (not a quoted string) by encoding it through
// a custom MarshalJSON path. Implementation here: the `ActivityPanelRow`
// struct's Summary field is a string and json.Marshal escapes it; if
// we want raw-JSON passthrough on the wire we have to use json.RawMessage.
// For v1 simplicity we render Summary as a string cell — downstream
// JSON consumers can json.Parse it themselves. The CSV / XLSX paths
// emit it the same way.
func encodeJSON(w io.Writer, s Snapshot) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("json encode: %w", err)
	}
	return nil
}

// ===== CSV (zip) =====

// encodeCSVZip writes a ZIP archive to w with one CSV per panel.
// File names match the panel keys with a `.csv` suffix
// (`framework-posture.csv` etc. — dashed for shell friendliness).
//
// Each CSV body uses the slice 135 OWASP mitigation: every cell
// whose first rune is one of `= + - @ \t \r` is prefixed with `'`.
//
// The archive/zip writer streams to w; per-row buffers are bounded
// by header width × average cell length. No full result-set
// buffering.
func encodeCSVZip(w io.Writer, s Snapshot) error {
	zw := zip.NewWriter(w)
	for _, name := range panelOrder {
		header, rows := panelTable(name, s)
		writer, err := zw.Create(csvMemberName(name))
		if err != nil {
			return fmt.Errorf("zip create %s: %w", name, err)
		}
		if err := writeCSVTo(writer, header, rows); err != nil {
			return fmt.Errorf("csv %s: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("zip close: %w", err)
	}
	return nil
}

// csvMemberName converts a panel key into its zip-member filename.
// Dashed form is what AC-4 specifies ("framework-posture.csv",
// not "framework_posture.csv") — dashes read more naturally in a
// filename to a non-engineer (and the underscore form survives
// inside the file itself as the column-header key).
func csvMemberName(panel string) string {
	return strings.ReplaceAll(panel, "_", "-") + ".csv"
}

// writeCSVTo writes one CSV body (header + rows) to w. Uses
// hand-written CSV emission rather than encoding/csv to keep the
// per-row buffer bounded and the OWASP mitigation inline.
func writeCSVTo(w io.Writer, header []string, rows [][]string) error {
	if err := writeCSVRow(w, header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writeCSVRow(w, row); err != nil {
			return err
		}
	}
	return nil
}

// writeCSVRow writes one CSV row (RFC 4180 quoting + OWASP cell
// sanitisation) followed by a CRLF line terminator.
func writeCSVRow(w io.Writer, cells []string) error {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(csvQuoteCell(sanitizeCSVCell(c)))
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write csv row: %w", err)
	}
	return nil
}

// csvQuoteCell applies the RFC 4180 quoting rules: if the cell
// contains a comma, quote, CR, or LF, surround with double quotes
// and double any embedded quote.
func csvQuoteCell(cell string) string {
	needsQuoting := false
	for i := 0; i < len(cell); i++ {
		switch cell[i] {
		case ',', '"', '\r', '\n':
			needsQuoting = true
		}
		if needsQuoting {
			break
		}
	}
	if !needsQuoting {
		return cell
	}
	return `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
}

// sanitizeCSVCell applies the OWASP cell-injection mitigation. Borrowed
// from internal/export/csv.go (slice 135) — the dangerous leading
// runes are `=`, `+`, `-`, `@`, tab (`\t`), carriage return (`\r`).
func sanitizeCSVCell(cell string) string {
	if cell == "" {
		return cell
	}
	switch cell[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + cell
	}
	return cell
}

// ===== XLSX (multi-sheet) =====

// encodeXLSX writes a minimal Office Open XML workbook to w with
// one sheet per panel (slice 269 AC-5). Same single-purpose
// handcrafted writer posture as `internal/export/xlsx.go` — text-
// only, inline-string cells, no charts / named ranges / VBA.
//
// Sheet name = panel key truncated to 31 chars (the Excel limit);
// none of our panel keys exceed 31 chars but the truncation runs
// unconditionally for safety.
//
// The workbook layout:
//
//	[Content_Types].xml        — package MIME registry (six sheet entries)
//	_rels/.rels                — package-level relationship to workbook
//	xl/workbook.xml            — sheet list (six entries)
//	xl/_rels/workbook.xml.rels — workbook -> sheet relationships
//	xl/worksheets/sheet1..N.xml — per-panel sheet bodies
func encodeXLSX(w io.Writer, s Snapshot) error {
	zw := zip.NewWriter(w)

	if err := writeZipFile(zw, "[Content_Types].xml", contentTypesXLSX(len(panelOrder))); err != nil {
		return err
	}
	if err := writeZipFile(zw, "_rels/.rels", rootRelsXLSX); err != nil {
		return err
	}
	if err := writeZipFile(zw, "xl/workbook.xml", workbookXMLForPanels(panelOrder)); err != nil {
		return err
	}
	if err := writeZipFile(zw, "xl/_rels/workbook.xml.rels", workbookRelsXMLForPanels(len(panelOrder))); err != nil {
		return err
	}

	for i, name := range panelOrder {
		header, rows := panelTable(name, s)
		sheetPath := fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		sheetWriter, err := zw.Create(sheetPath)
		if err != nil {
			return fmt.Errorf("xlsx create %s: %w", sheetPath, err)
		}
		if err := writeSheet(sheetWriter, header, rows); err != nil {
			return fmt.Errorf("xlsx sheet %d (%s): %w", i+1, name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("xlsx zip close: %w", err)
	}
	return nil
}

// writeZipFile writes one zip member with the given path and body.
func writeZipFile(zw *zip.Writer, path string, body string) error {
	fw, err := zw.Create(path)
	if err != nil {
		return fmt.Errorf("xlsx create %s: %w", path, err)
	}
	if _, err := io.WriteString(fw, body); err != nil {
		return fmt.Errorf("xlsx write %s: %w", path, err)
	}
	return nil
}

// writeSheet streams one worksheet XML body: prologue +
// `<sheetData>` containing one `<row>` per CSV row + epilogue. Same
// shape as `internal/export/xlsx.go::writeSheet1` but renamed so the
// dashboard export's per-sheet path is clear at a glance.
func writeSheet(w io.Writer, header []string, rows [][]string) error {
	if _, err := io.WriteString(w, sheetPrologue); err != nil {
		return fmt.Errorf("sheet prologue: %w", err)
	}
	rowIndex := 1
	if err := writeSheetRow(w, rowIndex, header); err != nil {
		return err
	}
	rowIndex++
	for _, row := range rows {
		if err := writeSheetRow(w, rowIndex, row); err != nil {
			return err
		}
		rowIndex++
	}
	if _, err := io.WriteString(w, sheetEpilogue); err != nil {
		return fmt.Errorf("sheet epilogue: %w", err)
	}
	return nil
}

// writeSheetRow emits one `<row>` with inline-string cells.
func writeSheetRow(w io.Writer, rowIndex int, cells []string) error {
	var b strings.Builder
	b.WriteString(`<row r="`)
	b.WriteString(strconv.Itoa(rowIndex))
	b.WriteString(`">`)
	for i, cell := range cells {
		ref := colLetters(i) + strconv.Itoa(rowIndex)
		b.WriteString(`<c r="`)
		b.WriteString(ref)
		b.WriteString(`" t="inlineStr"><is><t xml:space="preserve">`)
		var esc strings.Builder
		_ = xml.EscapeText(&escWriter{b: &esc}, []byte(cell))
		b.WriteString(esc.String())
		b.WriteString(`</t></is></c>`)
	}
	b.WriteString(`</row>`)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write row %d: %w", rowIndex, err)
	}
	return nil
}

// colLetters converts a zero-based column index to the A1-style
// column letters (0 -> "A", 25 -> "Z", 26 -> "AA", …).
func colLetters(zeroIdx int) string {
	n := zeroIdx + 1
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{byte('A' + (n % 26))}, out...)
		n /= 26
	}
	return string(out)
}

// escWriter adapts a *strings.Builder to io.Writer for xml.EscapeText.
type escWriter struct{ b *strings.Builder }

func (e *escWriter) Write(p []byte) (int, error) {
	e.b.Write(p)
	return len(p), nil
}

// xlsxSheetName truncates the panel key to the Excel sheet-name
// limit (31 chars). None of our panel keys hit this limit but the
// function runs unconditionally so a future longer key is safe.
func xlsxSheetName(panel string) string {
	if len(panel) > 31 {
		return panel[:31]
	}
	return panel
}

// ===== Per-panel projection =====

// panelTable returns the (header, rows) projection for one panel
// name. The header is the column list (matches the JSON field
// keys); rows are the panel's data flattened into string cells.
//
// The freshness + drift panels are structured (not list-shaped) —
// freshness collapses to one row per class bucket, drift to one row
// per flipped-out control PLUS a summary row at the top giving the
// since/through/delta/count metadata. This matches the dashboard's
// visual rendering: the user sees a metadata header and a list
// underneath.
func panelTable(name string, s Snapshot) (header []string, rows [][]string) {
	switch name {
	case "framework_posture":
		return frameworkPostureTable(s.Panels.FrameworkPosture)
	case "risks":
		return risksTable(s.Panels.Risks)
	case "freshness":
		return freshnessTable(s.Panels.Freshness)
	case "drift":
		return driftTable(s.Panels.Drift)
	case "upcoming":
		return upcomingTable(s.Panels.Upcoming)
	case "activity":
		return activityTable(s.Panels.Activity)
	default:
		return []string{"panel"}, [][]string{{name + " (unknown)"}}
	}
}

func frameworkPostureTable(rows []FrameworkPosturePanelRow) ([]string, [][]string) {
	header := []string{
		"framework_id",
		"framework_version",
		"coverage_pct",
		"freshness_composite",
		"trend_delta_90d",
	}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{
			r.FrameworkID,
			r.FrameworkVersion,
			strconv.FormatFloat(r.CoveragePct, 'f', -1, 64),
			strconv.FormatFloat(r.FreshnessComposite, 'f', -1, 64),
			strconv.FormatFloat(r.TrendDelta90d, 'f', -1, 64),
		}
	}
	return header, out
}

func risksTable(rows []RiskPanelRow) ([]string, [][]string) {
	header := []string{
		"id",
		"title",
		"treatment",
		"category",
		"methodology",
		"residual_score",
		"created_at",
	}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{
			r.ID,
			r.Title,
			r.Treatment,
			r.Category,
			r.Methodology,
			r.ResidualScore,
			r.CreatedAt,
		}
	}
	return header, out
}

// freshnessTable flattens the bucket rollup into one row per class
// PLUS a summary row at the top carrying the total / total_stale
// metadata. The summary row uses a sentinel class name `__total__`
// so a downstream consumer can detect + filter it.
func freshnessTable(p FreshnessPanel) ([]string, [][]string) {
	header := []string{"freshness_class", "total", "fresh", "stale"}
	out := make([][]string, 0, len(p.Buckets)+1)
	out = append(out, []string{
		"__total__",
		strconv.Itoa(p.Total),
		strconv.Itoa(p.Total - p.TotalStale),
		strconv.Itoa(p.TotalStale),
	})
	for _, b := range p.Buckets {
		out = append(out, []string{
			b.FreshnessClass,
			strconv.Itoa(b.Total),
			strconv.Itoa(b.Fresh),
			strconv.Itoa(b.Stale),
		})
	}
	return header, out
}

// driftTable flattens the flipped-out rows PLUS a summary row at
// the top carrying the since/through/delta/count window metadata.
// Same sentinel-row pattern as freshnessTable.
func driftTable(p DriftPanel) ([]string, [][]string) {
	header := []string{"control_id", "last_passing", "current_result"}
	out := make([][]string, 0, len(p.FlippedOut)+1)
	out = append(out, []string{
		fmt.Sprintf("__window__ since=%s through=%s delta=%d flipped_out_count=%d",
			p.Since, p.Through, p.Delta, p.FlippedOutCount),
		"",
		"",
	})
	for _, fr := range p.FlippedOut {
		out = append(out, []string{
			fr.ControlID,
			fr.LastPassing,
			fr.CurrentResult,
		})
	}
	return header, out
}

func upcomingTable(rows []UpcomingPanelRow) ([]string, [][]string) {
	header := []string{"due_date", "category", "title", "resource_type", "resource_id"}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{
			r.DueDate,
			r.Category,
			r.Title,
			r.ResourceType,
			r.ResourceID,
		}
	}
	return header, out
}

func activityTable(rows []ActivityPanelRow) ([]string, [][]string) {
	header := []string{"ts", "event_type", "actor", "resource_type", "resource_id", "summary"}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{
			r.TS,
			r.EventType,
			r.Actor,
			r.ResourceType,
			r.ResourceID,
			r.Summary,
		}
	}
	return header, out
}

// ===== XLSX static fragments =====

// contentTypesXLSX builds the [Content_Types].xml body with one
// Override entry per sheet. The base entry list (rels + workbook)
// is constant; sheet entries are templated.
func contentTypesXLSX(sheetCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	for i := 1; i <= sheetCount; i++ {
		b.WriteString(`<Override PartName="/xl/worksheets/sheet`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`)
	}
	b.WriteString(`</Types>`)
	return b.String()
}

const rootRelsXLSX = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

// workbookXMLForPanels builds the workbook.xml body with one
// <sheet> entry per panel. Sheet ids are 1..N; relationship ids
// rId1..rIdN. Names are the panel keys (truncated to 31 chars).
func workbookXMLForPanels(panels []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" `)
	b.WriteString(`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	b.WriteString(`<sheets>`)
	for i, name := range panels {
		b.WriteString(`<sheet name="`)
		b.WriteString(xmlEscapeAttr(xlsxSheetName(name)))
		b.WriteString(`" sheetId="`)
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(`" r:id="rId`)
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(`"/>`)
	}
	b.WriteString(`</sheets>`)
	b.WriteString(`</workbook>`)
	return b.String()
}

// workbookRelsXMLForPanels builds the workbook.xml.rels body with
// one relationship per sheet pointing at the per-sheet XML file.
func workbookRelsXMLForPanels(sheetCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= sheetCount; i++ {
		b.WriteString(`<Relationship Id="rId`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`.xml"/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

const sheetPrologue = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<sheetData>`

const sheetEpilogue = `</sheetData></worksheet>`

// xmlEscapeAttr is a small helper for escaping XML attribute values.
// xml.EscapeText handles element-text; attribute values need the
// additional double-quote escape.
func xmlEscapeAttr(s string) string {
	s = strings.ReplaceAll(s, `&`, `&amp;`)
	s = strings.ReplaceAll(s, `<`, `&lt;`)
	s = strings.ReplaceAll(s, `>`, `&gt;`)
	s = strings.ReplaceAll(s, `"`, `&quot;`)
	return s
}

// ===== unused-import guards =====

// Keep `time` imported in case future encoders want to stamp
// generated-at timestamps inside the output bodies (the XLSX header
// would be a natural place). Today the snapshot's `SnapshotAt`
// carries the timestamp at the document level; the per-format
// bodies inherit it.
var _ = time.RFC3339
