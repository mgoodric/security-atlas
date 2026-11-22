package riskimport

// The mapping step's preview (#2848): the file's header row, the field each
// header maps to through an alias or the column map, the fields the batch
// methodology reads, and the first data rows, so the web can show and change
// the mapping before anything is staged. It reads the file with the same
// caps and the same header matcher as Parse and stores nothing.

import (
	"strings"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// PreviewRows is how many data rows a preview carries.
const PreviewRows = 5

// PreviewColumn is one header cell and the field it maps to, "" when none.
// A column map names a column by its header text, so a column with a blank
// header (shown as "column N") or a header repeated from an earlier column
// is not Mappable: it can only stay as it is.
type PreviewColumn struct {
	Index    int    `json:"index"`
	Header   string `json:"header"`
	Field    string `json:"field,omitempty"`
	Mappable bool   `json:"mappable"`
}

// PreviewField is one field a column can map to under the methodology.
type PreviewField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// Preview is the mapping step's view of an uploaded file.
type Preview struct {
	Methodology dbx.RiskMethodology `json:"methodology"`
	// HeaderRow is the 1-based row the headers were read from.
	// HeaderDetected is false when no row in the search window names both
	// a title and a category column: the row with the most filled cells is
	// offered instead, for the user to map by hand.
	HeaderRow      int             `json:"header_row"`
	HeaderDetected bool            `json:"header_detected"`
	Columns        []PreviewColumn `json:"columns"`
	Fields         []PreviewField  `json:"fields"`
	// Rows are the first PreviewRows non-blank data rows, one cell per
	// column; DataRows counts every non-blank data row.
	Rows     [][]string `json:"rows"`
	DataRows int        `json:"data_rows"`
}

// PreviewFile reads an upload and reports its header mapping, without
// staging it. Its errors are Parse's.
func PreviewFile(filename string, raw []byte, opts ParseOptions) (*Preview, error) {
	var (
		rows [][]string
		err  error
	)
	switch FormatOf(filename) {
	case "csv":
		rows, err = readCSVRows(raw)
	case "xlsx":
		rows, err = readXLSXRows(raw)
	default:
		return nil, ErrUnsupportedFormat
	}
	if err != nil {
		return nil, err
	}
	return previewRows(rows, opts)
}

func previewRows(rows [][]string, opts ParseOptions) (*Preview, error) {
	methodology, err := ResolveMethodology(string(opts.Methodology))
	if err != nil {
		return nil, err
	}
	opts.Methodology = methodology
	match, err := newHeaderMatcher(opts)
	if err != nil {
		return nil, err
	}
	headerIdx, headers := findHeaderRow(rows, match)
	detected := headerIdx >= 0
	if !detected {
		headerIdx = fullestRow(rows)
		if headerIdx < 0 {
			return nil, ErrEmptyFile
		}
		headers = rows[headerIdx]
	}
	data := rows[headerIdx+1:]
	cols := mapColumns(headers, match, data)
	out := &Preview{
		Methodology:    methodology,
		HeaderRow:      headerIdx + 1,
		HeaderDetected: detected,
		Columns:        make([]PreviewColumn, 0, len(headers)),
		Fields:         previewFields(methodology),
		Rows:           make([][]string, 0, PreviewRows),
	}
	seen := map[string]bool{}
	for i, h := range headers {
		c := cols[i]
		if c.field == "" && c.key == "" {
			// A blank header over an empty column: nothing to map.
			continue
		}
		norm := normalizeHeader(h)
		col := PreviewColumn{Index: i, Header: strings.TrimSpace(h), Field: c.field, Mappable: norm != "" && !seen[norm]}
		if norm == "" {
			col.Header = c.key
		}
		seen[norm] = true
		out.Columns = append(out.Columns, col)
	}
	for _, row := range data {
		if isBlankRow(row) {
			continue
		}
		out.DataRows++
		if len(out.Rows) == PreviewRows {
			continue
		}
		cells := make([]string, len(out.Columns))
		for j, c := range out.Columns {
			if c.Index < len(row) {
				cells[j] = row[c.Index]
			}
		}
		out.Rows = append(out.Rows, cells)
	}
	return out, nil
}

// fullestRow is the row of the header search window with the most filled
// cells, the first of any tie; -1 when every row is blank.
func fullestRow(rows [][]string) int {
	best, bestCount := -1, 0
	for i := 0; i < min(headerSearchRows, len(rows)); i++ {
		n := 0
		for _, cell := range rows[i] {
			if strings.TrimSpace(cell) != "" {
				n++
			}
		}
		if n > bestCount {
			best, bestCount = i, n
		}
	}
	return best
}

// previewFields lists every field in FieldNames order that methodology
// reads; title and category are required.
func previewFields(methodology dbx.RiskMethodology) []PreviewField {
	names := FieldNames()
	out := make([]PreviewField, 0, len(names))
	for _, n := range names {
		if !scoreFieldApplies(n, methodology) {
			continue
		}
		out = append(out, PreviewField{Name: n, Required: n == FieldTitle || n == FieldCategory})
	}
	return out
}
