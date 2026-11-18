package grouprole

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// The me_audit_log actions for group-role mapping create and delete (#2609).
// CHECK constraint extended in migration
// 20261263000000_group_mapping_api_key_audit.sql.
const (
	AuditActionMappingCreate = "group_role_mapping_create"
	AuditActionMappingDelete = "group_role_mapping_delete"
)

// Actor is who created or deleted a mapping: the users(id) behind the
// caller (uuid.Nil for a caller that is not a user) and the acting
// credential's principal (credstore.PrincipalOf, #2329), written as
// me_audit_log.actor_principal.
type Actor struct {
	UserID    uuid.UUID
	Principal string
}

// mappingAudit is one side of a mapping audit row: which group grants which
// role, from which source.
type mappingAudit struct {
	MappingID   string  `json:"mapping_id"`
	IDPConfigID *string `json:"idp_config_id"`
	GroupRef    string  `json:"group_ref"`
	Role        string  `json:"role"`
}

func mappingAuditOf(m Mapping) mappingAudit {
	a := mappingAudit{MappingID: m.ID.String(), GroupRef: m.GroupRef, Role: m.Role}
	if m.IDPConfigID != nil {
		id := m.IDPConfigID.String()
		a.IDPConfigID = &id
	}
	return a
}

// audit writes one me_audit_log row in the caller's transaction, so the
// change it records cannot commit without it.
func (s *Store) audit(ctx context.Context, q *dbx.Queries, tenant pgtype.UUID, by Actor, action string, before, after any) error {
	beforeBlob, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("grouprole: audit before: %w", err)
	}
	afterBlob, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("grouprole: audit after: %w", err)
	}
	var principal *string
	if by.Principal != "" {
		p := by.Principal
		principal = &p
	}
	if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
		TenantID:       tenant,
		UserID:         pgtype.UUID{Bytes: by.UserID, Valid: true},
		Action:         action,
		Before:         beforeBlob,
		After:          afterBlob,
		ActorPrincipal: principal,
	}); err != nil {
		return fmt.Errorf("grouprole: audit %s: %w", action, err)
	}
	return nil
}
