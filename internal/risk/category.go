package risk

// #1809: organisation-owned risk categories, the expand step of #1745.
//
// Each tenant owns its categories (org_risk_categories). The seven seeded
// ones carry the old risk_category enum values as slugs, so a caller that
// sends `operational` behaves exactly as before. A risk carries both the
// enum and a reference to its tenant's category until the contract step
// drops the enum; the risks_category_sync trigger keeps the pair in
// agreement on every write path.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Category is one of a tenant's risk categories.
type Category struct {
	ID          uuid.UUID
	Slug        string
	Name        string
	Description string
	Archived    bool
	IsDefault   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UnknownCategoryError is returned when a risk names a category that is not
// one of the tenant's, by slug or by id. Valid lists the tenant's slugs so
// the caller can correct the request.
type UnknownCategoryError struct {
	Ref   string
	Valid []string
}

func (e *UnknownCategoryError) Error() string {
	return fmt.Sprintf("risk: unknown category %q; valid categories: %s",
		e.Ref, strings.Join(e.Valid, ", "))
}

// IsUnknownCategory reports whether err is an UnknownCategoryError.
func IsUnknownCategory(err error) bool {
	var target *UnknownCategoryError
	return errors.As(err, &target)
}

// ListCategories returns the tenant's risk categories, archived included,
// ordered by name. A tenant without categories (a bare tenant id that
// predates tenant rows) is seeded first, so every tenant sees its seven.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	var out []Category
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := q.SeedRiskCategories(ctx, pgUUID(tenantID)); err != nil {
			return fmt.Errorf("seed risk categories: %w", err)
		}
		rows, err := q.ListRiskCategories(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list risk categories: %w", err)
		}
		out = make([]Category, len(rows))
		for i, r := range rows {
			out[i] = categoryFromRow(r)
		}
		return nil
	})
	return out, err
}

// resolveCategory finds the tenant's category named by ref, a slug or an id,
// and returns it with the enum value the risk row still carries. An unknown
// ref is an UnknownCategoryError naming the tenant's slugs.
func resolveCategory(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ref string) (Category, dbx.RiskCategory, error) {
	return ResolveCategoryTx(ctx, q, tenantID, ref)
}

// IsCategoryRefusal reports whether err is a category the caller named but
// cannot assign to a new risk: unknown, archived, or one that cannot hold
// risks yet. Callers map it to a client error.
func IsCategoryRefusal(err error) bool {
	var (
		archived *ArchivedCategoryError
		invalid  *InvalidCategoryError
	)
	return IsUnknownCategory(err) || errors.As(err, &archived) || errors.As(err, &invalid)
}

// ResolveCategoryTx resolves ref (a slug or an id) to one of the tenant's
// categories inside the caller's tenant-scoped transaction, for writers
// outside Store such as the MCP create_risk applier (#1810). It returns the
// category with the enum value the risk row still carries, or an error that
// IsCategoryRefusal recognises when the category is unknown, archived, or
// cannot hold risks yet.
func ResolveCategoryTx(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ref string) (Category, dbx.RiskCategory, error) {
	if err := q.SeedRiskCategories(ctx, pgUUID(tenantID)); err != nil {
		return Category{}, "", fmt.Errorf("seed risk categories: %w", err)
	}
	row, err := q.ResolveRiskCategory(ctx, dbx.ResolveRiskCategoryParams{
		TenantID: pgUUID(tenantID),
		Ref:      ref,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, "", unknownCategory(ctx, q, tenantID, ref)
	}
	if err != nil {
		return Category{}, "", fmt.Errorf("resolve risk category: %w", err)
	}
	if row.Archived {
		// #1810: archived categories stay on the risks that have them but
		// take no new assignments.
		return Category{}, "", &ArchivedCategoryError{Slug: row.Slug}
	}
	enum := dbx.RiskCategory(row.Slug)
	if !slices.Contains(dbx.AllRiskCategoryValues(), enum) {
		// A category added through the management API (#1810) has no enum
		// value until #1745's contract step drops the enum column; refuse
		// rather than write a value the column cannot hold.
		return Category{}, "", &InvalidCategoryError{Reason: fmt.Sprintf(
			"category %q cannot hold risks until the risk_category enum is retired (#1745)", row.Slug)}
	}
	return categoryFromRow(row), enum, nil
}

func unknownCategory(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ref string) error {
	rows, err := q.ListRiskCategories(ctx, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("list risk categories: %w", err)
	}
	valid := make([]string, 0, len(rows))
	for _, r := range rows {
		if !r.Archived {
			valid = append(valid, r.Slug)
		}
	}
	slices.Sort(valid)
	return &UnknownCategoryError{Ref: ref, Valid: valid}
}

func categoryFromRow(r dbx.OrgRiskCategory) Category {
	c := Category{
		ID:          uuid.UUID(r.ID.Bytes),
		Slug:        r.Slug,
		Name:        r.Name,
		Description: r.Description,
		Archived:    r.Archived,
		IsDefault:   r.IsDefault,
	}
	if r.CreatedAt.Valid {
		c.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		c.UpdatedAt = r.UpdatedAt.Time
	}
	return c
}
