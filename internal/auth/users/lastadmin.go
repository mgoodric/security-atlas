package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrLastAdmin is returned by AdminGuard.Check when a change would leave a
// tenant that had an active admin with none (#2261). Handlers map it to 409.
var ErrLastAdmin = errors.New("users: change would leave the tenant with no active admin")

// AdminGuard holds the tenant's admin-roster lock for the life of the
// caller's transaction and remembers how many active admins the tenant had
// when the lock was taken (#2261).
//
// Every path that can remove an admin (revoke, role replacement, membership
// disable, the group-role reconciler) calls LockAdmins before its writes and,
// for the API paths, Check after them. Two such transactions on one tenant
// serialise on the lock, so they cannot both see a second admin and both
// remove one.
type AdminGuard struct {
	tenantID uuid.UUID
	before   int
}

// LockAdmins takes the tenant's admin-roster lock and counts the active
// admins. It must run inside the transaction that makes the change, before
// any write to user_roles or users.
func LockAdmins(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (AdminGuard, error) {
	tid := pgtype.UUID{Bytes: tenantID, Valid: true}
	if err := q.LockTenantAdminRoster(ctx, tid); err != nil {
		return AdminGuard{}, fmt.Errorf("users: lock admin roster: %w", err)
	}
	n, err := q.CountActiveTenantAdmins(ctx, tid)
	if err != nil {
		return AdminGuard{}, fmt.Errorf("users: count active admins: %w", err)
	}
	return AdminGuard{tenantID: tenantID, before: int(n)}, nil
}

// Before is the active-admin count at the time the lock was taken.
func (g AdminGuard) Before() int { return g.before }

// Check recounts the active admins after the caller's writes and returns
// ErrLastAdmin when the tenant had at least one and now has none. The caller
// rolls back on any error, so a refused change writes nothing. A tenant that
// already had no active admin is not made worse by the change, so it passes.
func (g AdminGuard) Check(ctx context.Context, q *dbx.Queries) error {
	n, err := q.CountActiveTenantAdmins(ctx, pgtype.UUID{Bytes: g.tenantID, Valid: true})
	if err != nil {
		return fmt.Errorf("users: recount active admins: %w", err)
	}
	if g.before > 0 && n == 0 {
		return ErrLastAdmin
	}
	return nil
}
