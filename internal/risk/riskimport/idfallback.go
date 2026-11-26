package riskimport

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/risk"
)

// The risk export writes a risk's owner and org unit as uuids of the
// exporting tenant: owner_user_id and org_unit_id (#2898). Re-imported into
// the same tenant they name the same user and unit, and placement and the
// linked owner survive the round trip. Into another tenant they name
// nothing.
//
// So an id cell is used only when it resolves in the uploading tenant:
//
//   - owner_user_id, when it is an active user of the tenant. It then wins
//     over the owner cell, as an owner user wins over the free-text owner on
//     a single create;
//   - org_unit_id, when the tenant-filtered GetOrgUnitByID finds it, and
//     only when the row names no org unit by name, which keeps the name
//     rule (placement.go) the one a register written by hand follows.
//
// Otherwise the cell is kept as an unmapped value, as it was before #2898,
// and nothing is rejected for it. A column whose every value was used is
// not listed among the batch's unmapped columns.

//
// The export's parent_risk_ids (#2957) follows the same rule, id by id:
//
//   - each id that is a risk of the uploading tenant is staged on the row,
//     and the row's risk is linked under it at approval through
//     risk.LinkChildrenTx (parentlink.go). Only when the row names no
//     parent by organisation risk ID, which keeps the parent-risk-ID
//     column the one a register written by hand follows;
//   - an id that names no risk of the tenant stays in the unmapped value,
//     which then holds only those ids.

// ID fields. Like the others they are column-map keys.
const (
	FieldOwnerUserID   = "owner_user_id"
	FieldOrgUnitID     = "org_unit_id"
	FieldParentRiskIDs = "parent_risk_ids"
)

// isIDField reports whether field's values are kept as unmapped until
// staging resolves them.
func isIDField(field string) bool {
	return field == FieldOwnerUserID || field == FieldOrgUnitID || field == FieldParentRiskIDs
}

// splitParentRiskIDs splits a parent_risk_ids cell on semicolons, as the
// export joins them, dropping blanks.
func splitParentRiskIDs(val string) []string {
	var out []string
	for _, p := range strings.Split(val, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveParentRiskIDs returns the refs that are the ids of risks of the
// caller's tenant, keyed by the ref as written. The lookup is filtered on
// the tenant as well as bound by RLS; another tenant's risk is not found.
func (s *Store) resolveParentRiskIDs(ctx context.Context, refs []string) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	byID := map[uuid.UUID][]string{}
	var ids []uuid.UUID
	for _, ref := range refs {
		id, err := uuid.Parse(ref)
		if err != nil {
			continue
		}
		if _, seen := byID[id]; !seen {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], ref)
	}
	if len(ids) == 0 {
		return out, nil
	}
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT id FROM risks WHERE tenant_id = $1 AND id = ANY($2::uuid[])
		`, tenantID, ids)
		if err != nil {
			return fmt.Errorf("riskimport: resolve parent risk ids: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("riskimport: scan parent risk id: %w", err)
			}
			for _, ref := range byID[id] {
				out[ref] = id
			}
		}
		return rows.Err()
	})
	return out, err
}

// applyParentRiskIDs stages the row's parent_risk_ids that resolved, once
// each, and leaves only the ids that did not in its unmapped value. A row
// that names a parent by organisation risk ID stages none, and keeps the
// cell unmapped as written.
func applyParentRiskIDs(row *BatchRow, r StagedRisk, resolved map[string]uuid.UUID, key string) {
	if len(r.ParentRiskIDRefs) == 0 || r.ParentRef != "" {
		return
	}
	var left []string
	seen := map[uuid.UUID]bool{}
	for _, ref := range r.ParentRiskIDRefs {
		id, ok := resolved[ref]
		if !ok {
			left = append(left, ref)
			continue
		}
		if !seen[id] {
			seen[id] = true
			row.ParentRiskIDs = append(row.ParentRiskIDs, id.String())
		}
	}
	if len(left) == 0 {
		delete(row.Unmapped, key)
	} else {
		row.Unmapped[key] = strings.Join(left, ";")
	}
}

// resolveOwnerUserIDs returns the refs that are the ids of active users of
// the caller's tenant, keyed by the ref as written.
func (s *Store) resolveOwnerUserIDs(ctx context.Context, refs []string) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	byID := map[uuid.UUID][]string{}
	var ids []uuid.UUID
	for _, ref := range refs {
		id, err := uuid.Parse(ref)
		if err != nil {
			continue
		}
		if _, seen := byID[id]; !seen {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], ref)
	}
	if len(ids) == 0 {
		return out, nil
	}
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT id FROM users
			WHERE tenant_id = $1 AND status = 'active' AND id = ANY($2::uuid[])
		`, tenantID, ids)
		if err != nil {
			return fmt.Errorf("riskimport: resolve owner user ids: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("riskimport: scan owner user id: %w", err)
			}
			for _, ref := range byID[id] {
				out[ref] = id
			}
		}
		return rows.Err()
	})
	return out, err
}

// resolveOrgUnitIDs returns the org units of the caller's tenant that the
// refs of rows naming no org unit by name are the ids of, keyed by the ref
// as written. Each id is looked up through risk.Store.GetOrgUnit, the
// tenant-filtered GetOrgUnitByID; another tenant's unit is not found.
func (s *Store) resolveOrgUnitIDs(ctx context.Context, staged []StagedRisk) (map[string]risk.OrgUnit, error) {
	out := map[string]risk.OrgUnit{}
	for _, r := range staged {
		if r.OrgUnit != "" || r.OrgUnitIDRef == "" {
			continue
		}
		if _, done := out[r.OrgUnitIDRef]; done {
			continue
		}
		id, err := uuid.Parse(r.OrgUnitIDRef)
		if err != nil {
			continue
		}
		u, err := s.risks.GetOrgUnit(ctx, id)
		if errors.Is(err, risk.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("riskimport: resolve org unit id: %w", err)
		}
		out[r.OrgUnitIDRef] = u
	}
	return out, nil
}

// dropUsedIDColumns removes from the batch's unmapped columns each id
// column no staged row still holds an unmapped value under.
func dropUsedIDColumns(b *Batch, idColumns map[string]string) {
	for _, key := range idColumns {
		held := slices.ContainsFunc(b.Staged, func(r BatchRow) bool {
			_, ok := r.Unmapped[key]
			return ok
		})
		if !held {
			b.UnmappedColumns = slices.DeleteFunc(b.UnmappedColumns, func(k string) bool { return k == key })
		}
	}
}
