package vendor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// pgErrUniqueViolation matches the SQLSTATE Postgres returns when a UNIQUE
// constraint trips. We translate it to ErrDuplicateDomain for the API
// handlers (the only UNIQUE on `vendors` not on the PK is the partial
// (tenant_id, lower(domain)) index).
const pgErrUniqueViolation = "23505"

// Store wraps the sqlc-generated Queries with tenancy plumbing. Every
// method opens a transaction, applies the tenant GUC, runs queries inside
// that transaction, and commits or rolls back. Mirrors internal/scope/Store.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// enforced; see migrations/bootstrap/01-roles.sql.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts a vendor and attaches its scope cells. Validates input
// up-front (criticality + cadence enums, DPA consistency); RLS handles the
// tenant boundary at INSERT WITH CHECK.
//
// The full create+attach happens in a single transaction so a partial
// failure leaves no half-bound vendor in the table.
func (s *Store) Create(ctx context.Context, in CreateVendorInput) (Vendor, error) {
	if err := validateInput(in); err != nil {
		return Vendor{}, err
	}
	var out Vendor
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		vendorID := uuid.New()
		row, err := q.CreateVendor(ctx, dbx.CreateVendorParams{
			ID:             pgUUID(vendorID),
			TenantID:       pgUUID(tenantID),
			Name:           strings.TrimSpace(in.Name),
			Domain:         normalizeDomain(in.Domain),
			Criticality:    dbx.VendorCriticality(in.Criticality),
			ContractStart:  pgDate(in.ContractStart),
			ContractEnd:    pgDate(in.ContractEnd),
			DpaSigned:      in.DPASigned,
			DpaSignedAt:    pgDate(in.DPASignedAt),
			ReviewCadence:  dbx.VendorReviewCadence(in.ReviewCadence),
			LastReviewDate: pgDate(in.LastReviewDate),
			OwnerUser:      in.OwnerUser,
			LinkedSowUri:   normalizeOpt(in.LinkedSOWURI),
			Notes:          in.Notes,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
				return ErrDuplicateDomain
			}
			return fmt.Errorf("create vendor: %w", err)
		}
		if err := bindScopeCells(ctx, q, tenantID, vendorID, in.ScopeCellIDs); err != nil {
			return err
		}
		v, err := hydrate(ctx, q, tenantID, row)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

// Update replaces every field of an existing vendor and re-binds its
// scope-cell set to the input list. Partial updates are not supported in
// lite; the caller sends every field. Returns ErrVendorNotFound if the
// row does not exist for the active tenant.
func (s *Store) Update(ctx context.Context, id uuid.UUID, in UpdateVendorInput) (Vendor, error) {
	if err := validateInput(in); err != nil {
		return Vendor{}, err
	}
	var out Vendor
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.UpdateVendor(ctx, dbx.UpdateVendorParams{
			TenantID:       pgUUID(tenantID),
			ID:             pgUUID(id),
			Name:           strings.TrimSpace(in.Name),
			Domain:         normalizeDomain(in.Domain),
			Criticality:    dbx.VendorCriticality(in.Criticality),
			ContractStart:  pgDate(in.ContractStart),
			ContractEnd:    pgDate(in.ContractEnd),
			DpaSigned:      in.DPASigned,
			DpaSignedAt:    pgDate(in.DPASignedAt),
			ReviewCadence:  dbx.VendorReviewCadence(in.ReviewCadence),
			LastReviewDate: pgDate(in.LastReviewDate),
			OwnerUser:      in.OwnerUser,
			LinkedSowUri:   normalizeOpt(in.LinkedSOWURI),
			Notes:          in.Notes,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrVendorNotFound
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
				return ErrDuplicateDomain
			}
			return fmt.Errorf("update vendor: %w", err)
		}
		// Re-bind scope cells: clear then add. Same tx so failure rolls
		// back the partial state.
		if err := q.ClearVendorScopeCells(ctx, dbx.ClearVendorScopeCellsParams{
			TenantID: pgUUID(tenantID),
			VendorID: pgUUID(id),
		}); err != nil {
			return fmt.Errorf("clear scope cells: %w", err)
		}
		if err := bindScopeCells(ctx, q, tenantID, id, in.ScopeCellIDs); err != nil {
			return err
		}
		v, err := hydrate(ctx, q, tenantID, row)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

// Get fetches a single vendor by id. Returns ErrVendorNotFound for misses.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Vendor, error) {
	var out Vendor
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetVendor(ctx, dbx.GetVendorParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrVendorNotFound
			}
			return fmt.Errorf("get vendor: %w", err)
		}
		v, err := hydrate(ctx, q, tenantID, row)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

// Delete removes a vendor by id. CASCADE on vendor_scope_cells handles the
// join rows. No error if the row does not exist — DELETE is idempotent.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		return q.DeleteVendor(ctx, dbx.DeleteVendorParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
	})
}

// ListFilter narrows the result set of List. Zero value = no filter.
type ListFilter struct {
	Criticality *Criticality
	OverdueOnly bool
	Cutoff      time.Time // only consulted when OverdueOnly = true
}

// List returns the active tenant's vendors. AC-2: filter by criticality;
// optionally restrict to overdue rows. When OverdueOnly is true and Cutoff
// is zero, the cutoff defaults to time.Now() UTC; production callers always
// pass an explicit Cutoff (testability + audit-period reproducibility).
func (s *Store) List(ctx context.Context, f ListFilter) ([]Vendor, error) {
	var out []Vendor
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		critArg := pgCriticality(f.Criticality)
		var rows []dbx.Vendor
		var err error
		if f.OverdueOnly {
			cutoff := f.Cutoff
			if cutoff.IsZero() {
				cutoff = time.Now().UTC()
			}
			rows, err = q.ListOverdueVendors(ctx, dbx.ListOverdueVendorsParams{
				TenantID:    pgUUID(tenantID),
				Criticality: critArg,
				Cutoff:      pgDate(&cutoff),
			})
		} else {
			rows, err = q.ListVendors(ctx, dbx.ListVendorsParams{
				TenantID:    pgUUID(tenantID),
				Criticality: critArg,
			})
		}
		if err != nil {
			return fmt.Errorf("list vendors: %w", err)
		}
		out = make([]Vendor, 0, len(rows))
		for _, r := range rows {
			v, err := hydrate(ctx, q, tenantID, r)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// Burndown computes AC-3 review-on-time fractions. AsOf must be a date the
// caller controls; the SQL CASE walks vendor.last_review_date + cadence vs
// AsOf to classify "overdue". Optional criticality filter restricts the
// universe; when nil, every band is returned.
//
// The total aggregate is computed in Go because the SQL query already
// returns per-band rows.
func (s *Store) Burndown(ctx context.Context, asOf time.Time, crit *Criticality) (Burndown, error) {
	out := Burndown{AsOf: asOf}
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.CountVendorsForBurndown(ctx, dbx.CountVendorsForBurndownParams{
			TenantID:    pgUUID(tenantID),
			Criticality: pgCriticality(crit),
			Cutoff:      pgDate(&asOf),
		})
		if err != nil {
			return fmt.Errorf("count burndown: %w", err)
		}
		out.Bands = make([]BurndownBand, 0, len(rows))
		var totalAll, overdueAll int64
		for _, r := range rows {
			band := BurndownBand{
				Criticality:    Criticality(r.Criticality),
				Total:          r.TotalCount,
				Overdue:        r.OverdueCount,
				OnTimeFraction: onTime(r.TotalCount, r.OverdueCount),
			}
			out.Bands = append(out.Bands, band)
			totalAll += r.TotalCount
			overdueAll += r.OverdueCount
		}
		out.Total = BurndownBand{
			Total:          totalAll,
			Overdue:        overdueAll,
			OnTimeFraction: onTime(totalAll, overdueAll),
		}
		return nil
	})
	return out, err
}

// ----- internal helpers -----

func bindScopeCells(ctx context.Context, q *dbx.Queries, tenantID, vendorID uuid.UUID, cellIDs []uuid.UUID) error {
	for _, cellID := range cellIDs {
		if err := q.AddVendorScopeCell(ctx, dbx.AddVendorScopeCellParams{
			TenantID:    pgUUID(tenantID),
			VendorID:    pgUUID(vendorID),
			ScopeCellID: pgUUID(cellID),
		}); err != nil {
			return fmt.Errorf("attach scope cell %s: %w", cellID, err)
		}
	}
	return nil
}

func hydrate(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, r dbx.Vendor) (Vendor, error) {
	cells, err := q.ListVendorScopeCells(ctx, dbx.ListVendorScopeCellsParams{
		TenantID: pgUUID(tenantID),
		VendorID: r.ID,
	})
	if err != nil {
		return Vendor{}, fmt.Errorf("list scope cells: %w", err)
	}
	cellIDs := make([]uuid.UUID, 0, len(cells))
	for _, c := range cells {
		cellIDs = append(cellIDs, uuid.UUID(c.Bytes))
	}
	return Vendor{
		ID:             uuid.UUID(r.ID.Bytes),
		TenantID:       uuid.UUID(r.TenantID.Bytes),
		Name:           r.Name,
		Domain:         r.Domain,
		Criticality:    Criticality(r.Criticality),
		ContractStart:  fromPgDate(r.ContractStart),
		ContractEnd:    fromPgDate(r.ContractEnd),
		DPASigned:      r.DpaSigned,
		DPASignedAt:    fromPgDate(r.DpaSignedAt),
		ReviewCadence:  ReviewCadence(r.ReviewCadence),
		LastReviewDate: fromPgDate(r.LastReviewDate),
		OwnerUser:      r.OwnerUser,
		LinkedSOWURI:   r.LinkedSowUri,
		Notes:          r.Notes,
		ScopeCellIDs:   cellIDs,
		CreatedAt:      r.CreatedAt.Time,
		UpdatedAt:      r.UpdatedAt.Time,
	}, nil
}

func validateInput(in CreateVendorInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if !in.Criticality.Valid() {
		return fmt.Errorf("%w: criticality %q is not one of low/medium/high", ErrInvalidInput, in.Criticality)
	}
	if !in.ReviewCadence.Valid() {
		return fmt.Errorf("%w: review_cadence %q is not one of monthly/quarterly/biannual/annual", ErrInvalidInput, in.ReviewCadence)
	}
	if in.DPASigned && in.DPASignedAt == nil {
		return fmt.Errorf("%w: dpa_signed=true requires dpa_signed_at", ErrInvalidInput)
	}
	if in.ContractStart != nil && in.ContractEnd != nil && in.ContractEnd.Before(*in.ContractStart) {
		return fmt.Errorf("%w: contract_end is before contract_start", ErrInvalidInput)
	}
	return nil
}

// inTx mirrors internal/scope.inTx — opens a transaction, applies the
// tenancy GUC, runs fn, and commits if fn returns nil. The transaction-
// scoped GUC is what keeps RLS honest (canvas §5.4); a session-scope set
// outside a tx would silently no-op for is_local=true.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("vendor: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("vendor: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("vendor: commit: %w", err)
	}
	return nil
}

func onTime(total, overdue int64) float64 {
	if total == 0 {
		return 1.0
	}
	return float64(total-overdue) / float64(total)
}

// ----- pgtype shims -----

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{Valid: false}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

func fromPgDate(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

func pgCriticality(c *Criticality) *dbx.VendorCriticality {
	if c == nil {
		return nil
	}
	v := dbx.VendorCriticality(*c)
	return &v
}

func normalizeDomain(d *string) *string {
	if d == nil {
		return nil
	}
	v := strings.ToLower(strings.TrimSpace(*d))
	if v == "" {
		return nil
	}
	return &v
}

func normalizeOpt(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}
