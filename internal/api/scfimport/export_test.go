package scfimport

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SetCarryTenantHook runs fn inside each tenant's overlay carry transaction
// for the rest of the test (#2568). The hook is package state, so a test
// that sets it must not run in parallel with another overlay carry.
func SetCarryTenantHook(t *testing.T, fn func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error) {
	t.Helper()
	carryTenantHook = fn
	t.Cleanup(func() { carryTenantHook = nil })
}
