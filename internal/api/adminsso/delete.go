package adminsso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// AuditActionSSODelete is the me_audit_log action DELETE /v1/admin/sso
// writes (#2760). CHECK constraint extended in migration
// 20261272000001_sso_config_delete_audit.sql.
const AuditActionSSODelete = "tenant_sso_config_delete"

// errNoConfig aborts the DELETE transaction when the tenant has no config.
var errNoConfig = errors.New("no SSO config")

// DeleteResponse is the JSON body of DELETE /v1/admin/sso.
type DeleteResponse struct {
	ConfigID string `json:"config_id"`
	// MappingCount is the group-role mappings the delete removed.
	MappingCount int64 `json:"mapping_count"`
	// RevokedRoleCount is the group-derived roles revoked because only this
	// config derived them.
	RevokedRoleCount int `json:"revoked_role_count"`
	// AffectedUserCount is the users who lost at least one role.
	AffectedUserCount int `json:"affected_user_count"`
}

// ssoDeleteAuditAfter is the after value of a tenant_sso_config_delete row.
type ssoDeleteAuditAfter struct {
	Configured        bool   `json:"configured"`
	ConfigID          string `json:"config_id"`
	MappingCount      int64  `json:"mapping_count"`
	RevokedRoleCount  int    `json:"revoked_role_count"`
	AffectedUserCount int    `json:"affected_user_count"`
}

// ssoDeleteAuditBefore is the before value: the config that was removed.
type ssoDeleteAuditBefore struct {
	ssoAuditState
	ConfigID string `json:"config_id"`
}

// Delete handles DELETE /v1/admin/sso (#2760). Admin-only. In one
// transaction it removes the tenant's IdP config, the group-role mappings
// scoped to it (FK cascade), and the group-derived roles only it derived,
// auditing each revocation in group_role_audit_log and the delete itself in
// me_audit_log. A delete that would leave the tenant with no active admin is
// refused with 409 and changes nothing.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}

	userID, principal := actor(r)
	var resp DeleteResponse
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		if lErr := q.LockAdminSSO(ctx, uuidToPgtype(tenantID)); lErr != nil {
			return fmt.Errorf("lock sso: %w", lErr)
		}
		// FOR UPDATE on the config row: a reconcile recording a derivation
		// against it (an FK KEY SHARE lock) either commits first, and its
		// row is revoked below, or waits and then fails the FK.
		cur, gErr := q.GetAdminSSOForUpdate(ctx, dbx.GetAdminSSOForUpdateParams{
			TenantID: uuidToPgtype(tenantID),
			Name:     configName,
		})
		if errors.Is(gErr, pgx.ErrNoRows) {
			return errNoConfig
		}
		if gErr != nil {
			return gErr
		}
		configID := uuidFromPgtype(cur.ID)

		guard, lErr := users.LockAdmins(ctx, q, tenantID)
		if lErr != nil {
			return lErr
		}
		preview, pErr := grouprole.PreviewConfigDelete(ctx, q, tenantID, configID)
		if pErr != nil {
			return pErr
		}
		rev, rErr := grouprole.RevokeConfigRoles(ctx, q, tenantID, configID)
		if rErr != nil {
			return rErr
		}
		if cErr := guard.Check(ctx, q); cErr != nil {
			return cErr
		}
		if _, dErr := q.DeleteAdminSSO(ctx, dbx.DeleteAdminSSOParams{
			TenantID: uuidToPgtype(tenantID),
			ID:       cur.ID,
		}); dErr != nil {
			return fmt.Errorf("delete sso: %w", dErr)
		}

		resp = DeleteResponse{
			ConfigID:          configID.String(),
			MappingCount:      preview.MappingCount,
			RevokedRoleCount:  rev.RevokedRoles,
			AffectedUserCount: rev.AffectedUsers,
		}
		before := ssoDeleteAuditBefore{
			ssoAuditState: ssoAuditState{
				Configured:                  true,
				IssuerURL:                   cur.IssuerUrl,
				ClientID:                    cur.ClientID,
				RedirectURL:                 cur.RedirectUrl,
				AllowedEmailDomains:         cur.AllowedEmailDomains,
				TrustUnverifiedEmailForLink: cur.TrustUnverifiedEmailForLink,
			},
			ConfigID: configID.String(),
		}
		after := ssoDeleteAuditAfter{
			Configured:        false,
			ConfigID:          configID.String(),
			MappingCount:      preview.MappingCount,
			RevokedRoleCount:  rev.RevokedRoles,
			AffectedUserCount: rev.AffectedUsers,
		}
		return h.auditSSODelete(ctx, q, tenantID, userID, principal, before, after)
	})
	switch {
	case errors.Is(err, errNoConfig):
		httpresp.WriteError(w, http.StatusNotFound, "no SSO config")
		return
	case errors.Is(err, users.ErrLastAdmin):
		httpresp.WriteError(w, http.StatusConflict,
			"deleting the SSO config would revoke the tenant's last admin; grant admin to another user first")
		return
	case err != nil:
		httperr.WriteInternal(w, r, "delete sso", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, resp)
}

// auditSSODelete writes the tenant_sso_config_delete row in the DELETE's own
// transaction, so the delete cannot commit without it.
func (h *Handler) auditSSODelete(ctx context.Context, q *dbx.Queries, tenantID, userID uuid.UUID, principal string, before ssoDeleteAuditBefore, after ssoDeleteAuditAfter) error {
	beforeBlob, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("audit before: %w", err)
	}
	afterBlob, err := json.Marshal(after)
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
		Action:         AuditActionSSODelete,
		Before:         beforeBlob,
		After:          afterBlob,
		ActorPrincipal: actorPrincipal,
	}); err != nil {
		return fmt.Errorf("audit sso delete: %w", err)
	}
	return nil
}
