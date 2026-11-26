package controlimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/control"
)

// #1782: a reviewer approves a staged row, creating a real control through
// the control store, or rejects it with a reason. Both are one transaction
// in the caller's tenant: the row decision, the control (on approve) and
// the audit row commit together or not at all.

var (
	ErrRowNotFound = errors.New("controlimport: staged row not found")
	// ErrRowDecided is a second approve or reject of a row that is no
	// longer pending. Decisions are final; nothing is written.
	ErrRowDecided = errors.New("controlimport: staged row is already decided")
	// ErrAnchorChoice is an approval naming neither or both of a candidate
	// rank and an SCF code.
	ErrAnchorChoice      = errors.New("controlimport: approve with exactly one of candidate_rank or scf_id")
	ErrCandidateNotFound = errors.New("controlimport: the row has no candidate with that rank")
	// ErrNoBatchRelease is a batch staged when no SCF release was loaded, so
	// there is no release to bind an anchor in.
	ErrNoBatchRelease = errors.New("controlimport: the batch has no SCF release to bind an anchor in")
	// ErrAnchorNotInRelease is an anchor code the batch's SCF release does
	// not have. The anchor is never re-bound in another release.
	ErrAnchorNotInRelease = errors.New("controlimport: SCF anchor is not in the batch's SCF release")
	// ErrBundleIDTaken is an organisation control ID whose bundle ID is
	// already used by a control of the tenant. That row fails; it stays
	// pending.
	ErrBundleIDTaken = errors.New("controlimport: a control with this bundle ID already exists")
	// ErrInvalidApproval is approval input, or the bundle built from the
	// row, that the control store would refuse.
	ErrInvalidApproval = errors.New("controlimport: invalid approval")
	ErrReasonRequired  = errors.New("controlimport: rejection requires a reason")
)

// Row decision statuses.
const (
	RowStatusApproved  = "approved"
	RowStatusRejected  = "rejected"
	RowStatusDuplicate = "duplicate"
)

// OrgControlRefSystem is the external_refs system an imported control's
// organisation control ID is recorded under.
const OrgControlRefSystem = "organisation"

// DefaultImplementationType is the implementation type of an approved row
// whose sheet and approver supplied none.
const DefaultImplementationType = "manual_attested"

// maxRejectReason caps a rejection reason, in characters.
const maxRejectReason = 2000

// beforeControlUpload, when set, runs between the bundle-ID check and the
// upload. Only the integration test sets it, to commit a colliding control
// in that window (the concurrent-approval race). Nil in production.
var beforeControlUpload func(ctx context.Context, bundleID string)

// DecisionStore approves and rejects staged rows. Implementations apply the
// caller's tenant to every read and write (RLS).
type DecisionStore interface {
	ApproveRow(ctx context.Context, in ApproveRowInput) (RowDecision, error)
	RejectRow(ctx context.Context, in RejectRowInput) (RowDecision, error)
	ApproveClear(ctx context.Context, in ApproveClearInput) (ApproveClearResult, error)
}

// ApproveRowInput approves one staged row with one of its candidates
// (CandidateRank) or any anchor of the batch's SCF release (SCFID).
type ApproveRowInput struct {
	BatchID string
	RowID   string
	// Approver is the users(id) of the person approving. A machine
	// credential never approves; the API resolves the caller and refuses
	// one that is not a person.
	Approver      string
	CandidateRank int
	SCFID         string
	// ImplementationType and ApplicabilityExpr override the row's values.
	// Without either the row's is used, then the defaults: manual
	// attestation, applicable everywhere.
	ImplementationType string
	ApplicabilityExpr  map[string]any
	// auditReason is written to the approval's audit row. Only
	// ApproveClear sets it (BulkAuditReason), so an auditor can tell the
	// rows of one bulk action apart; the API cannot.
	auditReason string
}

// RejectRowInput rejects one staged row with a reason.
type RejectRowInput struct {
	BatchID  string
	RowID    string
	Approver string
	Reason   string
}

// RowDecision is the outcome of an approve or reject. Status duplicate is
// an approval that found the organisation control ID already on a control
// of the tenant (DuplicateOf) and created nothing.
type RowDecision struct {
	BatchID         string                `json:"batch_id"`
	RowID           string                `json:"row_id"`
	RowNumber       int                   `json:"row_number"`
	ExternalID      string                `json:"external_id"`
	Status          string                `json:"status"`
	DecidedBy       string                `json:"decided_by"`
	ControlID       string                `json:"control_id,omitempty"`
	BundleID        string                `json:"bundle_id,omitempty"`
	SCFID           string                `json:"scf_id,omitempty"`
	SCFAnchorID     string                `json:"scf_anchor_id,omitempty"`
	SCFRelease      string                `json:"scf_release,omitempty"`
	ExternalRefs    []control.ExternalRef `json:"external_refs,omitempty"`
	AIAssisted      bool                  `json:"ai_assisted"`
	RejectionReason string                `json:"rejection_reason,omitempty"`
	DuplicateOf     string                `json:"duplicate_of,omitempty"`
}

func (in ApproveRowInput) validate() error {
	if strings.TrimSpace(in.Approver) == "" {
		return ErrApproverRequired
	}
	if (in.CandidateRank > 0) == (strings.TrimSpace(in.SCFID) != "") {
		return ErrAnchorChoice
	}
	if in.CandidateRank < 0 {
		return ErrAnchorChoice
	}
	if in.ImplementationType != "" && !allowedImplementationTypes[in.ImplementationType] {
		return fmt.Errorf("%w: implementation_type %q is not one of automated, semi_automated, manual_attested, manual_periodic",
			ErrInvalidApproval, in.ImplementationType)
	}
	return nil
}

func (in RejectRowInput) validate() error {
	if strings.TrimSpace(in.Approver) == "" {
		return ErrApproverRequired
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return ErrReasonRequired
	}
	if len([]rune(reason)) > maxRejectReason {
		return fmt.Errorf("%w: reason exceeds %d characters", ErrInvalidApproval, maxRejectReason)
	}
	return nil
}

// stagedForDecision is a locked pending row with what approval needs from
// its batch.
type stagedForDecision struct {
	BatchID            string
	RowNumber          int
	ExternalID         string
	Title              string
	Description        string
	TestedUnder        []Requirement
	ImplementationType string
	Applicability      string
	FrameworkSlug      string
	SCFRelease         string
}

// lockPendingRow reads and locks the row. A concurrent decision of the same
// row waits here, then sees it decided.
func lockPendingRow(ctx context.Context, tx pgx.Tx, batchID, rowID string) (stagedForDecision, error) {
	if _, err := uuid.Parse(batchID); err != nil {
		return stagedForDecision{}, ErrRowNotFound
	}
	if _, err := uuid.Parse(rowID); err != nil {
		return stagedForDecision{}, ErrRowNotFound
	}
	var r stagedForDecision
	var tested []byte
	var status string
	err := tx.QueryRow(ctx, `
		SELECT r.batch_id::text, r.row_number, r.org_control_id, r.title, r.description, r.tested_under,
		       COALESCE(r.implementation_type, ''), COALESCE(r.applicability, ''), r.status,
		       b.framework_slug, COALESCE(b.scf_release, '')
		FROM control_import_rows r
		JOIN control_import_batches b ON b.id = r.batch_id
		WHERE r.id = $1::uuid AND r.batch_id = $2::uuid
		FOR UPDATE OF r
	`, rowID, batchID).Scan(&r.BatchID, &r.RowNumber, &r.ExternalID, &r.Title, &r.Description, &tested,
		&r.ImplementationType, &r.Applicability, &status, &r.FrameworkSlug, &r.SCFRelease)
	if errors.Is(err, pgx.ErrNoRows) {
		return stagedForDecision{}, ErrRowNotFound
	}
	if err != nil {
		return stagedForDecision{}, fmt.Errorf("controlimport: lock staged row: %w", err)
	}
	if status != RowStatusPending {
		return stagedForDecision{}, fmt.Errorf("%w (status %s)", ErrRowDecided, status)
	}
	if err := json.Unmarshal(tested, &r.TestedUnder); err != nil {
		return stagedForDecision{}, fmt.Errorf("controlimport: decode tested_under: %w", err)
	}
	return r, nil
}

// ApproveRow approves a pending row: it builds a control bundle from the row
// and uploads it through the control store, binding the anchor within the
// batch's SCF release, then records the decision and an audit row. A row
// whose organisation control ID is already a reference of an active control
// is marked duplicate instead, and nothing is created.
func (s *Store) ApproveRow(ctx context.Context, in ApproveRowInput) (RowDecision, error) {
	if err := in.validate(); err != nil {
		return RowDecision{}, err
	}
	var out RowDecision
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID string) error {
		row, err := lockPendingRow(ctx, tx, in.BatchID, in.RowID)
		if err != nil {
			return err
		}
		out = RowDecision{
			BatchID: row.BatchID, RowID: in.RowID, RowNumber: row.RowNumber,
			ExternalID: row.ExternalID, DecidedBy: in.Approver,
		}

		if existing, ok, err := activeControlWithOrgRef(ctx, tx, tenantID, row.ExternalID); err != nil {
			return err
		} else if ok {
			out.Status = RowStatusDuplicate
			out.DuplicateOf = existing
			return markDuplicate(ctx, tx, tenantID, in, row, existing)
		}

		if row.SCFRelease == "" {
			return ErrNoBatchRelease
		}
		scfID, aiAssisted, err := chosenAnchor(ctx, tx, in)
		if err != nil {
			return err
		}
		bundle, err := bundleForRow(row, scfID, in)
		if err != nil {
			return err
		}
		var taken bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM controls WHERE tenant_id = $1::uuid AND bundle_id = $2)
		`, tenantID, bundle.Manifest.BundleID).Scan(&taken); err != nil {
			return fmt.Errorf("controlimport: check bundle id: %w", err)
		}
		if taken {
			return fmt.Errorf("%w: %s (from %s)", ErrBundleIDTaken, bundle.Manifest.BundleID, row.ExternalID)
		}
		if beforeControlUpload != nil {
			beforeControlUpload(ctx, bundle.Manifest.BundleID)
		}

		res, err := control.UploadTx(ctx, tx, bundle, in.Approver)
		switch {
		case errors.Is(err, control.ErrSCFCodeNotInRelease), errors.Is(err, control.ErrSCFReleaseUnknown):
			return fmt.Errorf("%w: %s is not in SCF release %s", ErrAnchorNotInRelease, scfID, row.SCFRelease)
		case err != nil:
			return fmt.Errorf("controlimport: upload control: %w", err)
		case !res.IsNewBundle:
			// The check above saw no control with this bundle ID, but a
			// concurrent approval committed one since: the upload found it
			// and versioned it (or matched it). Roll back, so another row's
			// control is never changed and this row stays pending.
			return fmt.Errorf("%w: %s (from %s), created concurrently", ErrBundleIDTaken, bundle.Manifest.BundleID, row.ExternalID)
		}

		out.Status = RowStatusApproved
		out.ControlID = res.ControlID.String()
		out.BundleID = res.BundleID
		out.SCFID = scfID
		out.SCFAnchorID = res.AnchorID.String()
		out.SCFRelease = res.SCFRelease
		out.ExternalRefs = bundle.Manifest.ExternalRefs
		out.AIAssisted = aiAssisted

		if _, err := tx.Exec(ctx, `
			UPDATE control_import_rows
			SET status = 'approved', decided_by = $2, decided_at = now(),
			    control_id = $3::uuid, bundle_id = $4, scf_anchor_id = $5::uuid, scf_id = $6,
			    implementation_type = $7, ai_assisted = $8,
			    human_approved = true, human_approver = $2, updated_at = now()
			WHERE id = $1::uuid
		`, in.RowID, in.Approver, out.ControlID, out.BundleID, out.SCFAnchorID, scfID,
			bundle.Manifest.ImplementationType, aiAssisted); err != nil {
			return fmt.Errorf("controlimport: record approval: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE control_import_candidates
			SET human_approved = true, human_approver = $2
			WHERE row_id = $1::uuid AND scf_id = $3
		`, in.RowID, in.Approver, scfID); err != nil {
			return fmt.Errorf("controlimport: record approved candidate: %w", err)
		}
		return insertAudit(ctx, tx, tenantID, auditEntry{
			BatchID: row.BatchID, RowID: in.RowID, RowNumber: row.RowNumber, OrgControlID: row.ExternalID,
			Action: "approve", Actor: in.Approver,
			SCFAnchorID: out.SCFAnchorID, SCFID: scfID, SCFRelease: out.SCFRelease,
			ControlID: out.ControlID, BundleID: out.BundleID, Reason: in.auditReason,
		})
	})
	if err != nil {
		return RowDecision{}, err
	}
	return out, nil
}

// RejectRow rejects a pending row with a reason and records an audit row.
func (s *Store) RejectRow(ctx context.Context, in RejectRowInput) (RowDecision, error) {
	if err := in.validate(); err != nil {
		return RowDecision{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	var out RowDecision
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID string) error {
		row, err := lockPendingRow(ctx, tx, in.BatchID, in.RowID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE control_import_rows
			SET status = 'rejected', rejection_reason = $2, decided_by = $3, decided_at = now(), updated_at = now()
			WHERE id = $1::uuid
		`, in.RowID, reason, in.Approver); err != nil {
			return fmt.Errorf("controlimport: record rejection: %w", err)
		}
		out = RowDecision{
			BatchID: row.BatchID, RowID: in.RowID, RowNumber: row.RowNumber, ExternalID: row.ExternalID,
			Status: RowStatusRejected, DecidedBy: in.Approver, RejectionReason: reason,
		}
		return insertAudit(ctx, tx, tenantID, auditEntry{
			BatchID: row.BatchID, RowID: in.RowID, RowNumber: row.RowNumber, OrgControlID: row.ExternalID,
			Action: "reject", Actor: in.Approver, Reason: reason,
		})
	})
	if err != nil {
		return RowDecision{}, err
	}
	return out, nil
}

// chosenAnchor returns the SCF code the approval binds, and whether the LLM
// placed it: a candidate by rank, or any code (which inherits a matching
// candidate's AI flag).
func chosenAnchor(ctx context.Context, tx pgx.Tx, in ApproveRowInput) (string, bool, error) {
	var scfID string
	var ai bool
	if in.CandidateRank > 0 {
		err := tx.QueryRow(ctx, `
			SELECT scf_id, ai_assisted FROM control_import_candidates WHERE row_id = $1::uuid AND rank = $2
		`, in.RowID, in.CandidateRank).Scan(&scfID, &ai)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, fmt.Errorf("%w: rank %d", ErrCandidateNotFound, in.CandidateRank)
		}
		if err != nil {
			return "", false, fmt.Errorf("controlimport: read candidate: %w", err)
		}
		return scfID, ai, nil
	}
	scfID = strings.TrimSpace(in.SCFID)
	if _, err := uuid.Parse(scfID); err == nil {
		// An anchor id: name it by its code, and only within the batch's
		// SCF release. Mapping an id from another release to the same code
		// here would silently re-bind it.
		err := tx.QueryRow(ctx, `
			SELECT a.scf_id
			FROM scf_anchors a
			JOIN control_import_batches b ON b.scf_framework_version_id = a.framework_version_id
			WHERE a.id = $1::uuid AND b.id = $2::uuid
		`, scfID, in.BatchID).Scan(&scfID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, fmt.Errorf("%w: anchor %s is not in the batch's SCF release", ErrAnchorNotInRelease, in.SCFID)
		}
		if err != nil {
			return "", false, fmt.Errorf("controlimport: resolve anchor id: %w", err)
		}
	}
	err := tx.QueryRow(ctx, `
		SELECT ai_assisted FROM control_import_candidates WHERE row_id = $1::uuid AND scf_id = $2
	`, in.RowID, scfID).Scan(&ai)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("controlimport: read candidate: %w", err)
	}
	return scfID, ai, nil
}

// activeControlWithOrgRef finds an active control of the tenant carrying the
// organisation control ID as a reference, under any system: a control
// written by hand with the ID counts as well as an earlier import.
func activeControlWithOrgRef(ctx context.Context, tx pgx.Tx, tenantID, orgID string) (string, bool, error) {
	var id string
	err := tx.QueryRow(ctx, `
		SELECT id::text FROM controls
		WHERE tenant_id = $1::uuid AND superseded_by IS NULL
		  AND external_refs @> jsonb_build_array(jsonb_build_object('kind', 'reference', 'id', $2::text))
		ORDER BY created_at
		LIMIT 1
	`, tenantID, orgID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("controlimport: check duplicate control: %w", err)
	}
	return id, true, nil
}

func markDuplicate(ctx context.Context, tx pgx.Tx, tenantID string, in ApproveRowInput, row stagedForDecision, existing string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE control_import_rows
		SET status = 'duplicate', decided_by = $2, decided_at = now(), control_id = $3::uuid, updated_at = now()
		WHERE id = $1::uuid
	`, in.RowID, in.Approver, existing); err != nil {
		return fmt.Errorf("controlimport: record duplicate: %w", err)
	}
	return insertAudit(ctx, tx, tenantID, auditEntry{
		BatchID: row.BatchID, RowID: in.RowID, RowNumber: row.RowNumber, OrgControlID: row.ExternalID,
		Action: "duplicate", Actor: in.Approver, ControlID: existing, Reason: in.auditReason,
	})
}

// bundleForRow builds the control bundle an approved row becomes. It goes
// through the same YAML parse and validation as an uploaded control.yaml.
func bundleForRow(row stagedForDecision, scfID string, in ApproveRowInput) (*control.Bundle, error) {
	implType := firstNonEmpty(in.ImplementationType, row.ImplementationType, DefaultImplementationType)
	appl := in.ApplicabilityExpr
	if appl == nil && strings.TrimSpace(row.Applicability) != "" {
		if err := json.Unmarshal([]byte(row.Applicability), &appl); err != nil {
			return nil, fmt.Errorf("%w: the row's applicability %q is not an applicability expression; pass applicability_expr",
				ErrInvalidApproval, row.Applicability)
		}
	}
	refs := []control.ExternalRef{{System: OrgControlRefSystem, Kind: control.ExternalRefKindReference, ID: row.ExternalID}}
	for _, r := range row.TestedUnder {
		refs = append(refs, control.ExternalRef{System: row.FrameworkSlug, Kind: control.ExternalRefKindTestedUnder, ID: r.Code})
	}
	m := control.Manifest{
		BundleSchemaVersion: control.BundleSchemaVersion,
		BundleID:            BundleIDFor(row.ExternalID),
		Title:               row.Title,
		Description:         row.Description,
		SCFAnchorID:         scfID,
		SCFRelease:          row.SCFRelease,
		ImplementationType:  implType,
		ApplicabilityExpr:   appl,
		ExternalRefs:        refs,
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("controlimport: encode control bundle: %w", err)
	}
	b, err := control.FinalizeBundleForHTTP(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidApproval, strings.TrimPrefix(err.Error(), "control bundle: "))
	}
	if err := b.ValidateApplicabilityExpr(); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidApproval, strings.TrimPrefix(err.Error(), "control bundle: "))
	}
	return b, nil
}

var bundleIDUnsafe = regexp.MustCompile(`[^a-z0-9_.-]+`)

// BundleIDFor slugs an organisation control ID into a bundle ID matching
// the control bundle pattern ^[a-z][a-z0-9_.-]{2,63}$: lower-cased, runs of
// other characters become "-", and an ID that does not start with a letter
// is prefixed "c-", as is one that would read as a uuid. Two IDs can slug
// alike ("AC 01", "AC-01"); approval refuses the second (ErrBundleIDTaken).
func BundleIDFor(orgID string) string {
	s := bundleIDUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(orgID)), "-")
	s = strings.Trim(s, "-.")
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "c-" + s
	}
	for len(s) < 3 {
		s += "-"
	}
	if len(s) > 64 {
		s = strings.TrimRight(s[:64], "-.")
	}
	// A uuid-shaped bundle ID is refused by the bundle validator (#2255).
	// An organisation ID that is a uuid starting a-f slugs to one, and so can
	// a longer ID cut back to a uuid above; checked last, on the final slug.
	if _, err := uuid.Parse(s); err == nil {
		s = "c-" + s
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

type auditEntry struct {
	BatchID, RowID, OrgControlID, Action, Actor string
	RowNumber                                   int
	SCFAnchorID, SCFID, SCFRelease              string
	ControlID, BundleID, Reason                 string
}

func insertAudit(ctx context.Context, tx pgx.Tx, tenantID string, e auditEntry) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO control_import_audit_log (
			tenant_id, batch_id, row_id, row_number, org_control_id, action, actor,
			scf_anchor_id, scf_id, scf_release, control_id, bundle_id, reason)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7,
			$8::uuid, $9, $10, $11::uuid, $12, $13)
	`, tenantID, e.BatchID, e.RowID, e.RowNumber, e.OrgControlID, e.Action, e.Actor,
		nullable(e.SCFAnchorID), nullable(e.SCFID), nullable(e.SCFRelease),
		nullable(e.ControlID), nullable(e.BundleID), nullable(e.Reason)); err != nil {
		return fmt.Errorf("controlimport: write audit row: %w", err)
	}
	return nil
}
