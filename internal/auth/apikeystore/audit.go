package apikeystore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// The me_audit_log actions for API key issue, rotate and revoke (#2609).
// CHECK constraint extended in migration
// 20261261000000_group_mapping_api_key_audit.sql.
const (
	AuditActionKeyIssue  = "api_key_issue"
	AuditActionKeyRotate = "api_key_rotate"
	AuditActionKeyRevoke = "api_key_revoke"
)

// Actor is who issued, rotated or revoked a key: the users(id) behind the
// caller (uuid.Nil for a caller that is not a user) and the acting
// credential's principal (credstore.PrincipalOf, #2329), written as
// me_audit_log.actor_principal.
type Actor struct {
	UserID    uuid.UUID
	Principal string
}

// keyAudit is one side of an API key audit row: the key's id, last4, flags
// and roles. It has no field that can hold the bearer token or its hash;
// last4 identifies the key, and anything more would be a copy of the secret
// in the audit log.
type keyAudit struct {
	KeyID          string          `json:"key_id"`
	Last4          string          `json:"last4"`
	IsAdmin        bool            `json:"is_admin"`
	IsApprover     bool            `json:"is_approver"`
	OwnerRoles     []string        `json:"owner_roles"`
	AllowedKinds   []string        `json:"allowed_kinds"`
	ScopePredicate json.RawMessage `json:"scope_predicate,omitempty"`
	TTLSeconds     int64           `json:"ttl_seconds"`
	RotatedFrom    string          `json:"rotated_from,omitempty"`
	Revoked        bool            `json:"revoked"`
	RetiresAt      *time.Time      `json:"retires_at,omitempty"`
}

func keyAuditOf(c credstore.Credential) keyAudit {
	a := keyAudit{
		KeyID:        c.ID,
		Last4:        c.Last4,
		IsAdmin:      c.IsAdmin,
		IsApprover:   c.IsApprover,
		OwnerRoles:   nonNil(c.OwnerRoles),
		AllowedKinds: nonNil(c.Kinds),
		TTLSeconds:   int64(c.TTL / time.Second),
		RotatedFrom:  c.RotatedFrom,
	}
	if json.Valid([]byte(c.ScopePredicate)) {
		a.ScopePredicate = json.RawMessage(c.ScopePredicate)
	}
	if !c.RetiresAt.IsZero() {
		t := c.RetiresAt
		a.RetiresAt = &t
	}
	return a
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// audit writes one me_audit_log row in the caller's transaction, so the
// change it records cannot commit without it.
func (s *Store) audit(ctx context.Context, q *dbx.Queries, tenant pgtype.UUID, by Actor, action string, before, after any) error {
	beforeBlob, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("apikeystore: audit before: %w", err)
	}
	afterBlob, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("apikeystore: audit after: %w", err)
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
		return fmt.Errorf("apikeystore: audit %s: %w", action, err)
	}
	return nil
}
