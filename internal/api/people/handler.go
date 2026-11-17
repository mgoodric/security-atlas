// Package people serves GET /v1/people (#2085): the caller's tenant's
// users and API keys, reduced to what a page needs to show a person in
// place of an ID.
//
// Every signed-in role reads it (defaults.rego). It runs on the app pool
// under the caller's RLS tenant (invariant #6), never the BYPASSRLS auth
// pool, so a super-admin sees only the tenant it is signed in to. The
// admin-only /v1/admin/users and /v1/admin/credentials are unchanged.
//
// ?module=<key> (#2174) lists only the users who can work in that optional
// module: an assignee picker on a record offers no one who could not open
// it. The filter runs here, on the server, from each person's live roles
// and module access (moduleaccess.Views.MembersWith).
//
// It returns no email address, token, prefix or last4: a user's
// display_name falls back to the email's local-part here, on the server.
package people

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// maxRows bounds each list. A tenant is a 50-150-person company; the
// bound keeps the scan finite, not paginated. A list that reaches it is
// truncated and logged (see truncated).
const maxRows = 5000

// truncated reports whether a list fetched with LIMIT maxRows+1 overflowed
// the bound, and logs it: past the bound, some actors read "Unknown".
func truncated(ctx context.Context, n int, list string, tenantID uuid.UUID) bool {
	if n <= maxRows {
		return false
	}
	slog.WarnContext(ctx, "people list truncated", "list", list, "max_rows", maxRows, "tenant_id", tenantID.String())
	return true
}

// User is one person in the caller's tenant.
type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}

// APIKey is one API key in the caller's tenant: its id and its owner
// (api_keys.issued_by), nothing else. A key with no owner has no owner_id;
// minted_by (the admin who minted it) is attribution, not ownership
// (apikeystore.IssueInput), so it is never a fallback.
type APIKey struct {
	ID      string `json:"id"`
	OwnerID string `json:"owner_id,omitempty"`
}

// Response is the GET /v1/people shape.
type Response struct {
	Users   []User   `json:"users"`
	APIKeys []APIKey `json:"api_keys"`
}

// Handler serves GET /v1/people.
type Handler struct {
	pool  *pgxpool.Pool
	views *moduleaccess.Views
}

// WithModules wires the module views ?module= filters by (#2174). Without
// them (a unit-test server) the filter keeps everyone.
func (h *Handler) WithModules(v *moduleaccess.Views) *Handler {
	h.views = v
	return h
}

// knownModule reports whether key names a module in the registry.
func knownModule(key string) bool {
	for _, m := range featureflag.Modules {
		if m.Key == key {
			return true
		}
	}
	return false
}

// withAccess keeps the users members names (user_roles keys, which for a
// user are its id).
func withAccess(users []User, members map[string]bool) []User {
	out := make([]User, 0, len(users))
	for _, u := range users {
		if members[u.ID] {
			out = append(out, u)
		}
	}
	return out
}

// New builds a Handler on the app pool.
func New(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

// DisplayName is the name shown for a user: display_name trimmed, else
// the email's local-part, else empty. The email itself never leaves.
func DisplayName(displayName, email string) string {
	if dn := strings.TrimSpace(displayName); dn != "" {
		return dn
	}
	e := strings.TrimSpace(email)
	if at := strings.IndexByte(e, '@'); at >= 0 {
		return e[:at]
	}
	return e
}

// List handles GET /v1/people.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	module := r.URL.Query().Get("module")
	if module != "" && !knownModule(module) {
		httpresp.WriteError(w, http.StatusBadRequest, "module: not a module")
		return
	}
	if h.pool == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "people list unavailable: no database")
		return
	}
	out, err := h.list(r.Context(), tenantID)
	if err != nil {
		httperr.WriteInternal(w, r, "list people", err)
		return
	}
	if module != "" {
		// A failed access read fails closed: 500, never everyone.
		members, filter, err := h.views.MembersWith(r.Context(), tenantID, module)
		if err != nil {
			httperr.WriteInternal(w, r, "list people with module access", err)
			return
		}
		if filter {
			out.Users = withAccess(out.Users, members)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) list(ctx context.Context, tenantID uuid.UUID) (Response, error) {
	out := Response{Users: []User{}, APIKeys: []APIKey{}}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return out, err
	}

	// RLS scopes both reads to the session tenant; the explicit
	// tenant_id predicate is defence in depth.
	rows, err := tx.Query(ctx,
		`SELECT id, display_name, email, status
		   FROM users
		  WHERE tenant_id = $1
		  ORDER BY id
		  LIMIT $2`, tenantID, maxRows+1)
	if err != nil {
		return out, fmt.Errorf("query users: %w", err)
	}
	for rows.Next() {
		var (
			id                         uuid.UUID
			displayName, email, status string
		)
		if err := rows.Scan(&id, &displayName, &email, &status); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan user: %w", err)
		}
		out.Users = append(out.Users, User{ID: id.String(), DisplayName: DisplayName(displayName, email), Status: status})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("users rows: %w", err)
	}
	if truncated(ctx, len(out.Users), "users", tenantID) {
		out.Users = out.Users[:maxRows]
	}

	// Revoked keys stay listed: they still name the actor on older records.
	krows, err := tx.Query(ctx,
		`SELECT id, issued_by
		   FROM api_keys
		  WHERE tenant_id = $1
		  ORDER BY id
		  LIMIT $2`, tenantID, maxRows+1)
	if err != nil {
		return out, fmt.Errorf("query api keys: %w", err)
	}
	for krows.Next() {
		var (
			id       uuid.UUID
			issuedBy pgtype.UUID
		)
		if err := krows.Scan(&id, &issuedBy); err != nil {
			krows.Close()
			return out, fmt.Errorf("scan api key: %w", err)
		}
		k := APIKey{ID: id.String()}
		if issuedBy.Valid {
			k.OwnerID = uuid.UUID(issuedBy.Bytes).String()
		}
		out.APIKeys = append(out.APIKeys, k)
	}
	krows.Close()
	if err := krows.Err(); err != nil {
		return out, fmt.Errorf("api keys rows: %w", err)
	}
	if truncated(ctx, len(out.APIKeys), "api_keys", tenantID) {
		out.APIKeys = out.APIKeys[:maxRows]
	}
	return out, tx.Commit(ctx)
}
