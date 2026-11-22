package riskimport

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// A parent-risk-ID column links each imported risk under the risk it rolls
// up to (spec #2812, Q17 / Q29; #2846):
//
//   - the cell names the parent by organisation risk ID, ignoring case, as
//     duplicate detection does. It is resolved against the tenant's risks,
//     which includes the risks this batch has already created, and against
//     the batch's pending rows. Another tenant's risks are never matched
//     (RLS, and every query here is tenant-scoped);
//   - the link is made only after the row is approved, through the risk
//     store's parent/child path (risk.LinkChildrenTx) and its rules: no
//     cycle, a child strictly below its parent's level, a 5x5-scored child;
//   - a row whose parent is a pending row of the batch waits. When that row
//     is decided, the rows waiting on it are linked (it became a risk, or
//     was a duplicate of one) or fail (it was rejected);
//   - a link that breaks a rule, or names a parent that is neither a risk
//     nor a pending row, fails on that row: the row keeps its risk, records
//     why, and is flagged parent_link_failed for review.
//
// Every decision that may make or settle a link takes the tenant's
// hierarchy lock before the link reads (the parent lookup, the pending-row
// check, the waiting-row scan), so a child and its parent decided
// concurrently cannot each miss the other: the second to take the lock
// sees the first's commit. The row lock, the create and the decision's own
// reads come earlier and are not covered by it.
//
// That relies on READ COMMITTED, the isolation inTx's transactions run at
// (the server default; pool.Begin sets none): each statement takes a fresh
// snapshot, so a read after the lock sees what the lock's previous holder
// committed. Under REPEATABLE READ or SERIALIZABLE the snapshot is fixed by
// the transaction's first statement, before the lock, and the lost-wakeup
// race returns: the child reads its parent row as pending, the parent reads
// no waiting child, and the child waits forever. Do not raise the
// isolation of these transactions without moving the lock to their first
// statement.

// FieldParentRef is the parent-risk-ID column. Like the others it is a
// column-map key.
const FieldParentRef = "parent_ref"

// Parent link statuses of a staged row (risk_import_rows_parent_link_chk).
const (
	ParentLinkNone    = "none"
	ParentLinkWaiting = "waiting"
	ParentLinkLinked  = "linked"
	ParentLinkFailed  = "failed"
)

// parentLink is the outcome of settling one row's parent link.
type parentLink struct {
	Status   string
	Error    string
	ParentID string
}

// lockHierarchy takes the tenant's risk hierarchy lock for the rest of the
// transaction. It is re-entrant, so risk.LinkChildrenTx taking it again is
// a no-op.
func lockHierarchy(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	if err := dbx.New(tx).LockRiskHierarchy(ctx, pgtype.UUID{Bytes: tenantID, Valid: true}); err != nil {
		return fmt.Errorf("riskimport: lock risk hierarchy: %w", err)
	}
	return nil
}

// linkOwnParent links the risk an approved row just created under the
// parent its cell names, and records the outcome on the row.
func linkOwnParent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, batchID, rowID string, child uuid.UUID, parentRef string) (parentLink, error) {
	link, err := settleLink(ctx, tx, tenantID, batchID, child, parentRef)
	if err != nil {
		return parentLink{}, err
	}
	return link, recordParentLink(ctx, tx, rowID, link)
}

// settleWaiting settles the links of the batch's approved rows waiting on a
// parent row carrying ref, once that row is decided.
func settleWaiting(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, batchID, ref string) error {
	if ref == "" {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, risk_id, parent_ref
		FROM risk_import_rows
		WHERE batch_id = $1::uuid AND parent_link_status = 'waiting' AND lower(parent_ref) = lower($2)
		ORDER BY row_number
		FOR UPDATE
	`, batchID, ref)
	if err != nil {
		return fmt.Errorf("riskimport: list rows waiting on a parent: %w", err)
	}
	type waiting struct {
		rowID     string
		riskID    uuid.UUID
		parentRef string
	}
	var ws []waiting
	for rows.Next() {
		var w waiting
		if err := rows.Scan(&w.rowID, &w.riskID, &w.parentRef); err != nil {
			rows.Close()
			return fmt.Errorf("riskimport: scan row waiting on a parent: %w", err)
		}
		ws = append(ws, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("riskimport: list rows waiting on a parent: %w", err)
	}
	for _, w := range ws {
		link, err := settleLink(ctx, tx, tenantID, batchID, w.riskID, w.parentRef)
		if err != nil {
			return err
		}
		if err := recordParentLink(ctx, tx, w.rowID, link); err != nil {
			return err
		}
	}
	return nil
}

// settleLink resolves parentRef and links child under it. A refusal by the
// hierarchy rules is a failed link, not an error: the savepoint around the
// link leaves the decision's transaction usable.
func settleLink(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, batchID string, child uuid.UUID, parentRef string) (parentLink, error) {
	parent, ok, err := riskWithExternalRef(ctx, tx, tenantID, parentRef)
	if err != nil {
		return parentLink{}, err
	}
	if !ok {
		var pending bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM risk_import_rows
				WHERE batch_id = $1::uuid AND status = 'pending'
				  AND external_ref IS NOT NULL AND lower(external_ref) = lower($2))
		`, batchID, parentRef).Scan(&pending); err != nil {
			return parentLink{}, fmt.Errorf("riskimport: find pending parent row: %w", err)
		}
		if pending {
			return parentLink{Status: ParentLinkWaiting}, nil
		}
		return failedLink(unknownParent(parentRef)), nil
	}
	parentID := uuid.MustParse(parent)
	sp, err := tx.Begin(ctx)
	if err != nil {
		return parentLink{}, fmt.Errorf("riskimport: begin parent link: %w", err)
	}
	if _, err := risk.LinkChildrenTx(ctx, dbx.New(sp), tenantID, parentID, []uuid.UUID{child}); err != nil {
		_ = sp.Rollback(ctx)
		if reason, refused := linkRefusal(err, parentRef); refused {
			return failedLink(reason), nil
		}
		return parentLink{}, fmt.Errorf("riskimport: link under parent %q: %w", parentRef, err)
	}
	if err := sp.Commit(ctx); err != nil {
		return parentLink{}, fmt.Errorf("riskimport: commit parent link: %w", err)
	}
	return parentLink{Status: ParentLinkLinked, ParentID: parent}, nil
}

func failedLink(reason string) parentLink { return parentLink{Status: ParentLinkFailed, Error: reason} }

func unknownParent(ref string) string {
	return fmt.Sprintf("parent risk ID %q matches no risk of this organisation and no pending row of this import", ref)
}

// linkRefusal names why the hierarchy rules refused a link, or reports the
// error as not a refusal.
func linkRefusal(err error, parentRef string) (string, bool) {
	switch {
	case errors.Is(err, risk.ErrAggregationCycle):
		return fmt.Sprintf("linking under parent %q would create a cycle in the risk hierarchy", parentRef), true
	case errors.Is(err, risk.ErrChildLevel):
		return fmt.Sprintf("the risk's level must be below the level of parent %q", parentRef), true
	case errors.Is(err, risk.ErrIncompatibleMethodology):
		return fmt.Sprintf("the risk must be scored on the 5x5 grid to roll up to parent %q", parentRef), true
	case errors.Is(err, risk.ErrNotFound), errors.Is(err, risk.ErrChildrenNotFound):
		// Removed between the read above and the link.
		return unknownParent(parentRef), true
	default:
		return "", false
	}
}

// recordParentLink writes a link outcome to its row; a failed link also
// flags the row for review.
func recordParentLink(ctx context.Context, tx pgx.Tx, rowID string, link parentLink) error {
	if _, err := tx.Exec(ctx, `
		UPDATE risk_import_rows
		SET parent_link_status = $2, parent_link_error = $3, parent_risk_id = $4::uuid,
		    flags = CASE WHEN $2 = 'failed' AND NOT ('parent_link_failed' = ANY(flags))
		                 THEN array_append(flags, 'parent_link_failed') ELSE flags END,
		    updated_at = now()
		WHERE id = $1::uuid
	`, rowID, link.Status, link.Error, nullable(link.ParentID)); err != nil {
		return fmt.Errorf("riskimport: record parent link: %w", err)
	}
	return nil
}
