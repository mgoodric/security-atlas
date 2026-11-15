package controlimport

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// #1783: approve-clear approves, in one action, every pending row of a batch
// whose top candidate is crosswalk-derived and reaches all of the row's
// tested criteria, with that candidate. Each row goes through ApproveRow in
// its own transaction, so it gets the same approver check, control creation,
// duplicate handling and audit row as a single approval, and a collision on
// one row never rolls back another's control. The batch row is locked for
// the whole run, so two approve-clears of one batch run one after the other;
// a concurrent single approval of a row is serialised by the row lock and
// counted here as skipped.

// ApproveClearInput approves the clear rows of one batch.
type ApproveClearInput struct {
	BatchID string
	// Approver is the users(id) of the person approving, as for ApproveRow.
	Approver string
}

// ApproveClearSkip is a qualifying row that was not approved, with why. It
// stays as it was: pending, or decided by someone else meanwhile.
type ApproveClearSkip struct {
	RowID      string `json:"row_id"`
	RowNumber  int    `json:"row_number"`
	ExternalID string `json:"external_id"`
	Reason     string `json:"reason"`
}

// ApproveClearResult reports an approve-clear. Approved and Duplicates are
// the rows' recorded decisions; Skipped are qualifying rows left as they
// were. Every row that did not qualify is untouched and not listed.
type ApproveClearResult struct {
	BatchID       string             `json:"batch_id"`
	Qualifying    int                `json:"qualifying"`
	ApprovedCount int                `json:"approved_count"`
	Approved      []RowDecision      `json:"approved"`
	Duplicates    []RowDecision      `json:"duplicates"`
	Skipped       []ApproveClearSkip `json:"skipped"`
}

// clearRow is a pending row that qualifies for approve-clear.
type clearRow struct {
	ID         string
	RowNumber  int
	ExternalID string
}

// ApproveClear approves every clear row of a batch with its top candidate.
// A row that fails for its own reason (bundle ID taken, decided meanwhile,
// anchor not in the release, an invalid bundle) is skipped and stays as it
// was; any other error stops the run and is returned, with the rows already
// approved left approved.
func (s *Store) ApproveClear(ctx context.Context, in ApproveClearInput) (ApproveClearResult, error) {
	if strings.TrimSpace(in.Approver) == "" {
		return ApproveClearResult{}, ErrApproverRequired
	}
	if _, err := uuid.Parse(in.BatchID); err != nil {
		return ApproveClearResult{}, ErrBatchNotFound
	}
	out := ApproveClearResult{
		BatchID:    in.BatchID,
		Approved:   []RowDecision{},
		Duplicates: []RowDecision{},
		Skipped:    []ApproveClearSkip{},
	}
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ string) error {
		var id string
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM control_import_batches WHERE id = $1::uuid FOR UPDATE
		`, in.BatchID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBatchNotFound
		}
		if err != nil {
			return fmt.Errorf("controlimport: lock batch: %w", err)
		}
		rows, err := clearRows(ctx, tx, in.BatchID)
		if err != nil {
			return err
		}
		out.Qualifying = len(rows)
		// Each approval runs in its own transaction, on another connection.
		// It never touches the batch row, so the lock held here cannot
		// block it.
		for _, r := range rows {
			d, err := s.ApproveRow(ctx, ApproveRowInput{
				BatchID: in.BatchID, RowID: r.ID, Approver: in.Approver, CandidateRank: 1,
			})
			switch {
			case err == nil && d.Status == RowStatusDuplicate:
				out.Duplicates = append(out.Duplicates, d)
			case err == nil:
				out.Approved = append(out.Approved, d)
			case errors.Is(err, ErrRowDecided), errors.Is(err, ErrRowNotFound),
				errors.Is(err, ErrBundleIDTaken), errors.Is(err, ErrAnchorNotInRelease),
				errors.Is(err, ErrNoBatchRelease), errors.Is(err, ErrCandidateNotFound),
				errors.Is(err, ErrInvalidApproval):
				out.Skipped = append(out.Skipped, ApproveClearSkip{
					RowID: r.ID, RowNumber: r.RowNumber, ExternalID: r.ExternalID,
					Reason: strings.TrimPrefix(err.Error(), "controlimport: "),
				})
			default:
				return err
			}
		}
		return nil
	})
	out.ApprovedCount = len(out.Approved)
	if err != nil {
		return out, err
	}
	return out, nil
}

// clearRows reads the batch's pending rows that have tested criteria and
// whose rank-1 candidate is crosswalk-derived and reaches every one of them,
// in row order.
func clearRows(ctx context.Context, tx pgx.Tx, batchID string) ([]clearRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT r.id::text, r.row_number, r.org_control_id
		FROM control_import_rows r
		JOIN control_import_candidates c ON c.row_id = r.id AND c.rank = 1
		WHERE r.batch_id = $1::uuid
		  AND r.status = 'pending'
		  AND c.source = 'crosswalk'
		  AND jsonb_array_length(r.tested_under) > 0
		  AND NOT EXISTS (
		      SELECT 1 FROM jsonb_array_elements(r.tested_under) t
		      WHERE NOT EXISTS (
		          SELECT 1 FROM jsonb_array_elements(c.reaches) x
		          WHERE x->>'requirement_id' = t->>'requirement_id'))
		ORDER BY r.row_number
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("controlimport: read clear rows: %w", err)
	}
	defer rows.Close()
	var out []clearRow
	for rows.Next() {
		var r clearRow
		if err := rows.Scan(&r.ID, &r.RowNumber, &r.ExternalID); err != nil {
			return nil, fmt.Errorf("controlimport: scan clear row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
