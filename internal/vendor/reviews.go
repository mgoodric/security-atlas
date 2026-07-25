package vendor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// pgErrForeignKeyViolation is the SQLSTATE Postgres returns when an FK
// constraint trips. RecordReview translates it to ErrVendorNotFound: a
// review whose (tenant_id, vendor_id) does not resolve to a vendor row for
// the active tenant is, from the caller's perspective, a missing vendor
// (RLS also keeps cross-tenant vendor_ids invisible, so this is the
// fabricated / cross-tenant-id path).
const pgErrForeignKeyViolation = "23503"

// RecordReview appends a completed review to the vendor_reviews ledger and,
// when the new review is the most-recent on file, keeps the vendor's
// last_review_date scalar consistent with the ledger (AC-2, decisions log
// D2). Both writes happen in one transaction so a partial failure leaves no
// review recorded without the scalar update (and vice versa).
//
// Append-only: there is no UpdateReview / DeleteReview. A recorded review is
// immutable (canvas Invariant 2 — append-only spirit).
func (s *Store) RecordReview(ctx context.Context, in RecordReviewInput) (Review, error) {
	if err := validateReview(in); err != nil {
		return Review{}, err
	}
	var out Review
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.CreateVendorReview(ctx, dbx.CreateVendorReviewParams{
			ID:         pgUUID(uuid.New()),
			TenantID:   pgUUID(tenantID),
			VendorID:   pgUUID(in.VendorID),
			ReviewedAt: pgDate(&in.ReviewedAt),
			Reviewer:   strings.TrimSpace(in.Reviewer),
			Outcome:    dbx.VendorReviewOutcome(in.Outcome),
			Notes:      in.Notes,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgErrForeignKeyViolation {
				return ErrVendorNotFound
			}
			return fmt.Errorf("create vendor review: %w", err)
		}
		// Keep vendors.last_review_date consistent: set it to the most-recent
		// reviewed_at on the ledger. The just-inserted row participates, so a
		// back-dated review never regresses the scalar below an existing newer
		// review (AC-2 / D2).
		latest, err := q.LatestVendorReviewDate(ctx, dbx.LatestVendorReviewDateParams{
			TenantID: pgUUID(tenantID),
			VendorID: pgUUID(in.VendorID),
		})
		if err != nil {
			// A freshly-inserted ledger row guarantees at least one row, so
			// ErrNoRows here is impossible; surface anything unexpected.
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("latest review date: %w", err)
			}
		} else if latest.Valid {
			if err := q.SetVendorLastReviewDate(ctx, dbx.SetVendorLastReviewDateParams{
				TenantID:       pgUUID(tenantID),
				ID:             pgUUID(in.VendorID),
				LastReviewDate: latest,
			}); err != nil {
				return fmt.Errorf("set last review date: %w", err)
			}
		}
		out = reviewFromRow(row)
		return nil
	})
	return out, err
}

// ListReviews returns a vendor's review history newest-first (AC-3). RLS
// scopes the read to the active tenant; a cross-tenant or fabricated
// vendor_id yields an empty slice (the rows are simply invisible), never a
// leak.
func (s *Store) ListReviews(ctx context.Context, vendorID uuid.UUID) ([]Review, error) {
	var out []Review
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListVendorReviews(ctx, dbx.ListVendorReviewsParams{
			TenantID: pgUUID(tenantID),
			VendorID: pgUUID(vendorID),
		})
		if err != nil {
			return fmt.Errorf("list vendor reviews: %w", err)
		}
		out = make([]Review, 0, len(rows))
		for _, r := range rows {
			out = append(out, reviewFromRow(r))
		}
		return nil
	})
	return out, err
}

// ----- internal helpers -----

func validateReview(in RecordReviewInput) error {
	if in.VendorID == uuid.Nil {
		return fmt.Errorf("%w: vendor_id is required", ErrInvalidInput)
	}
	if in.ReviewedAt.IsZero() {
		return fmt.Errorf("%w: reviewed_at is required", ErrInvalidInput)
	}
	if !in.Outcome.Valid() {
		return fmt.Errorf("%w: outcome %q is not one of pass/pass_with_findings/fail/waived", ErrInvalidInput, in.Outcome)
	}
	return nil
}

func reviewFromRow(r dbx.VendorReview) Review {
	rv := Review{
		ID:        uuid.UUID(r.ID.Bytes),
		VendorID:  uuid.UUID(r.VendorID.Bytes),
		Reviewer:  r.Reviewer,
		Outcome:   ReviewOutcome(r.Outcome),
		Notes:     r.Notes,
		CreatedAt: r.CreatedAt.Time,
	}
	if r.ReviewedAt.Valid {
		rv.ReviewedAt = r.ReviewedAt.Time
	}
	return rv
}
