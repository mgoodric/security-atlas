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

// ReviewCadenceDays returns the tenant's risk review cadence (#2059): a risk
// created without a review date is due this many days after creation. The
// default is applied by the risks_review_due_default trigger; this read
// lets a caller show the date before the risk exists.
func (s *Store) ReviewCadenceDays(ctx context.Context) (int, error) {
	var days int32
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		d, err := q.GetRiskReviewCadenceDays(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("read risk review cadence: %w", err)
		}
		days = d
		return nil
	})
	return int(days), err
}

// resolveCategory finds the tenant's category named by ref, a slug or an id,
// and returns it with the enum value the risk row still carries. An unknown
// ref is an UnknownCategoryError naming the tenant's slugs.
func resolveCategory(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ref string) (Category, dbx.RiskCategory, error) {
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
	enum := dbx.RiskCategory(row.Slug)
	if !slices.Contains(dbx.AllRiskCategoryValues(), enum) {
		// Only the seeded slugs exist until the management API lands with
		// the contract step, so this is unreachable today; refuse rather
		// than write an enum value the column cannot hold.
		return Category{}, "", fmt.Errorf("risk: category %q has no legacy enum value", row.Slug)
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
