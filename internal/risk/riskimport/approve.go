package riskimport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// A person approves every pending row of a batch, or rejects one row with a
// reason. Each row is decided in its own transaction: the row decision, the
// risk (on approve) with its `created` risk audit row, and the import audit
// row commit together or not at all, and one row's failure never rolls back
// another's risk.

// ApproveInput approves every pending row of one batch.
type ApproveInput struct {
	BatchID string
	// Approver is the users(id) of the person approving. A machine
	// credential never approves; the API resolves the caller and refuses
	// one that is not a person. It is the actor on each risk's `created`
	// audit row.
	Approver string
}

// ApproveRowInput approves one staged row.
type ApproveRowInput struct {
	BatchID  string
	RowID    string
	Approver string
}

// RejectRowInput rejects one staged row with a reason.
type RejectRowInput struct {
	BatchID  string
	RowID    string
	Approver string
	Reason   string
}

// RowDecision is the outcome of deciding one row. Status duplicate is an
// approval that found the row's organisation risk ID already on a risk of
// the tenant (RiskID) and created nothing. Status rejected from an
// approval is a row with a category or theme value the batch has not
// mapped, named in RejectionReason (#2844).
type RowDecision struct {
	BatchID         string `json:"batch_id"`
	RowID           string `json:"row_id"`
	RowNumber       int    `json:"row_number"`
	ExternalRef     string `json:"external_ref,omitempty"`
	Status          string `json:"status"`
	DecidedBy       string `json:"decided_by"`
	RiskID          string `json:"risk_id,omitempty"`
	RejectionReason string `json:"rejection_reason,omitempty"`
	// ParentLinkStatus, ParentLinkError and ParentRiskID are how linking an
	// approved row's risk under its parent went (parentlink.go): none,
	// waiting on a pending parent row, linked, or failed with the reason.
	ParentLinkStatus string `json:"parent_link_status,omitempty"`
	ParentLinkError  string `json:"parent_link_error,omitempty"`
	ParentRiskID     string `json:"parent_risk_id,omitempty"`
}

// ApproveSkip is a pending row that was not approved, with why. It stays as
// it was: pending, or decided by someone else meanwhile.
type ApproveSkip struct {
	RowID       string `json:"row_id"`
	RowNumber   int    `json:"row_number"`
	ExternalRef string `json:"external_ref,omitempty"`
	Reason      string `json:"reason"`
}

// ApproveResult reports an approval of a batch's pending rows.
type ApproveResult struct {
	BatchID       string        `json:"batch_id"`
	ApprovedCount int           `json:"approved_count"`
	Approved      []RowDecision `json:"approved"`
	Duplicates    []RowDecision `json:"duplicates"`
	// Rejected are the rows approval rejected for an unmapped category or
	// theme value (#2844).
	Rejected []RowDecision `json:"rejected"`
	Skipped  []ApproveSkip `json:"skipped"`
}

// pendingRow is a pending row of the batch, as listed before approval.
type pendingRow struct {
	ID          string
	RowNumber   int
	ExternalRef string
}

// lockedRow is a locked pending row with what approval needs.
type lockedRow struct {
	BatchID       string
	RowNumber     int
	ExternalRef   string
	Title         string
	Description   string
	CategoryValue string
	ThemeValues   []string
	ReviewDue     *time.Time
	Methodology   dbx.RiskMethodology
	InherentScore []byte
	ResidualScore []byte
	Treatment     dbx.RiskTreatment
	Justification string
	ProposedUntil *time.Time
	Instrument    string
	ControlIDs    []uuid.UUID
	OwnerUserID   *uuid.UUID
	OwnerUnlinked string
	Level         dbx.RiskLevel
	OrgUnitID     *uuid.UUID
	ParentRef     string
}

// ApprovePending approves every pending row of the batch. A row that fails
// for its own reason (decided meanwhile, a category archived since upload,
// a value the risk store refuses) is skipped and stays as it was; any other
// error stops the run and is returned, with the rows already approved left
// approved.
func (s *Store) ApprovePending(ctx context.Context, in ApproveInput) (ApproveResult, error) {
	if strings.TrimSpace(in.Approver) == "" {
		return ApproveResult{}, ErrApproverRequired
	}
	pending, err := s.pendingRows(ctx, in.BatchID)
	if err != nil {
		return ApproveResult{}, err
	}
	res := ApproveResult{
		BatchID: in.BatchID, Approved: []RowDecision{}, Duplicates: []RowDecision{},
		Rejected: []RowDecision{}, Skipped: []ApproveSkip{},
	}
	for _, p := range pending {
		d, err := s.approveRowRetrying(ctx, in, p.ID)
		if reason, skip := skipReason(err); skip {
			res.Skipped = append(res.Skipped, ApproveSkip{RowID: p.ID, RowNumber: p.RowNumber, ExternalRef: p.ExternalRef, Reason: reason})
			continue
		}
		if err != nil {
			return res, err
		}
		switch d.Status {
		case RowStatusDuplicate:
			res.Duplicates = append(res.Duplicates, d)
			continue
		case RowStatusRejected:
			res.Rejected = append(res.Rejected, d)
			continue
		}
		res.Approved = append(res.Approved, d)
	}
	res.ApprovedCount = len(res.Approved)
	return res, nil
}

// ApproveRow approves one pending row: it becomes a risk, or is marked
// duplicate, exactly as ApprovePending decides each of its rows. A row that
// is not pending is ErrRowDecided; a value the risk store refuses is
// returned as the store's error and the row stays pending.
func (s *Store) ApproveRow(ctx context.Context, in ApproveRowInput) (RowDecision, error) {
	if strings.TrimSpace(in.Approver) == "" {
		return RowDecision{}, ErrApproverRequired
	}
	return s.approveRowRetrying(ctx, ApproveInput{BatchID: in.BatchID, Approver: in.Approver}, in.RowID)
}

// approveRowRetrying approves one row, retrying once when a risk with the
// row's ID was created after the duplicate check read the risks: the retry
// finds it and marks the row duplicate.
func (s *Store) approveRowRetrying(ctx context.Context, in ApproveInput, rowID string) (RowDecision, error) {
	d, err := s.approveRow(ctx, in, rowID)
	if errors.Is(err, risk.ErrExternalRefTaken) {
		d, err = s.approveRow(ctx, in, rowID)
	}
	return d, err
}

// skipReason reports whether err is a row's own failure, and its text.
func skipReason(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, ErrRowDecided),
		errors.Is(err, risk.ErrExternalRefTaken),
		risk.IsCategoryRefusal(err),
		errors.Is(err, risk.ErrUnknownTheme),
		risk.IsRecordValidation(err),
		risk.IsTreatmentValidation(err),
		risk.IsPlacementValidation(err),
		risk.IsTransitionInput(err):
		return strings.TrimPrefix(strings.TrimPrefix(err.Error(), "riskimport: "), "risk: "), true
	default:
		return "", false
	}
}

// IsRowRefusal reports whether err is a refusal of the row's own values by
// the risk store (an archived category, a value it rejects, an ID taken
// meanwhile): the row stays pending and the caller can fix and retry.
func IsRowRefusal(err error) bool {
	_, skip := skipReason(err)
	return skip && !errors.Is(err, ErrRowDecided)
}

func (s *Store) pendingRows(ctx context.Context, batchID string) ([]pendingRow, error) {
	if _, err := uuid.Parse(batchID); err != nil {
		return nil, ErrBatchNotFound
	}
	var out []pendingRow
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM risk_import_batches WHERE id = $1::uuid)`, batchID).Scan(&exists); err != nil {
			return fmt.Errorf("riskimport: read batch: %w", err)
		}
		if !exists {
			return ErrBatchNotFound
		}
		rows, err := tx.Query(ctx, `
			SELECT id::text, row_number, COALESCE(external_ref, '')
			FROM risk_import_rows
			WHERE batch_id = $1::uuid AND status = 'pending'
			ORDER BY row_number
		`, batchID)
		if err != nil {
			return fmt.Errorf("riskimport: list pending rows: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p pendingRow
			if err := rows.Scan(&p.ID, &p.RowNumber, &p.ExternalRef); err != nil {
				return fmt.Errorf("riskimport: scan pending row: %w", err)
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// approveRow approves one pending row in its own transaction: a row whose
// organisation risk ID is already on a risk of the tenant is marked
// duplicate and nothing is created; a row with a category or theme value
// the batch has not mapped is rejected with those values as the reason;
// any other becomes a risk through the risk store, with the category and
// themes its values map to. Re-importing never updates an existing risk.
func (s *Store) approveRow(ctx context.Context, in ApproveInput, rowID string) (RowDecision, error) {
	var out RowDecision
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		row, err := lockPendingRow(ctx, tx, in.BatchID, rowID)
		if err != nil {
			return err
		}
		out = RowDecision{BatchID: row.BatchID, RowID: rowID, RowNumber: row.RowNumber, ExternalRef: row.ExternalRef, DecidedBy: in.Approver}

		if existing, ok, err := riskWithExternalRef(ctx, tx, tenantID, row.ExternalRef); err != nil {
			return err
		} else if ok {
			out.Status, out.RiskID = RowStatusDuplicate, existing
			if err := recordDecision(ctx, tx, rowID, RowStatusDuplicate, in.Approver, existing, ""); err != nil {
				return err
			}
			// Nothing is created, so the row links nothing; rows waiting
			// on it link under the risk it duplicates.
			if err := settleWaitingOn(ctx, tx, tenantID, row, decision{in.Approver, rowID, row.RowNumber}); err != nil {
				return err
			}
			return finishDecision(ctx, tx, tenantID, out, "duplicate", "")
		}

		// The map is read FOR SHARE: a mapping change waits for this
		// decision, so the row takes the mapping it was decided under.
		_, targets, err := readValueMaps(ctx, tx, row.BatchID, true)
		if err != nil {
			return err
		}
		category, themes, unmapped := targets.resolveRow(row.CategoryValue, row.ThemeValues)
		if len(unmapped) > 0 {
			reason := "not mapped in this import batch: " + strings.Join(unmapped, ", ")
			out.Status, out.RejectionReason = RowStatusRejected, reason
			if err := recordDecision(ctx, tx, rowID, RowStatusRejected, in.Approver, "", reason); err != nil {
				return err
			}
			if err := settleWaitingOn(ctx, tx, tenantID, row, decision{in.Approver, rowID, row.RowNumber}); err != nil {
				return err
			}
			return finishDecision(ctx, tx, tenantID, out, "reject", reason)
		}

		controls, err := existingControls(ctx, tx, tenantID, row.ControlIDs)
		if err != nil {
			return err
		}
		owner, unlinked, err := approvalOwner(ctx, tx, tenantID, row.OwnerUserID, row.OwnerUnlinked)
		if err != nil {
			return err
		}
		// An accepted row proposes its acceptance; the create path never
		// approves one, and the register's accepter is not sent (#2371).
		status, justification, until := proposalOf(row.Treatment, row.Justification, row.ProposedUntil)
		created, err := s.risks.CreateTx(ctx, dbx.New(tx), tenantID, risk.CreateInput{
			Title:       row.Title,
			Description: row.Description,
			CategoryRef: category,
			Themes:      themes,
			ReviewDueAt: row.ReviewDue,
			ExternalRef: row.ExternalRef,
			// The batch's one methodology, and the scores staged in its
			// shape; an unscored row creates an unscored risk (#2841).
			Methodology:   row.Methodology,
			InherentScore: row.InherentScore,
			ResidualScore: row.ResidualScore,
			// The staged treatment, already downgraded where the row could
			// not back it, under the create path's treatment rules (#2842).
			Treatment:           row.Treatment,
			AcceptanceStatus:    status,
			Justification:       justification,
			ProposedUntil:       until,
			InstrumentReference: row.Instrument,
			LinkedControlIDs:    controls,
			// The owner matched by email at staging, or the cell as the
			// unlinked owner (#2843).
			OwnerUserID:    owner,
			TreatmentOwner: unlinked,
			// The level and org unit staged from the row; the create path
			// re-checks the placement rule against the unit as it is now
			// (#2845).
			Level:     row.Level,
			OrgUnitID: row.OrgUnitID,
			// The approving person creates the risk and is named on its
			// `created` audit row (#2835).
			Actor:            in.Approver,
			Proposer:         in.Approver,
			ProposerIsPerson: true,
		})
		if err != nil {
			return err
		}
		out.Status, out.RiskID = RowStatusApproved, created.ID.String()
		if err := recordDecision(ctx, tx, rowID, RowStatusApproved, in.Approver, out.RiskID, ""); err != nil {
			return err
		}
		// The risk exists now: link it under its parent, then link the
		// rows waiting on it. A link the hierarchy rules refuse fails on
		// its row and the risk stays imported (#2846).
		//
		// The lock is taken here, after the create, and only the link
		// reads below come after it. That is enough under READ COMMITTED
		// (each statement reads a fresh snapshot), and not under
		// REPEATABLE READ, where the snapshot predates the lock and a
		// child and its parent approved concurrently can each miss the
		// other (parentlink.go).
		if row.ParentRef != "" || row.ExternalRef != "" {
			if err := lockHierarchy(ctx, tx, tenantID); err != nil {
				return err
			}
		}
		if row.ParentRef != "" {
			link, err := linkOwnParent(ctx, tx, tenantID, row.BatchID, decision{in.Approver, rowID, row.RowNumber}, created.ID, row.ParentRef)
			if err != nil {
				return err
			}
			out.ParentLinkStatus, out.ParentLinkError, out.ParentRiskID = link.Status, link.Error, link.ParentID
		}
		if err := settleWaiting(ctx, tx, tenantID, row.BatchID, row.ExternalRef, decision{in.Approver, rowID, row.RowNumber}); err != nil {
			return err
		}
		return finishDecision(ctx, tx, tenantID, out, "approve", "")
	})
	if err != nil {
		return RowDecision{}, err
	}
	return out, nil
}

// RejectRow rejects one pending row with a reason and records an audit row.
func (s *Store) RejectRow(ctx context.Context, in RejectRowInput) (RowDecision, error) {
	if strings.TrimSpace(in.Approver) == "" {
		return RowDecision{}, ErrApproverRequired
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return RowDecision{}, ErrReasonRequired
	}
	if len([]rune(reason)) > maxRejectReason {
		return RowDecision{}, fmt.Errorf("%w: reason exceeds %d characters", ErrInvalidDecision, maxRejectReason)
	}
	var out RowDecision
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		row, err := lockPendingRow(ctx, tx, in.BatchID, in.RowID)
		if err != nil {
			return err
		}
		out = RowDecision{
			BatchID: row.BatchID, RowID: in.RowID, RowNumber: row.RowNumber, ExternalRef: row.ExternalRef,
			Status: RowStatusRejected, DecidedBy: in.Approver, RejectionReason: reason,
		}
		if err := recordDecision(ctx, tx, in.RowID, RowStatusRejected, in.Approver, "", reason); err != nil {
			return err
		}
		// Rows waiting on a rejected parent row fail their link.
		if err := settleWaitingOn(ctx, tx, tenantID, row, decision{in.Approver, in.RowID, row.RowNumber}); err != nil {
			return err
		}
		return finishDecision(ctx, tx, tenantID, out, "reject", reason)
	})
	if err != nil {
		return RowDecision{}, err
	}
	return out, nil
}

// lockPendingRow reads and locks the row. A concurrent decision of the same
// row waits here, then sees it decided.
func lockPendingRow(ctx context.Context, tx pgx.Tx, batchID, rowID string) (lockedRow, error) {
	if _, err := uuid.Parse(batchID); err != nil {
		return lockedRow{}, ErrRowNotFound
	}
	if _, err := uuid.Parse(rowID); err != nil {
		return lockedRow{}, ErrRowNotFound
	}
	var r lockedRow
	var status string
	var reviewDue *time.Time
	var inherent, residual *string
	var proposedUntil *time.Time
	err := tx.QueryRow(ctx, `
		SELECT r.batch_id::text, r.row_number, COALESCE(r.external_ref, ''), r.title, r.description,
		       r.category_value, r.theme_values, r.review_due, r.inherent_score::text, r.residual_score::text,
		       b.methodology, r.status, r.treatment, r.acceptance_justification, r.proposed_until,
		       r.instrument_reference, r.linked_control_ids, r.owner_user_id, r.owner_unlinked,
		       COALESCE(r.level::text, ''), r.org_unit_id, r.parent_ref
		FROM risk_import_rows r
		JOIN risk_import_batches b ON b.tenant_id = r.tenant_id AND b.id = r.batch_id
		WHERE r.id = $1::uuid AND r.batch_id = $2::uuid
		FOR UPDATE OF r
	`, rowID, batchID).Scan(&r.BatchID, &r.RowNumber, &r.ExternalRef, &r.Title, &r.Description,
		&r.CategoryValue, &r.ThemeValues, &reviewDue, &inherent, &residual, &r.Methodology, &status, &r.Treatment, &r.Justification,
		&proposedUntil, &r.Instrument, &r.ControlIDs, &r.OwnerUserID, &r.OwnerUnlinked,
		&r.Level, &r.OrgUnitID, &r.ParentRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedRow{}, ErrRowNotFound
	}
	if err != nil {
		return lockedRow{}, fmt.Errorf("riskimport: lock staged row: %w", err)
	}
	if status != RowStatusPending {
		return lockedRow{}, fmt.Errorf("%w (status %s)", ErrRowDecided, status)
	}
	if reviewDue != nil {
		d := reviewDue.UTC()
		r.ReviewDue = &d
	}
	if proposedUntil != nil {
		d := proposedUntil.UTC()
		r.ProposedUntil = &d
	}
	r.InherentScore, r.ResidualScore = rawJSON(inherent), rawJSON(residual)
	return r, nil
}

// existingControls keeps the staged control links whose control is still
// an active version in the tenant, as resolveControls required at upload. A
// control deleted or superseded since upload is dropped; when that leaves a
// mitigate row with no link, the create path refuses it and the row stays
// pending with the reason.
func existingControls(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id FROM controls
		WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND superseded_by IS NULL
		ORDER BY id
	`, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("riskimport: read linked controls: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("riskimport: scan linked control: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// riskWithExternalRef finds the tenant's risk carrying the organisation
// risk ID, ignoring case as the risks_tenant_external_ref_key index does.
// A row without an ID has no duplicate.
func riskWithExternalRef(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, ref string) (string, bool, error) {
	if ref == "" {
		return "", false, nil
	}
	var id string
	err := tx.QueryRow(ctx, `
		SELECT id::text FROM risks
		WHERE tenant_id = $1 AND external_ref IS NOT NULL AND lower(external_ref) = lower($2)
	`, tenantID, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("riskimport: check duplicate risk: %w", err)
	}
	return id, true, nil
}

func recordDecision(ctx context.Context, tx pgx.Tx, rowID, status, approver, riskID, reason string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE risk_import_rows
		SET status = $2, decided_by = $3, decided_at = now(),
		    risk_id = $4::uuid, rejection_reason = $5, updated_at = now()
		WHERE id = $1::uuid
	`, rowID, status, approver, nullable(riskID), nullable(reason)); err != nil {
		return fmt.Errorf("riskimport: record %s: %w", status, err)
	}
	return nil
}

// finishDecision writes the decision's import audit row and completes the
// batch once no row is pending.
func finishDecision(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, d RowDecision, action, reason string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO risk_import_audit_log (
			tenant_id, batch_id, row_id, row_number, external_ref, action, actor, risk_id, reason)
		VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8::uuid, $9)
	`, tenantID, d.BatchID, d.RowID, d.RowNumber, nullable(d.ExternalRef), action, d.DecidedBy,
		nullable(d.RiskID), nullable(reason)); err != nil {
		return fmt.Errorf("riskimport: write audit row: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE risk_import_batches
		SET status = 'completed', updated_at = now()
		WHERE id = $1::uuid AND status = 'staged'
		  AND NOT EXISTS (SELECT 1 FROM risk_import_rows WHERE batch_id = $1::uuid AND status = 'pending')
	`, d.BatchID); err != nil {
		return fmt.Errorf("riskimport: complete batch: %w", err)
	}
	return nil
}

// settleWaitingOn settles the links of the rows waiting on a row decided
// without creating a risk (a duplicate or a rejection), under the
// hierarchy lock.
func settleWaitingOn(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, row lockedRow, d decision) error {
	if row.ExternalRef == "" {
		return nil
	}
	if err := lockHierarchy(ctx, tx, tenantID); err != nil {
		return err
	}
	return settleWaiting(ctx, tx, tenantID, row.BatchID, row.ExternalRef, d)
}
