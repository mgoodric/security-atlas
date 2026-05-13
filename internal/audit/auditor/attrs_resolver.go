package auditor

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DBAttrsResolver implements authz.AttrsResolver against Postgres. It
// hydrates `input.user.attrs.audit_period_ids` from the
// auditor_assignments table when the request reaches authz with the
// auditor role and no pre-populated attrs.
//
// The resolver opens its own short-lived transaction (it cannot share
// the caller's because the authz middleware runs upstream of every
// downstream handler) and applies the tenant GUC so the SELECT
// respects RLS as defense-in-depth.
type DBAttrsResolver struct {
	pool *pgxpool.Pool
}

// NewDBAttrsResolver constructs a resolver over pool. The pool is held
// but not owned -- callers (typically cmd/atlas) close it.
func NewDBAttrsResolver(pool *pgxpool.Pool) *DBAttrsResolver {
	return &DBAttrsResolver{pool: pool}
}

// AttrsFor returns the auditor attributes for (tenantID, userID).
// Currently only `audit_period_ids` -- the resolver is the single hook
// the authz layer needs to know about, so additional ABAC keys land
// here over time. The roles slice is accepted for future use; v1
// gates on `RoleAuditor` upstream in Engine.Decide so we can rely on
// the caller having pre-filtered to auditor traffic.
func (r *DBAttrsResolver) AttrsFor(ctx context.Context, tenantID, userID string, _ []authz.Role) (map[string]interface{}, error) {
	if tenantID == "" || userID == "" {
		return nil, nil
	}
	// Validate tenant id is a UUID -- machine-actor traffic carries
	// keystore ids like "key_..." that aren't tenant UUIDs and shouldn't
	// hit the resolver.
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, nil
	}

	// Build a synthetic tenant context for tenancy.ApplyTenant. The
	// upstream chi middleware already put the tenant id on the request
	// context for downstream handlers, but we re-wrap defensively so
	// the resolver does not implicitly depend on middleware ordering.
	var err error
	ctx, err = tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		return nil, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("auditor attrs: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}

	q := dbx.New(tx)
	rows, err := q.GetAuditPeriodIDsForUser(ctx, dbx.GetAuditPeriodIDsForUserParams{
		TenantID: pgUUID(uuid.MustParse(tenantID)),
		UserID:   userID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("auditor attrs: list period ids: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("auditor attrs: commit: %w", err)
	}

	// Convert pgtype.UUID -> string. The OPA Rego policy reads
	// audit_period_ids as a set of strings (Rego sees `some assigned in
	// input.user.attrs.audit_period_ids`); strings match the resource
	// attr shape (`input.resource.attrs.audit_period_id`) which the
	// handlers stuff with the UUID.String() representation.
	ids := make([]interface{}, 0, len(rows))
	for _, p := range rows {
		if !p.Valid {
			continue
		}
		ids = append(ids, uuid.UUID(p.Bytes).String())
	}
	return map[string]interface{}{
		"audit_period_ids": ids,
	}, nil
}
