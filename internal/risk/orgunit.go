// Org unit CRUD for slice 053 (canvas §6.4 — risk hierarchy).
//
// Slice 052 shipped the `org_units` table with `level risk_level NOT NULL`,
// nullable `parent_id`, and `acceptance_authorities` JSONB. Slice 053 wires
// the HTTP CRUD endpoints + cycle detection on parent_id transitions.
//
// Cycle detection walks the proposed parent chain via the
// `ParentChainIDs` recursive CTE. If the node being assigned to that
// parent appears in the returned ids, the assignment would create a
// cycle and the operation is rejected with 400. Self-parent
// (parent_id = self_id) is the trivial case and is also caught by the
// same query (the CTE seed row matches).

package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrCycleDetected is returned when an org_unit create/update would set
// parent_id such that walking up the parent chain reaches the node itself.
var ErrCycleDetected = errors.New("risk: org_unit parent chain would form a cycle")

// ErrInvalidLevel is returned when the supplied level is not in the
// closed `risk_level` enum (team/org/company).
var ErrInvalidLevel = errors.New("risk: org_unit level must be one of: team, org, company")

// OrgUnit is the domain shape returned from the store. ParentID is nil for
// root-level units; AcceptanceAuthorities is a JSONB blob whose keys are
// role names (canvas §6.4: "each tenant configures the role-to-level
// mapping in org_units.acceptance_authorities").
type OrgUnit struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	Name                  string
	ParentID              *uuid.UUID
	Level                 dbx.RiskLevel
	AcceptanceAuthorities json.RawMessage
}

// OrgUnitInput is the create/update shape. ParentID is optional; nil means
// root-level. AcceptanceAuthorities defaults to "{}" if empty.
type OrgUnitInput struct {
	Name                  string
	ParentID              *uuid.UUID
	Level                 dbx.RiskLevel
	AcceptanceAuthorities json.RawMessage
}

// CreateOrgUnit inserts a new org_unit and runs cycle detection if ParentID
// is set. Returns the created unit with its generated id.
func (s *Store) CreateOrgUnit(ctx context.Context, in OrgUnitInput) (OrgUnit, error) {
	if !isValidLevel(in.Level) {
		return OrgUnit{}, ErrInvalidLevel
	}
	id := uuid.New()
	var out OrgUnit
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if in.ParentID != nil {
			// On create, no cycle is possible — the new node id was
			// just generated and cannot already exist in any chain.
			// What we DO need: confirm ParentID exists for the tenant
			// (the CTE returns no rows if it doesn't), surface 400.
			parentChain, err := q.ParentChainIDs(ctx, dbx.ParentChainIDsParams{
				TenantID: pgUUID(tenantID),
				ID:       pgUUID(*in.ParentID),
			})
			if err != nil {
				return fmt.Errorf("parent chain lookup: %w", err)
			}
			if len(parentChain) == 0 {
				return fmt.Errorf("%w: parent_id %s not found", ErrNotFound, *in.ParentID)
			}
		}
		row, err := q.CreateOrgUnit(ctx, dbx.CreateOrgUnitParams{
			ID:                    pgUUID(id),
			TenantID:              pgUUID(tenantID),
			Name:                  in.Name,
			ParentID:              pgUUIDPtr(in.ParentID),
			Level:                 in.Level,
			AcceptanceAuthorities: defaultAuthoritiesJSON(in.AcceptanceAuthorities),
		})
		if err != nil {
			return fmt.Errorf("create org_unit: %w", err)
		}
		out = orgUnitFromRow(row)
		return nil
	})
	return out, err
}

// GetOrgUnit returns the unit by id; ErrNotFound if absent or cross-tenant.
func (s *Store) GetOrgUnit(ctx context.Context, id uuid.UUID) (OrgUnit, error) {
	var out OrgUnit
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetOrgUnitByID(ctx, dbx.GetOrgUnitByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get org_unit: %w", err)
		}
		out = orgUnitFromRow(row)
		return nil
	})
	return out, err
}

// ListOrgUnits returns every unit for the tenant ordered by level then name.
func (s *Store) ListOrgUnits(ctx context.Context) ([]OrgUnit, error) {
	var out []OrgUnit
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListOrgUnits(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list org_units: %w", err)
		}
		out = make([]OrgUnit, len(rows))
		for i, r := range rows {
			out[i] = orgUnitFromRow(r)
		}
		return nil
	})
	return out, err
}

// UpdateOrgUnit replaces the editable fields. Runs cycle detection if
// ParentID is set: walks the proposed parent chain; if `id` appears, returns
// ErrCycleDetected.
func (s *Store) UpdateOrgUnit(ctx context.Context, id uuid.UUID, in OrgUnitInput) (OrgUnit, error) {
	if !isValidLevel(in.Level) {
		return OrgUnit{}, ErrInvalidLevel
	}
	var out OrgUnit
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// AC-4 cycle detection: walk up from the proposed parent. If
		// `id` appears in the chain, setting parent_id = in.ParentID
		// would close a loop. Self-parent (in.ParentID == id) is the
		// trivial case — the CTE seed row returns `id`, the check
		// fires.
		if in.ParentID != nil {
			if *in.ParentID == id {
				return ErrCycleDetected
			}
			chain, err := q.ParentChainIDs(ctx, dbx.ParentChainIDsParams{
				TenantID: pgUUID(tenantID),
				ID:       pgUUID(*in.ParentID),
			})
			if err != nil {
				return fmt.Errorf("parent chain lookup: %w", err)
			}
			if len(chain) == 0 {
				return fmt.Errorf("%w: parent_id %s not found", ErrNotFound, *in.ParentID)
			}
			for _, nodeID := range chain {
				if uuid.UUID(nodeID.Bytes) == id {
					return ErrCycleDetected
				}
			}
		}
		row, err := q.UpdateOrgUnit(ctx, dbx.UpdateOrgUnitParams{
			TenantID:              pgUUID(tenantID),
			ID:                    pgUUID(id),
			Name:                  in.Name,
			ParentID:              pgUUIDPtr(in.ParentID),
			Level:                 in.Level,
			AcceptanceAuthorities: defaultAuthoritiesJSON(in.AcceptanceAuthorities),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("update org_unit: %w", err)
		}
		out = orgUnitFromRow(row)
		return nil
	})
	return out, err
}

// DeleteOrgUnit removes the unit. Risks bound to it survive (ON DELETE SET
// NULL on risks.org_unit_id per slice 052).
func (s *Store) DeleteOrgUnit(ctx context.Context, id uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if _, err := q.GetOrgUnitByID(ctx, dbx.GetOrgUnitByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get org_unit pre-delete: %w", err)
		}
		if err := q.DeleteOrgUnit(ctx, dbx.DeleteOrgUnitParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err != nil {
			return fmt.Errorf("delete org_unit: %w", err)
		}
		return nil
	})
}

// ----- helpers -----

func isValidLevel(l dbx.RiskLevel) bool {
	return l == dbx.RiskLevelTeam || l == dbx.RiskLevelOrg || l == dbx.RiskLevelCompany
}

func orgUnitFromRow(r dbx.OrgUnit) OrgUnit {
	u := OrgUnit{
		ID:                    uuid.UUID(r.ID.Bytes),
		TenantID:              uuid.UUID(r.TenantID.Bytes),
		Name:                  r.Name,
		Level:                 r.Level,
		AcceptanceAuthorities: json.RawMessage(r.AcceptanceAuthorities),
	}
	if r.ParentID.Valid {
		p := uuid.UUID(r.ParentID.Bytes)
		u.ParentID = &p
	}
	if len(u.AcceptanceAuthorities) == 0 {
		u.AcceptanceAuthorities = json.RawMessage("{}")
	}
	return u
}

func pgUUIDPtr(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *u, Valid: true}
}

// defaultAuthoritiesJSON returns the empty JSON array used as the default
// for org_units.acceptance_authorities. Slice 052's CHECK constraint
// requires the column to be a JSON array (canvas §6.4 — role tuples).
func defaultAuthoritiesJSON(in json.RawMessage) []byte {
	if len(in) == 0 {
		return []byte("[]")
	}
	return []byte(in)
}
