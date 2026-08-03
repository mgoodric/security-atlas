// store.go — the OE-663 package-local read layer for the checklist
// index (GET /v1/personnel-security/checklists).
//
// The OE-630 store (internal/personnelsecurity) exposes CreateManual /
// Load / CompleteItem / ListOverdueOffboarding and this slice must not
// change its semantics, so the one read it lacks — a filterable index
// across both workflow kinds — lives here instead, following the
// internal/api/calendar precedent for API-owned reads over another
// slice's tables. Read-only: the single query is a SELECT.
//
// Tenant isolation is enforced at the DB layer via slice-033 RLS. The
// ReadStore opens a transaction per call and applies the tenant GUC via
// internal/tenancy; the application-side `WHERE tenant_id = $1`
// predicate in the SQL is the primary guarantee, RLS is defense in
// depth — identical posture to the OE-630 store's inTx.

package personnelsecurity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/personnelsecurity"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ListFilter narrows the checklist index. Zero-valued fields are
// no-ops; all filters compose.
type ListFilter struct {
	Kind        string    // "" | "onboarding" | "offboarding"
	Status      string    // "" | "open" | "completed"
	OverdueOnly bool      // open checklists with due_at < Now
	Now         time.Time // the overdue cut-off; zero means time.Now
}

// ReadStore is the list-only read seam. It holds no write methods.
type ReadStore struct {
	pool *pgxpool.Pool
}

// NewReadStore wires a ReadStore over the application pgx pool. The
// pool MUST connect as the application role (NOSUPERUSER NOBYPASSRLS)
// so RLS is actually enforced.
func NewReadStore(pool *pgxpool.Pool) *ReadStore {
	if pool == nil {
		panic("personnel security api: pool is required")
	}
	return &ReadStore{pool: pool}
}

// List returns the tenant's checklists matching the filter, ordered by
// due_at then id. Items are not loaded — the index read stays one
// query; GET /v1/personnel-security/checklists/{id} loads items.
func (s *ReadStore) List(ctx context.Context, filter ListFilter) ([]personnelsecurity.Checklist, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return nil, fmt.Errorf("personnel security api: parse tenant id: %w", err)
	}
	now := filter.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("personnel security api: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := dbx.New(tx).ListPersonnelChecklists(ctx, dbx.ListPersonnelChecklistsParams{
		TenantID:     pgtype.UUID{Bytes: tenantID, Valid: true},
		WorkflowKind: optional(filter.Kind),
		Status:       optional(filter.Status),
		OverdueOnly:  filter.OverdueOnly,
		NowTs:        pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("personnel security api: commit: %w", err)
	}
	out := make([]personnelsecurity.Checklist, 0, len(rows))
	for _, row := range rows {
		out = append(out, checklistFromDBRow(row))
	}
	return out, nil
}

// checklistFromDBRow maps a dbx row onto the OE-630 domain type
// (items nil — the index read does not load them). Mirrors the store's
// unexported checklistFromRow.
func checklistFromDBRow(row dbx.PersonnelSecurityChecklist) personnelsecurity.Checklist {
	sourceEventID := ""
	if row.SourceEventID != nil {
		sourceEventID = *row.SourceEventID
	}
	controlID := uuid.Nil
	if row.ControlID.Valid {
		controlID = uuid.UUID(row.ControlID.Bytes)
	}
	return personnelsecurity.Checklist{
		ID:                uuid.UUID(row.ID.Bytes),
		Kind:              personnelsecurity.WorkflowKind(row.WorkflowKind),
		Source:            personnelsecurity.EventSource(row.Source),
		SourceEventID:     sourceEventID,
		PersonExternalID:  row.PersonExternalID,
		PersonWorkEmail:   row.PersonWorkEmail,
		PersonDisplayName: row.PersonDisplayName,
		ControlID:         controlID,
		DueAt:             row.DueAt.Time,
		Status:            row.Status,
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
