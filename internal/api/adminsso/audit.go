package adminsso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// AuditActionSSOUpdate is the me_audit_log action PATCH /v1/admin/sso writes
// (#2609). CHECK constraint extended in migration
// 20261262000000_sso_scim_audit.sql.
const AuditActionSSOUpdate = "tenant_sso_config_update"

// errSecretRequired aborts the PATCH transaction when the tenant has no
// config yet and the request carries no client secret.
var errSecretRequired = errors.New("client_secret is required on first config")

// ssoAuditState is one side of a tenant_sso_config_update row: every field
// of the config except the client secret. It has no field that can hold the
// secret, its ciphertext, a hash or a length: a copy of any of them in the
// audit log would be the secret outside the crypter. Configured is false on
// the before value of a tenant's first config.
type ssoAuditState struct {
	Configured                  bool     `json:"configured"`
	IssuerURL                   string   `json:"issuer_url"`
	ClientID                    string   `json:"client_id"`
	RedirectURL                 string   `json:"redirect_url"`
	AllowedEmailDomains         []string `json:"allowed_email_domains"`
	TrustUnverifiedEmailForLink bool     `json:"trust_unverified_email_for_link"`
}

// ssoAuditAfter is the after value: the new state, and whether this write
// replaced the client secret. secret_changed is the only trace the secret
// leaves in the row.
type ssoAuditAfter struct {
	ssoAuditState
	SecretChanged bool `json:"secret_changed"`
}

func (s ssoAuditState) equal(o ssoAuditState) bool {
	return s.Configured == o.Configured &&
		s.IssuerURL == o.IssuerURL &&
		s.ClientID == o.ClientID &&
		s.RedirectURL == o.RedirectURL &&
		slices.Equal(s.AllowedEmailDomains, o.AllowedEmailDomains) &&
		s.TrustUnverifiedEmailForLink == o.TrustUnverifiedEmailForLink
}

// auditSSOChange writes the tenant_sso_config_update row in the PATCH's own
// transaction, so a change without its audit row cannot commit. A PATCH
// that changes no field and no secret writes no row (the me_audit_log
// empty-diff rule, slice 108 ISC-A5).
func (h *Handler) auditSSOChange(ctx context.Context, q *dbx.Queries, tenantID, userID uuid.UUID, principal string, before, after ssoAuditState, secretChanged bool) error {
	if before.equal(after) && !secretChanged {
		return nil
	}
	beforeBlob, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("audit before: %w", err)
	}
	afterBlob, err := json.Marshal(ssoAuditAfter{ssoAuditState: after, SecretChanged: secretChanged})
	if err != nil {
		return fmt.Errorf("audit after: %w", err)
	}
	var actorPrincipal *string
	if principal != "" {
		actorPrincipal = &principal
	}
	insert := h.insertAudit
	if insert == nil {
		insert = func(ctx context.Context, q *dbx.Queries, p dbx.InsertMeAuditLogParams) error {
			return q.InsertMeAuditLog(ctx, p)
		}
	}
	if err := insert(ctx, q, dbx.InsertMeAuditLogParams{
		TenantID:       uuidToPgtype(tenantID),
		UserID:         uuidToPgtype(userID),
		Action:         AuditActionSSOUpdate,
		Before:         beforeBlob,
		After:          afterBlob,
		ActorPrincipal: actorPrincipal,
	}); err != nil {
		return fmt.Errorf("audit sso change: %w", err)
	}
	return nil
}

// actor is the user the audit row names, uuid.Nil for a caller that is not a
// user (a bootstrap key), and the acting credential's principal (#2329),
// which names that caller either way. Empty with no credential.
func actor(r *http.Request) (uuid.UUID, string) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		return uuid.Nil, ""
	}
	u, _ := credstore.CallerUserID(cred)
	return u, credstore.PrincipalOf(cred)
}
