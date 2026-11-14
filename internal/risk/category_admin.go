package risk

// #1810: organisation category management, a step of #1745.
//
// Add, rename (name and description; the slug never changes), archive and
// un-archive, set the default, and delete an unused category. Every change
// writes one taxonomy_audit_log row in the same transaction, so a refused or
// rolled-back change leaves no trail entry.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Category management errors. The HTTP layer maps each to a status.
var (
	// ErrCategoryNotFound: no category of the caller's tenant has that slug
	// or id.
	ErrCategoryNotFound = errors.New("risk: category not found")
	// ErrCategoryConflict: the slug is taken, or the name is taken by
	// another active category.
	ErrCategoryConflict = errors.New("risk: category slug or name already in use")
	// ErrCategoryIsDefault: the default category cannot be archived or
	// deleted; move the default first.
	ErrCategoryIsDefault = errors.New("risk: the default category cannot be archived or deleted; set another default first")
	// ErrCategoryArchived: an archived category cannot become the default.
	ErrCategoryArchived = errors.New("risk: an archived category cannot be the default; un-archive it first")
)

// InvalidCategoryError is a request that fails the category shape rules
// (slug pattern, name length, description length).
type InvalidCategoryError struct{ Reason string }

func (e *InvalidCategoryError) Error() string { return "risk: invalid category: " + e.Reason }

// CategoryInUseError refuses deleting a category that risks or aggregation
// rules still use, with the counts.
type CategoryInUseError struct {
	Slug  string
	Risks int64
	Rules int64
}

func (e *CategoryInUseError) Error() string {
	return fmt.Sprintf("risk: category %q is used by %d risk(s) and %d aggregation rule(s); archive it instead",
		e.Slug, e.Risks, e.Rules)
}

// ArchivedCategoryError refuses assigning an archived category to a new
// risk. Archived categories stay on the risks that already have them.
type ArchivedCategoryError struct{ Slug string }

func (e *ArchivedCategoryError) Error() string {
	return fmt.Sprintf("risk: category %q is archived and cannot be assigned", e.Slug)
}

// CategoryUsage is a category with how many risks reference it and how
// many aggregation rules have produced a meta-risk in it.
type CategoryUsage struct {
	Category
	RiskCount int64
	RuleCount int64
}

// NewCategory is the input to CreateCategory.
type NewCategory struct {
	Slug        string
	Name        string
	Description string
}

// CategoryText is the input to RenameCategory. A nil field keeps its
// current value.
type CategoryText struct {
	Name        *string
	Description *string
}

// The same shape rules the org_risk_categories CHECKs enforce, checked
// first so a bad request is a 400 naming the rule, not a constraint name.
var categorySlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

const (
	maxCategoryName        = 200
	maxCategoryDescription = 2000
)

func validateCategoryText(name, description string) error {
	if strings.TrimSpace(name) == "" {
		return &InvalidCategoryError{Reason: "name is required"}
	}
	if len(name) > maxCategoryName {
		return &InvalidCategoryError{Reason: fmt.Sprintf("name exceeds %d characters", maxCategoryName)}
	}
	if len(description) > maxCategoryDescription {
		return &InvalidCategoryError{Reason: fmt.Sprintf("description exceeds %d characters", maxCategoryDescription)}
	}
	return nil
}

// ListCategoriesWithUsage returns the tenant's categories with usage
// counts, ordered by name. Archived categories are included only when
// includeArchived is true.
func (s *Store) ListCategoriesWithUsage(ctx context.Context, includeArchived bool) ([]CategoryUsage, error) {
	var out []CategoryUsage
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := q.SeedRiskCategories(ctx, pgUUID(tenantID)); err != nil {
			return fmt.Errorf("seed risk categories: %w", err)
		}
		rows, err := q.ListRiskCategoriesWithUsage(ctx, dbx.ListRiskCategoriesWithUsageParams{
			TenantID:        pgUUID(tenantID),
			IncludeArchived: includeArchived,
		})
		if err != nil {
			return fmt.Errorf("list risk categories: %w", err)
		}
		out = make([]CategoryUsage, len(rows))
		for i, r := range rows {
			out[i] = CategoryUsage{
				Category: categoryFromRow(dbx.OrgRiskCategory{
					ID: r.ID, TenantID: r.TenantID, Slug: r.Slug, Name: r.Name,
					Description: r.Description, Archived: r.Archived, IsDefault: r.IsDefault,
					CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				}),
				RiskCount: r.RiskCount,
				RuleCount: r.RuleCount,
			}
		}
		return nil
	})
	return out, err
}

// CreateCategory adds a category to the caller's tenant.
func (s *Store) CreateCategory(ctx context.Context, in NewCategory, actor string) (Category, error) {
	if !categorySlugPattern.MatchString(in.Slug) {
		return Category{}, &InvalidCategoryError{Reason: "slug must be lowercase letters, digits, '-' or '_', starting with a letter or digit, at most 63 characters"}
	}
	if err := validateCategoryText(in.Name, in.Description); err != nil {
		return Category{}, err
	}
	var out Category
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Seed first: otherwise a bare tenant's first add would leave it
		// with one category and later seeding would add the seven beside it.
		if err := q.SeedRiskCategories(ctx, pgUUID(tenantID)); err != nil {
			return fmt.Errorf("seed risk categories: %w", err)
		}
		row, err := q.CreateRiskCategory(ctx, dbx.CreateRiskCategoryParams{
			TenantID:    pgUUID(tenantID),
			Slug:        in.Slug,
			Name:        in.Name,
			Description: in.Description,
		})
		if err != nil {
			return mapCategoryWriteErr("create risk category", err)
		}
		out = categoryFromRow(row)
		return writeCategoryAudit(ctx, q, tenantID, "create", nil, &out, actor)
	})
	return out, err
}

// RenameCategory changes a category's name and/or description. The slug
// never changes.
func (s *Store) RenameCategory(ctx context.Context, ref string, in CategoryText, actor string) (Category, error) {
	return s.changeCategory(ctx, ref, actor, "rename", func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, cur Category) (dbx.OrgRiskCategory, error) {
		name, description := cur.Name, cur.Description
		if in.Name != nil {
			name = *in.Name
		}
		if in.Description != nil {
			description = *in.Description
		}
		if err := validateCategoryText(name, description); err != nil {
			return dbx.OrgRiskCategory{}, err
		}
		return q.UpdateRiskCategoryText(ctx, dbx.UpdateRiskCategoryTextParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(cur.ID), Name: name, Description: description,
		})
	})
}

// ArchiveCategory hides a category from the default list and from new
// assignments. Risks that already use it keep it. The default cannot be
// archived.
func (s *Store) ArchiveCategory(ctx context.Context, ref, actor string) (Category, error) {
	return s.setCategoryArchived(ctx, ref, actor, true)
}

// UnarchiveCategory restores an archived category.
func (s *Store) UnarchiveCategory(ctx context.Context, ref, actor string) (Category, error) {
	return s.setCategoryArchived(ctx, ref, actor, false)
}

func (s *Store) setCategoryArchived(ctx context.Context, ref, actor string, archived bool) (Category, error) {
	action := "unarchive"
	if archived {
		action = "archive"
	}
	return s.changeCategory(ctx, ref, actor, action, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, cur Category) (dbx.OrgRiskCategory, error) {
		if archived && cur.IsDefault {
			return dbx.OrgRiskCategory{}, ErrCategoryIsDefault
		}
		return q.SetRiskCategoryArchived(ctx, dbx.SetRiskCategoryArchivedParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(cur.ID), Archived: archived,
		})
	})
}

// SetDefaultCategory makes a category the tenant's default; the previous
// default stops being one in the same transaction, so exactly one remains.
func (s *Store) SetDefaultCategory(ctx context.Context, ref, actor string) (Category, error) {
	return s.changeCategory(ctx, ref, actor, "set_default", func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, cur Category) (dbx.OrgRiskCategory, error) {
		if cur.Archived {
			return dbx.OrgRiskCategory{}, ErrCategoryArchived
		}
		if err := q.ClearRiskCategoryDefault(ctx, dbx.ClearRiskCategoryDefaultParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(cur.ID),
		}); err != nil {
			return dbx.OrgRiskCategory{}, fmt.Errorf("clear default risk category: %w", err)
		}
		return q.SetRiskCategoryDefault(ctx, dbx.SetRiskCategoryDefaultParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(cur.ID),
		})
	})
}

// DeleteCategory removes a category no risk or aggregation rule uses. A
// used category is a *CategoryInUseError with the counts; the default is
// ErrCategoryIsDefault.
func (s *Store) DeleteCategory(ctx context.Context, ref, actor string) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		cur, err := lockCategory(ctx, q, tenantID, ref)
		if err != nil {
			return err
		}
		if cur.IsDefault {
			return ErrCategoryIsDefault
		}
		usage, err := q.RiskCategoryUsage(ctx, dbx.RiskCategoryUsageParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(cur.ID),
		})
		if err != nil {
			return fmt.Errorf("risk category usage: %w", err)
		}
		if usage.RiskCount > 0 || usage.RuleCount > 0 {
			return &CategoryInUseError{Slug: cur.Slug, Risks: usage.RiskCount, Rules: usage.RuleCount}
		}
		if err := q.DeleteRiskCategory(ctx, dbx.DeleteRiskCategoryParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(cur.ID),
		}); err != nil {
			// A risk written to it after the usage read still holds the
			// FK, so the delete fails rather than orphaning the risk.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return &CategoryInUseError{Slug: cur.Slug, Risks: usage.RiskCount + 1, Rules: usage.RuleCount}
			}
			return fmt.Errorf("delete risk category: %w", err)
		}
		return writeCategoryAudit(ctx, q, tenantID, "delete", &cur, nil, actor)
	})
}

// changeCategory locks the category named by ref, applies change, and
// records the before and after.
func (s *Store) changeCategory(
	ctx context.Context, ref, actor, action string,
	change func(context.Context, *dbx.Queries, uuid.UUID, Category) (dbx.OrgRiskCategory, error),
) (Category, error) {
	var out Category
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		cur, err := lockCategory(ctx, q, tenantID, ref)
		if err != nil {
			return err
		}
		row, err := change(ctx, q, tenantID, cur)
		if err != nil {
			return mapCategoryWriteErr(action+" risk category", err)
		}
		out = categoryFromRow(row)
		return writeCategoryAudit(ctx, q, tenantID, action, &cur, &out, actor)
	})
	return out, err
}

func lockCategory(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ref string) (Category, error) {
	if err := q.SeedRiskCategories(ctx, pgUUID(tenantID)); err != nil {
		return Category{}, fmt.Errorf("seed risk categories: %w", err)
	}
	row, err := q.LockRiskCategory(ctx, dbx.LockRiskCategoryParams{TenantID: pgUUID(tenantID), Ref: ref})
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrCategoryNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("lock risk category: %w", err)
	}
	return categoryFromRow(row), nil
}

// mapCategoryWriteErr turns a unique violation (slug, or active name) into
// ErrCategoryConflict and passes the sentinel and typed errors through.
func mapCategoryWriteErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrCategoryConflict
	}
	var invalid *InvalidCategoryError
	if errors.Is(err, ErrCategoryIsDefault) || errors.Is(err, ErrCategoryArchived) || errors.As(err, &invalid) {
		return err
	}
	return fmt.Errorf("%s: %w", op, err)
}

// categoryAuditState is a category as the audit trail records it.
type categoryAuditState struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Archived    bool   `json:"archived"`
	IsDefault   bool   `json:"is_default"`
}

func auditStateJSON(c *Category) ([]byte, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(categoryAuditState{
		Slug: c.Slug, Name: c.Name, Description: c.Description,
		Archived: c.Archived, IsDefault: c.IsDefault,
	})
}

func writeCategoryAudit(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, action string, before, after *Category, actor string) error {
	if actor == "" {
		return errors.New("risk: category change has no actor")
	}
	subject := after
	if subject == nil {
		subject = before
	}
	b, err := auditStateJSON(before)
	if err != nil {
		return fmt.Errorf("marshal category audit before: %w", err)
	}
	a, err := auditStateJSON(after)
	if err != nil {
		return fmt.Errorf("marshal category audit after: %w", err)
	}
	if err := q.InsertTaxonomyAuditLog(ctx, dbx.InsertTaxonomyAuditLogParams{
		TenantID:    pgUUID(tenantID),
		SubjectKind: "category",
		SubjectID:   pgUUID(subject.ID),
		SubjectSlug: subject.Slug,
		Action:      action,
		Before:      b,
		After:       a,
		Actor:       actor,
	}); err != nil {
		return fmt.Errorf("write taxonomy audit log: %w", err)
	}
	return nil
}
