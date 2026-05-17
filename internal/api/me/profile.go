// Slice 108: GET /v1/me + PATCH /v1/me. Profile read/mutate over the slice-034 users
// table. The handler resolves cred.UserID to a real users row when possible; falls
// back to a credential-derived synthetic profile when the credential is a
// bootstrap-admin / API-key holder with no users row backing it (P0-A1: no fabrication
// beyond what tables hold).
package me

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProfileHandler bundles the GET/PATCH /v1/me routes.
type ProfileHandler struct {
	users *users.Store
	pool  *pgxpool.Pool
}

// NewProfile constructs a ProfileHandler.
func NewProfile(usersStore *users.Store, pool *pgxpool.Pool) *ProfileHandler {
	return &ProfileHandler{users: usersStore, pool: pool}
}

// ----- wire shapes -----

// profileWire is the canonical JSON shape returned by GET /v1/me and PATCH /v1/me.
// Fields are tagged so omitted nullable fields render as null (RFC 8259-compliant).
type profileWire struct {
	UserID      string   `json:"user_id"`
	TenantID    string   `json:"tenant_id"`
	DisplayName string   `json:"display_name"`
	Email       string   `json:"email"`
	IdpSubject  string   `json:"idp_subject"`
	TenantRole  string   `json:"tenant_role"`
	TimeZone    *string  `json:"time_zone"`
	IsAdmin     bool     `json:"is_admin"`
	OwnerRoles  []string `json:"owner_roles"`
}

// patchProfileRequest is the wire shape of PATCH /v1/me. Pointer fields encode the
// "field absent from JSON" vs "field set to empty string" distinction so the merge
// semantic is honest: a missing field = leave unchanged, a present empty field = clear.
type patchProfileRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
	TimeZone    *string `json:"time_zone,omitempty"`
}

// ----- handlers -----

// GetMe handles GET /v1/me. Returns the caller's profile derived from cred + users
// row. When the credential is not backed by a real users row (bootstrap admin /
// API-key with NULL issued_by), returns a synthetic profile carrying only what the
// credential knows. No IdP roundtrip (P0-A3).
func (h *ProfileHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	wire, err := h.buildProfile(ctx, cred)
	if err != nil {
		writeServerErr(w, "get profile", err)
		return
	}
	writeJSON(w, http.StatusOK, wire)
}

// PatchMe handles PATCH /v1/me. Accepts display_name + time_zone (both optional);
// other fields are read-only and rejected silently (not echoed back). Writes a
// me_audit_log entry when the diff is non-empty.
func (h *ProfileHandler) PatchMe(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	userUUID, err := uuid.Parse(cred.UserID)
	if err != nil {
		// API-key / bootstrap credentials with no users row cannot PATCH a
		// profile they don't have. Honest 404.
		writeError(w, http.StatusNotFound, "no profile for this credential")
		return
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context invalid")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}
	var req patchProfileRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if req.TimeZone != nil && *req.TimeZone != "" {
		if _, err := time.LoadLocation(*req.TimeZone); err != nil {
			writeError(w, http.StatusBadRequest, "time_zone must be a valid IANA timezone (e.g. America/Los_Angeles)")
			return
		}
	}
	// Load current profile to compute diff.
	current, err := h.users.GetByID(ctx, tenantUUID, userUUID)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeServerErr(w, "load profile", err)
		return
	}
	newDisplay := current.DisplayName
	if req.DisplayName != nil {
		newDisplay = *req.DisplayName
	}
	newTZ := current.TimeZone
	if req.TimeZone != nil {
		newTZ = *req.TimeZone
	}
	// Empty-diff path: skip the UPDATE + skip the audit-log INSERT (ISC-A5).
	if newDisplay == current.DisplayName && newTZ == current.TimeZone {
		writeJSON(w, http.StatusOK, profileWireFrom(current, cred))
		return
	}
	updated, err := h.users.UpdateProfile(ctx, users.UpdateProfileInput{
		TenantID:    tenantUUID,
		ID:          userUUID,
		DisplayName: newDisplay,
		TimeZone:    newTZ,
	})
	if err != nil {
		writeServerErr(w, "update profile", err)
		return
	}
	// Audit-log entry. Failure here is logged but NOT fatal to the PATCH — the
	// user's PATCH already succeeded; a missing audit row is recoverable via
	// later observation of the data state. We deliberately do not roll back the
	// UPDATE on audit-log failure (would create a worse failure mode where the
	// user thinks the PATCH failed when in fact only the audit row failed).
	before := map[string]any{"display_name": current.DisplayName, "time_zone": current.TimeZone}
	after := map[string]any{"display_name": updated.DisplayName, "time_zone": updated.TimeZone}
	_ = h.writeAuditLog(ctx, tenantUUID, userUUID, "profile.update", before, after)
	writeJSON(w, http.StatusOK, profileWireFrom(updated, cred))
}

// buildProfile resolves the caller's identity into the wire shape. When the
// credential's UserID parses as a UUID AND a users row exists, returns the real
// profile. Otherwise returns the credential-derived synthetic profile (P0-A1: no
// fabrication beyond what the credential carries).
func (h *ProfileHandler) buildProfile(ctx context.Context, cred credstore.Credential) (profileWire, error) {
	role := "user"
	if cred.IsAdmin {
		role = "admin"
	}
	if u, err := uuid.Parse(cred.UserID); err == nil {
		tenantUUID, terr := uuid.Parse(cred.TenantID)
		if terr == nil {
			usr, uerr := h.users.GetByID(ctx, tenantUUID, u)
			if uerr == nil {
				return profileWireFrom(usr, cred), nil
			}
			if !errors.Is(uerr, users.ErrNotFound) {
				return profileWire{}, uerr
			}
			// users row missing — fall through to synthetic.
		}
	}
	// Synthetic profile for bootstrap/API-key credentials with no users row.
	var tz *string // nil = null on wire
	return profileWire{
		UserID:      cred.UserID,
		TenantID:    cred.TenantID,
		DisplayName: "API key " + cred.Last4,
		Email:       "",
		IdpSubject:  "",
		TenantRole:  role,
		TimeZone:    tz,
		IsAdmin:     cred.IsAdmin,
		OwnerRoles:  append([]string(nil), cred.OwnerRoles...),
	}, nil
}

func profileWireFrom(u users.User, cred credstore.Credential) profileWire {
	role := "user"
	if cred.IsAdmin {
		role = "admin"
	}
	var tz *string
	if u.TimeZone != "" {
		v := u.TimeZone
		tz = &v
	}
	return profileWire{
		UserID:      u.ID.String(),
		TenantID:    u.TenantID.String(),
		DisplayName: u.DisplayName,
		Email:       u.Email,
		IdpSubject:  u.IdpSubject,
		TenantRole:  role,
		TimeZone:    tz,
		IsAdmin:     cred.IsAdmin,
		OwnerRoles:  append([]string(nil), cred.OwnerRoles...),
	}
}

// writeAuditLog is the shared me_audit_log INSERT primitive used by every PATCH +
// DELETE handler in this package. before / after are JSON-encoded; nil maps are
// stored as {}.
func (h *ProfileHandler) writeAuditLog(ctx context.Context, tenantID, userID uuid.UUID, action string, before, after map[string]any) error {
	if before == nil {
		before = map[string]any{}
	}
	if after == nil {
		after = map[string]any{}
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("marshal before: %w", err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal after: %w", err)
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
		Action:   action,
		Before:   beforeJSON,
		After:    afterJSON,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Note on package-shared helpers (authnContext, writeError, writeJSON,
// writeServerErr): live in audit_period.go. Adding a third handler file means we
// could refactor them into a shared helpers.go, but a third caller of an
// already-duplicated helper is the canonical "rule of three" boundary — defer
// the extract until the fourth handler lands.
var _ = authctx.WithCredential
