package policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Import outcomes, one per file, reported by Store.Import (#1747).
const (
	// ImportStatusImported: the policy was written.
	ImportStatusImported = "imported"
	// ImportStatusSkipped: a policy with the same title and version
	// already exists in the tenant, or earlier in the same import.
	ImportStatusSkipped = "skipped"
	// ImportStatusFailed: the file is invalid; Reason says why.
	ImportStatusFailed = "failed"
	// ImportStatusNotImported: the file is valid, but another file failed
	// and the import was not partial, so nothing was written.
	ImportStatusNotImported = "not_imported"
)

// auditActionImported is the policy_audit_log action for an import.
const auditActionImported = "imported"

// MaxImportFieldLen caps the free-text import fields: title, historical
// approver name and source file name. The migration enforces the same
// cap on historical_approver_name and policy_audit_log.source_file.
const MaxImportFieldLen = 500

// ImportInput is one parsed policy file. internal/policy/importfile builds
// it from Markdown with YAML front matter.
type ImportInput struct {
	SourceFile                  string
	Title                       string
	Version                     string
	BodyMd                      string
	OwnerRole                   string
	ApproverRole                string
	HistoricalApproverName      string
	HistoricalApprovedAt        time.Time
	EffectiveDate               time.Time
	NextReviewAt                *time.Time
	SourceURL                   string
	LinkedControlRefs           []string // control UUIDs or control bundle IDs
	AcknowledgmentRequiredRoles []string
}

// ImportResult is the outcome for one file.
type ImportResult struct {
	SourceFile string
	Status     string
	Reason     string
	Policy     *Policy // set when Status is ImportStatusImported
}

// ImportReport is the outcome of one Import call. Committed is false when
// any file failed and the import was not partial; nothing was written.
type ImportReport struct {
	Committed bool
	Results   []ImportResult
}

// Failed reports whether any file failed.
func (r ImportReport) Failed() bool {
	for _, res := range r.Results {
		if res.Status == ImportStatusFailed {
			return true
		}
	}
	return false
}

// Import writes an existing policy library as published policies that keep
// their historical lifecycle (#1747). The caller gates it on an admin
// credential; the in-tool approver-role and transition checks do not
// apply. importedBy is the importing credential id.
//
// parseFailures are files the caller could not parse; they are reported as
// failed alongside items that fail here (an unknown linked control). A
// title and version already present in the tenant is skipped, not failed.
// Unless partial is set, any failure writes nothing. Each written policy
// gets one policy_audit_log row. Everything runs in one transaction.
func (s *Store) Import(ctx context.Context, items []ImportInput, parseFailures []ImportResult, importedBy string, partial bool) (ImportReport, error) {
	if importedBy == "" {
		return ImportReport{}, ErrCreatedByRequired
	}
	report := ImportReport{}
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		results := append([]ImportResult{}, parseFailures...)
		type pending struct {
			idx   int // into results
			in    ImportInput
			links []uuid.UUID
		}
		var toWrite []pending
		seen := map[[2]string]bool{}
		for _, in := range items {
			res := ImportResult{SourceFile: in.SourceFile}
			if err := validateImport(in); err != nil {
				res.Status, res.Reason = ImportStatusFailed, err.Error()
				results = append(results, res)
				continue
			}
			links, reason, err := resolveControls(ctx, q, tenantID, in.LinkedControlRefs)
			if err != nil {
				return err
			}
			if reason != "" {
				res.Status, res.Reason = ImportStatusFailed, reason
				results = append(results, res)
				continue
			}
			key := [2]string{in.Title, in.Version}
			exists, err := q.PolicyTitleVersionExists(ctx, dbx.PolicyTitleVersionExistsParams{
				TenantID: pgUUID(tenantID),
				Title:    in.Title,
				Version:  in.Version,
			})
			if err != nil {
				return fmt.Errorf("import: duplicate check: %w", err)
			}
			if exists || seen[key] {
				res.Status = ImportStatusSkipped
				res.Reason = fmt.Sprintf("a policy titled %q at version %q already exists", in.Title, in.Version)
				results = append(results, res)
				continue
			}
			seen[key] = true
			results = append(results, res)
			toWrite = append(toWrite, pending{idx: len(results) - 1, in: in, links: links})
		}

		report.Results = results
		if report.Failed() && !partial {
			for _, p := range toWrite {
				report.Results[p.idx].Status = ImportStatusNotImported
				report.Results[p.idx].Reason = "another file failed validation; nothing was written (import with partial to write the valid files)"
			}
			return errAbortImport
		}
		for _, p := range toWrite {
			written, err := importOne(ctx, q, tenantID, p.in, p.links, importedBy)
			if err != nil {
				return err
			}
			report.Results[p.idx].Status = ImportStatusImported
			report.Results[p.idx].Policy = &written
		}
		return nil
	})
	if errors.Is(err, errAbortImport) {
		return report, nil
	}
	if err != nil {
		return ImportReport{}, err
	}
	report.Committed = true
	return report, nil
}

// errAbortImport rolls the import transaction back without surfacing an
// error: the report already says which files failed.
var errAbortImport = errors.New("policy: import aborted")

func importOne(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, in ImportInput, links []uuid.UUID, importedBy string) (Policy, error) {
	ackRoles := in.AcknowledgmentRequiredRoles
	if ackRoles == nil {
		ackRoles = []string{}
	}
	nextReview := pgtype.Timestamptz{}
	if in.NextReviewAt != nil {
		nextReview = pgtype.Timestamptz{Time: in.NextReviewAt.UTC(), Valid: true}
	}
	row, err := q.ImportPublishedPolicy(ctx, dbx.ImportPublishedPolicyParams{
		ID:                          pgUUID(uuid.New()),
		TenantID:                    pgUUID(tenantID),
		Title:                       in.Title,
		Version:                     in.Version,
		BodyMd:                      in.BodyMd,
		OwnerRole:                   in.OwnerRole,
		ApproverRole:                in.ApproverRole,
		LinkedControlIds:            uuidsToPg(links),
		AcknowledgmentRequiredRoles: ackRoles,
		EffectiveDate:               pgDate(in.EffectiveDate),
		NextReviewAt:                nextReview,
		ImportedBy:                  importedBy,
		HistoricalApproverName:      stringPtr(in.HistoricalApproverName),
		HistoricalApprovedAt:        pgDate(in.HistoricalApprovedAt),
		SourceUrl:                   stringPtr(in.SourceURL),
	})
	if err != nil {
		return Policy{}, mapCreateError(err)
	}
	if _, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
		TenantID:               pgUUID(tenantID),
		PolicyID:               row.ID,
		Action:                 auditActionImported,
		Actor:                  importedBy,
		SourceFile:             in.SourceFile,
		HistoricalApproverName: stringPtr(in.HistoricalApproverName),
		HistoricalApprovedAt:   pgDate(in.HistoricalApprovedAt),
		SourceUrl:              stringPtr(in.SourceURL),
	}); err != nil {
		return Policy{}, fmt.Errorf("import: audit log: %w", err)
	}
	return policyFromRow(row), nil
}

// resolveControls maps each reference to a control id in the tenant: a
// UUID names controls.id, anything else names the active version of a
// control bundle. reason is non-empty when a reference does not resolve.
func resolveControls(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, refs []string) (ids []uuid.UUID, reason string, err error) {
	ids = make([]uuid.UUID, 0, len(refs))
	seen := map[uuid.UUID]bool{}
	for _, ref := range refs {
		var id pgtype.UUID
		if u, perr := uuid.Parse(ref); perr == nil {
			row, gerr := q.GetControlByID(ctx, dbx.GetControlByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(u)})
			id, err = row.ID, gerr
		} else {
			row, gerr := q.GetActiveControlByBundleID(ctx, dbx.GetActiveControlByBundleIDParams{TenantID: pgUUID(tenantID), BundleID: ref})
			id, err = row.ID, gerr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Sprintf("unknown linked control %q", ref), nil
		}
		if err != nil {
			return nil, "", fmt.Errorf("import: resolve control %q: %w", ref, err)
		}
		u := uuid.UUID(id.Bytes)
		if !seen[u] {
			seen[u] = true
			ids = append(ids, u)
		}
	}
	return ids, "", nil
}

// validateImport re-checks what the parser guarantees, so a caller that
// builds ImportInput directly cannot write a row the CHECKs would reject
// with a less useful message.
func validateImport(in ImportInput) error {
	switch {
	case in.Title == "":
		return ErrTitleRequired
	case in.Version == "":
		return ErrVersionRequired
	case in.BodyMd == "":
		return ErrBodyRequired
	case in.OwnerRole == "":
		return ErrOwnerRoleRequired
	case in.ApproverRole == "":
		return ErrApproverRoleRequired
	case in.HistoricalApproverName == "":
		return errors.New("policy: historical approver name is required")
	case in.HistoricalApprovedAt.IsZero():
		return errors.New("policy: historical approval date is required")
	case in.EffectiveDate.IsZero():
		return errors.New("policy: effective date is required")
	case len(in.Title) > MaxImportFieldLen:
		return fmt.Errorf("policy: title is longer than %d characters", MaxImportFieldLen)
	case len(in.HistoricalApproverName) > MaxImportFieldLen:
		return fmt.Errorf("policy: historical approver name is longer than %d characters", MaxImportFieldLen)
	case len(in.SourceFile) > MaxImportFieldLen:
		return fmt.Errorf("policy: file name is longer than %d characters", MaxImportFieldLen)
	}
	return nil
}
