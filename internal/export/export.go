// Package export is the slice-135 data-export library. It is the third
// distinct export product in the platform, alongside:
//
//   - OSCAL export (`internal/api/oscalexport/`, slice 030) — audit-binding
//     SSP/AP/AR/POA&M bundles for auditor handoff. Cosigned. Constitutional
//     artifact discipline.
//   - PDF export (slice 042-area) — formatted deliverables for human
//     distribution (policies + board packs).
//
// This package ships **data export** — CSV / JSON / XLSX dumps of
// tenant-scoped data for operator workflows: drop into Excel, pipe into a
// script, archive offline, feed an external system. It is intentionally
// agnostic to the entity shape; per-entity callers (slice 135's audit-log
// reference impl + spillovers 136–139) build a row-iterator under their
// own RLS-aware tenant context and pass it to an [Exporter].
//
// # Threat model anchors
//
// The slice 135 threat model identifies two load-bearing concerns:
//
//   - Information disclosure: bulk exports are PII vacuums; tenant
//     isolation MUST hold across the export path. This package is
//     tenant-agnostic by design — the CALLER threads the tenant context
//     via `tenancy.ApplyTenant` BEFORE producing the row iterator;
//     this package's encoders see only opaque strings. Cross-tenant
//     isolation is a property of the caller's query, not of the encoder.
//
//   - DoS: an unbounded export is the DoS surface. Mitigations live here:
//
//   - `Exporter.WriteRows` consumes a pull-style [iter.Seq] so per-row
//     allocation is bounded; the full result set NEVER materializes.
//
//   - The caller MUST enforce a row cap (default 100,000; per-entity
//     override at registration). This package does NOT enforce the cap;
//     the caller wraps its iterator to count + bail before yielding
//     row N+1. See `internal/api/adminauditlog/export.go` for the
//     reference impl.
//
// # XLSX surface — explicit P0 by construction
//
// The XLSX encoder is a handcrafted single-sheet-text-only writer
// (slice 135 D1 JUDGMENT). It is structurally incapable of emitting
// chart objects, named ranges, embedded VBA, or hidden metadata sheets
// (slice 135 P0-A6). The encoder writes exactly five XML files inside
// the zip: `[Content_Types].xml`, `_rels/.rels`, `xl/workbook.xml`,
// `xl/_rels/workbook.xml.rels`, `xl/worksheets/sheet1.xml`. The
// produced .xlsx opens in Excel, LibreOffice, Numbers, and Google
// Sheets; tests pin the exact zip-member list.
//
// # CSV cell-injection mitigation
//
// The CSV encoder applies the OWASP guidance: every cell whose first
// rune is one of `= + - @ \t \r` is prefixed with a single quote `'`.
// JSON and XLSX are immune by structure — formula injection requires
// CSV-style cell-text interpretation that those formats do not perform.
//
// # Filename construction
//
// [BuildFilename] produces a sanitized filename of the shape
// `<entity>_<YYYYMMDD>_<param-summary>.<ext>`. ASCII alphanum + `-` /
// `_` only; max 80 chars (slice 135 P0-A2). Tenant name is NEVER
// included. CRLF / path-traversal / unicode in any filter value is
// dropped — the result is always safe to put into a
// `Content-Disposition` header without further quoting.
package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"sort"
	"strings"
	"time"
)

// Format is the canonical wire-format string used in the URL query
// parameter (`?format=csv`) and the registry. The three formats are the
// only ones slice 135 ships; PDF is explicitly out of scope
// (slice 135 P0-A11) and lives in the slice 042-area pipeline.
type Format string

const (
	FormatCSV  Format = "csv"
	FormatJSON Format = "json"
	FormatXLSX Format = "xlsx"
)

// AllFormats is the canonical ordered list, useful for handler param
// validation and frontend dropdown population.
var AllFormats = []Format{FormatCSV, FormatJSON, FormatXLSX}

// IsValid reports whether f is one of the three supported formats.
func IsValid(f Format) bool {
	for _, c := range AllFormats {
		if c == f {
			return true
		}
	}
	return false
}

// Exporter is the encoder interface every format implements.
//
// Implementations MUST stream — that is, consume the row iterator
// pull-style and write to `w` as each row arrives, with O(1) auxiliary
// memory per row. Buffering the full result set is forbidden by
// slice 135 P0-A7.
//
// Implementations MUST be safe to call concurrently across calls
// (different writers), but a single Exporter value is NOT required to be
// safe for concurrent use within one call.
type Exporter interface {
	// Format identifies the wire format. Used by the registry + the
	// HTTP layer to set Content-Type + filename extension.
	Format() Format

	// ContentType is the HTTP Content-Type header value the response
	// should carry.
	ContentType() string

	// FileExt is the filename extension (without leading dot). MUST
	// match the Format string.
	FileExt() string

	// WriteRows encodes the header + rows to w. `rows` is a pull-style
	// iterator — implementations MUST call its yield once per row and
	// MUST NOT pre-collect the iterator into a slice (DoS posture per
	// slice 135 P0-A7).
	//
	// The header slice is the column names; subsequent rows MUST have
	// the same length as the header. Cells are opaque strings — the
	// caller stringifies typed values before yielding the row.
	WriteRows(w io.Writer, header []string, rows iter.Seq[[]string]) error

	// WriteRowsWithOpts is the slice-145 variant that accepts encoder
	// options — currently a set of column names whose JSON rendering
	// should emit `null` when the cell value is empty (used by the
	// `?include_payload=false` audit-log redaction path). CSV + XLSX
	// implementations ignore [WriteOpts.NullForEmpty] — they emit the
	// empty cell verbatim, which is the same behavior an external
	// auditor expects from a redacted-handoff workflow.
	//
	// WriteRows MUST behave identically to WriteRowsWithOpts with a
	// zero-valued [WriteOpts] (backwards-compat for slice 135 callers
	// that have not yet adopted the opts variant).
	WriteRowsWithOpts(w io.Writer, header []string, rows iter.Seq[[]string], opts WriteOpts) error
}

// WriteOpts carries per-call encoder options. The zero value is
// equivalent to the slice 135 WriteRows behavior — no nulling, no
// special-casing — so call sites that don't need the options can
// continue to use WriteRows.
//
// Slice 145 introduces [WriteOpts.NullForEmpty] to support the
// `?include_payload=false` audit-log export workflow without
// disturbing the slice 135 wire shape.
type WriteOpts struct {
	// NullForEmpty is the set of header-column names whose JSON
	// rendering MUST emit the literal `null` token when the cell
	// value is the empty string. CSV + XLSX implementations ignore
	// this — they emit an empty cell either way, which is what
	// downstream auditors expect from a redacted CSV / XLSX export.
	//
	// The set is keyed on the exact header string (byte-for-byte
	// equality with one of the entries in `header`). Headers not in
	// the set render normally.
	NullForEmpty map[string]bool
}

// ResolveExporter returns the encoder for the given format, or an error
// if the format is not one of the three canonical values.
func ResolveExporter(f Format) (Exporter, error) {
	switch f {
	case FormatCSV:
		return NewCSVExporter(), nil
	case FormatJSON:
		return NewJSONExporter(), nil
	case FormatXLSX:
		return NewXLSXExporter(), nil
	default:
		return nil, fmt.Errorf("export: unknown format %q (want csv|json|xlsx)", f)
	}
}

// ===== CSV =====

// csvExporter writes RFC 4180 CSV with the OWASP cell-injection
// mitigation applied to every cell.
type csvExporter struct{}

// NewCSVExporter constructs the CSV encoder.
func NewCSVExporter() Exporter { return &csvExporter{} }

func (*csvExporter) Format() Format      { return FormatCSV }
func (*csvExporter) ContentType() string { return "text/csv; charset=utf-8" }
func (*csvExporter) FileExt() string     { return "csv" }

func (e *csvExporter) WriteRows(w io.Writer, header []string, rows iter.Seq[[]string]) error {
	return e.WriteRowsWithOpts(w, header, rows, WriteOpts{})
}

// WriteRowsWithOpts ignores opts — CSV emits empty cells verbatim for
// the slice-145 redaction case, which is the rendering external
// auditors expect ("cell is empty" = "column was redacted on
// handoff"). The slice 135 cell-injection sanitizer is still applied
// to every cell.
func (*csvExporter) WriteRowsWithOpts(w io.Writer, header []string, rows iter.Seq[[]string], _ WriteOpts) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(sanitizeCSVCells(header)); err != nil {
		return fmt.Errorf("csv header: %w", err)
	}
	for row := range rows {
		if len(row) != len(header) {
			return fmt.Errorf("csv: row length %d != header length %d", len(row), len(header))
		}
		if err := cw.Write(sanitizeCSVCells(row)); err != nil {
			return fmt.Errorf("csv row: %w", err)
		}
	}
	cw.Flush()
	return cw.Error()
}

// sanitizeCSVCells returns a copy of cells with the OWASP CSV-injection
// mitigation applied to every cell whose first rune is one of the
// formula-introducer characters. The original slice is not mutated so
// the caller can reuse buffers safely.
//
// Per OWASP "CSV Injection" cheat sheet, the dangerous leading runes
// are: `=`, `+`, `-`, `@`, tab (`\t`), carriage return (`\r`). Any cell
// whose first rune is one of these is prefixed with a single quote `'`
// so Excel / LibreOffice treats it as literal text.
func sanitizeCSVCells(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = sanitizeCSVCell(c)
	}
	return out
}

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

// ===== JSON =====

// jsonExporter writes an array of objects — NOT NDJSON. Object keys
// match the header strings byte-for-byte (slice 135 AC-5).
type jsonExporter struct{}

// NewJSONExporter constructs the JSON encoder.
func NewJSONExporter() Exporter { return &jsonExporter{} }

func (*jsonExporter) Format() Format      { return FormatJSON }
func (*jsonExporter) ContentType() string { return "application/json" }
func (*jsonExporter) FileExt() string     { return "json" }

func (e *jsonExporter) WriteRows(w io.Writer, header []string, rows iter.Seq[[]string]) error {
	return e.WriteRowsWithOpts(w, header, rows, WriteOpts{})
}

// WriteRowsWithOpts honors [WriteOpts.NullForEmpty]: when a column
// name appears in that set AND the row's cell for that column is the
// empty string, the JSON output emits the literal `null` token (not
// the empty string `""`). Slice 145 uses this to render the
// `?include_payload=false` audit-log export's payload_json column as
// `null` rather than `""` — which is the contract downstream
// consumers (jq, scripts) expect for a redacted field.
func (*jsonExporter) WriteRowsWithOpts(w io.Writer, header []string, rows iter.Seq[[]string], opts WriteOpts) error {
	// Streamed assembly: opening bracket, comma between rows, closing
	// bracket. encoding/json's standard Encoder appends a newline per
	// Encode call and writes the full value at once; for streaming we
	// hand-assemble the array envelope and json.Marshal each row map.
	if _, err := io.WriteString(w, "["); err != nil {
		return fmt.Errorf("json open: %w", err)
	}
	first := true
	for row := range rows {
		if len(row) != len(header) {
			return fmt.Errorf("json: row length %d != header length %d", len(row), len(header))
		}
		obj := make(map[string]string, len(header))
		for i, k := range header {
			obj[k] = row[i]
		}
		blob, err := jsonMarshalOrderedWithOpts(header, obj, opts)
		if err != nil {
			return fmt.Errorf("json row marshal: %w", err)
		}
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return fmt.Errorf("json comma: %w", err)
			}
		}
		first = false
		if _, err := w.Write(blob); err != nil {
			return fmt.Errorf("json write row: %w", err)
		}
	}
	if _, err := io.WriteString(w, "]"); err != nil {
		return fmt.Errorf("json close: %w", err)
	}
	return nil
}

// jsonMarshalOrdered marshals an object preserving header column order.
// The stdlib `map[string]string` would sort keys alphabetically; the
// slice 135 contract requires keys in header declaration order so the
// JSON output mirrors the CSV column order downstream consumers expect.
//
// Deprecated: use jsonMarshalOrderedWithOpts. Retained for symmetry
// with the slice 135 export.go surface; callers in this package now
// route through the opts variant exclusively.
func jsonMarshalOrdered(header []string, obj map[string]string) ([]byte, error) {
	return jsonMarshalOrderedWithOpts(header, obj, WriteOpts{})
}

// jsonMarshalOrderedWithOpts is the slice 145 variant. It marshals
// keys in header order (slice 135 contract) and renders nullable-set
// columns as the JSON `null` token when their cell value is the empty
// string (slice 145 AC-2).
func jsonMarshalOrderedWithOpts(header []string, obj map[string]string, opts WriteOpts) ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range header {
		if i > 0 {
			b.WriteByte(',')
		}
		// json.Marshal handles the string-escape contract for both
		// keys and values (control chars, quotes, backslashes,
		// non-ASCII via UTF-8).
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')

		v := obj[k]
		if v == "" && opts.NullForEmpty[k] {
			// Slice 145: redacted column renders as the literal
			// `null` token, not the empty string. Downstream
			// consumers (jq, scripts, importers) treat `null` as
			// "field absent" while `""` is "field present but
			// blank" — meaningfully different for the
			// `?include_payload=false` redaction workflow.
			b.WriteString("null")
			continue
		}
		vb, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// ===== Filename builder =====

// BuildFilename produces a sanitized filename of the shape
// `<entity>_<YYYYMMDD>_<param-summary>.<ext>` suitable for inclusion in
// a `Content-Disposition: attachment; filename="<...>"` header without
// further quoting.
//
// Slice 135 P0-A2 rules enforced here:
//
//   - ASCII alphanum + `-` + `_` only. Every other rune in entity,
//     params, or the implicit timestamp is dropped silently. CRLF,
//     path-traversal sequences (`../`), null bytes, and unicode are
//     therefore impossible to smuggle in.
//   - Max 80 characters total (including the extension). Filenames that
//     would exceed the cap are truncated at the param-summary segment
//     so the entity + timestamp always survive.
//   - Tenant name / tenant id is NEVER injected. The caller MUST NOT
//     pass tenant identifiers through params; this function does NOT
//     check that — the API-layer wrapper does.
//   - The "now" timestamp is taken at call time. Callers that need a
//     deterministic timestamp (e.g. tests) MUST pre-compute it and pass
//     it via params as `date` (which will be sanitized + included in
//     the param summary). The implicit timestamp is the request-time
//     UTC date — stable to second.
//
// param-summary is built by sorting the params map by key, joining
// `<key>-<value>` pairs with `_`, and sanitizing the result. Empty
// values are skipped so callers can pass nil / empty values without
// surfacing `=` in the filename.
func BuildFilename(entity string, ext string, params map[string]string) string {
	const maxLen = 80

	entity = sanitizeFilenameSegment(entity)
	ext = sanitizeFilenameSegment(ext)
	if entity == "" {
		entity = "export"
	}
	if ext == "" {
		ext = "bin"
	}

	date := time.Now().UTC().Format("20060102")

	// Build the param summary in sorted-key order so the same filter
	// set produces the same filename every time.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		v := params[k]
		if v == "" {
			continue
		}
		ks := sanitizeFilenameSegment(k)
		vs := sanitizeFilenameSegment(v)
		if ks == "" || vs == "" {
			continue
		}
		parts = append(parts, ks+"-"+vs)
	}
	summary := strings.Join(parts, "_")

	base := entity + "_" + date
	if summary != "" {
		base = base + "_" + summary
	}

	// Apply length cap. The extension always survives; the
	// param-summary is the first thing truncated.
	full := base + "." + ext
	if len(full) <= maxLen {
		return full
	}
	// Reserve room for "." + ext.
	allowed := maxLen - 1 - len(ext)
	if allowed < len(entity)+1+len(date) {
		// Pathological: entity+date already too long. Fall back to
		// just entity + date trimmed to fit.
		seed := entity + "_" + date
		if len(seed) > allowed {
			seed = seed[:allowed]
		}
		return seed + "." + ext
	}
	base = base[:allowed]
	// Avoid trailing separator from truncation.
	base = strings.TrimRight(base, "_-")
	return base + "." + ext
}

// sanitizeFilenameSegment drops every rune that is not ASCII alphanum,
// `-`, or `_`. The result MAY be empty; callers handle the empty case
// (e.g. BuildFilename substitutes a default for empty entity / ext).
func sanitizeFilenameSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			// drop
		}
	}
	return b.String()
}
