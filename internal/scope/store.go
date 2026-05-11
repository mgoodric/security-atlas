package scope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// pgErrUniqueViolation is the SQLSTATE Postgres returns when a UNIQUE
// constraint trips. We translate it to ErrCellExists for the API handlers.
const pgErrUniqueViolation = "23505"

// BuiltinDimensions is the canvas-§2.4 dimension set the platform seeds on
// tenant bootstrap. Admins can add custom dimensions on top of these.
var BuiltinDimensions = []DimensionSeed{
	{Name: "business_unit", ValueType: "string", IsRequired: false},
	{Name: "environment", ValueType: "string", IsRequired: true,
		AllowedValues: []string{"prod", "staging", "dev", "sandbox"}},
	{Name: "geography", ValueType: "string", IsRequired: false},
	{Name: "cloud_account", ValueType: "string", IsRequired: false},
	{Name: "data_classification", ValueType: "string", IsRequired: true,
		AllowedValues: []string{"restricted", "confidential", "internal", "public"}},
	{Name: "product_line", ValueType: "string", IsRequired: false},
}

// DefaultCellDimensions is the canvas-default tuple used by SeedTenant for a
// brand-new tenant — `bu=default`, `env=prod`, `data_classification=internal`.
// Other builtin dimensions are omitted (their values are nullable).
var DefaultCellDimensions = map[string]string{
	"business_unit":       "default",
	"environment":         "prod",
	"data_classification": "internal",
}

// DimensionSeed declares a built-in dimension. The platform seeds this set on
// tenant bootstrap so the default-cell INSERT has somewhere to validate against.
type DimensionSeed struct {
	Name          string
	ValueType     string
	AllowedValues []string
	IsRequired    bool
}

// ErrCellExists is returned by Store.CreateCell when a cell with the same
// dimensions already exists for the tenant. Callers treat this as 409 Conflict.
var ErrCellExists = errors.New("scope: cell with these dimensions already exists")

// ErrInvalidDimension is returned when a dimension on a write request is not
// declared in scope_dimensions or its value is outside allowed_values.
var ErrInvalidDimension = errors.New("scope: dimension not declared or value not allowed")

// Store wraps the sqlc Queries with the tenancy plumbing required for RLS.
// Every method opens a transaction, applies the tenant GUC, and runs queries
// inside that transaction so RLS policies see the tenant id.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced; see migrations/bootstrap/01-roles.sql.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// SeedTenant idempotently seeds the builtin dimensions and the canvas-default
// scope cell for a fresh tenant. It is safe to call repeatedly: dimensions are
// upserted by (tenant_id, name) and the default cell is created only if a cell
// with the same dimension hash does not already exist.
//
// Returns the cell that represents the default, whether newly created or found.
func (s *Store) SeedTenant(ctx context.Context) (Cell, error) {
	var seeded Cell
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Seed dimensions first (idempotent via per-tenant get-or-insert).
		for _, d := range BuiltinDimensions {
			if _, err := q.GetScopeDimensionByName(ctx, dbx.GetScopeDimensionByNameParams{
				TenantID: pgUUID(tenantID),
				Name:     d.Name,
			}); err == nil {
				continue // already seeded
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("get dimension %q: %w", d.Name, err)
			}
			allowed, err := marshalAllowedValues(d.AllowedValues)
			if err != nil {
				return err
			}
			if _, err := q.CreateScopeDimension(ctx, dbx.CreateScopeDimensionParams{
				ID:            pgUUID(uuid.New()),
				TenantID:      pgUUID(tenantID),
				Name:          d.Name,
				ValueType:     d.ValueType,
				AllowedValues: allowed,
				IsRequired:    d.IsRequired,
				IsBuiltin:     true,
			}); err != nil {
				return fmt.Errorf("create dimension %q: %w", d.Name, err)
			}
		}

		// Default cell — canonical dimensions hash to dedupe re-runs.
		canon, hash, err := Canonicalize(DefaultCellDimensions)
		if err != nil {
			return err
		}
		existing, err := q.GetScopeCellByHash(ctx, dbx.GetScopeCellByHashParams{
			TenantID:       pgUUID(tenantID),
			DimensionsHash: hash,
		})
		if err == nil {
			seeded = rowToCell(existing)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("get default cell: %w", err)
		}
		row, err := q.CreateScopeCell(ctx, dbx.CreateScopeCellParams{
			ID:             pgUUID(uuid.New()),
			TenantID:       pgUUID(tenantID),
			Label:          "default",
			Dimensions:     canon,
			DimensionsHash: hash,
		})
		if err != nil {
			return fmt.Errorf("create default cell: %w", err)
		}
		seeded = rowToCell(row)
		return nil
	})
	return seeded, err
}

// CreateCell inserts a new scope cell. It validates the dimensions against the
// tenant's scope_dimensions declarations (anti-criterion: do NOT silently drop
// cells that don't match the schema) and returns ErrCellExists on duplicate.
func (s *Store) CreateCell(ctx context.Context, label string, dims map[string]string) (Cell, error) {
	var out Cell
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := s.validateDimensions(ctx, q, tenantID, dims); err != nil {
			return err
		}
		canon, hash, err := Canonicalize(dims)
		if err != nil {
			return err
		}
		row, err := q.CreateScopeCell(ctx, dbx.CreateScopeCellParams{
			ID:             pgUUID(uuid.New()),
			TenantID:       pgUUID(tenantID),
			Label:          label,
			Dimensions:     canon,
			DimensionsHash: hash,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
				return ErrCellExists
			}
			return fmt.Errorf("create cell: %w", err)
		}
		out = rowToCell(row)
		return nil
	})
	return out, err
}

// ListCells returns every scope cell for the active tenant.
func (s *Store) ListCells(ctx context.Context) ([]Cell, error) {
	var cells []Cell
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListScopeCells(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list cells: %w", err)
		}
		cells = make([]Cell, len(rows))
		for i, r := range rows {
			cells[i] = rowToCell(r)
		}
		return nil
	})
	return cells, err
}

// ListDimensions returns the active tenant's declared dimensions.
func (s *Store) ListDimensions(ctx context.Context) ([]dbx.ScopeDimension, error) {
	var out []dbx.ScopeDimension
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListScopeDimensions(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list dimensions: %w", err)
		}
		out = rows
		return nil
	})
	return out, err
}

// ControlApplicability resolves the applicability set for a single control:
// loads its applicability_expr (TEXT containing JSON), enumerates the tenant's
// cell universe, and runs the JSON-AST evaluator.
//
// This is the public consumption hook for slice 012's evaluation engine
// (AC-6): given a control id, return the cells it applies to.
func (s *Store) ControlApplicability(ctx context.Context, controlID uuid.UUID) ([]Cell, error) {
	var matches []Cell
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetControlApplicabilityExpr(ctx, dbx.GetControlApplicabilityExprParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(controlID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("scope: control %s not found", controlID)
			}
			return fmt.Errorf("get applicability_expr: %w", err)
		}
		universe, err := listCellsInTx(ctx, q, tenantID)
		if err != nil {
			return err
		}
		expr := []byte(row.ApplicabilityExpr)
		// Slice-002 default is the literal string "true"; honour AC-4 by treating
		// it as "match every cell" without trying to JSON-parse it.
		if isLegacyTrueExpr(row.ApplicabilityExpr) {
			matches = universe
			return nil
		}
		out, err := Evaluate(expr, universe)
		if err != nil {
			return err
		}
		matches = out
		return nil
	})
	return matches, err
}

func listCellsInTx(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) ([]Cell, error) {
	rows, err := q.ListScopeCells(ctx, pgUUID(tenantID))
	if err != nil {
		return nil, fmt.Errorf("list cells (in tx): %w", err)
	}
	out := make([]Cell, len(rows))
	for i, r := range rows {
		out[i] = rowToCell(r)
	}
	return out, nil
}

func (s *Store) validateDimensions(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, dims map[string]string) error {
	if len(dims) == 0 {
		return fmt.Errorf("%w: empty dimension map", ErrInvalidDimension)
	}
	rows, err := q.ListScopeDimensions(ctx, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("list dimensions: %w", err)
	}
	declared := make(map[string]dbx.ScopeDimension, len(rows))
	for _, r := range rows {
		declared[r.Name] = r
	}
	for name, value := range dims {
		dim, ok := declared[name]
		if !ok {
			return fmt.Errorf("%w: %q is not a declared dimension", ErrInvalidDimension, name)
		}
		if err := checkAllowedValue(dim, value); err != nil {
			return err
		}
	}
	// Required dimensions must be present.
	for _, d := range rows {
		if !d.IsRequired {
			continue
		}
		if _, ok := dims[d.Name]; !ok {
			return fmt.Errorf("%w: required dimension %q missing", ErrInvalidDimension, d.Name)
		}
	}
	return nil
}

// inTx opens a transaction, applies the tenant GUC, runs fn, and commits if
// fn returns nil. The transaction-scoped GUC is the only mechanism that keeps
// RLS honest (canvas §5.4); querying outside a tx with set_config(..., true)
// silently no-ops.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("scope: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("scope: begin tx: %w", err)
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
		return fmt.Errorf("scope: commit: %w", err)
	}
	return nil
}

// rowToCell converts a sqlc-generated ScopeCell row into the public Cell type.
// The dimensions JSONB is parsed back into a string map; if it cannot be
// decoded (corruption) the cell is returned with a nil map rather than
// panicking — callers must tolerate that, but in practice it never happens
// because writes go through Canonicalize.
func rowToCell(r dbx.ScopeCell) Cell {
	c := Cell{
		ID:       uuid.UUID(r.ID.Bytes),
		TenantID: uuid.UUID(r.TenantID.Bytes),
		Label:    r.Label,
	}
	if dims, ok := decodeCanonicalDims(r.Dimensions); ok {
		c.Dimensions = dims
	}
	return c
}

// decodeCanonicalDims parses the canonical-JSONB dimensions blob into a string
// map. Returns ok=false on malformed input so callers see a nil map rather
// than a panic — writes go through Canonicalize so this only fires on
// out-of-band corruption.
func decodeCanonicalDims(blob []byte) (map[string]string, bool) {
	var out map[string]string
	if err := json.Unmarshal(blob, &out); err != nil {
		return nil, false
	}
	return out, true
}

func marshalAllowedValues(vs []string) ([]byte, error) {
	if vs == nil {
		return []byte("[]"), nil
	}
	b, err := json.Marshal(vs)
	if err != nil {
		return nil, fmt.Errorf("marshal allowed_values: %w", err)
	}
	return b, nil
}

func checkAllowedValue(dim dbx.ScopeDimension, value string) error {
	if len(dim.AllowedValues) == 0 || string(dim.AllowedValues) == "[]" || string(dim.AllowedValues) == "null" {
		return nil // open set
	}
	var allowed []string
	if err := json.Unmarshal(dim.AllowedValues, &allowed); err != nil {
		// Treat a corrupt allowed_values as open — better than denying writes.
		return nil
	}
	for _, a := range allowed {
		if a == value {
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not in allowed values for %q", ErrInvalidDimension, value, dim.Name)
}

// isLegacyTrueExpr returns true for the slice-002 default applicability_expr
// value, which is the literal string "true" (not JSON). Treat it as AC-4.
func isLegacyTrueExpr(s string) bool {
	t := strings.TrimSpace(s)
	return t == "true" || t == ""
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
