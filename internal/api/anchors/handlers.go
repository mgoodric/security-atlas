// Package anchors serves the read-only HTTP API for SCF anchors, the
// frameworks/versions catalog, and the requirement-mappings join. Slice 006
// landed the DB-backed anchor list/detail; the requirement-mappings come
// from anchorseed (in-memory) until slice 008 builds the real tables.
package anchors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/anchorseed"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Handler exposes the /v1/anchors + /v1/frameworks routes. Auth is enforced
// by middleware mounted at the router root.
type Handler struct {
	q            *dbx.Queries
	mappings     anchorseed.Store
	defaultLimit int32
	maxLimit     int32
}

// New constructs a Handler. q must be a non-nil sqlc Queries; mappings is
// the in-memory requirement-mappings seed.
func New(q *dbx.Queries, mappings anchorseed.Store) *Handler {
	return &Handler{q: q, mappings: mappings, defaultLimit: 100, maxLimit: 500}
}

// Routes returns a chi router with the slice-005 + slice-006 endpoints.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/v1/anchors", h.listAnchors)
	r.Get("/v1/anchors/{id}", h.getAnchor)
	r.Get("/v1/anchors/{id}/requirements", h.requirementsForAnchor)
	r.Get("/v1/frameworks", h.listFrameworks)
	r.Get("/v1/frameworks/scf/versions", h.listSCFVersions)
	return r
}

// listAnchors returns the SCF anchor catalog. Paginated via ?limit= and
// ?offset=; ?framework_version_id= optionally narrows the list.
func (h *Handler) listAnchors(w http.ResponseWriter, r *http.Request) {
	limit, offset := h.pagination(r)
	ctx := r.Context()

	if fvID := r.URL.Query().Get("framework_version_id"); fvID != "" {
		uid, err := uuid.Parse(fvID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "framework_version_id must be a UUID")
			return
		}
		rows, err := h.q.ListSCFAnchorsForVersion(ctx, dbx.ListSCFAnchorsForVersionParams{
			FrameworkVersionID: pgtype.UUID{Bytes: uid, Valid: true},
			Limit:              limit,
			Offset:             offset,
		})
		if err != nil {
			writeServerErr(w, "list anchors for version", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"anchors": rowsToWire(rows)})
		return
	}

	rows, err := h.q.ListSCFAnchorsLatest(ctx, dbx.ListSCFAnchorsLatestParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		writeServerErr(w, "list anchors latest", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anchors": rowsToWire(rows)})
}

// getAnchor returns one anchor. `:id` may be a UUID or an scf_id
// (e.g., "IAC-06"); scf_id resolves against the current SCF version.
func (h *Handler) getAnchor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	anchor, ok, err := h.lookupAnchor(r.Context(), id)
	if err != nil {
		writeServerErr(w, "lookup anchor", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "anchor not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anchor": anchor})
}

// requirementsForAnchor returns the anchor + its requirement mappings.
// Anchor comes from the DB; mappings come from the in-memory seed (slice 008).
func (h *Handler) requirementsForAnchor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	anchor, ok, err := h.lookupAnchor(r.Context(), id)
	if err != nil {
		writeServerErr(w, "lookup anchor", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "anchor not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"anchor":       anchor,
		"requirements": h.mappings.RequirementsForSCFID(anchor.SCFID),
	})
}

// listFrameworks returns the framework catalog (global only).
func (h *Handler) listFrameworks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListFrameworks(r.Context())
	if err != nil {
		writeServerErr(w, "list frameworks", err)
		return
	}
	out := make([]frameworkWire, 0, len(rows))
	for _, f := range rows {
		out = append(out, frameworkWire{
			ID:              uuidStr(f.ID),
			Name:            f.Name,
			Slug:            f.Slug,
			Issuer:          f.Issuer,
			LatestVersionID: uuidStr(f.LatestVersionID),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"frameworks": out})
}

// listSCFVersions returns every SCF framework_version for the slice's
// audit-replay use case (old versions stay queryable).
func (h *Handler) listSCFVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListFrameworkVersionsBySlug(r.Context(), "scf")
	if err != nil {
		writeServerErr(w, "list scf versions", err)
		return
	}
	out := make([]frameworkVersionWire, 0, len(rows))
	for _, v := range rows {
		out = append(out, frameworkVersionWire{
			ID:            uuidStr(v.ID),
			Version:       v.Version,
			Status:        string(v.Status),
			EffectiveFrom: dateStr(v.EffectiveFrom),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out})
}

func (h *Handler) lookupAnchor(ctx context.Context, idOrSCFID string) (anchorWire, bool, error) {
	if uid, err := uuid.Parse(idOrSCFID); err == nil {
		row, err := h.q.GetSCFAnchorByID(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return anchorWire{}, false, nil
		}
		if err != nil {
			return anchorWire{}, false, err
		}
		return anchorWireFromRow(row), true, nil
	}
	row, err := h.q.GetSCFAnchorBySCFID(ctx, idOrSCFID)
	if errors.Is(err, pgx.ErrNoRows) {
		return anchorWire{}, false, nil
	}
	if err != nil {
		return anchorWire{}, false, err
	}
	return anchorWireFromRow(row), true, nil
}

func (h *Handler) pagination(r *http.Request) (int32, int32) {
	limit := h.defaultLimit
	offset := int32(0)
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = int32(parsed)
			if limit > h.maxLimit {
				limit = h.maxLimit
			}
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}
	return limit, offset
}

// ---- wire types ----

type anchorWire struct {
	ID          string `json:"id"`
	SCFID       string `json:"scf_id"`
	Family      string `json:"family"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type frameworkWire struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Issuer          string `json:"issuer"`
	LatestVersionID string `json:"latest_version_id,omitempty"`
}

type frameworkVersionWire struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	Status        string `json:"status"`
	EffectiveFrom string `json:"effective_from,omitempty"`
}

func rowsToWire[R anchorRow](rows []R) []anchorWire {
	out := make([]anchorWire, len(rows))
	for i, r := range rows {
		out[i] = anchorWireFromRow(r)
	}
	return out
}

type anchorRow interface {
	dbx.ScfAnchor
}

func anchorWireFromRow[R anchorRow](r R) anchorWire {
	a := dbx.ScfAnchor(r)
	return anchorWire{
		ID:          uuidStr(a.ID),
		SCFID:       a.ScfID,
		Family:      a.Family,
		Name:        a.Title,
		Description: a.Description,
	}
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func dateStr(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeServerErr(w http.ResponseWriter, op string, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": op + ": " + err.Error(),
	})
}
