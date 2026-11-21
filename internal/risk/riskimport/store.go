package riskimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store stages import batches and decides their rows. Batch tables are
// tenant-scoped and every statement runs inside a transaction carrying
// app.current_tenant, so RLS binds on the atlas_app role (invariant #6).
// Approved rows become risks through risks, the platform's risk store, so
// its create-time rules and its after-write hook apply.
type Store struct {
	pool  *pgxpool.Pool
	risks *risk.Store
}

// NewStore returns a Store over pool that creates risks through risks.
func NewStore(pool *pgxpool.Pool, risks *risk.Store) *Store {
	return &Store{pool: pool, risks: risks}
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("riskimport: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("riskimport: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("riskimport: commit: %w", err)
	}
	return nil
}

// createBatch writes the batch, its staged rows and its rejected rows in
// one transaction.
func (s *Store) createBatch(ctx context.Context, b Batch) (Batch, error) {
	unmapped, err := json.Marshal(nonNil(b.UnmappedColumns))
	if err != nil {
		return Batch{}, fmt.Errorf("riskimport: encode unmapped columns: %w", err)
	}
	err = s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO risk_import_batches (
				tenant_id, source_filename, source_format, methodology, uploaded_by, status,
				total_rows, staged_count, rejected_count, unmapped_columns)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
			RETURNING id::text, created_at
		`, tenantID, b.SourceFilename, b.SourceFormat, b.Methodology, b.UploadedBy, b.Status,
			b.TotalRows, b.StagedCount, b.RejectedCount, string(unmapped),
		).Scan(&b.ID, &b.CreatedAt); err != nil {
			return fmt.Errorf("riskimport: insert batch: %w", err)
		}
		for i := range b.Staged {
			r := &b.Staged[i]
			vals, err := json.Marshal(nonNilMap(r.Unmapped))
			if err != nil {
				return fmt.Errorf("riskimport: encode unmapped values: %w", err)
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO risk_import_rows (
					tenant_id, batch_id, row_number, external_ref, title, description,
					category_slug, review_due, inherent_score, residual_score, unmapped, flags, status)
				VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8::date, $9::jsonb, $10::jsonb, $11::jsonb, $12, $13)
				RETURNING id::text
			`, tenantID, b.ID, r.RowNumber, nullable(r.ExternalRef), r.Title, r.Description,
				r.Category, nullable(r.ReviewDue), nullable(string(r.InherentScore)), nullable(string(r.ResidualScore)),
				string(vals), nonNil(r.Flags), r.Status,
			).Scan(&r.ID); err != nil {
				return fmt.Errorf("riskimport: insert staged row %d: %w", r.RowNumber, err)
			}
		}
		for _, r := range b.Rejected {
			if _, err := tx.Exec(ctx, `
				INSERT INTO risk_import_rejected_rows (
					tenant_id, batch_id, row_number, external_ref, reason, detail)
				VALUES ($1, $2::uuid, $3, $4, $5, $6)
			`, tenantID, b.ID, r.RowNumber, r.ExternalRef, r.Reason, r.Detail); err != nil {
				return fmt.Errorf("riskimport: insert rejected row %d: %w", r.RowNumber, err)
			}
		}
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	for i := range b.Staged {
		b.Staged[i].Unmapped = nonNilMap(b.Staged[i].Unmapped)
		b.Staged[i].Flags = nonNil(b.Staged[i].Flags)
	}
	b.UnmappedColumns = nonNil(b.UnmappedColumns)
	return b, nil
}

// getBatch reads one batch of the caller's tenant. Another tenant's batch
// is invisible under RLS and reads as ErrBatchNotFound.
func (s *Store) getBatch(ctx context.Context, id string) (Batch, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Batch{}, ErrBatchNotFound
	}
	var b Batch
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		var unmapped []byte
		err := tx.QueryRow(ctx, `
			SELECT id::text, source_filename, source_format, methodology, uploaded_by, status,
			       total_rows, staged_count, rejected_count, unmapped_columns, created_at
			FROM risk_import_batches
			WHERE id = $1::uuid
		`, id).Scan(&b.ID, &b.SourceFilename, &b.SourceFormat, &b.Methodology, &b.UploadedBy, &b.Status,
			&b.TotalRows, &b.StagedCount, &b.RejectedCount, &unmapped, &b.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBatchNotFound
		}
		if err != nil {
			return fmt.Errorf("riskimport: read batch: %w", err)
		}
		if err := json.Unmarshal(unmapped, &b.UnmappedColumns); err != nil {
			return fmt.Errorf("riskimport: decode unmapped columns: %w", err)
		}
		if b.Staged, err = readStaged(ctx, tx, id); err != nil {
			return err
		}
		b.Rejected, err = readRejected(ctx, tx, id)
		return err
	})
	if err != nil {
		return Batch{}, err
	}
	if b.Staged == nil {
		b.Staged = []BatchRow{}
	}
	if b.Rejected == nil {
		b.Rejected = []RejectedRow{}
	}
	b.UnmappedColumns = nonNil(b.UnmappedColumns)
	return b, nil
}

func readStaged(ctx context.Context, tx pgx.Tx, batchID string) ([]BatchRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, row_number, COALESCE(external_ref, ''), title, description, category_slug,
		       COALESCE(to_char(review_due, 'YYYY-MM-DD'), ''), inherent_score::text, residual_score::text, unmapped, flags, status,
		       COALESCE(decided_by, ''), COALESCE(risk_id::text, ''), COALESCE(rejection_reason, '')
		FROM risk_import_rows
		WHERE batch_id = $1::uuid
		ORDER BY row_number
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("riskimport: read staged rows: %w", err)
	}
	defer rows.Close()
	var out []BatchRow
	for rows.Next() {
		var r BatchRow
		var vals []byte
		var inherent, residual *string
		if err := rows.Scan(&r.ID, &r.RowNumber, &r.ExternalRef, &r.Title, &r.Description, &r.Category,
			&r.ReviewDue, &inherent, &residual, &vals, &r.Flags, &r.Status, &r.DecidedBy, &r.RiskID, &r.RejectionReason); err != nil {
			return nil, fmt.Errorf("riskimport: scan staged row: %w", err)
		}
		r.InherentScore, r.ResidualScore = rawJSON(inherent), rawJSON(residual)
		if err := json.Unmarshal(vals, &r.Unmapped); err != nil {
			return nil, fmt.Errorf("riskimport: decode unmapped values: %w", err)
		}
		r.Unmapped = nonNilMap(r.Unmapped)
		r.Flags = nonNil(r.Flags)
		out = append(out, r)
	}
	return out, rows.Err()
}

func readRejected(ctx context.Context, tx pgx.Tx, batchID string) ([]RejectedRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT row_number, external_ref, reason, detail
		FROM risk_import_rejected_rows
		WHERE batch_id = $1::uuid
		ORDER BY row_number
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("riskimport: read rejected rows: %w", err)
	}
	defer rows.Close()
	var out []RejectedRow
	for rows.Next() {
		var r RejectedRow
		if err := rows.Scan(&r.RowNumber, &r.ExternalRef, &r.Reason, &r.Detail); err != nil {
			return nil, fmt.Errorf("riskimport: scan rejected row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// rawJSON is a nullable jsonb column read as text, nil when NULL.
func rawJSON(s *string) json.RawMessage {
	if s == nil {
		return nil
	}
	return json.RawMessage(*s)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
