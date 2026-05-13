// Package anchors serves the read-only HTTP API for SCF anchors and the
// frameworks/versions catalog. Slice 006 landed the DB-backed anchor
// list/detail; slice 007 added the requirement → anchors reverse-traversal
// route; slice 008 moved the anchor → requirements DB-backed handler to
// internal/api/ucfcoverage and retired the slice-006 in-memory
// `anchorseed` placeholder route from this package.
package anchors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Handler exposes the /v1/anchors + /v1/frameworks routes. Auth is enforced
// by middleware mounted at the router root.
type Handler struct {
	q            *dbx.Queries
	defaultLimit int32
	maxLimit     int32
}

// New constructs a Handler. q must be a non-nil sqlc Queries.
func New(q *dbx.Queries) *Handler {
	return &Handler{q: q, defaultLimit: 100, maxLimit: 500}
}

// Routes returns a chi router with the slice-005 + slice-006 + slice-007 endpoints.
//
// Slice 008 supersedes the slice-006 in-memory `/v1/anchors/{id}/requirements`
// route with a DB-backed handler under internal/api/ucfcoverage. That route
// is no longer registered here. The slice-006 `requirementsForAnchor` method
// and the `anchorseed` mapping field stay in place for now (dead-code on the
// hot path; a future cleanup slice removes them) so the Handler signature
// doesn't churn across slices.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/v1/anchors", h.listAnchors)
	r.Get("/v1/anchors/{id}", h.getAnchor)
	r.Get("/v1/frameworks", h.listFrameworks)
	r.Get("/v1/frameworks/scf/versions", h.listSCFVersions)
	// Slice 007: reverse traversal — given a framework_requirements row
	// (by UUID or by `{slug}:{version}:{code}` form), list every SCF
	// anchor it maps to with relationship_type + strength + source
	// attribution + rationale. Slice 008 ships the richer `/coverage`
	// variant alongside this lightweight one (canvas §7.2).
	r.Get("/v1/requirements/{id}/anchors", h.anchorsForRequirement)
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

// anchorsForRequirement returns one framework_requirements row plus every
// fw_to_scf_edges row that originates from it, joined to the scf_anchors
// table for the anchor metadata. The path segment accepts:
//
//   - a UUID — direct framework_requirements.id lookup
//   - `{framework_slug}:{version}:{code}` — natural-key form, e.g.,
//     `soc2:2017:CC6.6`
//   - `{framework_slug}::{code}` — convenience form that resolves
//     {code} against the framework's "current" version, e.g.,
//     `soc2::CC6.6`
//
// Returns 404 when no requirement matches.
func (h *Handler) anchorsForRequirement(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, ok, err := h.lookupRequirement(r.Context(), id)
	if err != nil {
		writeServerErr(w, "lookup requirement", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "requirement not found")
		return
	}

	rows, err := h.q.ListFwToScfEdgesForRequirement(r.Context(), req.ID)
	if err != nil {
		writeServerErr(w, "list edges for requirement", err)
		return
	}
	out := make([]requirementEdgeWire, 0, len(rows))
	for _, row := range rows {
		out = append(out, requirementEdgeWire{
			EdgeID:            uuidStr(row.ID),
			SCFAnchorID:       uuidStr(row.ScfAnchorID),
			SCFID:             row.ScfID,
			Family:            row.Family,
			AnchorTitle:       row.AnchorTitle,
			RelationshipType:  string(row.RelationshipType),
			Strength:          row.Strength,
			SourceAttribution: string(row.SourceAttribution),
			Rationale:         row.Rationale,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requirement": requirementWire{
			ID:    uuidStr(req.ID),
			Code:  req.Code,
			Title: req.Title,
			Body:  req.Body,
		},
		"anchors": out,
	})
}

// lookupRequirement resolves the {id} path segment to a framework_requirement
// row, supporting all three forms documented on anchorsForRequirement.
func (h *Handler) lookupRequirement(ctx context.Context, idOrCode string) (dbx.FrameworkRequirement, bool, error) {
	if uid, err := uuid.Parse(idOrCode); err == nil {
		row, err := h.q.GetFrameworkRequirementByID(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.FrameworkRequirement{}, false, nil
		}
		if err != nil {
			return dbx.FrameworkRequirement{}, false, err
		}
		return row, true, nil
	}

	// Natural-key form: slug:version:code (version may be empty —
	// `soc2::CC6.6` resolves against the framework's current version).
	parts := strings.SplitN(idOrCode, ":", 3)
	if len(parts) != 3 {
		return dbx.FrameworkRequirement{}, false, nil
	}
	slug, version, code := parts[0], parts[1], parts[2]
	if version == "" {
		row, err := h.q.GetFrameworkRequirementByCurrentVersion(ctx, dbx.GetFrameworkRequirementByCurrentVersionParams{
			Slug: slug,
			Code: code,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.FrameworkRequirement{}, false, nil
		}
		return row, err == nil, err
	}
	row, err := h.q.GetFrameworkRequirementByFrameworkSlugVersionCode(ctx, dbx.GetFrameworkRequirementByFrameworkSlugVersionCodeParams{
		Slug:    slug,
		Version: version,
		Code:    code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbx.FrameworkRequirement{}, false, nil
	}
	return row, err == nil, err
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

// requirementWire is the public shape of a framework_requirement.
type requirementWire struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// requirementEdgeWire is one row of "anchors for a requirement" — the
// STRM edge metadata plus the anchor's identifying fields. Joined view
// so callers don't need a second round trip per anchor.
type requirementEdgeWire struct {
	EdgeID            string  `json:"edge_id"`
	SCFAnchorID       string  `json:"scf_anchor_id"`
	SCFID             string  `json:"scf_id"`
	Family            string  `json:"family"`
	AnchorTitle       string  `json:"anchor_title"`
	RelationshipType  string  `json:"relationship_type"`
	Strength          float64 `json:"strength"`
	SourceAttribution string  `json:"source_attribution"`
	Rationale         string  `json:"rationale,omitempty"`
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
