// Package controlimport implements the bring-your-own-control-set import
// backend: CSV/XLSX parsing plus the AI-assisted SCF matching orchestration.
//
// It deliberately stops at staged rows + proposals. A canonical mapping is
// written only through Approve, which requires a human approver and delegates
// the actual persistence to the caller-supplied MappingStore.
package controlimport

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/xuri/excelize/v2"
)

const (
	MaxUploadBytes = 5 * 1024 * 1024
	MaxRows        = 5000
)

var (
	ErrUploadTooLarge = errors.New("controlimport: upload exceeds size cap")
	ErrEmptyFile      = errors.New("controlimport: empty control file")
	ErrNoHeaderRow    = errors.New("controlimport: no recognizable header row")
	ErrTooManyRows    = errors.New("controlimport: control file exceeds row cap")
)

// StagedControl is one operator-supplied control row ready for AI matching.
// ExternalID, Title, and Description are the minimum ingest contract.
type StagedControl struct {
	RowNumber    int    `json:"row_number"`
	ExternalID   string `json:"external_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	SourceFormat string `json:"source_format"`
}

type ParseResult struct {
	Controls        []StagedControl `json:"controls"`
	UnmappedColumns []string        `json:"unmapped_columns"`
}

var fieldAliases = map[string][]string{
	"id":          {"id", "control id", "control_id", "controlid", "code", "control code", "reference", "ref"},
	"title":       {"title", "control title", "name", "control name"},
	"description": {"description", "control description", "control text", "text", "statement", "requirement"},
}

// ParseCSV parses an operator control CSV export into staged rows.
func ParseCSV(raw []byte) (*ParseResult, error) {
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
	return parseRows(rows, "csv")
}

// ParseXLSX parses the first sheet of an operator control workbook.
func ParseXLSX(raw []byte) (*ParseResult, error) {
	if len(raw) == 0 {
		return nil, ErrEmptyFile
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	f, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("controlimport: open xlsx: %w", err)
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, ErrEmptyFile
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("controlimport: read xlsx rows: %w", err)
	}
	return parseRows(rows, "xlsx")
}

func parseRows(rows [][]string, format string) (*ParseResult, error) {
	if len(rows) == 0 {
		return nil, ErrEmptyFile
	}
	if len(rows) > MaxRows {
		return nil, ErrTooManyRows
	}
	headerIdx, headers := findHeaderRow(rows)
	if headerIdx < 0 {
		return nil, ErrNoHeaderRow
	}
	colMap := make(map[int]string, len(headers))
	unmapped := make([]string, 0)
	for i, h := range headers {
		canon := matchAlias(h)
		if canon == "" {
			if strings.TrimSpace(h) != "" {
				unmapped = append(unmapped, strings.TrimSpace(h))
			}
			continue
		}
		colMap[i] = canon
	}
	if !hasField(colMap, "id") || !hasField(colMap, "title") || !hasField(colMap, "description") {
		return nil, ErrNoHeaderRow
	}

	out := make([]StagedControl, 0, len(rows)-headerIdx-1)
	for idx, row := range rows[headerIdx+1:] {
		if isBlankRow(row) {
			continue
		}
		c := StagedControl{RowNumber: headerIdx + idx + 2, SourceFormat: format}
		for colIdx, canon := range colMap {
			if colIdx >= len(row) {
				continue
			}
			val := strings.TrimSpace(row[colIdx])
			switch canon {
			case "id":
				c.ExternalID = val
			case "title":
				c.Title = val
			case "description":
				c.Description = val
			}
		}
		if c.ExternalID == "" || c.Title == "" || c.Description == "" {
			continue
		}
		out = append(out, c)
	}
	return &ParseResult{Controls: out, UnmappedColumns: unmapped}, nil
}

func findHeaderRow(rows [][]string) (int, []string) {
	limit := 5
	if len(rows) < limit {
		limit = len(rows)
	}
	for i := 0; i < limit; i++ {
		matches := 0
		for _, cell := range rows[i] {
			if matchAlias(cell) != "" {
				matches++
			}
		}
		if matches >= 2 {
			return i, rows[i]
		}
	}
	return -1, nil
}

func matchAlias(cell string) string {
	norm := normalizeHeader(cell)
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
