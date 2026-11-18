package people

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// NamesByID returns, for each user of tenantID, the name GET /v1/people
// shows for them ([DisplayName]). q must run in a transaction scoped to
// tenantID (tenancy.ApplyTenant); RLS keeps every other tenant's users
// out. The risk export names each risk's owner with it (#2450).
func NamesByID(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := q.ListTenantUserNames(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list user names: %w", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[uuid.UUID(r.ID.Bytes)] = DisplayName(r.DisplayName, r.Email)
	}
	return out, nil
}
