// Package superadmin answers one question against the live
// `super_admins` table: is this user a platform super_admin right now?
//
// The `atlas:super_admin` JWT claim is a snapshot taken at mint time.
// Every path that would carry that snapshot forward re-reads the table
// through a Checker instead of trusting the snapshot or a per-tenant
// credential's IsAdmin bit (#1850, #1757):
//
//   - device-code approval snapshots the approver's super_admin status
//     from the table, never from credstore.Credential.IsAdmin;
//   - RFC 8693 token exchange copies the claim only if the table still
//     agrees;
//   - the JWT middleware drops the claim from a live token whose
//     subject has been demoted, so a demote takes effect on the next
//     request instead of after AccessTokenLifetime. The token itself
//     stays valid for the user's tenants; only the claim goes.
package superadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoPool is returned by a DBChecker built without a pool. Callers
// treat any error as "not a super_admin" (fail closed).
var ErrNoPool = errors.New("superadmin: checker has no pgxpool")

// Checker reports whether userID currently holds super_admin.
type Checker interface {
	IsSuperAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
}

// CheckerFunc adapts a function to Checker. Tests use it as a stub.
type CheckerFunc func(ctx context.Context, userID uuid.UUID) (bool, error)

// IsSuperAdmin calls f.
func (f CheckerFunc) IsSuperAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	return f(ctx, userID)
}

// DBChecker reads `super_admins` through pool.
//
// pool should be the BYPASSRLS `atlas_migrate` pool, the same one the
// DBUserResolver uses: a super_admin row is keyed on one tenant-scoped
// users.id, and a token minted in another tenant carries that tenant's
// users.id as its subject. The check therefore matches the subject
// directly OR any active users row sharing the subject's
// (idp_issuer, idp_subject), mirroring DBUserResolver.lookupSuperAdmin.
// Under an RLS-bound pool the identity join sees no rows and only the
// direct match applies.
type DBChecker struct {
	pool *pgxpool.Pool
}

// NewDBChecker constructs a DBChecker. pool may be nil, in which case
// every check fails closed with ErrNoPool.
func NewDBChecker(pool *pgxpool.Pool) *DBChecker {
	return &DBChecker{pool: pool}
}

const isSuperAdminSQL = `
	SELECT EXISTS (
		SELECT 1 FROM super_admins WHERE user_id = $1
		UNION ALL
		SELECT 1
		  FROM users me
		  JOIN users u
		    ON u.idp_issuer = me.idp_issuer
		   AND u.idp_subject = me.idp_subject
		  JOIN super_admins sa ON sa.user_id = u.id
		 WHERE me.id = $1
		   AND me.idp_issuer <> '' AND me.idp_subject <> ''
		   AND u.status = 'active'
	)`

// IsSuperAdmin implements Checker.
func (c *DBChecker) IsSuperAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	if c == nil || c.pool == nil {
		return false, ErrNoPool
	}
	var ok bool
	if err := c.pool.QueryRow(ctx, isSuperAdminSQL, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("superadmin: query: %w", err)
	}
	return ok, nil
}

// Confirm reports whether subject (a JWT `sub` or credential UserID,
// with or without the "user:" prefix) currently holds super_admin.
// It fails closed: a nil checker, a non-UUID subject or a query error
// all return false.
func Confirm(ctx context.Context, c Checker, subject string) bool {
	if c == nil {
		return false
	}
	id, err := uuid.Parse(strings.TrimPrefix(subject, "user:"))
	if err != nil {
		return false
	}
	ok, err := c.IsSuperAdmin(ctx, id)
	return err == nil && ok
}
