package frameworkscope

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// CSV import of applicability decisions (issue #1790, Statement of
// Applicability #1744). The file has a header row naming its columns:
// requirement code and decision are required, justification and
// implementation reference are optional. Each data row is validated on its
// own; the valid rows are applied as one change through the same write path
// as a single edit, and the bad rows are reported with their reason.

// ErrImportHeader is returned when the CSV has no header row, or the header
// lacks a requirement-code or decision column. HTTP 400.
var ErrImportHeader = errors.New("frameworkscope: CSV header must name `requirement_code` and `decision` columns (`justification` and `implementation_ref` are optional)")

// ErrImportEmpty is returned when the CSV has a header but no data rows.
// HTTP 400.
var ErrImportEmpty = errors.New("frameworkscope: CSV has no data rows")

// Rejection reasons for an import row.
const (
	ReasonUnknownCode           = "unknown requirement code"
	ReasonDuplicateCode         = "requirement code appears more than once"
	ReasonInvalidDecision       = "decision must be `applicable` or `excluded`"
	ReasonJustificationRequired = "a justification is required to exclude a requirement"
	ReasonMalformedRow          = "row could not be read"
)

// ImportRow is one data row of the CSV. Line is its 1-based line number in
// the file, counting the header as line 1.
type ImportRow struct {
	Line              int
	RequirementCode   string
	Decision          string
	Justification     string
	ImplementationRef string
}

// ImportRejection is a row that was not applied, and why.
type ImportRejection struct {
	Line            int
	RequirementCode string
	Reason          string
}

// ImportResult reports an import. When Applied is 0 nothing was written and
// Scope is the revision as it stood.
type ImportResult struct {
	Applied     int
	Rejected    []ImportRejection
	Scope       FrameworkScope
	Invalidated bool
}

// headerAliases maps a normalised header cell to its column.
var headerAliases = map[string]string{
	"requirement_code":         "code",
	"requirement":              "code",
	"code":                     "code",
	"decision":                 "decision",
	"justification":            "justification",
	"implementation_ref":       "implementation_ref",
	"implementation_reference": "implementation_ref",
}

func normaliseHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\ufeff")))
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '-' || r == '_' }), "_")
}

// ParseDecisionCSV reads the import file. A row that cannot be read (a
// quoting error) is returned as a rejection rather than failing the file;
// header problems and an empty file fail it.
func ParseDecisionCSV(r io.Reader) ([]ImportRow, []ImportRejection, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, ErrImportHeader
		}
		return nil, nil, fmt.Errorf("%w: %v", ErrImportHeader, err)
	}
	col := map[string]int{}
	for i, h := range header {
		if c, ok := headerAliases[normaliseHeader(h)]; ok {
			if _, dup := col[c]; !dup {
				col[c] = i
			}
		}
	}
	if _, ok := col["code"]; !ok {
		return nil, nil, ErrImportHeader
	}
	if _, ok := col["decision"]; !ok {
		return nil, nil, ErrImportHeader
	}
	cell := func(rec []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	var rows []ImportRow
	var rejected []ImportRejection
	for {
		rec, rerr := cr.Read()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			line := 0
			var pe *csv.ParseError
			if errors.As(rerr, &pe) {
				line = pe.StartLine
			}
			rejected = append(rejected, ImportRejection{Line: line, Reason: ReasonMalformedRow})
			continue
		}
		line, _ := cr.FieldPos(0)
		row := ImportRow{
			Line:              line,
			RequirementCode:   cell(rec, "code"),
			Decision:          strings.ToLower(cell(rec, "decision")),
			Justification:     cell(rec, "justification"),
			ImplementationRef: cell(rec, "implementation_ref"),
		}
		if row.RequirementCode == "" && row.Decision == "" && row.Justification == "" && row.ImplementationRef == "" {
			continue // a blank spreadsheet row
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 && len(rejected) == 0 {
		return nil, nil, ErrImportEmpty
	}
	return rows, rejected, nil
}

// checkImportRows splits rows into decisions to apply and rejections, given
// the revision's requirement codes. Codes match exactly after trimming.
func checkImportRows(rows []ImportRow, codes map[string]uuid.UUID) ([]DecisionInput, []ImportRejection) {
	var apply []DecisionInput
	var rejected []ImportRejection
	seen := map[string]bool{}
	for _, r := range rows {
		reject := func(reason string) {
			rejected = append(rejected, ImportRejection{Line: r.Line, RequirementCode: r.RequirementCode, Reason: reason})
		}
		id, ok := codes[r.RequirementCode]
		switch {
		case !ok:
			reject(ReasonUnknownCode)
			continue
		case seen[r.RequirementCode]:
			reject(ReasonDuplicateCode)
			continue
		}
		seen[r.RequirementCode] = true
		switch err := ValidateDecision(r.Decision, r.Justification); {
		case errors.Is(err, ErrInvalidDecision):
			reject(ReasonInvalidDecision)
			continue
		case errors.Is(err, ErrJustificationRequired):
			reject(ReasonJustificationRequired)
			continue
		}
		apply = append(apply, DecisionInput{
			RequirementID:     id,
			Decision:          r.Decision,
			Justification:     r.Justification,
			ImplementationRef: r.ImplementationRef,
		})
	}
	return apply, rejected
}

// ImportDecisions applies the valid rows of a parsed import to a scope
// revision as one change, with the same draft reset and workflow-log rows as
// single edits. parseRejected carries rows the parser could not read; they
// are reported alongside the rows that fail validation. With no valid row,
// nothing is written.
func (s *Store) ImportDecisions(ctx context.Context, scopeID uuid.UUID, rows []ImportRow, parseRejected []ImportRejection, actor string) (ImportResult, error) {
	var out ImportResult
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		first, err := writableScope(ctx, q, tenantID, scopeID)
		if err != nil {
			return err
		}
		reqs, err := q.ListFrameworkScopeApplicability(ctx, dbx.ListFrameworkScopeApplicabilityParams{
			TenantID:           pgUUID(tenantID),
			FrameworkScopeID:   pgtype.UUID{},
			FrameworkVersionID: first.FrameworkVersionID,
		})
		if err != nil {
			return fmt.Errorf("list requirements: %w", err)
		}
		codes := make(map[string]uuid.UUID, len(reqs))
		for _, r := range reqs {
			codes[r.Code] = uuid.UUID(r.RequirementID.Bytes)
		}
		apply, rejected := checkImportRows(rows, codes)
		out.Rejected = append(append([]ImportRejection{}, parseRejected...), rejected...)
		sort.SliceStable(out.Rejected, func(i, j int) bool { return out.Rejected[i].Line < out.Rejected[j].Line })
		last := first
		if len(apply) > 0 {
			if _, last, err = writeDecisions(ctx, q, tenantID, first, apply, actor); err != nil {
				return err
			}
		}
		out.Applied = len(apply)
		out.Scope = rowToScope(last)
		out.Invalidated = first.State != last.State && last.State == StateDraft
		return nil
	})
	return out, err
}
