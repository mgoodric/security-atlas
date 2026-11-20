package grouprole

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ConfigRevocation is what RevokeConfigRoles did for one OIDC config (#2760).
type ConfigRevocation struct {
	// Derivations is how many (user, role) derivations the config held; each
	// is removed and audited.
	Derivations int
	// RevokedRoles is how many user_roles rows were removed: derivations no
	// other source shared, on a role the user did not also hold manually.
	RevokedRoles int
	// AffectedUsers is how many distinct users lost at least one role.
	AffectedUsers int
}

// ConfigDeletePreview is what deleting an OIDC config would remove, for the
// confirm dialog (#2760).
type ConfigDeletePreview struct {
	MappingCount int64
	// DerivedRoleCount is the group-derived roles only this config derives:
	// the ones RevokeConfigRoles would remove.
	DerivedRoleCount int64
}

// PreviewConfigDelete counts what DeleteAdminSSO would remove, in the
// caller's transaction.
func PreviewConfigDelete(ctx context.Context, q *dbx.Queries, tenantID, configID uuid.UUID) (ConfigDeletePreview, error) {
	tID := pgtype.UUID{Bytes: tenantID, Valid: true}
	cfg := pgtype.UUID{Bytes: configID, Valid: true}
	mappings, err := q.CountGroupRoleMappingsForConfig(ctx, dbx.CountGroupRoleMappingsForConfigParams{
		TenantID: tID, IdpConfigID: cfg,
	})
	if err != nil {
		return ConfigDeletePreview{}, fmt.Errorf("grouprole: count config mappings: %w", err)
	}
	roles, err := q.CountSoleSourceGroupRolesForConfig(ctx, dbx.CountSoleSourceGroupRolesForConfigParams{
		TenantID: tID, IdpConfigID: cfg,
	})
	if err != nil {
		return ConfigDeletePreview{}, fmt.Errorf("grouprole: count config roles: %w", err)
	}
	return ConfigDeletePreview{MappingCount: mappings, DerivedRoleCount: roles}, nil
}

// RevokeConfigRoles removes every role derivation an OIDC config holds, in
// the caller's transaction, before the config itself is deleted (#2760).
// A role another source also derives, or the user also holds manually,
// stays. Each derivation removed writes a group_role_audit_log 'revoke' row
// naming the config; detail.role_removed says whether the user lost the
// role. The caller holds the admin-roster lock and runs the last-admin
// check after this, so a delete that strands the tenant rolls back.
func RevokeConfigRoles(ctx context.Context, q *dbx.Queries, tenantID, configID uuid.UUID) (ConfigRevocation, error) {
	tID := pgtype.UUID{Bytes: tenantID, Valid: true}
	cfg := pgtype.UUID{Bytes: configID, Valid: true}
	rows, err := q.ListGroupRoleSourcesForConfig(ctx, dbx.ListGroupRoleSourcesForConfigParams{
		TenantID: tID, IdpConfigID: cfg,
	})
	if err != nil {
		return ConfigRevocation{}, fmt.Errorf("grouprole: list config derivations: %w", err)
	}
	var out ConfigRevocation
	users := map[string]struct{}{}
	for _, row := range rows {
		removed, err := revokeSource(ctx, q, tID, row.UserID, row.Role, cfg)
		if err != nil {
			return ConfigRevocation{}, err
		}
		out.Derivations++
		if removed {
			out.RevokedRoles++
			users[row.UserID] = struct{}{}
		}
		detail, _ := json.Marshal(map[string]any{
			"reason":       "idp_config_delete",
			"role_removed": removed,
		})
		if err := q.InsertGroupRoleAudit(ctx, dbx.InsertGroupRoleAuditParams{
			TenantID:    tID,
			UserID:      row.UserID,
			Role:        row.Role,
			Change:      "revoke",
			Source:      string(SourceOIDC),
			IdpConfigID: cfg,
			Detail:      detail,
		}); err != nil {
			return ConfigRevocation{}, fmt.Errorf("grouprole: audit config revoke %s: %w", row.Role, err)
		}
	}
	out.AffectedUsers = len(users)
	return out, nil
}
