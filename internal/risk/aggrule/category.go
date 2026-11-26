// category.go — the category a rule-generated meta-risk is created in
// (#1813, the migrate step of #1745).
//
// A meta-risk takes the category of its first scored child whose category
// is still active, otherwise the organisation's default category. An
// archived category takes no new assignments (#1810), and a meta-risk is a
// new risk, so an archived child category is passed over rather than
// copied. There is no hard-coded fallback: the default is whatever the
// tenant has set, custom categories included since the contract step
// (#1819). A tenant with no default gets the first scored child's
// category, archived or not.
package aggrule

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// metaRiskCategories is the tenant's category state as the engine needs it:
// which categories are archived, and which is the default.
type metaRiskCategories struct {
	archived map[uuid.UUID]bool
	// fallback is the default category; invalid when the tenant has none.
	fallback pgtype.UUID
}

// loadCategories reads the tenant's categories. It is a plain read: the
// engine calls it before any hierarchy, key or row lock.
func (e *Engine) loadCategories(ctx context.Context, tenantID uuid.UUID) (metaRiskCategories, error) {
	rows, err := e.w.ListRiskCategories(ctx, pgUUID(tenantID))
	if err != nil {
		return metaRiskCategories{}, fmt.Errorf("aggrule: list risk categories: %w", err)
	}
	return categoriesFromRows(rows), nil
}

func categoriesFromRows(rows []dbx.OrgRiskCategory) metaRiskCategories {
	cats := metaRiskCategories{archived: make(map[uuid.UUID]bool, len(rows))}
	for _, r := range rows {
		cats.archived[uuid.UUID(r.ID.Bytes)] = r.Archived
		if r.IsDefault {
			cats.fallback = r.ID
		}
	}
	return cats
}

// errNoMetaRiskCategory is returned when there is neither a scored child
// nor a default to take a category from. The rule thresholds make the
// first impossible for a firing rule; the second is a tenant without
// seeded categories.
var errNoMetaRiskCategory = errors.New("no scored child and no default risk category")

// pick chooses the meta-risk's category from its scored children, in
// candidate order.
func (c metaRiskCategories) pick(scored []dbx.Risk) (pgtype.UUID, error) {
	for _, r := range scored {
		if r.CategoryID.Valid && !c.archived[uuid.UUID(r.CategoryID.Bytes)] {
			return r.CategoryID, nil
		}
	}
	if c.fallback.Valid {
		return c.fallback, nil
	}
	if len(scored) > 0 && scored[0].CategoryID.Valid {
		return scored[0].CategoryID, nil
	}
	return pgtype.UUID{}, errNoMetaRiskCategory
}
