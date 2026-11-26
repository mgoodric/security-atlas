package riskimport

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
//     the pending rows of every import batch of the tenant (#2957).
//     Another tenant's risks and rows are never matched (RLS, and every
//     query here is tenant-scoped);
//   - the link is made only after the row is approved, through the risk
//     store's parent/child path (risk.LinkChildrenTx) and its rules: no
//     cycle, a child strictly below its parent's level, a 5x5-scored child;
//   - a row whose parent is a pending row, in its own batch or another,
//     waits. When a row carrying that ID is decided, the rows waiting on
//     it are linked (it became a risk, or was a duplicate of one), keep
//     waiting (it was rejected, and another pending row carries the ID) or
//     fail (it was rejected, and none does);
//   - when the row's own batch completes or is closed (#3002), a row still
//     waiting on an ID no pending row of the tenant carries fails
//     (failStranded). Decisions settle waiting rows as they go, so this is
//     a backstop: a row stranded there would otherwise wait for ever,
//     unflagged. Closing a batch rejects its pending rows, which settles
//     the rows of every batch waiting on them;
//   - a link that breaks a rule, or names a parent that is neither a risk
//     nor a pending row, fails on that row: the row keeps its risk, records
//     why, and is flagged parent_link_failed for review.
//
// A row that names no parent by organisation risk ID may carry the risk
// export's parent_risk_ids instead: the ids staging found to be risks of
// the tenant (idfallback.go). The row's risk is linked under each of them
// in turn, through the same path and rules. Those parents already exist,
// so such a row never waits.
//
// Every link made writes a child_linked row on the parent (#2958) by the
// person whose decision made it, naming the import batch and the child's
// row. A link settled by a parent row's decision also carries a note
// naming that row.
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

// decision is the row decision that makes or settles a link: who decided
// it and which row of which batch it was, for the link's audit row (#2958).
type decision struct {
	Approver  string
	BatchID   string
	RowID     string
	RowNumber int
}

// linkAudit is the audit attribution of a link made for the child row
// childRowID of batch childBatchID. A link settled by another row's
// decision (a waiting row) notes that row, and its batch when it is
// another one (#2957).
func linkAudit(childBatchID, childRowID string, d decision) risk.HierarchyAudit {
	detail := map[string]any{"import_batch_id": childBatchID, "import_row_id": childRowID}
	if d.RowID != childRowID {
		detail["settled_by_import_row_id"] = d.RowID
		if d.BatchID != childBatchID {
			detail["note"] = fmt.Sprintf("linked when the parent's row %d of another import was decided", d.RowNumber)
			detail["settled_by_import_batch_id"] = d.BatchID
		} else {
			detail["note"] = fmt.Sprintf("linked when the parent's import row %d was decided", d.RowNumber)
		}
	}
	return risk.HierarchyAudit{Actor: d.Approver, Detail: detail}
}

// linkOwnParent links the risk an approved row just created under the
// parent its cell names, or else under the parents its parent_risk_ids
// staged, and records the outcome on the row. A row with neither links
// nothing and records nothing.
func linkOwnParent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, d decision, child uuid.UUID, row lockedRow) (parentLink, bool, error) {
	audit := linkAudit(d.BatchID, d.RowID, d)
	var link parentLink
	var err error
	switch {
	case row.ParentRef != "":
		link, err = settleLink(ctx, tx, tenantID, child, row.ParentRef, audit)
	case len(row.ParentRiskIDs) > 0:
		link, err = linkParentIDs(ctx, tx, tenantID, child, row.ParentRiskIDs, audit)
	default:
		return parentLink{}, false, nil
	}
	if err != nil {
		return parentLink{}, false, err
	}
	return link, true, recordParentLink(ctx, tx, d.RowID, link)
}

// linkParentIDs links child under each of the parents a row's
// parent_risk_ids staged. It is linked when every link is made, and failed
// when any is refused, with each refusal's reason; the links made stay.
func linkParentIDs(ctx context.Context, tx pgx.Tx, tenantID, child uuid.UUID, parents []uuid.UUID, audit risk.HierarchyAudit) (parentLink, error) {
	var first string
	var reasons []string
	for _, p := range parents {
		ok, reason, err := linkUnder(ctx, tx, tenantID, p, child, p.String(), audit)
		if err != nil {
			return parentLink{}, err
		}
		if !ok {
			reasons = append(reasons, reason)
		} else if first == "" {
			first = p.String()
		}
	}
	if len(reasons) > 0 {
		return failedLink(strings.Join(reasons, "; ")), nil
	}
	return parentLink{Status: ParentLinkLinked, ParentID: first}, nil
}

// settleWaiting settles the links of the tenant's approved rows, in any
// import batch, waiting on a parent row carrying ref, once a row carrying
// ref is decided by d.
func settleWaiting(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, ref string, d decision) error {
	if ref == "" {
		return nil
	}
	ws, err := waitingRows(ctx, tx, `
		SELECT id::text, batch_id::text, risk_id, parent_ref
		FROM risk_import_rows
		WHERE tenant_id = $1 AND parent_link_status = 'waiting' AND lower(parent_ref) = lower($2)
		ORDER BY created_at, batch_id, row_number
		FOR UPDATE
	`, tenantID, ref)
	if err != nil {
		return err
	}
	for _, w := range ws {
		link, err := settleLink(ctx, tx, tenantID, w.riskID, w.parentRef, linkAudit(w.batchID, w.rowID, d))
		if err != nil {
			return err
		}
		if err := recordParentLink(ctx, tx, w.rowID, link); err != nil {
			return err
		}
	}
	return nil
}

// batchEnd is how a batch stopped taking decisions, for failStranded: the
// verb its reasons use (completed, closed) and the audit attribution of a
// link it still makes.
type batchEnd struct {
	BatchID string
	Verb    string
	Audit   func(w waitingRow) risk.HierarchyAudit
}

// completedBy is the end of a batch whose last pending row d decided.
func completedBy(d decision) batchEnd {
	return batchEnd{BatchID: d.BatchID, Verb: "completed", Audit: func(w waitingRow) risk.HierarchyAudit {
		return risk.HierarchyAudit{Actor: d.Approver, Detail: map[string]any{
			"import_batch_id": w.batchID, "import_row_id": w.rowID,
			"settled_by_import_row_id": d.RowID, "note": "linked when its import completed",
		}}
	}}
}

// closedBy is the end of a batch the approver closed (#3002).
func closedBy(batchID, approver string) batchEnd {
	return batchEnd{BatchID: batchID, Verb: "was closed", Audit: func(w waitingRow) risk.HierarchyAudit {
		return risk.HierarchyAudit{Actor: approver, Detail: map[string]any{
			"import_batch_id": w.batchID, "import_row_id": w.rowID,
			"note": "linked when its import was closed",
		}}
	}}
}

// failStranded fails the rows of a batch that has just completed or been
// closed which are still waiting on a parent ID no pending row of the
// tenant carries. A row waiting on a pending row of another batch keeps
// waiting: that row's decision, or the close of its batch, settles it. The
// caller holds the hierarchy lock.
func failStranded(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, end batchEnd) error {
	ws, err := waitingRows(ctx, tx, `
		SELECT w.id::text, w.batch_id::text, w.risk_id, w.parent_ref
		FROM risk_import_rows w
		WHERE w.tenant_id = $1 AND w.batch_id = $2::uuid AND w.parent_link_status = 'waiting'
		  AND NOT EXISTS (
			SELECT 1 FROM risk_import_rows p
			WHERE p.tenant_id = $1 AND p.status = 'pending'
			  AND p.external_ref IS NOT NULL AND lower(p.external_ref) = lower(w.parent_ref))
		ORDER BY w.row_number
		FOR UPDATE OF w
	`, tenantID, end.BatchID)
	if err != nil {
		return err
	}
	for _, w := range ws {
		// settleLink re-reads the parent, so a parent that is a risk by
		// now is still linked, by the person who ended the batch.
		// Otherwise the row fails.
		link, err := settleLink(ctx, tx, tenantID, w.riskID, w.parentRef, end.Audit(w))
		if err != nil {
			return err
		}
		if link.Status == ParentLinkWaiting {
			continue
		}
		if link.Status == ParentLinkFailed {
			link.Error = strandedParent(w.parentRef, end.Verb, link.Error)
		}
		if err := recordParentLink(ctx, tx, w.rowID, link); err != nil {
			return err
		}
	}
	return nil
}

// waitingRow is an approved row waiting on its parent row.
type waitingRow struct {
	rowID     string
	batchID   string
	riskID    uuid.UUID
	parentRef string
}

// waitingRows reads waiting rows with query, which selects id, batch_id,
// risk_id and parent_ref.
func waitingRows(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]waitingRow, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("riskimport: list rows waiting on a parent: %w", err)
	}
	defer rows.Close()
	var ws []waitingRow
	for rows.Next() {
		var w waitingRow
		if err := rows.Scan(&w.rowID, &w.batchID, &w.riskID, &w.parentRef); err != nil {
			return nil, fmt.Errorf("riskimport: scan row waiting on a parent: %w", err)
		}
		ws = append(ws, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("riskimport: list rows waiting on a parent: %w", err)
	}
	return ws, nil
}

// settleLink resolves parentRef and links child under it. A refusal by the
// hierarchy rules is a failed link, not an error: the savepoint around the
// link leaves the decision's transaction usable. A link made writes its
// child_linked row attributed by audit.
func settleLink(ctx context.Context, tx pgx.Tx, tenantID, child uuid.UUID, parentRef string, audit risk.HierarchyAudit) (parentLink, error) {
	parent, ok, err := riskWithExternalRef(ctx, tx, tenantID, parentRef)
	if err != nil {
		return parentLink{}, err
	}
	if !ok {
		var pending bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM risk_import_rows
				WHERE tenant_id = $1 AND status = 'pending'
				  AND external_ref IS NOT NULL AND lower(external_ref) = lower($2))
		`, tenantID, parentRef).Scan(&pending); err != nil {
			return parentLink{}, fmt.Errorf("riskimport: find pending parent row: %w", err)
		}
		if pending {
			return parentLink{Status: ParentLinkWaiting}, nil
		}
		return failedLink(unknownParent(parentRef)), nil
	}
	linked, reason, err := linkUnder(ctx, tx, tenantID, uuid.MustParse(parent), child, parentRef, audit)
	if err != nil {
		return parentLink{}, err
	}
	if !linked {
		return failedLink(reason), nil
	}
	return parentLink{Status: ParentLinkLinked, ParentID: parent}, nil
}

// linkUnder links child under parent in a savepoint. A refusal by the
// hierarchy rules reports false with its reason, naming the parent as
// parentRef, and leaves the transaction usable.
func linkUnder(ctx context.Context, tx pgx.Tx, tenantID, parent, child uuid.UUID, parentRef string, audit risk.HierarchyAudit) (bool, string, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, "", fmt.Errorf("riskimport: begin parent link: %w", err)
	}
	if _, err := risk.LinkChildrenTx(ctx, dbx.New(sp), tenantID, parent, []uuid.UUID{child}, audit); err != nil {
		_ = sp.Rollback(ctx)
		if reason, refused := linkRefusal(err, parentRef); refused {
			return false, reason, nil
		}
		return false, "", fmt.Errorf("riskimport: link under parent %q: %w", parentRef, err)
	}
	if err := sp.Commit(ctx); err != nil {
		return false, "", fmt.Errorf("riskimport: commit parent link: %w", err)
	}
	return true, "", nil
}

func failedLink(reason string) parentLink { return parentLink{Status: ParentLinkFailed, Error: reason} }

func unknownParent(ref string) string {
	return fmt.Sprintf("parent risk ID %q matches no risk of this organisation and no pending row of any import", ref)
}

// strandedParent is the reason a row that was still waiting when its
// import completed or was closed (verb) failed.
func strandedParent(ref, verb, reason string) string {
	return fmt.Sprintf("still waiting on parent risk ID %q when this import %s: %s", ref, verb, reason)
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
