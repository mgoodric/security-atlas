// excepted.go — excepted cells in the assessment results (#2269).
//
// A cell the engine recorded as `excepted` (#2112) is waived by an approved
// exception. The AR carries it as a risk-accepted observation linked to that
// exception, never as a finding. The evaluation row does not record which
// exception excepted it (#2112 D3), so this file reconstructs it: an
// exception in force at the evaluation's instant whose predicate covers the
// cell, by the engine's own coverage rule (eval.ExceptionCovers).
package oscal

import (
	"time"

	"github.com/google/uuid"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// inForceAt reports whether an activated exception waived evaluation at t.
// It mirrors the engine's window (#2112 D4), [effective_from, expires_at),
// read from the exception's history rather than its current status: it
// had been activated by t and had not been revoked by t. A NULL
// effective_from falls back to activated_at.
func inForceAt(x dbx.ListActivatedExceptionsForExportRow, t time.Time) bool {
	if !x.ActivatedAt.Valid || t.Before(x.ActivatedAt.Time) {
		return false
	}
	if x.EffectiveFrom.Valid && t.Before(x.EffectiveFrom.Time) {
		return false
	}
	if !x.ExpiresAt.Valid || !t.Before(x.ExpiresAt.Time) {
		return false
	}
	if x.RevokedAt.Valid && !t.Before(x.RevokedAt.Time) {
		return false
	}
	return true
}

// exceptedCells builds the AR's excepted-cell inputs. cells maps scope cell
// id to the cell; a row whose cell is missing (deleted since) or whose
// exception cannot be resolved is still emitted, with no exceptions, so the
// AR never silently drops an excepted cell. A malformed predicate is treated
// as not covering: the export reports what it can rather than failing.
func exceptedCells(
	rows []dbx.ListExceptedEvaluationsAsOfRow,
	exceptions []dbx.ListActivatedExceptionsForExportRow,
	cells map[uuid.UUID]scope.Cell,
) []*oscalv1.ExceptedCell {
	byControl := map[uuid.UUID][]dbx.ListActivatedExceptionsForExportRow{}
	for _, x := range exceptions {
		cid := uuid.UUID(x.ControlID.Bytes)
		byControl[cid] = append(byControl[cid], x)
	}

	out := make([]*oscalv1.ExceptedCell, 0, len(rows))
	for _, r := range rows {
		controlID := uuid.UUID(r.ControlID.Bytes)
		ec := &oscalv1.ExceptedCell{
			EvaluationId: uuid.UUID(r.ID.Bytes).String(),
			ControlId:    controlID.String(),
		}
		var at time.Time
		if r.EvaluatedAt.Valid {
			at = r.EvaluatedAt.Time
			ec.EvaluatedAt = at.UTC().Format(time.RFC3339)
		}

		var cell *scope.Cell
		cellKnown := true
		if r.ScopeCellID.Valid {
			cellID := uuid.UUID(r.ScopeCellID.Bytes)
			ec.ScopeCellId = cellID.String()
			if c, ok := cells[cellID]; ok {
				cell = &c
			} else {
				cellKnown = false
			}
		}

		if cellKnown {
			for _, x := range byControl[controlID] {
				if !inForceAt(x, at) {
					continue
				}
				covered, err := eval.ExceptionCovers(x.ScopeCellPredicate, cell)
				if err != nil || !covered {
					continue
				}
				ec.Exceptions = append(ec.Exceptions, acceptedException(x))
			}
		}
		out = append(out, ec)
	}
	return out
}

func acceptedException(x dbx.ListActivatedExceptionsForExportRow) *oscalv1.AcceptedException {
	ae := &oscalv1.AcceptedException{
		Id:            uuid.UUID(x.ID.Bytes).String(),
		Justification: x.Justification,
		Status:        x.Status,
	}
	if x.ExpiresAt.Valid {
		ae.ExpiresAt = x.ExpiresAt.Time.UTC().Format(time.RFC3339)
	}
	if x.ApprovedBy != nil {
		ae.ApprovedBy = *x.ApprovedBy
	}
	if x.ApprovedAt.Valid {
		ae.ApprovedAt = x.ApprovedAt.Time.UTC().Format(time.RFC3339)
	}
	return ae
}
