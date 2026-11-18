package scim

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// The me_audit_log actions for SCIM credential issue and revoke (#2609).
// CHECK constraint extended in migration 20261260000000_sso_scim_audit.sql.
const (
	AuditActionCredentialIssue  = "scim_credential_issue"
	AuditActionCredentialRevoke = "scim_credential_revoke"
)

// Actor is who issued or revoked a credential. UserID is the users(id)
// behind the caller, nil for a caller that is not a user (the bootstrap
// admin); it is stored as scim_credentials.issued_by on issue. Principal is
// the acting credential's principal (credstore.PrincipalOf, #2329), written
// as me_audit_log.actor_principal; empty for a caller with no credential.
type Actor struct {
	UserID    *uuid.UUID
	Principal string
}

// credentialAudit is one side of a SCIM credential audit row. It has no
// field that can hold the bearer token or its hash: the id and last four
// characters identify the credential, and anything more would be a copy of
// the secret in the audit log.
type credentialAudit struct {
	CredentialID string `json:"credential_id"`
	Last4        string `json:"last4"`
	Description  string `json:"description"`
	Revoked      bool   `json:"revoked"`
}

func credentialAuditOf(c Credential, revoked bool) credentialAudit {
	return credentialAudit{
		CredentialID: c.ID.String(),
		Last4:        c.Last4,
		Description:  c.Description,
		Revoked:      revoked,
	}
}

// audit writes one me_audit_log row in the caller's transaction, so the
// change it records cannot commit without it.
func (s *CredentialStore) audit(ctx context.Context, q *dbx.Queries, tenant pgtype.UUID, by Actor, action string, before, after any) error {
	beforeBlob, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("scim: audit before: %w", err)
	}
	afterBlob, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("scim: audit after: %w", err)
	}
	userID := uuid.Nil
	if by.UserID != nil {
		userID = *by.UserID
	}
	var principal *string
	if by.Principal != "" {
		p := by.Principal
		principal = &p
	}
	insert := s.insertAudit
	if insert == nil {
		insert = func(ctx context.Context, q *dbx.Queries, p dbx.InsertMeAuditLogParams) error {
			return q.InsertMeAuditLog(ctx, p)
		}
	}
	if err := insert(ctx, q, dbx.InsertMeAuditLogParams{
		TenantID:       tenant,
		UserID:         pgtype.UUID{Bytes: userID, Valid: true},
		Action:         action,
		Before:         beforeBlob,
		After:          afterBlob,
		ActorPrincipal: principal,
	}); err != nil {
		return fmt.Errorf("scim: audit %s: %w", action, err)
	}
	return nil
}
