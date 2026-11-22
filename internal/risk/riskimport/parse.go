// Package riskimport implements the risk register import (#2812): CSV/XLSX
// parsing, batch staging, and approval of staged rows into real risks.
//
// It follows the controls import (internal/controlimport) rather than
// sharing a framework with it. Parsing accounts for every data row: each one
// is either a StagedRisk or a RejectedRow carrying a reason. A staged row
// becomes a risk only when a person approves it, and the risk is created
// through risk.Store, so every create-time rule applies.
//
// The tracer (#2839) maps the organisation risk ID, title, description,
// category and review due; #2841 adds the inherent and residual scores, read
// in the shape of the batch's one methodology (scores.go); #2842 adds the
// treatment, acceptance and control links (treatment.go); #2843 the owner,
// matched by email at staging (owner.go). Every other
// column is kept on the staged row as an unmapped value and never written
// into the description.
package riskimport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/xlsxbound"
)

// The upload caps are the controls import's (spec #2812, Q30).
const (
	MaxUploadBytes = 5 * 1024 * 1024
	MaxRows        = 5000
	// MaxUnzipBytes caps an XLSX's total uncompressed size, and
	// MaxUnzipXMLBytes the part of it excelize holds in memory per sheet.
	MaxUnzipBytes    = 64 << 20
	MaxUnzipXMLBytes = 16 << 20
	MaxColumns       = 200
)

var (
	ErrUploadTooLarge     = errors.New("riskimport: upload exceeds size cap")
	ErrWorkbookTooLarge   = errors.New("riskimport: workbook exceeds uncompressed size cap")
	ErrEmptyFile          = errors.New("riskimport: empty risk register file")
	ErrNoHeaderRow        = errors.New("riskimport: no header row with a title and a category column")
	ErrTooManyRows        = errors.New("riskimport: risk register exceeds row cap")
	ErrTooManyColumns     = errors.New("riskimport: risk register row exceeds column cap")
	ErrUnsupportedFormat  = errors.New("riskimport: file must be .csv or .xlsx")
	ErrInvalidColumnMap   = errors.New("riskimport: invalid column map")
	ErrMappedColumnAbsent = errors.New("riskimport: column map names a header that is not in the file")
)

// Canonical field names. These are also the keys of an explicit column map.
const (
	FieldExternalRef = "external_ref"
	FieldTitle       = "title"
	FieldDescription = "description"
	FieldCategory    = "category"
	FieldReviewDue   = "review_due"
)

// Rejection reasons recorded on a RejectedRow. The risk_import_rejected_rows
// reason CHECK holds the same vocabulary.
const (
	RejectMissingField       = "missing_required_field"
	RejectDuplicateExternal  = "duplicate_external_ref"
	RejectInvalidExternalRef = "invalid_external_ref"
	RejectUnknownCategory    = "unknown_category"
	RejectInvalidReviewDue   = "invalid_review_due"
)

// blankHeaderColumnTemplate keys the values of a column with no header.
const blankHeaderColumnTemplate = "column %d"

// StagedRisk is one register row ready for review. Title and Category are
// required; the rest are optional. Unmapped holds the value of every column
// no field took, keyed by its header.
type StagedRisk struct {
	RowNumber   int        `json:"row_number"`
	ExternalRef string     `json:"external_ref,omitempty"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	ReviewDue   *time.Time `json:"review_due,omitempty"`
	// InherentScore and ResidualScore are in the batch methodology's shape,
	// as the risk will hold them; nil is unscored (a nil residual takes the
	// create path's default).
	InherentScore json.RawMessage `json:"inherent_score,omitempty"`
	ResidualScore json.RawMessage `json:"residual_score,omitempty"`
	// Treatment is the register's, read as written; staging downgrades a
	// mitigate or transfer the row cannot back (applyTreatmentRules).
	Treatment dbx.RiskTreatment `json:"treatment"`
	// Justification and ProposedUntil are an accepted row's proposal.
	Justification string     `json:"justification,omitempty"`
	ProposedUntil *time.Time `json:"proposed_until,omitempty"`
	// HistoricalAccepter is the register's accepter, kept as history: an
	// import never records who accepted a risk (#2371).
	HistoricalAccepter  string   `json:"historical_accepter,omitempty"`
	InstrumentReference string   `json:"instrument_reference,omitempty"`
	ControlRefs         []string `json:"control_refs,omitempty"`
	// Owner is the owner cell as written; staging matches it to a user.
	Owner    string            `json:"owner,omitempty"`
	Unmapped map[string]string `json:"unmapped"`
}

// RejectedRow is a data row that could not be staged, with the reason.
type RejectedRow struct {
	RowNumber   int    `json:"row_number"`
	ExternalRef string `json:"external_ref,omitempty"`
	Reason      string `json:"reason"`
	Detail      string `json:"detail"`
}

type ParseResult struct {
	// Methodology is the batch's, resolved from ParseOptions.Methodology.
	Methodology     dbx.RiskMethodology `json:"methodology"`
	Risks           []StagedRisk        `json:"risks"`
	Rejected        []RejectedRow       `json:"rejected"`
	UnmappedColumns []string            `json:"unmapped_columns"`
}

// ParseOptions tunes header detection. ColumnMap maps a canonical field name
// to the header text used in the file; a mapped field overrides alias
// detection for that field. Methodology is the batch's; empty is the
// platform default (ResolveMethodology). Only its score fields are read, and
// a column whose header names another methodology's score stays unmapped.
type ParseOptions struct {
	ColumnMap   map[string]string
	Methodology dbx.RiskMethodology
}

// fieldAliases are the header spellings each field is recognised by. The
// risk export's own headers are among them (external_ref, title,
// description, category, review_due_at), so an export re-imports with no
// column map. The export's "id" is the risk's uuid and is deliberately not
// an alias of external_ref. Every other export column (computed severity,
// timestamps, ids, and fields later slices map) lands in unmapped; no
// export header is an alias of a second field, so none errors.
// TestRiskImport_ExportRoundTrip pins this against the export's real
// header in CSV and XLSX.
var fieldAliases = map[string][]string{
	FieldExternalRef: {
		"external_ref", "external ref", "risk id", "risk_id", "riskid", "risk ref", "risk reference",
		"org risk id", "organisation risk id", "organization risk id", "reference", "ref",
	},
	FieldTitle:       {"title", "risk title", "name", "risk name", "risk"},
	FieldDescription: {"description", "risk description", "details", "risk details"},
	FieldCategory:    {"category", "risk category", "category name", "category_name"},
	FieldReviewDue: {
		"review_due", "review due", "review_due_at", "review due at", "review date",
		"next review", "next review date", "next review due",
	},
	FieldInherentLikelihood: {"inherent_likelihood", "inherent likelihood", "likelihood"},
	FieldInherentImpact:     {"inherent_impact", "inherent impact", "impact"},
	FieldResidualLikelihood: {"residual_likelihood", "residual likelihood"},
	FieldResidualImpact:     {"residual_impact", "residual impact"},
	FieldInherentLossEventFrequency: {
		"inherent_loss_event_frequency", "inherent loss event frequency", "loss event frequency",
		"inherent lef", "lef",
	},
	FieldInherentLossMagnitude: {
		"inherent_loss_magnitude", "inherent loss magnitude", "loss magnitude", "inherent lm", "lm",
	},
	FieldResidualLossEventFrequency: {"residual_loss_event_frequency", "residual loss event frequency", "residual lef"},
	FieldResidualLossMagnitude:      {"residual_loss_magnitude", "residual loss magnitude", "residual lm"},
	// treatment_owner is the risk export's free-text owner header.
	FieldOwner: {
		"owner", "risk owner", "owner email", "owner e-mail", "risk owner email", "owner_email",
		"treatment owner", "treatment_owner",
	},
	FieldTreatment: {"treatment", "risk treatment", "treatment decision", "risk response"},
	FieldJustification: {
		"justification", "acceptance justification", "acceptance_justification", "acceptance rationale", "rationale",
	},
	FieldAcceptedUntil: {
		"accepted_until", "accepted until", "accepted until date", "acceptance expiry", "acceptance expires",
		"acceptance end date",
	},
	FieldAccepter:            {"accepter", "acceptor", "accepted by", "risk accepter", "risk acceptor"},
	FieldInstrumentReference: {"instrument_reference", "instrument reference", "instrument", "transfer instrument", "transfer reference"},
	FieldLinkedControls: {
		"linked_controls", "linked controls", "linked_control_ids", "linked control ids", "controls",
		"control ids", "mitigating controls",
	},
}

// ParseCSV parses a risk register CSV into staged rows.
func ParseCSV(raw []byte) (*ParseResult, error) { return ParseCSVWith(raw, ParseOptions{}) }

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

// ParseCSVWith parses a CSV register with explicit options.
func ParseCSVWith(raw []byte, opts ParseOptions) (*ParseResult, error) {
	if len(raw) == 0 {
		return nil, ErrEmptyFile
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	var rows [][]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("riskimport: read csv: %w", err)
		}
		if len(rows) >= MaxRows {
			return nil, ErrTooManyRows
		}
		if len(rec) > MaxColumns {
			return nil, ErrTooManyColumns
		}
		rows = append(rows, rec)
	}
	return parseRows(rows, opts)
}

// xlsxLimits bounds an uploaded workbook through internal/xlsxbound, as the
// controls import does.
var xlsxLimits = xlsxbound.Limits{
	MaxDecompressedBytes: MaxUnzipBytes,
	MaxXMLPartBytes:      MaxUnzipXMLBytes,
	MaxRows:              MaxRows,
	MaxColumns:           MaxColumns,
}

// ParseXLSXWith parses the first sheet of a workbook with explicit options.
func ParseXLSXWith(raw []byte, opts ParseOptions) (*ParseResult, error) {
	if len(raw) == 0 {
		return nil, ErrEmptyFile
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	f, err := xlsxbound.Open(raw, xlsxLimits)
	if err != nil {
		return nil, xlsxError("open xlsx", err)
	}
	defer func() { _ = f.Close() }()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, ErrEmptyFile
	}
	rows, err := xlsxbound.ReadRows(f, sheets[0], xlsxLimits)
	if err != nil {
		return nil, xlsxError("read xlsx rows", err)
	}
	return parseRows(rows, opts)
}

func xlsxError(op string, err error) error {
	switch {
	case errors.Is(err, xlsxbound.ErrDecompressedTooLarge):
		return ErrWorkbookTooLarge
	case errors.Is(err, xlsxbound.ErrTooManyRows):
		return ErrTooManyRows
	case errors.Is(err, xlsxbound.ErrTooManyColumns):
		return ErrTooManyColumns
	default:
		return fmt.Errorf("riskimport: %s: %w", op, err)
	}
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
		if !scoreFieldApplies(field, opts.Methodology) {
			return nil, fmt.Errorf("%w: field %q is not a %s score field", ErrInvalidColumnMap, field, opts.Methodology)
		}
		norm := normalizeHeader(header)
		if norm == "" {
			return nil, fmt.Errorf("%w: field %q maps to an empty header", ErrInvalidColumnMap, field)
		}
		if other, dup := explicit[norm]; dup {
			return nil, fmt.Errorf("%w: header %q is mapped to both %s and %s", ErrInvalidColumnMap, header, other, field)
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
		if mappedFields[field] || !scoreFieldApplies(field, opts.Methodology) {
			// An explicit mapping for this field wins over alias
			// detection, and another methodology's score column is
			// kept as unmapped.
			return ""
		}
		return field
	}, nil
}

// column is one header column: the field it maps to ("" when unmapped) and
// the key its values are kept under when unmapped.
type column struct {
	field string
	key   string
}

func parseRows(rows [][]string, opts ParseOptions) (*ParseResult, error) {
	if len(rows) == 0 {
		return nil, ErrEmptyFile
	}
	if len(rows) > MaxRows {
		return nil, ErrTooManyRows
	}
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
	if headerIdx < 0 {
		// A mapped header that is nowhere in the search window is the
		// likelier mistake, and the one the user can act on.
		if err := absentMappedHeader(rows, opts); err != nil {
			return nil, err
		}
		return nil, ErrNoHeaderRow
	}
	data := rows[headerIdx+1:]
	cols := mapColumns(headers, match, data)
	for _, field := range FieldNames() {
		if header, ok := opts.ColumnMap[field]; ok && !hasField(cols, field) {
			return nil, fmt.Errorf("%w: %s=%q", ErrMappedColumnAbsent, field, header)
		}
	}
	out := &ParseResult{
		Methodology:     methodology,
		Risks:           make([]StagedRisk, 0, len(data)),
		Rejected:        make([]RejectedRow, 0),
		UnmappedColumns: make([]string, 0),
	}
	for _, c := range cols {
		if c.field == "" && c.key != "" {
			out.UnmappedColumns = append(out.UnmappedColumns, c.key)
		}
	}
	firstRowForRef := make(map[string]int)
	for idx, row := range data {
		if isBlankRow(row) {
			continue
		}
		staged, rej, ok := rowToRisk(row, cols, methodology, headerIdx+idx+2, firstRowForRef)
		if !ok {
			out.Rejected = append(out.Rejected, rej)
			continue
		}
		out.Risks = append(out.Risks, staged)
	}
	return out, nil
}

// mapColumns assigns each header column its field. The first column to
// match a field takes it; a later one goes unmapped. An unmapped column is
// keyed by its header; a blank header is kept as "column N" when any data
// row has a value under it, so no value is dropped. A repeated key gets
// the column number appended.
func mapColumns(headers []string, match headerMatcher, data [][]string) []column {
	cols := make([]column, len(headers))
	taken := map[string]bool{}
	keys := map[string]bool{}
	for i, h := range headers {
		h = strings.TrimSpace(h)
		if field := match(h); field != "" && !taken[field] {
			taken[field] = true
			cols[i] = column{field: field}
			continue
		}
		key := h
		if key == "" {
			if !columnHasValue(data, i) {
				continue
			}
			key = fmt.Sprintf(blankHeaderColumnTemplate, i+1)
		}
		if keys[key] {
			key = fmt.Sprintf("%s (column %d)", key, i+1)
		}
		keys[key] = true
		cols[i] = column{key: key}
	}
	return cols
}

func columnHasValue(data [][]string, i int) bool {
	for _, row := range data {
		if i < len(row) && strings.TrimSpace(row[i]) != "" {
			return true
		}
	}
	return false
}

// rowToRisk reads one data row and applies the row checks. It records the
// row's organisation risk ID for in-file duplicate detection when it passes.
func rowToRisk(row []string, cols []column, methodology dbx.RiskMethodology, rowNumber int, firstRowForRef map[string]int) (StagedRisk, RejectedRow, bool) {
	s := StagedRisk{RowNumber: rowNumber, Unmapped: map[string]string{}}
	var reviewDue string
	scoreCells := map[string]string{}
	var tc treatmentCells
	for i, c := range cols {
		if i >= len(row) {
			break
		}
		val := strings.TrimSpace(row[i])
		switch c.field {
		case FieldExternalRef:
			s.ExternalRef = val
		case FieldTitle:
			s.Title = val
		case FieldDescription:
			s.Description = val
		case FieldCategory:
			s.Category = val
		case FieldReviewDue:
			reviewDue = val
		case FieldTreatment:
			tc.treatment = val
		case FieldJustification:
			tc.justification = val
		case FieldAcceptedUntil:
			tc.acceptedUntil = val
		case FieldAccepter:
			tc.accepter = val
		case FieldInstrumentReference:
			tc.instrument = val
		case FieldLinkedControls:
			tc.controls = val
		case FieldOwner:
			s.Owner = val
		case "":
			if c.key != "" && val != "" {
				s.Unmapped[c.key] = val
			}
		default:
			// Only the batch methodology's score fields are mapped.
			scoreCells[c.field] = val
		}
	}
	reject := func(reason, detail string) (StagedRisk, RejectedRow, bool) {
		return StagedRisk{}, RejectedRow{RowNumber: rowNumber, ExternalRef: s.ExternalRef, Reason: reason, Detail: detail}, false
	}
	if missing := missingFields(s); len(missing) > 0 {
		return reject(RejectMissingField, "missing "+strings.Join(missing, ", "))
	}
	if utf8.RuneCountInString(s.ExternalRef) > risk.MaxExternalRefLen {
		return reject(RejectInvalidExternalRef, fmt.Sprintf("risk ID is longer than %d characters", risk.MaxExternalRefLen))
	}
	key := strings.ToLower(s.ExternalRef)
	if key != "" {
		if first, dup := firstRowForRef[key]; dup {
			return reject(RejectDuplicateExternal, fmt.Sprintf("risk ID %q already appears on row %d", s.ExternalRef, first))
		}
	}
	if reviewDue != "" {
		d, ok := parseDate(reviewDue)
		if !ok {
			return reject(RejectInvalidReviewDue, fmt.Sprintf("review due %q is not a date (YYYY-MM-DD)", reviewDue))
		}
		s.ReviewDue = &d
	}
	inherent, residual, detail, ok := scoresFromCells(methodology, scoreCells)
	if !ok {
		return reject(RejectInvalidScore, detail)
	}
	s.InherentScore, s.ResidualScore = inherent, residual
	if reason, detail, ok := readTreatment(&s, tc); !ok {
		return reject(reason, detail)
	}
	if key != "" {
		firstRowForRef[key] = rowNumber
	}
	return s, RejectedRow{}, true
}

// parseDate reads a review-due cell: a YYYY-MM-DD date, or an RFC 3339
// timestamp as the risk export writes review_due_at, kept as its UTC date.
func parseDate(raw string) (time.Time, bool) {
	if d, err := time.Parse(time.DateOnly, raw); err == nil {
		return d, true
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		y, m, d := ts.UTC().Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), true
	}
	return time.Time{}, false
}

func missingFields(s StagedRisk) []string {
	var missing []string
	if s.Title == "" {
		missing = append(missing, FieldTitle)
	}
	if s.Category == "" {
		missing = append(missing, FieldCategory)
	}
	return missing
}

// headerSearchRows is how far down the sheet the header row is looked for,
// so a title, an export note or a blank line above it is skipped.
const headerSearchRows = 5

// findHeaderRow returns the first of the first headerSearchRows rows in
// which both required fields (title and category) resolve, through an alias
// or the column map. A title or notes row above the header that happens to
// hold alias words ("Risk", "Reference") is passed over, because it does not
// name both required columns.
func findHeaderRow(rows [][]string, match headerMatcher) (int, []string) {
	limit := min(headerSearchRows, len(rows))
	for i := 0; i < limit; i++ {
		var title, category bool
		for _, cell := range rows[i] {
			switch match(cell) {
			case FieldTitle:
				title = true
			case FieldCategory:
				category = true
			}
		}
		if title && category {
			return i, rows[i]
		}
	}
	return -1, nil
}

// absentMappedHeader reports the first column-map entry, in field order,
// whose header appears in none of the first headerSearchRows rows.
func absentMappedHeader(rows [][]string, opts ParseOptions) error {
	seen := map[string]bool{}
	for _, row := range rows[:min(headerSearchRows, len(rows))] {
		for _, cell := range row {
			seen[normalizeHeader(cell)] = true
		}
	}
	for _, field := range FieldNames() {
		if header, ok := opts.ColumnMap[field]; ok && !seen[normalizeHeader(header)] {
			return fmt.Errorf("%w: %s=%q", ErrMappedColumnAbsent, field, header)
		}
	}
	return nil
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

func hasField(cols []column, field string) bool {
	for _, c := range cols {
		if c.field == field {
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

// ReadAllBounded enforces MaxUploadBytes while an HTTP handler reads a
// multipart file.
func ReadAllBounded(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.CopyN(&buf, r, MaxUploadBytes+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("riskimport: read upload: %w", err)
	}
	if buf.Len() > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	return buf.Bytes(), nil
}
