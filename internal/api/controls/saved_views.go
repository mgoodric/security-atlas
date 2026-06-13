// Slice 468 — server-backed saved filter-views for the /controls list.
//
// Backend half of slice 448's client-side saved-views (which persisted to
// localStorage). The seam slice 448 built (the injected SavedViewStore in
// web/.../saved-views.ts) swaps to a fetch-backed store that calls these
// routes.
//
// ISOLATION (threat-model I / P0-448-5):
//   - TENANT half is RLS — every query runs in a tenant-GUC tx and the
//     saved_views table FORCEs current_tenant_matches(tenant_id).
//   - USER half is the mandatory user_id predicate sourced from the
//     VERIFIED credential (never the request body). There is no
//     app.current_user GUC at v1, so the per-user cut lives in the WHERE
//     clause exactly as user_notification_preferences (slice 016) does it.
//     A caller therefore CANNOT read, create-for, or delete another user's
//     view — the id it could pass is matched against `user_id = <caller>`,
//     so a foreign id resolves to "not found".
//
// FILTER VALIDATION (threat-model T): the persisted `filters` payload is
// narrowed to exactly the slice-224 controls-filter keys
// (sanitizeControlFilters) before INSERT — no arbitrary JSON round-trips
// into a stored view that could later become a query fragment. This is the
// server analogue of slice 448's client-side sanitizeFilters (D5).
//
// CAPS: per-user MaxSavedViews (mirrors the client MAX_SAVED_VIEWS = 20) and
// a 60-char name cap (DB CHECK + handler guard). Over-cap / duplicate-name /
// empty-name each map to a distinct 4xx.
package controls

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// savedViewSurface is the only surface v1 ships (controls list). The
	// DB CHECK pins it; slice 448 P0-448-7 keeps this slice controls-only.
	savedViewSurface = "controls"
	// MaxSavedViews caps a user's saved views per surface. Mirrors the
	// client MAX_SAVED_VIEWS (web .../saved-views.ts = 20).
	MaxSavedViews = 20
	// maxViewNameLen mirrors the client MAX_VIEW_NAME_LENGTH (= 60) and the
	// DB saved_views_name_len CHECK.
	maxViewNameLen = 60
	// pgUniqueViolation is the SQLSTATE for unique_violation — the
	// case-insensitive name index raises it on a duplicate name.
	pgUniqueViolation = "23505"
)

// controlFilterKeys is the slice-224 controls-filter allow-list. A persisted
// view's filter payload is narrowed to EXACTLY these keys (threat-model T).
// MUST stay in sync with web/app/(authed)/controls/filters.ts ControlFilters.
var controlFilterKeys = []string{"framework", "family", "result", "freshness", "scope"}

// SavedViewsHandler binds the saved-views routes. pool may be nil in
// unit-only servers (the route gates return 503).
type SavedViewsHandler struct {
	pool *pgxpool.Pool
}

// NewSavedViewsHandler constructs the handler.
func NewSavedViewsHandler(pool *pgxpool.Pool) *SavedViewsHandler {
	return &SavedViewsHandler{pool: pool}
}

// ----- wire types -----

type savedViewWire struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Filters map[string]string `json:"filters"`
}

type listSavedViewsResponse struct {
	Views []savedViewWire `json:"views"`
}

type createSavedViewRequest struct {
	Name    string            `json:"name"`
	Filters map[string]string `json:"filters"`
}

type savedViewErrorBody struct {
	Error string `json:"error"`
}

// ----- handlers -----

// List serves GET /v1/saved-views (controls surface). Returns the caller's
// own views only.
func (h *SavedViewsHandler) List(w http.ResponseWriter, r *http.Request) {
	cred, tenantUUID, userUUID, ok := h.identity(w, r)
	if !ok {
		return
	}
	var views []dbx.SavedView
	err := h.inTenantTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		var lerr error
		views, lerr = q.ListSavedViews(ctx, dbx.ListSavedViewsParams{
			TenantID: pgUUID(tenantUUID),
			UserID:   pgUUID(userUUID),
			Surface:  savedViewSurface,
		})
		return lerr
	})
	_ = cred
	if err != nil {
		writeSavedViewError(w, http.StatusInternalServerError, "list saved views: "+err.Error())
		return
	}
	out := listSavedViewsResponse{Views: make([]savedViewWire, 0, len(views))}
	for _, v := range views {
		out.Views = append(out.Views, toWire(v))
	}
	writeSavedViewJSON(w, http.StatusOK, out)
}

// Create serves POST /v1/saved-views. Persists a view for the CALLING user
// (user_id from the credential, never the body).
func (h *SavedViewsHandler) Create(w http.ResponseWriter, r *http.Request) {
	_, tenantUUID, userUUID, ok := h.identity(w, r)
	if !ok {
		return
	}
	var req createSavedViewRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeSavedViewError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if jerr := json.Unmarshal(body, &req); jerr != nil {
		writeSavedViewError(w, http.StatusBadRequest, "invalid JSON body: "+jerr.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeSavedViewError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > maxViewNameLen {
		writeSavedViewError(w, http.StatusBadRequest, "name exceeds the 60-character cap")
		return
	}
	// Validate the filter payload to the allow-list (threat-model T).
	filtersBlob, ferr := sanitizeControlFilters(req.Filters)
	if ferr != nil {
		writeSavedViewError(w, http.StatusBadRequest, ferr.Error())
		return
	}

	var created dbx.SavedView
	txErr := h.inTenantTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		// Per-user cap check (within the tx so concurrent creates can't
		// both pass — the unique index + cap together bound the set).
		count, cerr := q.CountSavedViews(ctx, dbx.CountSavedViewsParams{
			TenantID: pgUUID(tenantUUID),
			UserID:   pgUUID(userUUID),
			Surface:  savedViewSurface,
		})
		if cerr != nil {
			return cerr
		}
		if count >= MaxSavedViews {
			return errCapReached
		}
		row, ierr := q.InsertSavedView(ctx, dbx.InsertSavedViewParams{
			TenantID: pgUUID(tenantUUID),
			UserID:   pgUUID(userUUID),
			Surface:  savedViewSurface,
			Name:     name,
			Filters:  filtersBlob,
		})
		if ierr != nil {
			return ierr
		}
		created = row
		return nil
	})
	if txErr != nil {
		h.writeCreateError(w, txErr)
		return
	}
	writeSavedViewJSON(w, http.StatusCreated, toWire(created))
}

// Delete serves DELETE /v1/saved-views/{id}. Deletes one of the CALLER's
// own views; a foreign id (another user's view) resolves to 404 because the
// query is scoped to the caller's user_id.
func (h *SavedViewsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	_, tenantUUID, userUUID, ok := h.identity(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSavedViewError(w, http.StatusBadRequest, "view id must be a uuid")
		return
	}
	var deleted bool
	txErr := h.inTenantTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		_, derr := q.DeleteSavedView(ctx, dbx.DeleteSavedViewParams{
			TenantID: pgUUID(tenantUUID),
			UserID:   pgUUID(userUUID),
			ID:       pgUUID(id),
		})
		if errors.Is(derr, pgx.ErrNoRows) {
			return nil // not the caller's view (or already gone) -> 404 below
		}
		if derr != nil {
			return derr
		}
		deleted = true
		return nil
	})
	if txErr != nil {
		writeSavedViewError(w, http.StatusInternalServerError, "delete saved view: "+txErr.Error())
		return
	}
	if !deleted {
		writeSavedViewError(w, http.StatusNotFound, "saved view not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- helpers -----

var errCapReached = errors.New("saved-view cap reached")

// identity resolves the calling credential + tenant + user UUIDs. The user
// id is the verified credential's user (the "user:" prefix stripped) — never
// the request body. A non-user (machine) credential is rejected: saved views
// are a per-human surface.
func (h *SavedViewsHandler) identity(w http.ResponseWriter, r *http.Request) (credstore.Credential, uuid.UUID, uuid.UUID, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeSavedViewError(w, http.StatusUnauthorized, "authentication required")
		return cred, uuid.Nil, uuid.Nil, false
	}
	if h.pool == nil {
		writeSavedViewError(w, http.StatusServiceUnavailable, "saved-views store not configured")
		return cred, uuid.Nil, uuid.Nil, false
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeSavedViewError(w, http.StatusInternalServerError, "tenant context: invalid tenant id")
		return cred, uuid.Nil, uuid.Nil, false
	}
	userUUID, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID))
	if err != nil {
		writeSavedViewError(w, http.StatusForbidden, "saved views require a user credential")
		return cred, uuid.Nil, uuid.Nil, false
	}
	return cred, tenantUUID, userUUID, true
}

func (h *SavedViewsHandler) inTenantTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if terr := tenancy.ApplyTenant(ctx, tx); terr != nil {
		return terr
	}
	if ferr := fn(ctx, dbx.New(tx)); ferr != nil {
		return ferr
	}
	return tx.Commit(ctx)
}

func (h *SavedViewsHandler) writeCreateError(w http.ResponseWriter, err error) {
	if errors.Is(err, errCapReached) {
		writeSavedViewError(w, http.StatusUnprocessableEntity,
			"saved-view limit reached; delete one before saving another")
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		writeSavedViewError(w, http.StatusConflict, "a view with that name already exists")
		return
	}
	writeSavedViewError(w, http.StatusInternalServerError, "create saved view: "+err.Error())
}

// sanitizeControlFilters narrows an arbitrary filter map to EXACTLY the
// slice-224 controls-filter keys (threat-model T). Unknown keys are dropped;
// only non-empty string values survive. Returns the canonical JSON blob to
// persist. A nil/empty input yields "{}" (a view with no narrowing — the
// handler rejects that earlier via the empty-name path? no: an all-default
// filter set is allowed server-side; the CLIENT disables Save in that case).
func sanitizeControlFilters(raw map[string]string) ([]byte, error) {
	out := make(map[string]string)
	allowed := make(map[string]struct{}, len(controlFilterKeys))
	for _, k := range controlFilterKeys {
		allowed[k] = struct{}{}
	}
	for k, v := range raw {
		if _, ok := allowed[k]; !ok {
			continue // drop unknown key
		}
		if v == "" {
			continue
		}
		out[k] = v
	}
	blob, err := json.Marshal(out)
	if err != nil {
		return nil, errors.New("filters payload is not serializable")
	}
	return blob, nil
}

// toWire converts a stored row to the wire shape, re-sanitizing the stored
// filters on read (defense in depth — a row written before a key-set change
// degrades to the known keys rather than leaking an unknown key to the UI).
func toWire(v dbx.SavedView) savedViewWire {
	filters := map[string]string{}
	if len(v.Filters) > 0 {
		var stored map[string]any
		if err := json.Unmarshal(v.Filters, &stored); err == nil {
			allowed := make(map[string]struct{}, len(controlFilterKeys))
			for _, k := range controlFilterKeys {
				allowed[k] = struct{}{}
			}
			keys := make([]string, 0, len(stored))
			for k := range stored {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if _, ok := allowed[k]; !ok {
					continue
				}
				if s, ok := stored[k].(string); ok && s != "" {
					filters[k] = s
				}
			}
		}
	}
	return savedViewWire{
		ID:      uuid.UUID(v.ID.Bytes).String(),
		Name:    v.Name,
		Filters: filters,
	}
}

func writeSavedViewJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeSavedViewError(w http.ResponseWriter, status int, msg string) {
	writeSavedViewJSON(w, status, savedViewErrorBody{Error: msg})
}
