// Package controlimport implements the bring-your-own-control-set import
// backend: CSV/XLSX parsing, batch staging, plus the AI-assisted SCF matching
// orchestration.
//
// It deliberately stops at staged rows + proposals. A canonical mapping is
// written only through Approve, which requires a human approver and delegates
// the actual persistence to the caller-supplied MappingStore.
//
// Parsing accounts for every data row: each one is either a StagedControl or a
// RejectedRow carrying a reason. Nothing is silently dropped (#1779).
package controlimport

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

const (
	MaxUploadBytes = 5 * 1024 * 1024
	MaxRows        = 5000
	// MaxUnzipBytes caps an XLSX's total uncompressed size, and
	// MaxUnzipXMLBytes the part of it excelize holds in memory per sheet.
	MaxUnzipBytes    = 64 << 20
	MaxUnzipXMLBytes = 16 << 20
	// MaxColumns caps the cells kept per row; a control sheet has a handful.
	MaxColumns = 200
)

var (
	ErrUploadTooLarge     = errors.New("controlimport: upload exceeds size cap")
	ErrWorkbookTooLarge   = errors.New("controlimport: workbook exceeds uncompressed size cap")
	ErrEmptyFile          = errors.New("controlimport: empty control file")
	ErrNoHeaderRow        = errors.New("controlimport: no recognizable header row")
	ErrTooManyRows        = errors.New("controlimport: control file exceeds row cap")
	ErrTooManyColumns     = errors.New("controlimport: control file row exceeds column cap")
	ErrUnsupportedFormat  = errors.New("controlimport: file must be .csv or .xlsx")
	ErrInvalidColumnMap   = errors.New("controlimport: invalid column map")
	ErrMappedColumnAbsent = errors.New("controlimport: column map names a header that is not in the file")
)

// Canonical field names. These are also the keys of an explicit column map.
const (
	FieldID                 = "id"
	FieldTitle              = "title"
	FieldDescription        = "description"
	FieldTestedUnder        = "tested_under"
	FieldImplementationType = "implementation_type"
	FieldApplicability      = "applicability"
)

// Rejection reasons recorded on a RejectedRow.
const (
	RejectMissingField              = "missing_required_field"
	RejectUnknownTestedUnder        = "unknown_tested_under"
	RejectDuplicateID               = "duplicate_id"
	RejectInvalidImplementationType = "invalid_implementation_type"
)

// StagedControl is one operator-supplied control row ready for SCF matching.
// ExternalID, Title, and Description are the minimum ingest contract;
// TestedUnder, ImplementationType and Applicability are optional.
type StagedControl struct {
	RowNumber          int      `json:"row_number"`
	ExternalID         string   `json:"external_id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	TestedUnder        []string `json:"tested_under,omitempty"`
	ImplementationType string   `json:"implementation_type,omitempty"`
	Applicability      string   `json:"applicability,omitempty"`
	SourceFormat       string   `json:"source_format"`
}

// RejectedRow is a data row that could not be staged, with the reason.
type RejectedRow struct {
	RowNumber  int    `json:"row_number"`
	ExternalID string `json:"external_id,omitempty"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
}

type ParseResult struct {
	Controls        []StagedControl `json:"controls"`
	Rejected        []RejectedRow   `json:"rejected"`
	UnmappedColumns []string        `json:"unmapped_columns"`
}

// ParseOptions tunes header detection. ColumnMap maps a canonical field name
// (FieldID, FieldTitle, ...) to the header text used in the file; a mapped
// field overrides alias detection for that field.
type ParseOptions struct {
	ColumnMap map[string]string
}

var fieldAliases = map[string][]string{
	FieldID:          {"id", "control id", "control_id", "controlid", "code", "control code", "reference", "ref"},
	FieldTitle:       {"title", "control title", "name", "control name"},
	FieldDescription: {"description", "control description", "control text", "text", "statement", "requirement"},
	FieldTestedUnder: {
		"tested under", "tested against", "criteria", "criterion",
		"audit criteria", "tested criteria", "framework criteria", "trust services criteria",
		"tsc", "mapped criteria", "framework mapping", "mappings",
	},
	FieldImplementationType: {"implementation type", "implementation", "control type", "type", "automation"},
	FieldApplicability:      {"applicability", "applicability expr", "applicability expression", "applies to", "scope"},
}

// allowedImplementationTypes mirrors internal/control's manifest enum.
var allowedImplementationTypes = map[string]bool{
	"automated":       true,
	"semi_automated":  true,
	"manual_attested": true,
	"manual_periodic": true,
}

// ParseCSV parses an operator control CSV export into staged rows.
func ParseCSV(raw []byte) (*ParseResult, error) { return ParseCSVWith(raw, ParseOptions{}) }

// ParseXLSX parses the first sheet of an operator control workbook.
func ParseXLSX(raw []byte) (*ParseResult, error) { return ParseXLSXWith(raw, ParseOptions{}) }

// Parse dispatches on the filename's extension (.csv or .xlsx).
func Parse(filename string, raw []byte, opts ParseOptions) (*ParseResult, error) {
	switch FormatOf(filename) {
	case "csv":
		return ParseCSVWith(raw, opts)
	case "xlsx":
		return ParseXLSXWith(raw, opts)
	default:
		return nil, ErrUnsupportedFormat
	}
}

// FormatOf returns "csv", "xlsx", or "" for an unsupported filename.
func FormatOf(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".csv":
		return "csv"
	case ".xlsx":
		return "xlsx"
	default:
		return ""
	}
}

// ParseCSVWith parses a CSV export with explicit options.
func ParseCSVWith(raw []byte, opts ParseOptions) (*ParseResult, error) {
	if len(raw) == 0 {
		return nil, ErrEmptyFile
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("controlimport: read csv: %w", err)
	}
	return parseRows(rows, "csv", opts)
}

// ParseXLSXWith parses the first sheet of a workbook with explicit options.
//
// An XLSX is a zip, so a 5 MiB upload can inflate to gigabytes. The workbook
// is refused before excelize opens it when its entries declare more than
// MaxUnzipBytes in total (archive/zip refuses an entry that inflates past its
// declared size, so the declaration is binding). excelize gets the same
// limits as a second layer, and rows are streamed and counted so the row cap
// stops the read instead of following a full load.
func ParseXLSXWith(raw []byte, opts ParseOptions) (*ParseResult, error) {
	if len(raw) == 0 {
		return nil, ErrEmptyFile
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	if err := checkUnzipSize(raw); err != nil {
		return nil, err
	}
	f, err := excelize.OpenReader(bytes.NewReader(raw), excelize.Options{
		UnzipSizeLimit:    MaxUnzipBytes,
		UnzipXMLSizeLimit: MaxUnzipXMLBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("controlimport: open xlsx: %w", err)
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, ErrEmptyFile
	}
	rows, err := readSheetRows(f, sheets[0])
	if err != nil {
		return nil, err
	}
	return parseRows(rows, "xlsx", opts)
}

// checkUnzipSize refuses a workbook whose zip entries declare more than
// MaxUnzipBytes uncompressed in total.
func checkUnzipSize(raw []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return fmt.Errorf("controlimport: open xlsx: %w", err)
	}
	// Compare before adding: a zip64 entry declaring close to 2^64 bytes
	// would wrap a plain running sum back under the cap.
	var total uint64
	for _, zf := range zr.File {
		if zf.UncompressedSize64 > MaxUnzipBytes-total {
			return ErrWorkbookTooLarge
		}
		total += zf.UncompressedSize64
	}
	return nil
}

// readSheetRows streams a sheet's rows, stopping with ErrTooManyRows at the
// first row past MaxRows.
func readSheetRows(f *excelize.File, sheet string) ([][]string, error) {
	it, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("controlimport: read xlsx rows: %w", err)
	}
	defer func() { _ = it.Close() }()
	var rows [][]string
	for it.Next() {
		if len(rows) == MaxRows {
			return nil, ErrTooManyRows
		}
		cols, err := it.Columns()
		if err != nil {
			return nil, fmt.Errorf("controlimport: read xlsx row: %w", err)
		}
		// Columns() pads a row out to its highest cell index, so one cell
		// at XFD yields 16,384 entries. Keep only up to the last non-blank
		// cell, copied so the padded array is not retained.
		n := len(cols)
		for n > 0 && strings.TrimSpace(cols[n-1]) == "" {
			n--
		}
		if n > MaxColumns {
			return nil, ErrTooManyColumns
		}
		rows = append(rows, append([]string(nil), cols[:n]...))
	}
	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("controlimport: read xlsx rows: %w", err)
	}
	return rows, nil
}

// FieldNames lists the canonical field names in a stable order.
func FieldNames() []string {
	out := make([]string, 0, len(fieldAliases))
	for k := range fieldAliases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// headerMatcher resolves one header cell to a canonical field, or "".
type headerMatcher func(cell string) string

func newHeaderMatcher(opts ParseOptions) (headerMatcher, error) {
	explicit := make(map[string]string, len(opts.ColumnMap)) // normalized header -> field
	mappedFields := make(map[string]bool, len(opts.ColumnMap))
	for field, header := range opts.ColumnMap {
		if _, ok := fieldAliases[field]; !ok {
			return nil, fmt.Errorf("%w: unknown field %q (want one of %s)", ErrInvalidColumnMap, field, strings.Join(FieldNames(), ", "))
		}
		norm := normalizeHeader(header)
		if norm == "" {
			return nil, fmt.Errorf("%w: field %q maps to an empty header", ErrInvalidColumnMap, field)
		}
		explicit[norm] = field
		mappedFields[field] = true
	}
	return func(cell string) string {
		norm := normalizeHeader(cell)
		if norm == "" {
			return ""
		}
		if field, ok := explicit[norm]; ok {
			return field
		}
		field := matchAlias(norm)
		if mappedFields[field] {
			// An explicit mapping for this field wins over alias detection.
			return ""
		}
		return field
	}, nil
}

func parseRows(rows [][]string, format string, opts ParseOptions) (*ParseResult, error) {
	if len(rows) == 0 {
		return nil, ErrEmptyFile
	}
	if len(rows) > MaxRows {
		return nil, ErrTooManyRows
	}
	match, err := newHeaderMatcher(opts)
	if err != nil {
		return nil, err
	}
	headerIdx, headers := findHeaderRow(rows, match)
	if headerIdx < 0 {
		return nil, ErrNoHeaderRow
	}
	colMap := make(map[int]string, len(headers))
	unmapped := make([]string, 0)
	for i, h := range headers {
		canon := match(h)
		if canon == "" || hasField(colMap, canon) {
			if strings.TrimSpace(h) != "" {
				unmapped = append(unmapped, strings.TrimSpace(h))
			}
			continue
		}
		colMap[i] = canon
	}
	for _, field := range FieldNames() {
		if header, ok := opts.ColumnMap[field]; ok && !hasField(colMap, field) {
			return nil, fmt.Errorf("%w: %s=%q", ErrMappedColumnAbsent, field, header)
		}
	}
	if !hasField(colMap, FieldID) || !hasField(colMap, FieldTitle) || !hasField(colMap, FieldDescription) {
		return nil, ErrNoHeaderRow
	}

	out := &ParseResult{
		Controls:        make([]StagedControl, 0, len(rows)-headerIdx-1),
		Rejected:        make([]RejectedRow, 0),
		UnmappedColumns: unmapped,
	}
	firstRowForID := make(map[string]int)
	for idx, row := range rows[headerIdx+1:] {
		if isBlankRow(row) {
			continue
		}
		c := rowToControl(row, colMap, headerIdx+idx+2, format)
		if rej, ok := rejectRow(&c, firstRowForID); ok {
			out.Rejected = append(out.Rejected, rej)
			continue
		}
		out.Controls = append(out.Controls, c)
	}
	return out, nil
}

func rowToControl(row []string, colMap map[int]string, rowNumber int, format string) StagedControl {
	c := StagedControl{RowNumber: rowNumber, SourceFormat: format}
	for colIdx, canon := range colMap {
		if colIdx >= len(row) {
			continue
		}
		val := strings.TrimSpace(row[colIdx])
		switch canon {
		case FieldID:
			c.ExternalID = val
		case FieldTitle:
			c.Title = val
		case FieldDescription:
			c.Description = val
		case FieldTestedUnder:
			c.TestedUnder = SplitTestedUnder(val)
		case FieldImplementationType:
			c.ImplementationType = val
		case FieldApplicability:
			c.Applicability = val
		}
	}
	return c
}

// rejectRow applies the file-level row checks. It normalizes the row's
// implementation type in place and records its ID for duplicate detection
// when the row passes.
func rejectRow(c *StagedControl, firstRowForID map[string]int) (RejectedRow, bool) {
	reject := func(reason, detail string) (RejectedRow, bool) {
		return RejectedRow{RowNumber: c.RowNumber, ExternalID: c.ExternalID, Reason: reason, Detail: detail}, true
	}
	if missing := missingFields(*c); len(missing) > 0 {
		return reject(RejectMissingField, "missing "+strings.Join(missing, ", "))
	}
	key := strings.ToLower(c.ExternalID)
	if first, dup := firstRowForID[key]; dup {
		return reject(RejectDuplicateID, fmt.Sprintf("control ID %q already appears on row %d", c.ExternalID, first))
	}
	if c.ImplementationType != "" {
		norm := normalizeImplementationType(c.ImplementationType)
		if !allowedImplementationTypes[norm] {
			return reject(RejectInvalidImplementationType, fmt.Sprintf(
				"implementation type %q is not one of automated, semi_automated, manual_attested, manual_periodic", c.ImplementationType))
		}
		c.ImplementationType = norm
	}
	firstRowForID[key] = c.RowNumber
	return RejectedRow{}, false
}

func missingFields(c StagedControl) []string {
	var missing []string
	if c.ExternalID == "" {
		missing = append(missing, FieldID)
	}
	if c.Title == "" {
		missing = append(missing, FieldTitle)
	}
	if c.Description == "" {
		missing = append(missing, FieldDescription)
	}
	return missing
}

func normalizeImplementationType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("-", "_", " ", "_").Replace(s)
}

// SplitTestedUnder splits a tested-under cell into criterion codes the same
// way the SCF workbook importer splits crosswalk cells
// (internal/api/soc2import splitWorkbookCodes): newline, semicolon or comma
// separated, bullets trimmed, "N/A" and repeats dropped.
func SplitTestedUnder(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ';' || r == ','
	})
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "•"))
		if p == "" || strings.EqualFold(p, "N/A") {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func findHeaderRow(rows [][]string, match headerMatcher) (int, []string) {
	limit := 5
	if len(rows) < limit {
		limit = len(rows)
	}
	for i := 0; i < limit; i++ {
		matches := 0
		for _, cell := range rows[i] {
			if match(cell) != "" {
				matches++
			}
		}
		if matches >= 2 {
			return i, rows[i]
		}
	}
	return -1, nil
}

// matchAlias resolves an already-normalized header to a canonical field.
func matchAlias(norm string) string {
	if norm == "" {
		return ""
	}
	for canon, aliases := range fieldAliases {
		for _, alias := range aliases {
			if norm == normalizeHeader(alias) {
				return canon
			}
		}
	}
	return ""
}

func normalizeHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	return strings.Join(strings.Fields(s), " ")
}

func hasField(colMap map[int]string, field string) bool {
	for _, got := range colMap {
		if got == field {
			return true
		}
	}
	return false
}

func isBlankRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// ReadAllBounded is a helper for HTTP handlers: it enforces MaxUploadBytes
// while reading a multipart file before format-specific parsing.
func ReadAllBounded(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.CopyN(&buf, r, MaxUploadBytes+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("controlimport: read upload: %w", err)
	}
	if buf.Len() > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	return buf.Bytes(), nil
}
