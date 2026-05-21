// Package questionnaire implements slice 155's tracer-bullet:
//   - Excel-only parsing of inbound vendor security questionnaires
//   - AnswerLibrary lookup (SCF-anchor keyed)
//   - PDF export of a populated questionnaire
//
// Scope discipline: this package implements ONLY the tracer-bullet shape
// locked by the maintainer on 2026-05-20. CSV/JSON/Word parsing,
// HECVAT-bundled templates, vendor-portal flows, and AI-assisted drafting
// are all DEFERRED as spillover slices. Do not add formats here.
package questionnaire

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// MaxUploadBytes caps the inbound Excel size BEFORE parsing. 5 MB is
// generously larger than the canonical CAIQ v4.1 (283 questions ~ 80 KB)
// or HECVAT 4.1.5 (321 questions ~ 110 KB) but small enough that a
// pathological zip-bomb file blows the cap before excelize touches it.
const MaxUploadBytes = 5 * 1024 * 1024

// MaxRows is the per-sheet row ceiling. Largest known canonical
// questionnaire is SIG Core (~855 rows). 5000 leaves headroom for custom
// vendor questionnaires while denying obvious DoS shapes.
const MaxRows = 5000

// ErrUploadTooLarge is returned when the inbound xlsx exceeds MaxUploadBytes.
var ErrUploadTooLarge = errors.New("questionnaire: excel upload exceeds size cap")

// ErrTooManyRows is returned when the parsed sheet exceeds MaxRows.
var ErrTooManyRows = errors.New("questionnaire: excel exceeds row cap")

// ErrEmptyWorkbook is returned when the file parses but has no sheets.
var ErrEmptyWorkbook = errors.New("questionnaire: excel has no sheets")

// ErrNoHeaderRow is returned when the parser cannot find a header row
// matching the recognized field set. The operator can still upload via
// an explicit column-mapping override (future spillover slice); for
// the tracer-bullet ship, the heuristic is the only path.
var ErrNoHeaderRow = errors.New("questionnaire: no recognizable header row")

// ParsedQuestion is one row from the Excel input ready for insertion
// as a `questionnaire_questions` row. ScfAnchorID is empty when the
// parser could not infer an anchor — the operator resolves the mapping
// via PATCH after upload (decision D5; never reject the upload).
type ParsedQuestion struct {
	Code        string
	Text        string
	Domain      string
	AnswerType  string
	ScfAnchorID string // empty when needs_mapping
}

// ParseResult is the structured return of ParseExcel.
type ParseResult struct {
	Questions       []ParsedQuestion
	UnmappedColumns []string // source-file columns we ignored
}

// fieldAliases maps the canonical field name we store to the alternate
// header strings vendors actually use in the wild. Lookup is
// case-insensitive — we lowercase both sides before comparing.
var fieldAliases = map[string][]string{
	"code":        {"code", "question id", "question_id", "questionid", "id", "control id", "control_id"},
	"text":        {"text", "question", "question text", "question_text", "description"},
	"domain":      {"domain", "category", "section", "control domain", "ccm domain"},
	"answer_type": {"answer type", "answer_type", "response type", "response_type", "expected answer", "answer"},
}

// ParseExcel reads an xlsx byte stream into a ParseResult. Caller is
// responsible for size-capping before invocation (the function still
// double-checks via MaxUploadBytes for defense-in-depth).
//
// Security posture (see docs/audit-log/155-questionnaire-tracer-decisions.md):
//   - Size cap enforced as the very first action.
//   - Formulas are read as their RAW STRING via excelize, never evaluated;
//     we treat formula cells as opaque text. This neutralizes the
//     spreadsheet-formula-injection class.
//   - Row count is capped at MaxRows before we allocate the result slice.
//   - Only the first sheet is read — additional sheets are ignored.
func ParseExcel(raw []byte) (*ParseResult, error) {
	if len(raw) == 0 {
		return nil, ErrEmptyWorkbook
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}

	f, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("questionnaire: open xlsx: %w", err)
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, ErrEmptyWorkbook
	}
	sheet := sheets[0]
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("questionnaire: read sheet rows: %w", err)
	}
	if len(rows) > MaxRows {
		return nil, ErrTooManyRows
	}
	if len(rows) < 2 {
		return nil, ErrNoHeaderRow
	}

	headerIdx, headers := findHeaderRow(rows)
	if headerIdx < 0 {
		return nil, ErrNoHeaderRow
	}

	// Map header column -> canonical field name. Unrecognized columns
	// are surfaced to the caller as UnmappedColumns (the operator can
	// see them in the import-review UI).
	colMap := make(map[int]string, len(headers))
	unmapped := make([]string, 0)
	for i, h := range headers {
		canon := matchAlias(h)
		if canon == "" {
			if strings.TrimSpace(h) != "" {
				unmapped = append(unmapped, h)
			}
			continue
		}
		colMap[i] = canon
	}

	// Require at minimum code + text.
	hasCode, hasText := false, false
	for _, canon := range colMap {
		if canon == "code" {
			hasCode = true
		}
		if canon == "text" {
			hasText = true
		}
	}
	if !hasCode || !hasText {
		return nil, ErrNoHeaderRow
	}

	out := make([]ParsedQuestion, 0, len(rows)-headerIdx-1)
	for _, row := range rows[headerIdx+1:] {
		if isBlankRow(row) {
			continue
		}
		q := ParsedQuestion{}
		for colIdx, canon := range colMap {
			if colIdx >= len(row) {
				continue
			}
			val := strings.TrimSpace(row[colIdx])
			switch canon {
			case "code":
				q.Code = val
			case "text":
				q.Text = val
			case "domain":
				q.Domain = val
			case "answer_type":
				q.AnswerType = val
			}
		}
		if q.Code == "" || q.Text == "" {
			continue // skip rows missing the load-bearing fields
		}
		out = append(out, q)
	}

	return &ParseResult{
		Questions:       out,
		UnmappedColumns: unmapped,
	}, nil
}

// findHeaderRow inspects the first 5 rows and returns the (rowIndex,
// headers) tuple for the first row containing at least one
// recognizable alias. -1 when no header row is found.
func findHeaderRow(rows [][]string) (int, []string) {
	scanLimit := 5
	if len(rows) < scanLimit {
		scanLimit = len(rows)
	}
	for i := 0; i < scanLimit; i++ {
		row := rows[i]
		for _, cell := range row {
			if matchAlias(cell) != "" {
				return i, row
			}
		}
	}
	return -1, nil
}

// matchAlias normalizes a header cell and returns the canonical field
// name (empty if no alias matches).
func matchAlias(cell string) string {
	norm := strings.ToLower(strings.TrimSpace(cell))
	if norm == "" {
		return ""
	}
	for canon, aliases := range fieldAliases {
		for _, alias := range aliases {
			if norm == alias {
				return canon
			}
		}
	}
	return ""
}

// isBlankRow returns true if every cell is empty/whitespace.
func isBlankRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}
