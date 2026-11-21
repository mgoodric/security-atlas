// A risk's origin and its theme changes in the audit trail (#2835).
//
// Every create path writes one `created` risk_audit_log row in the create's
// transaction: Store.Create (POST /v1/risks), Store.Aggregate (a manual
// aggregation parent), the MCP create_risk applier, and the aggregation
// engine's rule-generated meta-risks. They all write it through
// RecordCreatedTx, so the entry has one shape whichever path made the risk.
// Theme assignment writes `themes_changed`.

package risk

import (
	"context"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Audit actions for a risk's origin and its themes (migration
// 20261277000000).
const (
	ActionCreated       = "created"
	ActionThemesChanged = "themes_changed"
)

// AuditWriter is the one query a risk_audit_log write needs. *dbx.Queries
// satisfies it; the aggregation engine passes its narrow writer.
type AuditWriter interface {
	InsertRiskAuditLog(ctx context.Context, arg dbx.InsertRiskAuditLogParams) error
}

// CreatedState is a new risk's initial record as its `created` row holds
// it: title, category, treatment and owner, keyed by wire name. An unset
// owner is null, and treatment_owner is the free-text owner a risk without
// an owner_user_id may carry.
type CreatedState struct {
	Title          string
	CategoryID     uuid.UUID
	Treatment      dbx.RiskTreatment
	OwnerUserID    *uuid.UUID
	TreatmentOwner string
}

func (c CreatedState) after() map[string]any {
	return map[string]any{
		"title":           c.Title,
		"category_id":     c.CategoryID,
		"treatment":       c.Treatment,
		"owner_user_id":   userIDValue(c.OwnerUserID),
		"treatment_owner": c.TreatmentOwner,
	}
}

// RecordCreatedTx writes the `created` row for riskID inside the caller's
// transaction, naming actor (the creating person or credential). Every risk
// create path calls it. The row's tenant_id is tenantID.
func RecordCreatedTx(ctx context.Context, w AuditWriter, tenantID, riskID uuid.UUID, actor string, state CreatedState) error {
	return insertRiskAudit(ctx, w, tenantID, riskID, actor, ActionCreated, map[string]any{}, state.after())
}

// createdStateOf is the CreatedState of a stored risk.
func createdStateOf(r Risk) CreatedState {
	return CreatedState{
		Title:          r.Title,
		CategoryID:     r.CategoryID,
		Treatment:      r.Treatment,
		OwnerUserID:    r.OwnerUserID,
		TreatmentOwner: r.TreatmentOwner,
	}
}

// themesValue is a theme set as a themes_changed row holds it: never null.
func themesValue(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}
