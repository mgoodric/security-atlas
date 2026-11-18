package people

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ExportNamesByID returns, for each user of tenantID that has a display
// name, that name, for the owner_name column of the risk and vendor
// exports (#2450). Unlike [DisplayName] it never falls back to the email's
// local-part: an export can leave the deployment, and no export names a
// user by their email (slice 139 D1). A user with no display name is
// absent, so their owner_name is blank. q must run in a transaction scoped
// to tenantID (tenancy.ApplyTenant); RLS keeps every other tenant's users
// out.
func ExportNamesByID(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := q.ListTenantUserNames(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list user names: %w", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[uuid.UUID(r.ID.Bytes)] = r.DisplayName
	}
	return out, nil
}
