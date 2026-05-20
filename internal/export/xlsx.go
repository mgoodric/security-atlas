// Slice 135 — handcrafted minimal-XLSX writer.
//
// JUDGMENT D1: this slice ships a from-scratch Open Office XML writer
// rather than pulling in xuri/excelize/v2 or tealeg/xlsx. Rationale
// captured in docs/audit-log/135-data-export-library-decisions.md:
//
//   - Zero new module dependencies → smaller supply-chain footprint;
//     no Dependabot tracking burden for a transitive-dep tree we don't
//     control.
//   - P0-A6 (single-sheet, text-only, no charts / named ranges / VBA /
//     formatting) is satisfied BY CONSTRUCTION — this writer literally
//     cannot emit any of the forbidden surfaces because the code path
//     for them does not exist.
//   - Single-sheet-text-only XLSX is ~5 XML files in a zip; the spec
//     surface is small enough to write + test in one sitting.
//
// The file shape produced here matches the minimal Office Open XML
// (ECMA-376 part 1) requirements that Excel, LibreOffice Calc,
// Numbers, and Google Sheets all accept:
//
//	[Content_Types].xml          — MIME registry for the package
//	_rels/.rels                  — package-level relationship to workbook
//	xl/workbook.xml              — workbook with ONE sheet entry
//	xl/_rels/workbook.xml.rels   — workbook -> sheet1 relationship
//	xl/worksheets/sheet1.xml     — the data: a <sheetData> element with
//	                               one <row> per export row, each cell
//	                               typed as inline-string ("inlineStr")
//
// Note: inline-string cells avoid the need for `xl/sharedStrings.xml`,
// keeping the zip member list to exactly 5 entries. The slice 135 AC-4
// test pins this list verbatim.

package export

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"iter"
	"strconv"
	"strings"
)

// xlsxExporter is the handcrafted minimal-XLSX writer (slice 135 D1).
type xlsxExporter struct{}

// NewXLSXExporter constructs the XLSX encoder.
func NewXLSXExporter() Exporter { return &xlsxExporter{} }

func (*xlsxExporter) Format() Format { return FormatXLSX }
func (*xlsxExporter) ContentType() string {
	return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
}
func (*xlsxExporter) FileExt() string { return "xlsx" }

// WriteRows writes a minimal single-sheet-text-only .xlsx zip to w.
//
// Memory posture: the zip writer streams to w directly; each sheet row
// is built into a small per-row XML fragment buffer then written out.
// Total per-row allocation is bounded — no full result-set buffering
// (slice 135 P0-A7).
func (e *xlsxExporter) WriteRows(w io.Writer, header []string, rows iter.Seq[[]string]) error {
	return e.WriteRowsWithOpts(w, header, rows, WriteOpts{})
}

// WriteRowsWithOpts ignores opts — XLSX is structurally a tabular
// format where the slice 145 redaction case is best represented as an
// empty cell. An external auditor opening the .xlsx in Excel sees
// "this column is blank for every row" which is the same visual
// affordance as a redaction box on a paper report. The other two
// slice 145 mitigations (`null` for JSON, CSV-injection sanitizer
// untouched) are honored where they live.
func (*xlsxExporter) WriteRowsWithOpts(w io.Writer, header []string, rows iter.Seq[[]string], _ WriteOpts) error {
	zw := zip.NewWriter(w)

	// 1. [Content_Types].xml
	if err := writeZipFile(zw, "[Content_Types].xml", contentTypesXML); err != nil {
		return err
	}
	// 2. _rels/.rels
	if err := writeZipFile(zw, "_rels/.rels", rootRelsXML); err != nil {
		return err
	}
	// 3. xl/workbook.xml
	if err := writeZipFile(zw, "xl/workbook.xml", workbookXML); err != nil {
		return err
	}
	// 4. xl/_rels/workbook.xml.rels
	if err := writeZipFile(zw, "xl/_rels/workbook.xml.rels", workbookRelsXML); err != nil {
		return err
	}

	// 5. xl/worksheets/sheet1.xml — streamed.
	sheetWriter, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return fmt.Errorf("xlsx: create sheet1: %w", err)
	}
	if err := writeSheet1(sheetWriter, header, rows); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("xlsx: zip close: %w", err)
	}
	return nil
}

// writeSheet1 streams the sheet1.xml body. Each row is built into a
// small buffer (bounded by len(header) * average-cell-bytes) and
// written immediately, then discarded — no accumulation.
func writeSheet1(w io.Writer, header []string, rows iter.Seq[[]string]) error {
	if _, err := io.WriteString(w, sheet1Prologue); err != nil {
		return fmt.Errorf("xlsx: sheet prologue: %w", err)
	}
	// Header row at index 1.
	rowIndex := 1
	if err := writeSheetRow(w, rowIndex, header); err != nil {
		return err
	}
	rowIndex++
	for row := range rows {
		if len(row) != len(header) {
			return fmt.Errorf("xlsx: row length %d != header length %d", len(row), len(header))
		}
		if err := writeSheetRow(w, rowIndex, row); err != nil {
			return err
		}
		rowIndex++
	}
	if _, err := io.WriteString(w, sheet1Epilogue); err != nil {
		return fmt.Errorf("xlsx: sheet epilogue: %w", err)
	}
	return nil
}

// writeSheetRow emits one <row> element with inline-string cells.
//
// Cell reference style is the standard A1 notation: column letters
// derived from the zero-based column index (A, B, …, Z, AA, AB, …).
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
		// xml.EscapeText handles < > & ' " and control chars.
		var esc strings.Builder
		_ = xml.EscapeText(&escWriter{b: &esc}, []byte(cell))
		b.WriteString(esc.String())
		b.WriteString(`</t></is></c>`)
	}
	b.WriteString(`</row>`)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("xlsx: write row %d: %w", rowIndex, err)
	}
	return nil
}

// escWriter is a tiny io.Writer over a *strings.Builder so we can pipe
// xml.EscapeText (which wants io.Writer) into a Builder without a
// bytes.Buffer round-trip.
type escWriter struct{ b *strings.Builder }

func (e *escWriter) Write(p []byte) (int, error) {
	e.b.Write(p)
	return len(p), nil
}

// colLetters converts a zero-based column index to the spreadsheet
// A1-style column letters (0 -> "A", 25 -> "Z", 26 -> "AA", …).
func colLetters(zeroIdx int) string {
	n := zeroIdx + 1 // shift to 1-based for the % 26 arithmetic.
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{byte('A' + (n % 26))}, out...)
		n /= 26
	}
	return string(out)
}

// writeZipFile writes a single zip member with the given path and body.
func writeZipFile(zw *zip.Writer, path string, body string) error {
	w, err := zw.Create(path)
	if err != nil {
		return fmt.Errorf("xlsx: create %s: %w", path, err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		return fmt.Errorf("xlsx: write %s: %w", path, err)
	}
	return nil
}

// ===== Static XML fragments =====
//
// These are the four non-data XML files. The fifth file (sheet1.xml)
// is dynamic and built per-call by writeSheet1.

const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
	`</Types>`

const rootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

const workbookXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
	`<sheets><sheet name="data" sheetId="1" r:id="rId1"/></sheets>` +
	`</workbook>`

const workbookRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
	`</Relationships>`

const sheet1Prologue = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<sheetData>`

const sheet1Epilogue = `</sheetData></worksheet>`
