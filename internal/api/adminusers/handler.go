// Package adminusers is the HTTP surface for /v1/admin/users (slice 062).
//
// Three routes:
//
//	GET   /v1/admin/users               -- paginated list with roles
//	GET   /v1/admin/users/{id}          -- single-user detail
//	PATCH /v1/admin/users/{id}/roles    -- replace role assignments
//
// All routes require an admin credential (cred.IsAdmin) -- the slice 035
// OPA RBAC middleware also gates the path; this handler is
// defense-in-depth.
//
// Anti-criteria honored (P0):
//
//   - Self-demotion guard: a caller cannot remove their own 'admin' role
//     without an explicit `confirm_self_demotion: true` field in the
//     PATCH body. This prevents the "I lost my own admin" footgun in
//     single-admin deployments.
//   - Role validation: every requested role must be in the canonical set
//     (admin, grc_engineer, control_owner, auditor, viewer) per
//     authz.IsCanonical. Unknown roles return 400 -- the DB CHECK
//     constraint on user_roles.role is a backstop, not the primary gate.
//
// Constitutional invariants honored:
//
//   - Invariant 6 (RLS): all DB access goes through the tenancy-applied
//     transaction; users + user_roles RLS policies fire.
//   - Slice 033 D1: no tenant_id in any request body. The handler reads
//     tenant from the credential, never from the wire.
//   - Slice 035 RBAC: role replacement validates against authz.IsCanonical
//     before writing.
package adminusers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// defaultListLimit / maxListLimit cap GET /v1/admin/users pagination.
const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// Handler owns the user admin routes.
type Handler struct {
	pool *pgxpool.Pool
}

// New constructs a Handler.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// --- response shapes ---

// UserResponse is the per-user JSON shape returned by list + get.
type UserResponse struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name"`
	Status      string     `json:"status"`
	Roles       []string   `json:"roles"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

// ListResponse is the GET /v1/admin/users shape.
type ListResponse struct {
	Items      []UserResponse `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// PatchRolesRequest is the PATCH /v1/admin/users/{id}/roles body.
type PatchRolesRequest struct {
	Roles               []string `json:"roles"`
	ConfirmSelfDemotion bool     `json:"confirm_self_demotion,omitempty"`
}

// --- handlers ---

// List handles GET /v1/admin/users.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}

	limit := defaultListLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, perr := strconv.Atoi(s); perr == nil && n > 0 {
			if n > maxListLimit {
				n = maxListLimit
			}
			limit = n
		}
	}

	var cursorAfter pgtype.UUID
	if c := r.URL.Query().Get("cursor"); c != "" {
		raw, derr := base64.URLEncoding.DecodeString(c)
		if derr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		parsed, perr := uuid.Parse(string(raw))
		if perr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "invalid cursor payload")
			return
		}
		cursorAfter = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	var rows []dbx.ListAdminUsersRow
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		// Fetch limit+1 to determine whether a next page exists.
		got, qErr := q.ListAdminUsers(ctx, dbx.ListAdminUsersParams{
			TenantID:    pgtype.UUID{Bytes: tenantID, Valid: true},
			Limit:       int32(limit + 1),
			CursorAfter: cursorAfter,
		})
		rows = got
		return qErr
	})
	if err != nil {
		httperr.WriteInternal(w, r, "list users", err)
		return
	}

	items := make([]UserResponse, 0, len(rows))
	var nextCursor string
	if len(rows) > limit {
		last := rows[limit-1]
		nextCursor = base64.URLEncoding.EncodeToString([]byte(uuidFromPgtype(last.ID).String()))
		rows = rows[:limit]
	}
	for _, row := range rows {
		items = append(items, userResponseFromListRow(row))
	}

	httpresp.WriteJSON(w, http.StatusOK, ListResponse{Items: items, NextCursor: nextCursor})
}

// Get handles GET /v1/admin/users/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var row dbx.GetAdminUserRow
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		got, qErr := q.GetAdminUser(ctx, dbx.GetAdminUserParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			ID:       pgtype.UUID{Bytes: userID, Valid: true},
		})
		row = got
		return qErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpresp.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		httperr.WriteInternal(w, r, "get user", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, userResponseFromGetRow(row))
}

// PatchRoles handles PATCH /v1/admin/users/{id}/roles.
//
// Anti-criterion P0: rejects self-demotion from admin without
// confirm_self_demotion: true. The caller's user-id is taken from the
// credential context; the path-param id is the target. When they match
// and 'admin' is being removed, we require explicit confirmation.
func (h *Handler) PatchRoles(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req PatchRolesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate every requested role against authz.IsCanonical.
	seen := make(map[string]struct{}, len(req.Roles))
	for _, role := range req.Roles {
		role = strings.TrimSpace(role)
		if !authz.IsCanonical(authz.Role(role)) {
			httpresp.WriteError(w, http.StatusBadRequest, "unknown role: "+role)
			return
		}
		seen[role] = struct{}{}
	}
	hasAdmin := false
	for r := range seen {
		if r == string(authz.RoleAdmin) {
			hasAdmin = true
		}
	}

	// Self-demotion guard. cred.UserID is the caller's identity (set by
	// the auth layer). When the target equals the caller and admin is
	// being dropped, require explicit confirmation.
	if cred.UserID == targetID.String() && !hasAdmin && !req.ConfirmSelfDemotion {
		httpresp.WriteError(w, http.StatusBadRequest, "self-demotion from admin requires confirm_self_demotion=true")
		return
	}

	// Apply the replacement in one transaction so the user is never
	// briefly role-less mid-edit (RLS-safe: the tx runs under the
	// caller's tenant GUC; user_roles RLS policies fire on every
	// statement).
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		if dErr := q.DeleteUserRoles(ctx, dbx.DeleteUserRolesParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			UserID:   targetID.String(),
		}); dErr != nil {
			return fmt.Errorf("delete: %w", dErr)
		}
		for role := range seen {
			grantedBy := cred.UserID
			if grantedBy == "" {
				grantedBy = cred.ID
			}
			if iErr := q.InsertUserRole(ctx, dbx.InsertUserRoleParams{
				TenantID:  pgtype.UUID{Bytes: tenantID, Valid: true},
				UserID:    targetID.String(),
				Role:      role,
				GrantedBy: grantedBy,
			}); iErr != nil {
				return fmt.Errorf("insert role %s: %w", role, iErr)
			}
		}
		return nil
	})
	if err != nil {
		httperr.WriteInternal(w, r, "patch roles", err)
		return
	}

	// Return the updated user so the caller doesn't need a follow-up GET.
	var row dbx.GetAdminUserRow
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		got, qErr := q.GetAdminUser(ctx, dbx.GetAdminUserParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			ID:       pgtype.UUID{Bytes: targetID, Valid: true},
		})
		row = got
		return qErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpresp.WriteError(w, http.StatusNotFound, "user not found after role update")
			return
		}
		httperr.WriteInternal(w, r, "refetch user", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, userResponseFromGetRow(row))
}

// --- helpers ---

func (h *Handler) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func userResponseFromListRow(row dbx.ListAdminUsersRow) UserResponse {
	out := UserResponse{
		ID:          uuidFromPgtype(row.ID).String(),
		Email:       row.Email,
		DisplayName: row.DisplayName,
		Status:      row.Status,
		Roles:       row.Roles,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}
	if row.LastLoginAt.Valid {
		t := row.LastLoginAt.Time
		out.LastLoginAt = &t
	}
	if out.Roles == nil {
		out.Roles = []string{}
	}
	return out
}

func userResponseFromGetRow(row dbx.GetAdminUserRow) UserResponse {
	out := UserResponse{
		ID:          uuidFromPgtype(row.ID).String(),
		Email:       row.Email,
		DisplayName: row.DisplayName,
		Status:      row.Status,
		Roles:       row.Roles,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}
	if row.LastLoginAt.Valid {
		t := row.LastLoginAt.Time
		out.LastLoginAt = &t
	}
	if out.Roles == nil {
		out.Roles = []string{}
	}
	return out
}

func uuidFromPgtype(u pgtype.UUID) uuid.UUID {
	if !u.Valid {
		return uuid.Nil
	}
	return uuid.UUID(u.Bytes)
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	if !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return false
	}
	return true
}
