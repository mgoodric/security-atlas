package risk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/duedate"
)

// PageSortKey is one of the register's sortable columns (#2132). Each is a
// fixed CASE branch in ListRisksPage's ORDER BY; nothing is interpolated.
type PageSortKey string

const (
	PageSortResidual    PageSortKey = "residual"
	PageSortTitle       PageSortKey = "title"
	PageSortCategory    PageSortKey = "category"
	PageSortOwner       PageSortKey = "owner"
	PageSortTreatment   PageSortKey = "treatment"
	PageSortReviewDue   PageSortKey = "review_due"
	PageSortStatus      PageSortKey = "status"
	PageSortMethodology PageSortKey = "methodology"
	PageSortLevel       PageSortKey = "level"
	PageSortOrgUnit     PageSortKey = "org_unit"
	PageSortThemes      PageSortKey = "themes"
	PageSortCreatedAt   PageSortKey = "created_at"
	PageSortID          PageSortKey = "id"
	// PageSortResidualAge is the dashboard's `residual,age` (slice 066):
	// residual descending, then oldest first. It takes no direction.
	PageSortResidualAge PageSortKey = "residual_age"
)

// pageSortKeys is the `?sort=` allow-list.
var pageSortKeys = map[PageSortKey]bool{
	PageSortResidual: true, PageSortTitle: true, PageSortCategory: true,
	PageSortOwner: true, PageSortTreatment: true, PageSortReviewDue: true,
	PageSortStatus: true, PageSortMethodology: true, PageSortLevel: true,
	PageSortOrgUnit: true, PageSortThemes: true, PageSortCreatedAt: true,
	PageSortID: true,
}

// ErrInvalidPageSort is returned by ParsePageSort for a value outside the
// allow-list. The HTTP layer maps it to 400.
var ErrInvalidPageSort = errors.New("risk: sort must be [-]residual|title|category|owner|treatment|review_due|status|methodology|level|org_unit|themes|created_at|id, or residual,age")

// PageSort is a parsed `?sort=`.
type PageSort struct {
	Key  PageSortKey
	Desc bool
}

// DefaultPageSort is the register's order with no `?sort=`: residual,
// highest first (#2132).
var DefaultPageSort = PageSort{Key: PageSortResidual, Desc: true}

// ParsePageSort reads `?sort=`: empty is DefaultPageSort, `key` sorts
// ascending, `-key` descending, and `residual,age` is the dashboard's
// ranking. Anything else is ErrInvalidPageSort.
func ParsePageSort(raw string) (PageSort, error) {
	switch raw {
	case "":
		return DefaultPageSort, nil
	case string(SortResidualAge):
		return PageSort{Key: PageSortResidualAge}, nil
	}
	desc := strings.HasPrefix(raw, "-")
	key := PageSortKey(strings.TrimPrefix(raw, "-"))
	if !pageSortKeys[key] {
		return PageSort{}, ErrInvalidPageSort
	}
	return PageSort{Key: key, Desc: desc}, nil
}

// ReviewDue is the `?review_due=` filter: overdue or due soon, by the
// duedate rule the calendar uses.
type ReviewDue string

const (
	ReviewDueAny     ReviewDue = ""
	ReviewDueOverdue ReviewDue = "overdue"
	ReviewDueSoon    ReviewDue = "due_soon"
)

// ReviewDueSoonDays is the register's due-soon window. It matches the
// calendar's risk-review window (calendar.DueSoonDays, #2130).
const ReviewDueSoonDays = 14

// PageFilter narrows, orders and pages the register (#2132). Zero fields
// are off.
type PageFilter struct {
	// Status is open or closed; empty keeps both.
	Status         dbx.RiskStatus
	IncludeRollUps bool
	Treatment      dbx.RiskTreatment
	Methodology    dbx.RiskMethodology
	Level          dbx.RiskLevel
	Theme          string
	OrgUnitID      *uuid.UUID
	// Categories are slugs or ids; an unknown one is an
	// UnknownCategoryError.
	Categories []string
	// Owner keeps one person's risks; Unowned the risks with no owner user.
	Owner   *uuid.UUID
	Unowned bool
	// ReviewDue, and the inclusive UTC-day range DueFrom..DueTo, narrow on
	// review_due_at. They compose: a risk must satisfy all that are set.
	ReviewDue ReviewDue
	DueFrom   *time.Time
	DueTo     *time.Time
	// SeverityBand keeps the non-FAIR risks whose assessed severity (the
	// wire `severity`, AssessedSeverity) is in that band: high, medium or
	// low (SeverityBandRange, #2490). Empty is off.
	SeverityBand string
	Sort         PageSort
	Limit        int32
	Offset       int32
	// Now is the instant overdue and due-soon are measured from; zero is
	// the current time.
	Now time.Time
}

func (f PageFilter) now() time.Time {
	if f.Now.IsZero() {
		return time.Now()
	}
	return f.Now
}

// dueBounds folds ReviewDue and the date range into the query's half-open
// [from, before) on review_due_at, in whole UTC days.
func (f PageFilter) dueBounds(now time.Time) (from, before pgtype.Timestamptz) {
	today := duedate.UTCDay(now)
	var lo, hi *time.Time
	narrowLo := func(t time.Time) {
		if lo == nil || t.After(*lo) {
			lo = &t
		}
	}
	narrowHi := func(t time.Time) {
		if hi == nil || t.Before(*hi) {
			hi = &t
		}
	}
	switch f.ReviewDue {
	case ReviewDueOverdue:
		narrowHi(today)
	case ReviewDueSoon:
		narrowLo(today)
		narrowHi(today.AddDate(0, 0, ReviewDueSoonDays+1))
	}
	if f.DueFrom != nil {
		narrowLo(duedate.UTCDay(*f.DueFrom))
	}
	if f.DueTo != nil {
		narrowHi(duedate.UTCDay(*f.DueTo).AddDate(0, 0, 1))
	}
	if lo != nil {
		from = pgtype.Timestamptz{Time: *lo, Valid: true}
	}
	if hi != nil {
		before = pgtype.Timestamptz{Time: *hi, Valid: true}
	}
	return from, before
}

// ErrInvalidSeverityBand is returned for a PageFilter.SeverityBand outside
// SeverityBandRange. The HTTP layer maps it to 400.
var ErrInvalidSeverityBand = errors.New("risk: severity_band must be one of high|medium|low")

// severityBounds turns SeverityBand into the query's inclusive bounds; both
// are nil when the band is off.
func (f PageFilter) severityBounds() (lo, hi *int32, err error) {
	if f.SeverityBand == "" {
		return nil, nil, nil
	}
	bandLo, bandHi, ok := SeverityBandRange(f.SeverityBand)
	if !ok {
		return nil, nil, ErrInvalidSeverityBand
	}
	l := int32(bandLo)
	lo = &l
	if bandHi != nil {
		h := int32(*bandHi)
		hi = &h
	}
	return lo, hi, nil
}

func optText[T ~string](v T) *string {
	if v == "" {
		return nil
	}
	s := string(v)
	return &s
}

func optUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgUUID(*id)
}

// ListPage returns one page of the active tenant's risk register and the
// filtered total (#2132). Filtering, sorting and paging are in SQL; the
// tenant is the explicit tenant_id plus RLS.
func (s *Store) ListPage(ctx context.Context, filter PageFilter) ([]Risk, int64, error) {
	var (
		out   []Risk
		total int64
	)
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if len(filter.Categories) > 0 {
			// A tenant with no risks may not have its seeded categories
			// yet; filtering by `operational` must still be valid.
			if err := q.SeedRiskCategories(ctx, pgUUID(tenantID)); err != nil {
				return fmt.Errorf("seed risk categories: %w", err)
			}
		}
		byID, err := categoriesByID(ctx, q, tenantID)
		if err != nil {
			return err
		}
		want, err := categoryFilter(byID, filter.Categories)
		if err != nil {
			return err
		}
		var categoryIDs []pgtype.UUID
		for id := range want {
			categoryIDs = append(categoryIDs, pgUUID(id))
		}
		dueFrom, dueBefore := filter.dueBounds(filter.now())
		sevMin, sevMax, err := filter.severityBounds()
		if err != nil {
			return err
		}
		count := dbx.CountRisksPageParams{
			TenantID:       pgUUID(tenantID),
			Status:         optText(filter.Status),
			IncludeRollups: filter.IncludeRollUps,
			Treatment:      optText(filter.Treatment),
			Methodology:    optText(filter.Methodology),
			Level:          optText(filter.Level),
			Theme:          optText(filter.Theme),
			OrgUnitID:      optUUID(filter.OrgUnitID),
			CategoryIds:    categoryIDs,
			OwnerUserID:    optUUID(filter.Owner),
			Unowned:        filter.Unowned,
			DueFrom:        dueFrom,
			DueBefore:      dueBefore,
			SeverityMin:    sevMin,
			SeverityMax:    sevMax,
		}
		total, err = q.CountRisksPage(ctx, count)
		if err != nil {
			return fmt.Errorf("count risks: %w", err)
		}
		rows, err := q.ListRisksPage(ctx, dbx.ListRisksPageParams{
			TenantID:       count.TenantID,
			Status:         count.Status,
			IncludeRollups: count.IncludeRollups,
			Treatment:      count.Treatment,
			Methodology:    count.Methodology,
			Level:          count.Level,
			Theme:          count.Theme,
			OrgUnitID:      count.OrgUnitID,
			CategoryIds:    count.CategoryIds,
			OwnerUserID:    count.OwnerUserID,
			Unowned:        count.Unowned,
			DueFrom:        count.DueFrom,
			DueBefore:      count.DueBefore,
			SeverityMin:    count.SeverityMin,
			SeverityMax:    count.SeverityMax,
			SortKey:        string(filter.Sort.Key),
			SortDesc:       filter.Sort.Desc,
			RowLimit:       filter.Limit,
			RowOffset:      filter.Offset,
		})
		if err != nil {
			return fmt.Errorf("list risks page: %w", err)
		}
		out = make([]Risk, len(rows))
		for i, r := range rows {
			rk := riskFromRow(r.Risk)
			setCategoryNames(byID, &rk)
			rk.ChildCount = int(r.ChildCount)
			out[i] = rk
		}
		return nil
	})
	return out, total, err
}
