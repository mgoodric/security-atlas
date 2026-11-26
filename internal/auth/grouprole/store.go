package grouprole

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrMappingNotFound is returned when a mapping id is not present in the tenant.
var ErrMappingNotFound = errors.New("grouprole: mapping not found")

// ErrUnknownIDPConfig is returned by Create when idp_config_id is not one of
// the tenant's own oidc_idp_configs (#2643). A nonexistent id and another
// tenant's id return the same error, so the create is no existence oracle.
var ErrUnknownIDPConfig = errors.New("grouprole: unknown idp config")

// Mapping is the admin-facing view of one oidc_idp_group_mappings row.
type Mapping struct {
	ID          uuid.UUID
	IDPConfigID *uuid.UUID // nil = SCIM / IdP-config-agnostic source
	GroupRef    string
	Role        string
}

// CreateMappingInput is the validated CRUD create payload.
type CreateMappingInput struct {
	IDPConfigID *uuid.UUID
	GroupRef    string
	Role        string
	CreatedBy   *uuid.UUID
}

// Store owns the mapping CRUD surface (the admin control plane, AC-8). It runs
// every operation under the tenant RLS context (invariant #6).
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a mapping Store over the RLS app pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts a (group -> role) mapping. The role MUST be an existing
// canonical atlas role (P0-509-4) — callers validate via ValidateMappingRole
// first; the DB CHECK is the backstop. Idempotent on the unique index.
//
// The group_role_mapping_create audit row naming by is written in the same
// transaction (#2609). A create that matches an existing mapping writes one
// too: the insert returns the existing row and cannot tell the two apart,
// and the admin did ask for the mapping.
func (s *Store) Create(ctx context.Context, in CreateMappingInput, by Actor) (Mapping, error) {
	if err := ValidateMappingRole(in.Role); err != nil {
		return Mapping{}, err
	}
	if in.GroupRef == "" {
		return Mapping{}, errors.New("grouprole: group_ref required")
	}
	tID, err := tenantPg(ctx)
	if err != nil {
		return Mapping{}, err
	}
	var out Mapping
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		if cfg := ptrUUIDToPg(in.IDPConfigID); cfg.Valid {
			exists, eErr := q.GroupRoleMappingIdpConfigExists(ctx, dbx.GroupRoleMappingIdpConfigExistsParams{
				TenantID: tID, ID: cfg,
			})
			if eErr != nil {
				return fmt.Errorf("grouprole: look up idp config: %w", eErr)
			}
			if !exists {
				return ErrUnknownIDPConfig
			}
		}
		row, cErr := q.InsertGroupRoleMapping(ctx, dbx.InsertGroupRoleMappingParams{
			TenantID:    tID,
			IdpConfigID: ptrUUIDToPg(in.IDPConfigID),
			GroupRef:    in.GroupRef,
			Role:        in.Role,
			CreatedBy:   ptrUUIDToPg(in.CreatedBy),
		})
		if isIDPConfigFKViolation(cErr) {
			// The config was deleted between the lookup and the insert.
			return ErrUnknownIDPConfig
		}
		if cErr != nil {
			return fmt.Errorf("grouprole: insert mapping: %w", cErr)
		}
		out = mappingFromRow(row)
		return s.audit(ctx, q, tID, by, AuditActionMappingCreate, map[string]any{}, mappingAuditOf(out))
	})
	if err != nil {
		return Mapping{}, err
	}
	return out, nil
}

// List returns every mapping in the tenant (AC-8).
func (s *Store) List(ctx context.Context) ([]Mapping, error) {
	tID, err := tenantPg(ctx)
	if err != nil {
		return nil, err
	}
	var out []Mapping
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		rows, lErr := q.ListGroupRoleMappings(ctx, tID)
		if lErr != nil {
			return fmt.Errorf("grouprole: list mappings: %w", lErr)
		}
		out = make([]Mapping, 0, len(rows))
		for _, r := range rows {
			out = append(out, mappingFromRow(r))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes a mapping by id within the tenant (AC-8). Returns
// ErrMappingNotFound when the id is absent. The group_role_mapping_delete
// audit row naming by is written in the same transaction (#2609).
func (s *Store) Delete(ctx context.Context, id uuid.UUID, by Actor) error {
	tID, err := tenantPg(ctx)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		cur, gErr := q.GetGroupRoleMapping(ctx, dbx.GetGroupRoleMappingParams{
			TenantID: tID, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if errors.Is(gErr, pgx.ErrNoRows) {
			return ErrMappingNotFound
		}
		if gErr != nil {
			return fmt.Errorf("grouprole: get mapping: %w", gErr)
		}
		n, dErr := q.DeleteGroupRoleMapping(ctx, dbx.DeleteGroupRoleMappingParams{
			TenantID: tID, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if dErr != nil {
			return fmt.Errorf("grouprole: delete mapping: %w", dErr)
		}
		if n == 0 {
			return ErrMappingNotFound
		}
		return s.audit(ctx, q, tID, by, AuditActionMappingDelete, mappingAuditOf(mappingFromRow(cur)), map[string]any{})
	})
}

// --- internals ---

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("grouprole: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func tenantPg(ctx context.Context) (pgtype.UUID, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	id, err := uuid.Parse(tenantStr)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("grouprole: parse tenant id: %w", err)
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func mappingFromRow(r dbx.OidcIdpGroupMapping) Mapping {
	m := Mapping{
		ID:       uuid.UUID(r.ID.Bytes),
		GroupRef: r.GroupRef,
		Role:     r.Role,
	}
	if r.IdpConfigID.Valid {
		cfg := uuid.UUID(r.IdpConfigID.Bytes)
		m.IDPConfigID = &cfg
	}
	return m
}

// idpConfigFKConstraint is the mapping -> IdP config FK from migration
// 20261271000000_group_mapping_idp_config_fk.sql.
const idpConfigFKConstraint = "oidc_idp_group_mappings_idp_config_fk"

// isIDPConfigFKViolation reports a foreign-key violation (SQLSTATE 23503) on
// the IdP config FK only (#2731), so an unrelated FK failure on the insert
// surfaces as itself rather than as ErrUnknownIDPConfig.
func isIDPConfigFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == idpConfigFKConstraint
}

func ptrUUIDToPg(u *uuid.UUID) pgtype.UUID {
	if u == nil || *u == uuid.Nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: *u, Valid: true}
}
