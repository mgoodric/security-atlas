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

// AmbiguousCategoryError is returned when a category named by display name
// (#1815) matches more than one of the tenant's categories: an archived
// category can share a name with an active one. Slugs lists every match so
// the caller can name one by slug instead.
type AmbiguousCategoryError struct {
	Ref   string
	Slugs []string
}

func (e *AmbiguousCategoryError) Error() string {
	return fmt.Sprintf("risk: category name %q matches more than one category; name one by slug: %s",
		e.Ref, strings.Join(e.Slugs, ", "))
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
	return ResolveCategoryTx(ctx, q, tenantID, ref)
}

// IsCategoryRefusal reports whether err is a category the caller named but
// cannot assign to a new risk: unknown, archived, or one that cannot hold
// risks yet. Callers map it to a client error.
func IsCategoryRefusal(err error) bool {
	var (
		archived  *ArchivedCategoryError
		invalid   *InvalidCategoryError
		ambiguous *AmbiguousCategoryError
	)
	return IsUnknownCategory(err) || errors.As(err, &archived) ||
		errors.As(err, &invalid) || errors.As(err, &ambiguous)
}

// ResolveCategoryTx resolves ref (a slug, an id, or a display name) to one
// of the tenant's categories inside the caller's tenant-scoped transaction,
// for writers outside Store such as the MCP create_risk applier (#1810). A
// slug or id match wins; otherwise ref is matched against display names,
// case-insensitively on the trimmed value (#1815). It returns the category
// with the enum value the risk row still carries, or an error that
// IsCategoryRefusal recognises when the category is unknown, ambiguous,
// archived, or cannot hold risks yet.
//
// Every caller assigns a risk to the result, so the matched rows are held
// FOR SHARE until the caller's transaction ends (#2233): an ArchiveCategory
// racing the write either waits for it to commit, or commits first and is
// seen here as archived. Category admin takes only the category row lock
// and never a risk row lock, so the share lock adds no deadlock cycle.
func ResolveCategoryTx(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ref string) (Category, dbx.RiskCategory, error) {
	if err := q.SeedRiskCategories(ctx, pgUUID(tenantID)); err != nil {
		return Category{}, "", fmt.Errorf("seed risk categories: %w", err)
	}
	row, err := q.ShareLockRiskCategory(ctx, dbx.ShareLockRiskCategoryParams{
		TenantID: pgUUID(tenantID),
		Ref:      ref,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if row, err = resolveCategoryByName(ctx, q, tenantID, ref); err != nil {
			return Category{}, "", err
		}
	case err != nil:
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

// resolveCategoryByName finds the tenant category whose display name is ref
// (#1815). Active names are unique per tenant and archived categories take
// no assignments, so a single active match wins over any archived ones
// (#2233). No match is an UnknownCategoryError naming the valid slugs;
// several active matches are an AmbiguousCategoryError naming theirs; only
// archived matches return the first, which the caller refuses as archived.
func resolveCategoryByName(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ref string) (dbx.OrgRiskCategory, error) {
	rows, err := q.ShareLockRiskCategoriesByName(ctx, dbx.ShareLockRiskCategoriesByNameParams{
		TenantID: pgUUID(tenantID),
		Name:     ref,
	})
	if err != nil {
		return dbx.OrgRiskCategory{}, fmt.Errorf("resolve risk category by name: %w", err)
	}
	if len(rows) == 0 {
		return dbx.OrgRiskCategory{}, unknownCategory(ctx, q, tenantID, ref)
	}
	active := slices.DeleteFunc(slices.Clone(rows), func(r dbx.OrgRiskCategory) bool { return r.Archived })
	switch len(active) {
	case 0:
		return rows[0], nil
	case 1:
		return active[0], nil
	}
	slugs := make([]string, len(active))
	for i, r := range active {
		slugs[i] = r.Slug
	}
	return dbx.OrgRiskCategory{}, &AmbiguousCategoryError{Ref: ref, Slugs: slugs}
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

// categoriesByID returns the tenant's categories, archived included, keyed
// by id. A risk's category slug and name are read from here (#1812): the
// reference is the source of truth, the enum only a dual-written copy.
func categoriesByID(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (map[uuid.UUID]Category, error) {
	rows, err := q.ListRiskCategories(ctx, pgUUID(tenantID))
	if err != nil {
		return nil, fmt.Errorf("list risk categories: %w", err)
	}
	out := make(map[uuid.UUID]Category, len(rows))
	for _, r := range rows {
		c := categoryFromRow(r)
		out[c.ID] = c
	}
	return out, nil
}

// setCategoryNames fills r's category slug and name from byID.
func setCategoryNames(byID map[uuid.UUID]Category, r *Risk) {
	c := byID[r.CategoryID]
	r.CategorySlug, r.CategoryName = c.Slug, c.Name
}

// withCategoryNames fills the category slug and name of each risk from the
// category it references.
func withCategoryNames(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, risks ...*Risk) error {
	byID, err := categoriesByID(ctx, q, tenantID)
	if err != nil {
		return err
	}
	for _, r := range risks {
		setCategoryNames(byID, r)
	}
	return nil
}

// categoryFilter resolves a list filter's category refs (slugs or ids)
// against the tenant's categories, archived included: an archived category
// stays filterable. It returns nil when refs is empty (no filter), and an
// UnknownCategoryError naming every slug the tenant has when a ref matches
// none of them.
func categoryFilter(byID map[uuid.UUID]Category, refs []string) (map[uuid.UUID]bool, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	want := make(map[uuid.UUID]bool, len(refs))
	for _, ref := range refs {
		id, ok := matchCategory(byID, ref)
		if !ok {
			valid := make([]string, 0, len(byID))
			for _, c := range byID {
				valid = append(valid, c.Slug)
			}
			slices.Sort(valid)
			return nil, &UnknownCategoryError{Ref: ref, Valid: valid}
		}
		want[id] = true
	}
	return want, nil
}

// matchCategory finds the category ref names, by slug or by id.
func matchCategory(byID map[uuid.UUID]Category, ref string) (uuid.UUID, bool) {
	for id, c := range byID {
		if c.Slug == ref || id.String() == ref {
			return id, true
		}
	}
	return uuid.Nil, false
}
