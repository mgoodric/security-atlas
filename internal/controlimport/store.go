package controlimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the Postgres BatchStore. Batch tables are tenant-scoped and every
// statement runs inside a transaction carrying app.current_tenant, so RLS
// binds on the atlas_app role (invariant #6).
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ BatchStore = (*Store)(nil)

func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, string) error) error {
	tenantID, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("controlimport: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("controlimport: commit: %w", err)
	}
	return nil
}

// ResolveFramework returns the current version of slug. A tenant's own
// framework with that slug wins over the global catalog's.
func (s *Store) ResolveFramework(ctx context.Context, slug string) (FrameworkRef, bool, error) {
	var out FrameworkRef
	var ok bool
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ string) error {
		err := tx.QueryRow(ctx, `
			SELECT f.slug, fv.id::text, fv.version
			FROM frameworks f
			JOIN framework_versions fv ON fv.framework_id = f.id
			WHERE f.slug = $1 AND fv.status = 'current'
			ORDER BY f.tenant_id NULLS LAST
			LIMIT 1
		`, slug).Scan(&out.Slug, &out.VersionID, &out.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("controlimport: read framework: %w", err)
		}
		ok = true
		return nil
	})
	return out, ok, err
}

// ResolveRequirements matches codes case-insensitively within one framework
// version, keyed by the lower-cased code.
func (s *Store) ResolveRequirements(ctx context.Context, versionID string, codes []string) (map[string]Requirement, error) {
	out := make(map[string]Requirement, len(codes))
	if len(codes) == 0 {
		return out, nil
	}
	lowered := make([]string, len(codes))
	for i, c := range codes {
		lowered[i] = strings.ToLower(c)
	}
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ string) error {
		rows, err := tx.Query(ctx, `
			SELECT code, id::text
			FROM framework_requirements
			WHERE framework_version_id = $1::uuid AND lower(code) = ANY($2)
		`, versionID, lowered)
		if err != nil {
			return fmt.Errorf("controlimport: read requirements: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var r Requirement
			if err := rows.Scan(&r.Code, &r.RequirementID); err != nil {
				return fmt.Errorf("controlimport: scan requirement: %w", err)
			}
			out[strings.ToLower(r.Code)] = r
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CreateBatch writes the batch, its staged rows and its rejected rows in one
// transaction.
func (s *Store) CreateBatch(ctx context.Context, b Batch) (Batch, error) {
	unmapped, err := json.Marshal(nonNil(b.UnmappedColumns))
	if err != nil {
		return Batch{}, fmt.Errorf("controlimport: encode unmapped columns: %w", err)
	}
	err = s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID string) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO control_import_batches (
				tenant_id, framework_slug, framework_version_id, framework_version,
				source_filename, source_format, uploaded_by, status,
				total_rows, staged_count, rejected_count, unmapped_columns)
			VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb)
			RETURNING id::text, created_at
		`, tenantID, b.Framework.Slug, b.Framework.VersionID, b.Framework.Version,
			b.SourceFilename, b.SourceFormat, b.UploadedBy, b.Status,
			b.TotalRows, b.StagedCount, b.RejectedCount, string(unmapped),
		).Scan(&b.ID, &b.CreatedAt); err != nil {
			return fmt.Errorf("controlimport: insert batch: %w", err)
		}
		for i := range b.Staged {
			c := &b.Staged[i]
			tested, err := json.Marshal(nonNilReqs(c.TestedUnder))
			if err != nil {
				return fmt.Errorf("controlimport: encode tested_under: %w", err)
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO control_import_rows (
					tenant_id, batch_id, row_number, org_control_id, title, description,
					tested_under, implementation_type, applicability, status)
				VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8, $9, $10)
				RETURNING id::text
			`, tenantID, b.ID, c.RowNumber, c.ExternalID, c.Title, c.Description,
				string(tested), nullable(c.ImplementationType), nullable(c.Applicability), c.Status,
			).Scan(&c.ID); err != nil {
				return fmt.Errorf("controlimport: insert staged row %d: %w", c.RowNumber, err)
			}
		}
		for _, r := range b.Rejected {
			if _, err := tx.Exec(ctx, `
				INSERT INTO control_import_rejected_rows (
					tenant_id, batch_id, row_number, org_control_id, reason, detail)
				VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
			`, tenantID, b.ID, r.RowNumber, r.ExternalID, r.Reason, r.Detail); err != nil {
				return fmt.Errorf("controlimport: insert rejected row %d: %w", r.RowNumber, err)
			}
		}
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	b.Staged = nonNilControls(b.Staged)
	b.Rejected = nonNilRejected(b.Rejected)
	b.UnmappedColumns = nonNil(b.UnmappedColumns)
	return b, nil
}

// GetBatch reads one batch of the caller's tenant. Another tenant's batch is
// invisible under RLS and reads as ErrBatchNotFound.
func (s *Store) GetBatch(ctx context.Context, id string) (Batch, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Batch{}, ErrBatchNotFound
	}
	var b Batch
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ string) error {
		var unmapped []byte
		err := tx.QueryRow(ctx, `
			SELECT id::text, framework_slug, framework_version_id::text, framework_version,
			       source_filename, source_format, uploaded_by, status,
			       total_rows, staged_count, rejected_count, unmapped_columns, created_at
			FROM control_import_batches
			WHERE id = $1::uuid
		`, id).Scan(&b.ID, &b.Framework.Slug, &b.Framework.VersionID, &b.Framework.Version,
			&b.SourceFilename, &b.SourceFormat, &b.UploadedBy, &b.Status,
			&b.TotalRows, &b.StagedCount, &b.RejectedCount, &unmapped, &b.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBatchNotFound
		}
		if err != nil {
			return fmt.Errorf("controlimport: read batch: %w", err)
		}
		if err := json.Unmarshal(unmapped, &b.UnmappedColumns); err != nil {
			return fmt.Errorf("controlimport: decode unmapped columns: %w", err)
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
	b.Staged = nonNilControls(b.Staged)
	b.Rejected = nonNilRejected(b.Rejected)
	b.UnmappedColumns = nonNil(b.UnmappedColumns)
	return b, nil
}

func readStaged(ctx context.Context, tx pgx.Tx, batchID string) ([]BatchControl, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, row_number, org_control_id, title, description, tested_under,
		       COALESCE(implementation_type, ''), COALESCE(applicability, ''), status
		FROM control_import_rows
		WHERE batch_id = $1::uuid
		ORDER BY row_number
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("controlimport: read staged rows: %w", err)
	}
	defer rows.Close()
	var out []BatchControl
	for rows.Next() {
		var c BatchControl
		var tested []byte
		if err := rows.Scan(&c.ID, &c.RowNumber, &c.ExternalID, &c.Title, &c.Description, &tested,
			&c.ImplementationType, &c.Applicability, &c.Status); err != nil {
			return nil, fmt.Errorf("controlimport: scan staged row: %w", err)
		}
		if err := json.Unmarshal(tested, &c.TestedUnder); err != nil {
			return nil, fmt.Errorf("controlimport: decode tested_under: %w", err)
		}
		c.TestedUnder = nonNilReqs(c.TestedUnder)
		out = append(out, c)
	}
	return out, rows.Err()
}

func readRejected(ctx context.Context, tx pgx.Tx, batchID string) ([]RejectedRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT row_number, org_control_id, reason, detail
		FROM control_import_rejected_rows
		WHERE batch_id = $1::uuid
		ORDER BY row_number
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("controlimport: read rejected rows: %w", err)
	}
	defer rows.Close()
	var out []RejectedRow
	for rows.Next() {
		var r RejectedRow
		if err := rows.Scan(&r.RowNumber, &r.ExternalID, &r.Reason, &r.Detail); err != nil {
			return nil, fmt.Errorf("controlimport: scan rejected row: %w", err)
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

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilReqs(s []Requirement) []Requirement {
	if s == nil {
		return []Requirement{}
	}
	return s
}

func nonNilControls(s []BatchControl) []BatchControl {
	if s == nil {
		return []BatchControl{}
	}
	return s
}

func nonNilRejected(s []RejectedRow) []RejectedRow {
	if s == nil {
		return []RejectedRow{}
	}
	return s
}
