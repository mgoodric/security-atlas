package mappingoverlay

// Bulk import and export of overlay edges (#1799).
//
// An import file is CSV (a header row, then one edge per row) or YAML (a
// list of mappings), with the columns in ImportColumns. Each row is parsed
// here and then written through create, the same path a single creation
// takes, so one set of rules applies. All rows go in one transaction, each
// under its own savepoint: by default any bad row rolls the whole import
// back, and with partial the valid rows commit.
//
// A bad row is reported by its number and a reason. The reason is fixed
// text chosen by the kind of failure; it never repeats the file's cell
// content back to the caller.

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// MaxImportRows bounds the rows in one import file.
const MaxImportRows = 5000

// ImportColumns are the columns an import file may carry, in export order.
var ImportColumns = []string{
	"framework", "framework_version", "requirement_code", "scf_release", "anchor_code",
	"relationship_type", "strength", "rationale", "source_ref",
}

// requiredColumns must be present in a CSV header. framework_version and
// scf_release may be left out: empty means the current version or release,
// as on single creation.
var requiredColumns = []string{
	"framework", "requirement_code", "anchor_code", "relationship_type", "strength", "rationale", "source_ref",
}

// tenantColumns are refused outright: the tenant comes from the credential.
var tenantColumns = map[string]bool{"tenant": true, "tenant_id": true}

// ExportColumns are the export's CSV header.
var ExportColumns = []string{
	"id", "framework", "framework_version", "requirement_code", "scf_release", "anchor_code",
	"relationship_type", "strength", "rationale", "source_ref", "status", "retirement_reason",
	"created_by", "created_at", "retired_by", "retired_at",
}

// FileError refuses an import file as a whole: an unreadable file, a
// missing or unknown column, a tenant column, too many rows.
type FileError struct{ Reason string }

func (e *FileError) Error() string { return "mapping overlay import: " + e.Reason }

// RowError is one refused import row. Row is the 1-based data row (the CSV
// header and YAML document are not rows); Line is the line in the file.
type RowError struct {
	Row    int
	Line   int
	Code   string
	Reason string
}

// ImportRow is one parsed row. Err is set when the row could not be parsed
// into an edge; such a row is reported and never written.
type ImportRow struct {
	Row  int
	Line int
	Edge NewEdge
	Err  *RowError
}

// ImportedEdge is a row that was written.
type ImportedEdge struct {
	Row  int
	Edge Edge
}

// ImportResult is the outcome of an import. Written is false when nothing
// was committed: a bad row without partial, or no valid row at all.
type ImportResult struct {
	Created  []ImportedEdge
	Rejected []RowError
	Written  bool
}

// ParseCSV reads an import file in CSV form.
func ParseCSV(r io.Reader) ([]ImportRow, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, &FileError{Reason: "the file is empty"}
	}
	if err != nil {
		return nil, &FileError{Reason: "the CSV header could not be read"}
	}
	cols := make([]string, len(header))
	seen := map[string]bool{}
	for i, h := range header {
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if err := checkColumn(name, seen); err != nil {
			return nil, err
		}
		cols[i] = name
	}
	for _, c := range requiredColumns {
		if !seen[c] {
			return nil, &FileError{Reason: "the CSV header has no " + c + " column"}
		}
	}

	var rows []ImportRow
	for n := 1; ; n++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if n > MaxImportRows {
			return nil, &FileError{Reason: fmt.Sprintf("the file has more than %d rows", MaxImportRows)}
		}
		if err != nil {
			// A quoting error: the reader cannot find where the next row
			// starts, so the file is refused rather than the row.
			line := 0
			var perr *csv.ParseError
			if errors.As(err, &perr) {
				line = perr.Line
			}
			return nil, &FileError{Reason: fmt.Sprintf("row %d (line %d) is not valid CSV", n, line)}
		}
		line, _ := cr.FieldPos(0)
		if len(rec) != len(cols) {
			rows = append(rows, ImportRow{Row: n, Line: line, Err: rowInvalid(n, line,
				fmt.Sprintf("the row has %d fields; the header has %d", len(rec), len(cols)))})
			continue
		}
		fields := make(map[string]string, len(cols))
		for i, c := range cols {
			fields[c] = rec[i]
		}
		rows = append(rows, rowFromFields(n, line, fields))
	}
	return rows, nil
}

// ParseYAML reads an import file in YAML form: a list of mappings whose
// keys are ImportColumns.
func ParseYAML(r io.Reader) ([]ImportRow, error) {
	dec := yaml.NewDecoder(r)
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, &FileError{Reason: "the file is empty"}
		}
		return nil, &FileError{Reason: "the file is not valid YAML"}
	}
	// One document only: a second one would otherwise be silently dropped.
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, &FileError{Reason: "the YAML file must hold exactly one document"}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.SequenceNode {
		return nil, &FileError{Reason: "the YAML file must be a list of overlay edges"}
	}
	items := doc.Content[0].Content
	if len(items) > MaxImportRows {
		return nil, &FileError{Reason: fmt.Sprintf("the file has more than %d rows", MaxImportRows)}
	}
	rows := make([]ImportRow, 0, len(items))
	for i, item := range items {
		n, line := i+1, item.Line
		if item.Kind != yaml.MappingNode {
			rows = append(rows, ImportRow{Row: n, Line: line, Err: rowInvalid(n, line, "the row is not a mapping")})
			continue
		}
		fields := map[string]string{}
		seen := map[string]bool{}
		var bad string
		for k := 0; k+1 < len(item.Content); k += 2 {
			key, val := item.Content[k], item.Content[k+1]
			name := strings.ToLower(strings.TrimSpace(key.Value))
			if err := checkColumn(name, seen); err != nil {
				// A tenant, unknown or repeated key refuses the file, as
				// the same CSV header column does.
				return nil, &FileError{Reason: fmt.Sprintf("row %d (line %d): %s", n, line, err.(*FileError).Reason)}
			}
			switch {
			case val.Kind != yaml.ScalarNode:
				bad = name + " must be a single value"
			case val.Tag == "!!null":
				fields[name] = ""
			default:
				fields[name] = val.Value
			}
		}
		if bad != "" {
			rows = append(rows, ImportRow{Row: n, Line: line, Err: rowInvalid(n, line, bad)})
			continue
		}
		rows = append(rows, rowFromFields(n, line, fields))
	}
	return rows, nil
}

// checkColumn admits a column name once. The reason names the column only
// when it is one of ours; an unknown name is not echoed back.
func checkColumn(name string, seen map[string]bool) error {
	switch {
	case tenantColumns[name]:
		return &FileError{Reason: "the file names a tenant; the tenant comes from the credential"}
	case !isImportColumn(name):
		return &FileError{Reason: "the file has a column that is not one of " + strings.Join(ImportColumns, ", ")}
	case seen[name]:
		return &FileError{Reason: "the column " + name + " appears twice"}
	}
	seen[name] = true
	return nil
}

func isImportColumn(name string) bool {
	for _, c := range ImportColumns {
		if c == name {
			return true
		}
	}
	return false
}

func rowFromFields(n, line int, f map[string]string) ImportRow {
	get := func(k string) string { return strings.TrimSpace(f[k]) }
	row := ImportRow{Row: n, Line: line, Edge: NewEdge{
		Framework:        get("framework"),
		FrameworkVersion: get("framework_version"),
		RequirementCode:  get("requirement_code"),
		SCFRelease:       get("scf_release"),
		AnchorCode:       get("anchor_code"),
		RelationshipType: get("relationship_type"),
		Rationale:        get("rationale"),
		SourceRef:        get("source_ref"),
	}}
	raw := get("strength")
	if raw == "" {
		row.Err = rowInvalid(n, line, "strength is required")
		return row
	}
	s, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(s) || math.IsInf(s, 0) {
		row.Err = rowInvalid(n, line, "strength must be a number between 0 and 1")
		return row
	}
	row.Edge.Strength = s
	return row
}

func rowInvalid(n, line int, reason string) *RowError {
	return &RowError{Row: n, Line: line, Code: "invalid_overlay_edge", Reason: reason}
}

// Import writes the parsed rows for the caller's tenant in one transaction.
// Without partial, any bad row (a parse failure or a refusal from create)
// writes nothing; with partial the valid rows are written. Either way every
// bad row is reported. A refusal that is not about the row (a database
// failure) aborts the import and is returned as the error.
func (s *Store) Import(ctx context.Context, rows []ImportRow, partial bool, actor string) (ImportResult, error) {
	if len(rows) == 0 {
		return ImportResult{}, &FileError{Reason: "the file has no rows"}
	}
	if actor == "" {
		return ImportResult{}, errors.New("mapping overlay: import has no actor")
	}
	var res ImportResult
	errRollback := errors.New("rollback")
	err := s.inRawTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		for _, row := range rows {
			if row.Err != nil {
				res.Rejected = append(res.Rejected, *row.Err)
				continue
			}
			edge, rowErr, err := importRow(ctx, tx, tenantID, row, actor)
			if err != nil {
				return err
			}
			if rowErr != nil {
				res.Rejected = append(res.Rejected, *rowErr)
				continue
			}
			res.Created = append(res.Created, ImportedEdge{Row: row.Row, Edge: edge})
		}
		if len(res.Created) == 0 || (len(res.Rejected) > 0 && !partial) {
			return errRollback
		}
		return nil
	})
	if errors.Is(err, errRollback) {
		res.Created = nil
		return res, nil
	}
	if err != nil {
		return ImportResult{}, err
	}
	res.Written = true
	return res, nil
}

// importRow writes one row under a savepoint, so a refused row (including a
// constraint violation, which aborts the statement) leaves the rest of the
// transaction usable.
func importRow(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, row ImportRow, actor string) (Edge, *RowError, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return Edge{}, nil, fmt.Errorf("mapping overlay: savepoint: %w", err)
	}
	edge, err := create(ctx, dbx.New(sp), tenantID, row.Edge, actor)
	if err != nil {
		_ = sp.Rollback(ctx)
		code, reason, ok := Classify(err)
		if !ok {
			return Edge{}, nil, err
		}
		return Edge{}, &RowError{Row: row.Row, Line: row.Line, Code: code, Reason: reason}, nil
	}
	if err := sp.Commit(ctx); err != nil {
		return Edge{}, nil, fmt.Errorf("mapping overlay: release savepoint: %w", err)
	}
	return edge, nil, nil
}

// Classify names a refusal from the store with a stable code and a reason
// that carries no caller-supplied text. ok is false for an error that is
// not a refusal (a database failure).
func Classify(err error) (code, reason string, ok bool) {
	var (
		invalid *InvalidEdgeError
		catalog *CatalogEdgeExistsError
	)
	switch {
	case errors.As(err, &invalid):
		return "invalid_overlay_edge", invalid.Reason, true
	case errors.Is(err, ErrUnknownFrameworkVersion):
		return "unknown_framework_version", "no catalog framework has this slug and version", true
	case errors.Is(err, ErrUnknownRequirement):
		return "unknown_requirement", "the framework version has no requirement with this code", true
	case errors.Is(err, scfrelease.ErrUnknownRelease), errors.Is(err, scfrelease.ErrNoCurrentRelease):
		return "unknown_scf_release", "no SCF release has this label", true
	case errors.Is(err, scfrelease.ErrCodeNotInRelease):
		return "unknown_anchor", "the SCF release has no anchor with this code", true
	case errors.As(err, &catalog):
		return "catalog_edge_exists", fmt.Sprintf("catalog edge %s (%s, %s) already joins this requirement and anchor",
			catalog.EdgeID, catalog.RelationshipType, catalog.MappingTier), true
	case errors.Is(err, ErrDuplicateOverlay):
		return "overlay_edge_exists", "an active overlay edge already joins this requirement and anchor", true
	case errors.Is(err, ErrAlreadyRetired):
		return "already_retired", "the overlay edge is already retired", true
	case errors.Is(err, ErrNotFound):
		return "not_found", "overlay edge not found", true
	}
	return "", "", false
}

// ExportRow renders an edge as the export's CSV cells, in ExportColumns
// order.
func ExportRow(e Edge) []string {
	retiredAt := ""
	if e.RetiredAt != nil {
		retiredAt = e.RetiredAt.UTC().Format(time.RFC3339)
	}
	return []string{
		e.ID.String(), e.Framework, e.FrameworkVersion, e.RequirementCode, e.SCFRelease, e.AnchorCode,
		e.RelationshipType, strconv.FormatFloat(e.Strength, 'f', -1, 64), e.Rationale, e.SourceRef,
		e.Status, e.RetirementReason, e.CreatedBy, e.CreatedAt.UTC().Format(time.RFC3339),
		e.RetiredBy, retiredAt,
	}
}
