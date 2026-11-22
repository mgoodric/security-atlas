package controlimport

// The mapping step's preview (#2836): the file's header row, the field each
// header maps to through an alias or the column map, the fields a control
// row reads, and the first data rows, so the web can show and change the
// mapping before anything is staged. It reads the file with the same caps
// and the same header matcher as Parse and stores nothing. Its shape is the
// risk import's preview (riskimport.Preview, #2848), so the web's shared
// mapping step reads either.

import (
	"fmt"
	"strings"
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

// PreviewField is one field a column can map to.
type PreviewField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// Preview is the mapping step's view of an uploaded file.
type Preview struct {
	// HeaderRow is the 1-based row the headers were read from.
	// HeaderDetected is false when no row in the search window has two
	// columns that map to a field: the row with the most filled cells is
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

// previewFieldOrder is the order the mapping step lists the fields in: the
// three a row needs, then the optional ones.
var previewFieldOrder = []string{
	FieldID, FieldTitle, FieldDescription,
	FieldTestedUnder, FieldImplementationType, FieldApplicability,
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
	detected := headerIdx >= 0
	if !detected {
		headerIdx = fullestRow(rows)
		if headerIdx < 0 {
			return nil, ErrEmptyFile
		}
		headers = rows[headerIdx]
	}
	data := rows[headerIdx+1:]
	out := &Preview{
		HeaderRow:      headerIdx + 1,
		HeaderDetected: detected,
		Columns:        make([]PreviewColumn, 0, len(headers)),
		Fields:         make([]PreviewField, 0, len(previewFieldOrder)),
		Rows:           make([][]string, 0, PreviewRows),
	}
	for _, n := range previewFieldOrder {
		out.Fields = append(out.Fields, PreviewField{Name: n, Required: n == FieldID || n == FieldTitle || n == FieldDescription})
	}
	seen := map[string]bool{}
	taken := map[string]bool{}
	for i, h := range headers {
		norm := normalizeHeader(h)
		if norm == "" && !columnHasValue(data, i) {
			// A blank header over an empty column: nothing to map.
			continue
		}
		col := PreviewColumn{Index: i, Header: strings.TrimSpace(h), Mappable: norm != "" && !seen[norm]}
		if norm == "" {
			col.Header = fmt.Sprintf("column %d", i+1)
		}
		// The first column a field matches takes it, as Parse reads it.
		if field := match(h); field != "" && !taken[field] {
			taken[field] = true
			col.Field = field
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

func columnHasValue(data [][]string, i int) bool {
	for _, row := range data {
		if i < len(row) && strings.TrimSpace(row[i]) != "" {
			return true
		}
	}
	return false
}
