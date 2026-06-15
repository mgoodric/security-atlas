// Package adminframeworkversions is the admin HTTP surface for the slice-484
// framework-versioning capability (ADR 0019):
//
//	POST /v1/admin/framework-versions/{id}/promote            — promote a version to current
//	POST /v1/admin/framework-versions/{id}/revert             — reverse a promotion
//	POST /v1/admin/framework-versions/migrations:suggest      — run the migration-suggest job
//	GET  /v1/admin/framework-versions/migrations              — list the review queue
//	POST /v1/admin/framework-versions/migrations/{id}/decision — approve/reject one suggestion
//
// Every route requires an ADMIN atlas credential (cred.IsAdmin). Version
// promotion is a privileged catalog-write capability (ADR 0019 §1 /
// threat-model E / P0-484-3); a non-admin caller gets 403 — the load-bearing E
// mitigation asserted in the integration test. The suggest job NEVER
// auto-applies a carryover and the approve/reject acts only record a human
// decision (P0-484-1 / the AI-assist "no auto-approve its own mappings"
// boundary).
//
// frameworks / framework_versions are CATALOG tables (no tenant_id, no RLS):
// the gate is this admin-role authz check plus the append-only
// framework_version_audit the store writes in the same transaction — NOT the
// tenant-RLS pattern. Mirrors slice 483's admincrosswalktier shape.
package adminframeworkversions

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/frameworkversion"
)

const maxBody = 16 * 1024

// Handler owns the admin framework-versioning routes.
type Handler struct {
	store *frameworkversion.Store
}

// New constructs a Handler over the lifecycle store.
func New(store *frameworkversion.Store) *Handler { return &Handler{store: store} }

// --- request/response shapes ---

type promoteRequest struct {
	Note string `json:"note,omitempty"`
}

type revertRequest struct {
	PriorVersionID string `json:"prior_version_id"`
	Note           string `json:"note,omitempty"`
}

type promotionResponse struct {
	FrameworkID     string `json:"framework_id"`
	PromotedID      string `json:"promoted_version_id"`
	PromotedVersion string `json:"promoted_version"`
	DemotedID       string `json:"demoted_version_id,omitempty"`
	DemotedVersion  string `json:"demoted_version,omitempty"`
	ActorID         string `json:"actor_id"`
	CreatedAt       string `json:"created_at"`
}

type suggestRequest struct {
	FromVersionID string `json:"from_version_id"`
	ToVersionID   string `json:"to_version_id"`
}

type suggestResponse struct {
	FromVersionID string `json:"from_version_id"`
	ToVersionID   string `json:"to_version_id"`
	ExactCode     int    `json:"exact_code_carryovers"`
	Added         int    `json:"added_flagged"`
	Removed       int    `json:"removed_flagged"`
}

type decisionRequest struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note,omitempty"`
}

type decisionResponse struct {
	MigrationID string `json:"migration_id"`
	Status      string `json:"status"`
	ReviewerID  string `json:"reviewer_id"`
	DecidedAt   string `json:"decided_at"`
}

type migrationRow struct {
	ID                string `json:"id"`
	FrameworkID       string `json:"framework_id"`
	FromVersionID     string `json:"from_version_id"`
	ToVersionID       string `json:"to_version_id"`
	FromRequirementID string `json:"from_requirement_id,omitempty"`
	ToRequirementID   string `json:"to_requirement_id,omitempty"`
	RequirementCode   string `json:"requirement_code"`
	MatchKind         string `json:"match_kind"`
	Status            string `json:"status"`
}

// Promote handles POST /v1/admin/framework-versions/{id}/promote (AC-1).
func (h *Handler) Promote(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdminActor(w, r)
	if !ok {
		return
	}
	versionID, ok := parseID(w, chi.URLParam(r, "id"), "version id")
	if !ok {
		return
	}
	var req promoteRequest
	if !decodeBody(w, r, &req) {
		return
	}

	p, err := h.store.Promote(r.Context(), versionID, actor, req.Note)
	if !h.writePromotionResult(w, r, p, err) {
		return
	}
}

// Revert handles POST /v1/admin/framework-versions/{id}/revert (AC-1
// reversibility).
func (h *Handler) Revert(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdminActor(w, r)
	if !ok {
		return
	}
	versionID, ok := parseID(w, chi.URLParam(r, "id"), "version id")
	if !ok {
		return
	}
	var req revertRequest
	if !decodeBody(w, r, &req) {
		return
	}
	priorID, ok := parseID(w, req.PriorVersionID, "prior_version_id")
	if !ok {
		return
	}

	p, err := h.store.Revert(r.Context(), versionID, priorID, actor, req.Note)
	if !h.writePromotionResult(w, r, p, err) {
		return
	}
}

// Suggest handles POST /v1/admin/framework-versions/migrations:suggest (AC-3).
// It populates the review queue; it NEVER auto-applies (P0-484-1).
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	_, ok := requireAdminActor(w, r)
	if !ok {
		return
	}
	var req suggestRequest
	if !decodeBody(w, r, &req) {
		return
	}
	fromID, ok := parseID(w, req.FromVersionID, "from_version_id")
	if !ok {
		return
	}
	toID, ok := parseID(w, req.ToVersionID, "to_version_id")
	if !ok {
		return
	}

	summary, err := h.store.SuggestMigrations(r.Context(), fromID, toID)
	switch {
	case errors.Is(err, frameworkversion.ErrVersionNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "unknown framework_version id")
		return
	case errors.Is(err, frameworkversion.ErrNotSameFramework):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "the two versions belong to different frameworks")
		return
	case errors.Is(err, frameworkversion.ErrIllegalTransition):
		httpresp.WriteError(w, http.StatusBadRequest, "from and to versions must differ")
		return
	case err != nil:
		httperr.WriteInternal(w, r, "suggest framework-version migrations", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, suggestResponse{
		FromVersionID: req.FromVersionID,
		ToVersionID:   req.ToVersionID,
		ExactCode:     summary.ExactCode,
		Added:         summary.Added,
		Removed:       summary.Removed,
	})
}

// ListMigrations handles GET /v1/admin/framework-versions/migrations?from=&to=.
func (h *Handler) ListMigrations(w http.ResponseWriter, r *http.Request) {
	_, ok := requireAdminActor(w, r)
	if !ok {
		return
	}
	fromID, ok := parseID(w, r.URL.Query().Get("from"), "from")
	if !ok {
		return
	}
	toID, ok := parseID(w, r.URL.Query().Get("to"), "to")
	if !ok {
		return
	}

	rows, err := h.store.ListMigrations(r.Context(), fromID, toID)
	if err != nil {
		httperr.WriteInternal(w, r, "list framework-version migrations", err)
		return
	}
	out := make([]migrationRow, 0, len(rows))
	for _, m := range rows {
		out = append(out, migrationRow{
			ID:                uuidStr(m.ID),
			FrameworkID:       uuidStr(m.FrameworkID),
			FromVersionID:     uuidStr(m.FromVersionID),
			ToVersionID:       uuidStr(m.ToVersionID),
			FromRequirementID: uuidStr(m.FromRequirementID),
			ToRequirementID:   uuidStr(m.ToRequirementID),
			RequirementCode:   m.RequirementCode,
			MatchKind:         string(m.MatchKind),
			Status:            string(m.Status),
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"migrations": out})
}

// Decide handles POST /v1/admin/framework-versions/migrations/{id}/decision
// (AC-4). One suggestion at a time; audited. NEVER auto-applies (P0-484-1).
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdminActor(w, r)
	if !ok {
		return
	}
	migrationID, ok := parseID(w, chi.URLParam(r, "id"), "migration id")
	if !ok {
		return
	}
	var req decisionRequest
	if !decodeBody(w, r, &req) {
		return
	}

	d, err := h.store.DecideMigration(r.Context(), migrationID, actor, req.Approve, req.Note)
	switch {
	case errors.Is(err, frameworkversion.ErrMigrationNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "unknown migration id")
		return
	case errors.Is(err, frameworkversion.ErrAlreadyDecided):
		httpresp.WriteError(w, http.StatusConflict, "migration already decided")
		return
	case err != nil:
		httperr.WriteInternal(w, r, "decide framework-version migration", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, decisionResponse{
		MigrationID: d.MigrationID.String(),
		Status:      d.Status,
		ReviewerID:  d.ReviewerID.String(),
		DecidedAt:   d.DecidedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// --- shared result + helpers ---

func (h *Handler) writePromotionResult(w http.ResponseWriter, r *http.Request, p frameworkversion.Promotion, err error) bool {
	switch {
	case errors.Is(err, frameworkversion.ErrVersionNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "unknown framework_version id")
		return false
	case errors.Is(err, frameworkversion.ErrNotSameFramework):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "the two versions belong to different frameworks")
		return false
	case errors.Is(err, frameworkversion.ErrIllegalTransition):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "illegal version status transition")
		return false
	case err != nil:
		httperr.WriteInternal(w, r, "framework-version lifecycle", err)
		return false
	}
	resp := promotionResponse{
		FrameworkID:     p.FrameworkID.String(),
		PromotedID:      p.PromotedID.String(),
		PromotedVersion: p.PromotedVersion,
		ActorID:         p.ActorID.String(),
		CreatedAt:       p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if p.DemotedID != (uuid.UUID{}) {
		resp.DemotedID = p.DemotedID.String()
		resp.DemotedVersion = p.DemotedVersion
	}
	httpresp.WriteJSON(w, http.StatusOK, resp)
	return true
}

// requireAdminActor enforces the admin gate (P0-484-3 / threat-model E): a
// missing credential is 401; a non-admin credential is 403. Returns the acting
// admin's atlas user id for the audit row.
func requireAdminActor(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return uuid.UUID{}, false
	}
	if !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return uuid.UUID{}, false
	}
	actor, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID))
	if err != nil {
		// A verified admin JWT always carries a parseable user subject; fail
		// closed rather than write an audit row with a nil actor.
		httpresp.WriteError(w, http.StatusForbidden, "admin credential lacks a resolvable user id")
		return uuid.UUID{}, false
	}
	return actor, true
}

func parseID(w http.ResponseWriter, raw, field string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid "+field)
		return uuid.UUID{}, false
	}
	return id, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
