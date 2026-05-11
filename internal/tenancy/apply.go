package tenancy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ApplyTenant sets the tenant GUC on the given transaction via set_config,
// which accepts a bound parameter (unlike bare SET LOCAL). Effects die on
// commit/rollback because the third argument requests session-local scope.
//
// Requiring pgx.Tx (not *pgx.Conn) is intentional: outside a transaction the
// is_local flag is silently inert, RLS sees the GUC as empty, and queries
// return zero rows. The type signature makes that footgun impossible.
func ApplyTenant(ctx context.Context, tx pgx.Tx) error {
	tenant, err := TenantFromContext(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", GUCName, tenant); err != nil {
		return fmt.Errorf("tenancy: set_config %s: %w", GUCName, err)
	}
	return nil
}
