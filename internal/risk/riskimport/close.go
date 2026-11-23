package riskimport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// A batch nobody finishes would keep its pending rows, and every row of any
// batch waiting on them, open for ever (#3002). An approver closes it: each
// pending row is rejected with the closer's reason, exactly as a per-row
// rejection would reject it, the rows waiting on them are settled, and the
// batch is closed. A closed batch takes no further decision or mapping.
//
// There is no expiry. Every rejection names the person who made it (#2881,
// risk_import_audit_log.actor), and a sweeper has no person to name
// (decisions log 3002, D2).

// ErrBatchNotStaged is a close of a batch that is already completed or
// closed. Nothing is written.
var ErrBatchNotStaged = errors.New("riskimport: import batch is already completed or closed")

// closedRowReason is the rejection reason a close gives each pending row.
func closedRowReason(reason string) string { return "import closed: " + reason }

// CloseInput closes one staged batch.
type CloseInput struct {
	BatchID string
	// Approver is the users(id) of the person closing, the actor on each
	// rejection's audit row.
	Approver string
	Reason   string
}

// CloseResult reports a close: the batch as closed and the rows it
// rejected.
type CloseResult struct {
	BatchID     string        `json:"batch_id"`
	Status      string        `json:"status"`
	ClosedBy    string        `json:"closed_by"`
	ClosedAt    time.Time     `json:"closed_at"`
	CloseReason string        `json:"close_reason"`
	Rejected    []RowDecision `json:"rejected"`
}

// CloseBatch closes a staged batch, rejecting its pending rows, in one
// transaction. The lock order is a decision's (#2953): the pending rows FOR
// UPDATE, then the tenant hierarchy lock, then the waiting rows FOR UPDATE,
// and the batch row last, by the status update. A row decided while the
// close waits on its lock is left as decided; if that decision completed
// the batch the close is ErrBatchNotStaged and rolls back.
func (s *Store) CloseBatch(ctx context.Context, in CloseInput) (CloseResult, error) {
	if strings.TrimSpace(in.Approver) == "" {
		return CloseResult{}, ErrApproverRequired
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return CloseResult{}, ErrReasonRequired
	}
	if len([]rune(reason)) > maxRejectReason {
		return CloseResult{}, fmt.Errorf("%w: reason exceeds %d characters", ErrInvalidDecision, maxRejectReason)
	}
	if _, err := uuid.Parse(in.BatchID); err != nil {
		return CloseResult{}, ErrBatchNotFound
	}
	out := CloseResult{BatchID: in.BatchID, Status: BatchStatusClosed, ClosedBy: in.Approver, CloseReason: reason, Rejected: []RowDecision{}}
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		var status, uploader string
		err := tx.QueryRow(ctx, `
			SELECT status, COALESCE(uploader_user_id::text, '') FROM risk_import_batches WHERE id = $1::uuid
		`, in.BatchID).Scan(&status, &uploader)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBatchNotFound
		}
		if err != nil {
			return fmt.Errorf("riskimport: read batch: %w", err)
		}
		if status != BatchStatusStaged {
			return ErrBatchNotStaged
		}
		// Closing rejects rows, so the uploader is refused as for a
		// per-row rejection.
		if err := notUploader(uploader, in.Approver); err != nil {
			return err
		}
		pending, err := lockPendingRows(ctx, tx, in.BatchID)
		if err != nil {
			return err
		}
		rowReason := closedRowReason(reason)
		for _, p := range pending {
			d := RowDecision{
				BatchID: in.BatchID, RowID: p.ID, RowNumber: p.RowNumber, ExternalRef: p.ExternalRef,
				Status: RowStatusRejected, DecidedBy: in.Approver, RejectionReason: rowReason,
			}
			if err := recordDecision(ctx, tx, p.ID, RowStatusRejected, in.Approver, "", rowReason); err != nil {
				return err
			}
			if err := writeDecisionAudit(ctx, tx, tenantID, d, "reject", rowReason); err != nil {
				return err
			}
			out.Rejected = append(out.Rejected, d)
		}
		// Every pending row is rejected before any waiting row is settled,
		// so a settle sees none of this batch's rows as pending.
		if err := lockHierarchy(ctx, tx, tenantID); err != nil {
			return err
		}
		for _, p := range pending {
			if err := settleWaiting(ctx, tx, tenantID, p.ExternalRef, decision{in.Approver, in.BatchID, p.ID, p.RowNumber}); err != nil {
				return err
			}
		}
		err = tx.QueryRow(ctx, `
			UPDATE risk_import_batches
			SET status = 'closed', closed_by = $2, closed_at = now(), close_reason = $3, updated_at = now()
			WHERE id = $1::uuid AND status = 'staged'
			RETURNING closed_at
		`, in.BatchID, in.Approver, reason).Scan(&out.ClosedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			// Completed by a decision that held one of its rows.
			return ErrBatchNotStaged
		}
		if err != nil {
			return fmt.Errorf("riskimport: close batch: %w", err)
		}
		return failStranded(ctx, tx, tenantID, closedBy(in.BatchID, in.Approver))
	})
	if err != nil {
		return CloseResult{}, err
	}
	return out, nil
}

// lockPendingRows locks the batch's pending rows in row order. A row a
// concurrent decision holds is waited for and, once decided, left out.
func lockPendingRows(ctx context.Context, tx pgx.Tx, batchID string) ([]pendingRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, row_number, COALESCE(external_ref, '')
		FROM risk_import_rows
		WHERE batch_id = $1::uuid AND status = 'pending'
		ORDER BY row_number
		FOR UPDATE
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("riskimport: lock pending rows: %w", err)
	}
	defer rows.Close()
	var out []pendingRow
	for rows.Next() {
		var p pendingRow
		if err := rows.Scan(&p.ID, &p.RowNumber, &p.ExternalRef); err != nil {
			return nil, fmt.Errorf("riskimport: scan pending row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MaxListedBatches caps List: the newest batches of the tenant.
const MaxListedBatches = 100

// BatchSummary is one batch as the import list shows it (#3002).
type BatchSummary struct {
	ID             string `json:"id"`
	SourceFilename string `json:"source_filename"`
	SourceFormat   string `json:"source_format"`
	Methodology    string `json:"methodology"`
	UploadedBy     string `json:"uploaded_by"`
	UploaderUserID string `json:"uploader_user_id,omitempty"`
	Status         string `json:"status"`
	TotalRows      int    `json:"total_rows"`
	StagedCount    int    `json:"staged_count"`
	RejectedCount  int    `json:"rejected_count"`
	// PendingCount is the batch's rows awaiting a decision.
	PendingCount int `json:"pending_count"`
	// WaitingCount is the batch's approved rows whose parent link waits on
	// a pending row, of this import or another.
	WaitingCount int `json:"waiting_count"`
	// ChildrenWaitingCount is the approved rows, of any import of the
	// tenant, waiting on a pending row of this batch: what closing it, or
	// deciding those rows, settles.
	ChildrenWaitingCount int        `json:"children_waiting_count"`
	CreatedAt            time.Time  `json:"created_at"`
	ClosedBy             string     `json:"closed_by,omitempty"`
	ClosedAt             *time.Time `json:"closed_at,omitempty"`
	CloseReason          string     `json:"close_reason,omitempty"`
}

// List returns the tenant's newest batches, at most MaxListedBatches, with
// their pending and waiting counts.
func (s *Store) List(ctx context.Context) ([]BatchSummary, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return nil, err
	}
	out := []BatchSummary{}
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT b.id::text, b.source_filename, b.source_format, b.methodology, b.uploaded_by,
			       COALESCE(b.uploader_user_id::text, ''), b.status,
			       b.total_rows, b.staged_count, b.rejected_count,
			       (SELECT count(*) FROM risk_import_rows r
			        WHERE r.tenant_id = b.tenant_id AND r.batch_id = b.id AND r.status = 'pending'),
			       (SELECT count(*) FROM risk_import_rows r
			        WHERE r.tenant_id = b.tenant_id AND r.batch_id = b.id AND r.parent_link_status = 'waiting'),
			       (SELECT count(*) FROM risk_import_rows w
			        WHERE w.tenant_id = b.tenant_id AND w.parent_link_status = 'waiting'
			          AND EXISTS (
			            SELECT 1 FROM risk_import_rows p
			            WHERE p.tenant_id = b.tenant_id AND p.batch_id = b.id AND p.status = 'pending'
			              AND p.external_ref IS NOT NULL AND lower(p.external_ref) = lower(w.parent_ref))),
			       b.created_at, COALESCE(b.closed_by, ''), b.closed_at, COALESCE(b.close_reason, '')
			FROM risk_import_batches b
			WHERE b.tenant_id = $1
			ORDER BY b.created_at DESC, b.id
			LIMIT $2
		`, tenantID, MaxListedBatches)
		if err != nil {
			return fmt.Errorf("riskimport: list batches: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var b BatchSummary
			if err := rows.Scan(&b.ID, &b.SourceFilename, &b.SourceFormat, &b.Methodology, &b.UploadedBy,
				&b.UploaderUserID, &b.Status, &b.TotalRows, &b.StagedCount, &b.RejectedCount,
				&b.PendingCount, &b.WaitingCount, &b.ChildrenWaitingCount,
				&b.CreatedAt, &b.ClosedBy, &b.ClosedAt, &b.CloseReason); err != nil {
				return fmt.Errorf("riskimport: scan batch: %w", err)
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
