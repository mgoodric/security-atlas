// CSV completion import (OPENENGINE-659): the bulk bridge between whatever
// LMS a compliance team uses and the training-assignment tracker, until a
// real LMS connector exists. Rows key a person (source_person_id preferred,
// work_email fallback — both tenant-unique at the DB layer), a course code,
// and a campaign name; matched rows complete the assignment with
// completion_source 'csv' and carry the same completion-evidence record the
// manual path builds. Row-level problems are reported per row; only an
// infrastructure error aborts (and rolls back) the batch.
//
// Contract doc: docs/spec/security-awareness-csv-completion-import.md
package securityawareness

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// CSV column names (header-matched case-insensitively).
const (
	CSVColWorkEmail         = "work_email"
	CSVColSourcePersonID    = "source_person_id"
	CSVColPersonSource      = "person_source"
	CSVColCourseCode        = "course_code"
	CSVColCampaignName      = "campaign_name"
	CSVColCompletedAt       = "completed_at"
	CSVColPhishSimulationID = "phishing_simulation_id"
	CSVColPhishSentAt       = "phishing_sent_at"
	CSVColPhishOutcome      = "phishing_outcome"
	CSVColPhishClickedAt    = "phishing_clicked_at"
	CSVColPhishReportedAt   = "phishing_reported_at"
)

// Errors fatal to the whole import (nothing is written when these return).
var (
	ErrCSVTooManyRows   = errors.New("securityawareness: csv row count exceeds MaxRows")
	ErrCSVFieldTooLarge = errors.New("securityawareness: csv field bytes exceed MaxFieldBytes")
	ErrCSVHeader        = errors.New("securityawareness: csv header missing required columns")
)

// CSVImportLimits caps the parser so an attacker-supplied file cannot
// exhaust memory (same posture as connectors/manual's parser). Zero values
// are rejected — use DefaultCSVImportLimits.
type CSVImportLimits struct {
	MaxRows       int
	MaxFieldBytes int
}

// DefaultCSVImportLimits mirrors the manual connector's defaults.
var DefaultCSVImportLimits = CSVImportLimits{MaxRows: 100_000, MaxFieldBytes: 1 << 20}

// Row statuses in an ImportReport.
const (
	RowImported        = "imported"
	RowAlreadyComplete = "already_complete"
	RowError           = "error"
)

// RowResult is the per-row outcome. Row is the 1-based data-row number
// (the header is row 0). Evidence is set only for RowImported rows.
type RowResult struct {
	Row          int
	Status       string
	AssignmentID uuid.UUID
	Error        string
	Evidence     *evidencev1.EvidenceRecord
}

// ImportReport summarizes one ImportCompletionsCSV call.
type ImportReport struct {
	Results         []RowResult
	Imported        int
	AlreadyComplete int
	Failed          int
}

func (r *ImportReport) add(res RowResult) {
	switch res.Status {
	case RowImported:
		r.Imported++
	case RowAlreadyComplete:
		r.AlreadyComplete++
	default:
		r.Failed++
	}
	r.Results = append(r.Results, res)
}

// completionRow is one parsed, pre-validated CSV data row.
type completionRow struct {
	row            int
	workEmail      string
	sourcePersonID string
	personSource   string
	courseCode     string
	campaignName   string
	completedAt    time.Time
	phishing       *PhishingInput // AssignmentID filled in after resolution
}

// ImportCompletionsCSV parses r per the CSV completion-import contract and
// records completions (completion_source 'csv') against existing
// assignments, plus phishing-simulation results where the phishing column
// group is present. actorID attributes the built evidence records, exactly
// as the manual Complete + BuildCompletionEvidence path does.
//
// All matched rows commit in ONE transaction: per-row validation and
// resolution failures are collected in the report and never abort the
// batch, while an infrastructure error returns non-nil and rolls the whole
// batch back — the batch never partially corrupts state. Re-importing the
// same file is idempotent: rows whose assignment is already completed at
// the same instant report RowAlreadyComplete without rewriting.
func (s *Store) ImportCompletionsCSV(ctx context.Context, r io.Reader, actorID string, lim CSVImportLimits) (ImportReport, error) {
	var report ImportReport
	rows, parseFailures, err := parseCompletionCSV(r, lim)
	if err != nil {
		return ImportReport{}, err
	}
	for _, f := range parseFailures {
		report.add(f)
	}
	if len(rows) == 0 {
		return report, nil
	}
	err = s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		for _, row := range rows {
			res, err := ingestCompletion(ctx, tx, tenantID, row, CompletionSourceCSV, actorID)
			if err != nil {
				return fmt.Errorf("securityawareness: csv import row %d: %w", row.row, err)
			}
			report.add(res)
		}
		return nil
	})
	if err != nil {
		return ImportReport{}, translateErr(err)
	}
	return report, nil
}

// ingestCompletion resolves one externally-sourced completion record to an
// assignment and completes it with the given completion source. It is
// source-neutral on purpose: the CSV importer calls it with
// CompletionSourceCSV, and the future LMS connector path (decisions log
// D6) calls the same mechanism with CompletionSourceLMSConnector.
//
// Row-level failures come back as RowError results and never abort the
// batch; a non-nil error is an infrastructure failure that the caller
// turns into a whole-batch rollback. Every row-level failure path here is
// a no-rows or pre-validated condition, never a failed SQL statement, so
// the transaction stays usable for subsequent rows.
func ingestCompletion(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, row completionRow, source, actorID string) (RowResult, error) {
	res := RowResult{Row: row.row}
	personID, rowErr, err := resolvePerson(ctx, tx, tenantID, row)
	if err != nil {
		return res, err
	}
	if rowErr != "" {
		res.Status, res.Error = RowError, rowErr
		return res, nil
	}
	var (
		assignmentID     uuid.UUID
		completedAt      *time.Time
		completionSource *string
	)
	err = tx.QueryRow(ctx, `
SELECT a.id, a.completed_at, a.completion_source
FROM security_training_assignments a
JOIN security_training_campaigns camp ON camp.tenant_id = a.tenant_id AND camp.id = a.campaign_id
JOIN security_training_courses c ON c.tenant_id = camp.tenant_id AND c.id = camp.course_id
WHERE a.tenant_id = $1 AND a.person_id = $2 AND lower(c.code) = lower($3) AND lower(camp.name) = lower($4)`,
		tenantID, personID, row.courseCode, row.campaignName).Scan(&assignmentID, &completedAt, &completionSource)
	if errors.Is(err, pgx.ErrNoRows) {
		res.Status = RowError
		res.Error = fmt.Sprintf("no assignment for person in course %q campaign %q", row.courseCode, row.campaignName)
		return res, nil
	}
	if err != nil {
		return res, err
	}
	res.AssignmentID = assignmentID
	switch {
	case completedAt == nil:
		detail, err := completeAssignment(ctx, tx, tenantID, CompleteInput{
			AssignmentID: assignmentID,
			CompletedAt:  row.completedAt,
			Source:       source,
		})
		if err != nil {
			return res, err
		}
		evidence, err := BuildCompletionEvidence(detail, actorID)
		if err != nil {
			return res, err
		}
		res.Status, res.Evidence = RowImported, evidence
	case completedAt.Equal(row.completedAt):
		// Idempotent re-import: same instant, nothing to rewrite.
		res.Status = RowAlreadyComplete
	default:
		existingSource := "unknown"
		if completionSource != nil {
			existingSource = *completionSource
		}
		res.Status = RowError
		res.Error = fmt.Sprintf("assignment already completed at %s (source %s); refusing to overwrite",
			completedAt.UTC().Format(time.RFC3339), existingSource)
		return res, nil
	}
	// Phishing results upsert for imported and already-complete rows alike;
	// the DB dedupes on (assignment, simulation_id).
	if row.phishing != nil {
		ph := *row.phishing
		ph.AssignmentID = assignmentID
		if _, err := upsertPhishing(ctx, tx, tenantID, ph); err != nil {
			return res, err
		}
	}
	return res, nil
}

// resolvePerson keys the row to a tenant person. source_person_id wins when
// present (optionally narrowed by person_source); work_email is the
// fallback. Both are DB-unique per tenant, but a source_person_id shared
// across sources (hris vs scim) is reported as ambiguous rather than
// guessed. RLS scopes every lookup to the calling tenant, so a row can
// never resolve to another tenant's person.
func resolvePerson(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, row completionRow) (uuid.UUID, string, error) {
	if row.sourcePersonID != "" {
		rows, err := tx.Query(ctx, `
SELECT id FROM security_training_people
WHERE tenant_id = $1 AND source_person_id = $2 AND ($3 = '' OR source = $3)`,
			tenantID, row.sourcePersonID, row.personSource)
		if err != nil {
			return uuid.Nil, "", err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return uuid.Nil, "", err
		}
		switch len(ids) {
		case 0:
			return uuid.Nil, fmt.Sprintf("no person with source_person_id %q", row.sourcePersonID), nil
		case 1:
			return ids[0], "", nil
		default:
			return uuid.Nil, fmt.Sprintf("source_person_id %q matches %d people across sources; add %s", row.sourcePersonID, len(ids), CSVColPersonSource), nil
		}
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
SELECT id FROM security_training_people WHERE tenant_id = $1 AND lower(work_email) = lower($2)`,
		tenantID, row.workEmail).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Sprintf("no person with work_email %q", row.workEmail), nil
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	return id, "", nil
}

// parseCompletionCSV reads r as RFC 4180 CSV with caps. It returns the
// valid rows plus per-row validation failures; the error return is fatal
// (caps exceeded, unreadable CSV, header missing required columns).
func parseCompletionCSV(r io.Reader, lim CSVImportLimits) ([]completionRow, []RowResult, error) {
	if lim.MaxRows <= 0 || lim.MaxFieldBytes <= 0 {
		return nil, nil, fmt.Errorf("securityawareness: CSVImportLimits.MaxRows and MaxFieldBytes must both be > 0")
	}
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // ragged rows are per-row validation errors, not parse aborts
	var (
		cols     map[string]int
		rows     []completionRow
		failures []RowResult
		idx      int
	)
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("securityawareness: csv read: %w", err)
		}
		for _, f := range rec {
			if len(f) > lim.MaxFieldBytes {
				return nil, nil, fmt.Errorf("%w (got %d bytes, cap %d)", ErrCSVFieldTooLarge, len(f), lim.MaxFieldBytes)
			}
		}
		if cols == nil {
			cols, err = mapHeader(rec)
			if err != nil {
				return nil, nil, err
			}
			continue
		}
		idx++
		if idx > lim.MaxRows {
			return nil, nil, fmt.Errorf("%w (cap %d)", ErrCSVTooManyRows, lim.MaxRows)
		}
		row, rowErr := buildRow(idx, cols, rec)
		if rowErr != "" {
			failures = append(failures, RowResult{Row: idx, Status: RowError, Error: rowErr})
			continue
		}
		rows = append(rows, row)
	}
	if cols == nil {
		return nil, nil, fmt.Errorf("%w: empty file", ErrCSVHeader)
	}
	return rows, failures, nil
}

// mapHeader resolves column positions case-insensitively. The header must
// name course_code, campaign_name, completed_at, and at least one person
// key column (work_email or source_person_id). Unknown columns are ignored
// so LMS exports can carry extra fields.
func mapHeader(rec []string) (map[string]int, error) {
	cols := make(map[string]int, len(rec))
	for i, name := range rec {
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\uFEFF")))
		if key == "" {
			continue
		}
		if _, dup := cols[key]; dup {
			return nil, fmt.Errorf("%w: duplicate column %q", ErrCSVHeader, key)
		}
		cols[key] = i
	}
	for _, required := range []string{CSVColCourseCode, CSVColCampaignName, CSVColCompletedAt} {
		if _, ok := cols[required]; !ok {
			return nil, fmt.Errorf("%w: missing %q", ErrCSVHeader, required)
		}
	}
	if _, hasEmail := cols[CSVColWorkEmail]; !hasEmail {
		if _, hasID := cols[CSVColSourcePersonID]; !hasID {
			return nil, fmt.Errorf("%w: need %q or %q", ErrCSVHeader, CSVColWorkEmail, CSVColSourcePersonID)
		}
	}
	return cols, nil
}

// buildRow validates one data record against the contract. The returned
// string is empty on success, else the per-row error message.
func buildRow(idx int, cols map[string]int, rec []string) (completionRow, string) {
	field := func(name string) string {
		i, ok := cols[name]
		if !ok || i >= len(rec) {
			return ""
		}
		// Clone so kept fields don't pin the record's full backing line
		// (encoding/csv backs all of a record's fields with one allocation,
		// and ignored LMS extra columns can be large).
		return strings.Clone(strings.TrimSpace(rec[i]))
	}
	row := completionRow{
		row:            idx,
		workEmail:      field(CSVColWorkEmail),
		sourcePersonID: field(CSVColSourcePersonID),
		personSource:   strings.ToLower(field(CSVColPersonSource)),
		courseCode:     field(CSVColCourseCode),
		campaignName:   field(CSVColCampaignName),
	}
	if row.workEmail == "" && row.sourcePersonID == "" {
		return completionRow{}, fmt.Sprintf("need %s or %s", CSVColWorkEmail, CSVColSourcePersonID)
	}
	if row.personSource != "" && row.personSource != PersonSourceManual && row.personSource != PersonSourceHRIS && row.personSource != PersonSourceSCIM {
		return completionRow{}, fmt.Sprintf("invalid %s %q", CSVColPersonSource, row.personSource)
	}
	if row.courseCode == "" {
		return completionRow{}, fmt.Sprintf("missing %s", CSVColCourseCode)
	}
	if row.campaignName == "" {
		return completionRow{}, fmt.Sprintf("missing %s", CSVColCampaignName)
	}
	completedAt, err := parseCSVTime(field(CSVColCompletedAt))
	if err != nil {
		return completionRow{}, fmt.Sprintf("invalid %s: %v", CSVColCompletedAt, err)
	}
	if completedAt == nil {
		return completionRow{}, fmt.Sprintf("missing %s", CSVColCompletedAt)
	}
	row.completedAt = *completedAt
	phishing, rowErr := buildPhishing(field)
	if rowErr != "" {
		return completionRow{}, rowErr
	}
	row.phishing = phishing
	return row, ""
}

// buildPhishing validates the optional phishing column group. The group is
// keyed on phishing_simulation_id: absent means no phishing result; present
// requires phishing_sent_at and a valid phishing_outcome.
func buildPhishing(field func(string) string) (*PhishingInput, string) {
	simID := field(CSVColPhishSimulationID)
	if simID == "" {
		for _, dependent := range []string{CSVColPhishSentAt, CSVColPhishOutcome, CSVColPhishClickedAt, CSVColPhishReportedAt} {
			if field(dependent) != "" {
				return nil, fmt.Sprintf("%s set without %s", dependent, CSVColPhishSimulationID)
			}
		}
		return nil, ""
	}
	sentAt, err := parseCSVTime(field(CSVColPhishSentAt))
	if err != nil {
		return nil, fmt.Sprintf("invalid %s: %v", CSVColPhishSentAt, err)
	}
	if sentAt == nil {
		return nil, fmt.Sprintf("missing %s", CSVColPhishSentAt)
	}
	outcome := strings.ToLower(field(CSVColPhishOutcome))
	if !validPhishingOutcome(outcome) {
		return nil, fmt.Sprintf("invalid %s %q", CSVColPhishOutcome, outcome)
	}
	clickedAt, err := parseCSVTime(field(CSVColPhishClickedAt))
	if err != nil {
		return nil, fmt.Sprintf("invalid %s: %v", CSVColPhishClickedAt, err)
	}
	reportedAt, err := parseCSVTime(field(CSVColPhishReportedAt))
	if err != nil {
		return nil, fmt.Sprintf("invalid %s: %v", CSVColPhishReportedAt, err)
	}
	return &PhishingInput{
		SimulationID: simID,
		SentAt:       *sentAt,
		Outcome:      outcome,
		ClickedAt:    clickedAt,
		ReportedAt:   reportedAt,
	}, ""
}

// parseCSVTime accepts RFC 3339 timestamps or bare dates (YYYY-MM-DD,
// interpreted as UTC midnight — the common LMS-export shape). Empty input
// returns (nil, nil) so callers distinguish absent from invalid.
func parseCSVTime(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		u := t.UTC()
		return &u, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, fmt.Errorf("want RFC 3339 or YYYY-MM-DD, got %q", v)
	}
	u := t.UTC()
	return &u, nil
}
