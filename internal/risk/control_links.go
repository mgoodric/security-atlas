// Unlinking a control from a risk (#2106).
//
// Linking (Store.LinkControl) and unlinking take the same single lock, the
// risk row, refuse a closed risk, and write one risk_audit_log row in the
// link's transaction. Control links are not an aggregation input, so no
// rule lock is taken.

package risk

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Audit actions the control links write (migration 20261220000000).
const (
	ActionControlLinked   = "control_linked"
	ActionControlUnlinked = "control_unlinked"
)

// ErrControlNotLinked is returned when unlinking a control the risk does
// not link. The HTTP layer renders a 404.
var ErrControlNotLinked = errors.New("risk: control is not linked to the risk")

// UnlinkControlInput unlinks ControlID from RiskID.
type UnlinkControlInput struct {
	RiskID    uuid.UUID
	ControlID uuid.UUID
	Actor     string
}

// UnlinkControl removes a control link. It refuses a closed risk
// (ErrTransitionState), a link that does not exist (ErrControlNotLinked),
// and the last link that a mitigate treatment rests on: a mitigate risk
// keeps at least one linked control or live action plan
// (ErrTreatmentValidation). Change the treatment, or add an action plan,
// first.
func (s *Store) UnlinkControl(ctx context.Context, in UnlinkControlInput) error {
	if in.Actor == "" {
		return fmt.Errorf("%w: actor is required", ErrTransitionInput)
	}
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		current, err := lockOpenRisk(ctx, q, tenantID, in.RiskID)
		if err != nil {
			return err
		}
		linked, err := controlLinked(ctx, q, tenantID, in.RiskID, in.ControlID)
		if err != nil {
			return err
		}
		if !linked {
			return ErrControlNotLinked
		}
		// Only the mitigate rule counts links; the other treatments'
		// fields are not this write's to re-validate.
		if current.Treatment == dbx.RiskTreatmentMitigate {
			controls, plans, err := linkCounts(ctx, q, tenantID, in.RiskID)
			if err != nil {
				return err
			}
			if err := ValidateTreatment(TreatmentInput{
				Treatment:             current.Treatment,
				LinkedControlCount:    controls - 1,
				LinkedActionPlanCount: plans,
			}); err != nil {
				return err
			}
		}
		if err := q.UnlinkRiskControl(ctx, dbx.UnlinkRiskControlParams{
			TenantID:  pgUUID(tenantID),
			RiskID:    pgUUID(in.RiskID),
			ControlID: pgUUID(in.ControlID),
		}); err != nil {
			return fmt.Errorf("unlink control: %w", err)
		}
		return insertRiskAudit(ctx, q, tenantID, in.RiskID, in.Actor, ActionControlUnlinked,
			map[string]any{"control_id": in.ControlID}, map[string]any{"control_id": nil})
	})
}

// controlLinked reports whether the risk links the control.
func controlLinked(ctx context.Context, q *dbx.Queries, tenantID, riskID, controlID uuid.UUID) (bool, error) {
	_, err := q.GetRiskControlLink(ctx, dbx.GetRiskControlLinkParams{
		TenantID:  pgUUID(tenantID),
		RiskID:    pgUUID(riskID),
		ControlID: pgUUID(controlID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get risk control link: %w", err)
	}
	return true, nil
}
