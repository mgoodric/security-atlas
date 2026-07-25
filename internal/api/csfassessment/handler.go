// Package csfassessment serves the slice-515 HTTP API for the NIST CSF 2.0
// Tier / Profile assessment workflow. Routes:
//
//	PUT    /v1/csf/tier?framework_version=<uuid>                 — set/re-rate the Tier
//	GET    /v1/csf/tier?framework_version=<uuid>                 — read the Tier
//	PUT    /v1/csf/profiles/{kind}?framework_version=<uuid>      — ensure a current|target profile
//	GET    /v1/csf/profiles/{kind}?framework_version=<uuid>      — read a profile + its selections
//	PUT    /v1/csf/profiles/{kind}/selections?framework_version=<uuid> — set one Subcategory outcome
//	DELETE /v1/csf/profiles/{kind}/selections/{requirement_id}?framework_version=<uuid> — clear one
//	GET    /v1/csf/gap?framework_version=<uuid>                  — Current-vs-Target gap view
//
// Auth + role cut (threat-model E, slice 515 decisions-log D4): the existing
// httpAuthMiddleware mounts the credential; READ routes require any
// authenticated tenant credential, WRITE routes require the grc_engineer role
// OR admin (a viewer / auditor / control_owner cannot edit the assessment).
// The role gate uses cred.HasOwnerRole("grc_engineer") (admin is a wildcard
// inside HasOwnerRole), matching the canvas §9.5 role model.
package csfassessment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/csfassessment"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// editRole is the role permitted to edit the CSF assessment. admin is a
// wildcard inside Credential.HasOwnerRole, so an admin also passes.
const editRole = "grc_engineer"

// Handler bundles the slice-515 routes over the domain store.
type Handler struct {
	store *csfassessment.Store
}

// New constructs a Handler.
func New(store *csfassessment.Store) *Handler { return &Handler{store: store} }

// ----- wire types -----

type rateTierReq struct {
	Tier      string `json:"tier"`
	Rationale string `json:"rationale"`
}

type tierWire struct {
	ID                 string `json:"id"`
	FrameworkVersionID string `json:"framework_version_id"`
	Tier               string `json:"tier"`
	Rationale          string `json:"rationale"`
	RatedBy            string `json:"rated_by"`
	RatedAt            string `json:"rated_at"`
}

type profileReq struct {
	Name string `json:"name"`
}

type selectionReq struct {
	RequirementID string `json:"requirement_id"`
	TargetOutcome string `json:"target_outcome"`
	Note          string `json:"note"`
}

type selectionWire struct {
	SubcategoryCode  string `json:"subcategory_code"`
	SubcategoryTitle string `json:"subcategory_title"`
	RequirementID    string `json:"requirement_id"`
	TargetOutcome    string `json:"target_outcome"`
	Note             string `json:"note"`
}

// ----- handlers -----

// PutTier handles PUT /v1/csf/tier. Edit-role gated.
func (h *Handler) PutTier(w http.ResponseWriter, r *http.Request) {
	ctx, actor, ok := h.editContext(w, r)
	if !ok {
		return
	}
	fvID, ok := h.frameworkVersion(w, r)
	if !ok {
		return
	}
	var req rateTierReq
	if !decodeBody(w, r, &req) {
		return
	}
	out, _, err := h.store.RateTier(ctx, csfassessment.RateTierRequest{
		FrameworkVersionID: fvID,
		Tier:               req.Tier,
		Rationale:          req.Rationale,
		Actor:              actor,
	})
	if err != nil {
		if errors.Is(err, csfassessment.ErrInvalidTier) {
			httpresp.WriteError(w, http.StatusBadRequest, "tier must be one of tier1_partial, tier2_risk_informed, tier3_repeatable, tier4_adaptive")
			return
		}
		httperr.WriteInternal(w, r, "rate csf tier", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"tier_rating": tierToWire(out)})
}

// GetTier handles GET /v1/csf/tier. Read-only (any tenant credential).
func (h *Handler) GetTier(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.readContext(w, r)
	if !ok {
		return
	}
	fvID, ok := h.frameworkVersion(w, r)
	if !ok {
		return
	}
	out, err := h.store.GetTier(ctx, fvID)
	if err != nil {
		if errors.Is(err, csfassessment.ErrNotFound) {
			httpresp.WriteJSON(w, http.StatusOK, map[string]any{"tier_rating": nil})
			return
		}
		httperr.WriteInternal(w, r, "get csf tier", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"tier_rating": tierToWire(out)})
}

// PutProfile handles PUT /v1/csf/profiles/{kind}. Edit-role gated.
func (h *Handler) PutProfile(w http.ResponseWriter, r *http.Request) {
	ctx, actor, ok := h.editContext(w, r)
	if !ok {
		return
	}
	kind, ok := h.profileKind(w, r)
	if !ok {
		return
	}
	fvID, ok := h.frameworkVersion(w, r)
	if !ok {
		return
	}
	var req profileReq
	// Body is optional (name only).
	if r.ContentLength > 0 {
		if !decodeBody(w, r, &req) {
			return
		}
	}
	out, err := h.store.EnsureProfile(ctx, csfassessment.EnsureProfileRequest{
		FrameworkVersionID: fvID,
		Kind:               kind,
		Name:               req.Name,
		Actor:              actor,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "ensure csf profile", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"profile": profileToWire(out)})
}

// GetProfile handles GET /v1/csf/profiles/{kind}. Read-only. Returns the
// profile plus its per-Subcategory selections.
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.readContext(w, r)
	if !ok {
		return
	}
	kind, ok := h.profileKind(w, r)
	if !ok {
		return
	}
	fvID, ok := h.frameworkVersion(w, r)
	if !ok {
		return
	}
	prof, err := h.store.GetProfile(ctx, fvID, kind)
	if err != nil {
		if errors.Is(err, csfassessment.ErrNotFound) {
			httpresp.WriteJSON(w, http.StatusOK, map[string]any{"profile": nil, "selections": []selectionWire{}})
			return
		}
		httperr.WriteInternal(w, r, "get csf profile", err)
		return
	}
	sels, err := h.store.ListSelections(ctx, prof.ID)
	if err != nil {
		httperr.WriteInternal(w, r, "list csf selections", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"profile":    profileToWire(prof),
		"selections": selectionsToWire(sels),
	})
}

// PutSelection handles PUT /v1/csf/profiles/{kind}/selections. Edit-role gated.
// Ensures the profile exists (idempotent) before setting the selection so the
// editor can write a Subcategory outcome without a separate create call.
func (h *Handler) PutSelection(w http.ResponseWriter, r *http.Request) {
	ctx, actor, ok := h.editContext(w, r)
	if !ok {
		return
	}
	kind, ok := h.profileKind(w, r)
	if !ok {
		return
	}
	fvID, ok := h.frameworkVersion(w, r)
	if !ok {
		return
	}
	var req selectionReq
	if !decodeBody(w, r, &req) {
		return
	}
	reqID, err := uuid.Parse(strings.TrimSpace(req.RequirementID))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "requirement_id must be a UUID")
		return
	}
	prof, err := h.store.EnsureProfile(ctx, csfassessment.EnsureProfileRequest{
		FrameworkVersionID: fvID,
		Kind:               kind,
		Actor:              actor,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "ensure csf profile", err)
		return
	}
	out, err := h.store.SetSelection(ctx, csfassessment.SetSelectionRequest{
		ProfileID:     prof.ID,
		RequirementID: reqID,
		TargetOutcome: req.TargetOutcome,
		Note:          req.Note,
		Actor:         actor,
	})
	if err != nil {
		switch {
		case errors.Is(err, csfassessment.ErrInvalidOutcome):
			httpresp.WriteError(w, http.StatusBadRequest, "target_outcome must be one of not_targeted, partial, largely, fully")
		case errors.Is(err, csfassessment.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "profile not found")
		default:
			httperr.WriteInternal(w, r, "set csf selection", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"selection": map[string]any{
		"requirement_id": out.RequirementID.String(),
		"target_outcome": out.TargetOutcome,
		"note":           out.Note,
	}})
}

// DeleteSelection handles DELETE /v1/csf/profiles/{kind}/selections/{requirement_id}.
// Edit-role gated.
func (h *Handler) DeleteSelection(w http.ResponseWriter, r *http.Request) {
	ctx, actor, ok := h.editContext(w, r)
	if !ok {
		return
	}
	kind, ok := h.profileKind(w, r)
	if !ok {
		return
	}
	fvID, ok := h.frameworkVersion(w, r)
	if !ok {
		return
	}
	reqID, err := uuid.Parse(chi.URLParam(r, "requirement_id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "requirement_id must be a UUID")
		return
	}
	prof, err := h.store.GetProfile(ctx, fvID, kind)
	if err != nil {
		if errors.Is(err, csfassessment.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "profile not found")
			return
		}
		httperr.WriteInternal(w, r, "get csf profile", err)
		return
	}
	if err := h.store.ClearSelection(ctx, prof.ID, reqID, actor); err != nil {
		if errors.Is(err, csfassessment.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "selection not found")
			return
		}
		httperr.WriteInternal(w, r, "clear csf selection", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"cleared": true})
}

// Gap handles GET /v1/csf/gap. Read-only. Computes the Current-vs-Target gap
// over the two profiles' selections plus the Tier delta. The per-Subcategory
// SCF-anchor coverage traversal is intentionally left to the existing
// /v1/requirements/{id}/coverage route (invariant #1) — this view surfaces the
// requirement_id so the UI can deep-link to that coverage read.
func (h *Handler) Gap(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.readContext(w, r)
	if !ok {
		return
	}
	fvID, ok := h.frameworkVersion(w, r)
	if !ok {
		return
	}

	current, err := h.profileSelections(ctx, fvID, "current")
	if err != nil {
		httperr.WriteInternal(w, r, "current profile selections", err)
		return
	}
	target, err := h.profileSelections(ctx, fvID, "target")
	if err != nil {
		httperr.WriteInternal(w, r, "target profile selections", err)
		return
	}
	rows := csfassessment.Gap(current, target)

	resp := map[string]any{
		"framework_version_id": fvID.String(),
		"gap":                  rows,
		"gap_count":            len(rows),
	}
	// Tier delta when both a current... here the Tier is a single rating, so
	// the gap view surfaces the rating itself; a current-vs-target Tier delta
	// is a v2 spillover (per-function Tiers). Surface the current rating.
	if tr, terr := h.store.GetTier(ctx, fvID); terr == nil {
		resp["tier_rating"] = tierToWire(tr)
	} else if !errors.Is(terr, csfassessment.ErrNotFound) {
		httperr.WriteInternal(w, r, "gap tier", terr)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, resp)
}

// profileSelections returns a profile's selections, or an empty slice when the
// profile doesn't exist yet (a half-built assessment still produces a gap view).
func (h *Handler) profileSelections(ctx context.Context, fvID uuid.UUID, kind string) ([]csfassessment.Selection, error) {
	prof, err := h.store.GetProfile(ctx, fvID, kind)
	if err != nil {
		if errors.Is(err, csfassessment.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return h.store.ListSelections(ctx, prof.ID)
}

// ----- helpers -----

// readContext confirms an authenticated tenant credential + active GUC.
func (h *Handler) readContext(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return nil, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return nil, false
	}
	return r.Context(), true
}

// editContext confirms an authenticated tenant credential carrying the
// edit role (grc_engineer) or admin, and returns the actor id. Threat-model E.
func (h *Handler) editContext(w http.ResponseWriter, r *http.Request) (context.Context, string, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return nil, "", false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return nil, "", false
	}
	if !cred.HasOwnerRole(editRole) {
		httpresp.WriteError(w, http.StatusForbidden, "grc_engineer or admin role required to edit the CSF assessment")
		return nil, "", false
	}
	actor := cred.UserID
	if actor == "" {
		actor = cred.TenantID
	}
	return r.Context(), actor, true
}

// frameworkVersion parses the required ?framework_version=<uuid> query param.
func (h *Handler) frameworkVersion(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	v := strings.TrimSpace(r.URL.Query().Get("framework_version"))
	if v == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version query parameter is required")
		return uuid.UUID{}, false
	}
	fvID, err := uuid.Parse(v)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version must be a UUID")
		return uuid.UUID{}, false
	}
	return fvID, true
}

// profileKind parses + validates the {kind} path param.
func (h *Handler) profileKind(w http.ResponseWriter, r *http.Request) (string, bool) {
	kind := chi.URLParam(r, "kind")
	if !csfassessment.ValidKinds[kind] {
		httpresp.WriteError(w, http.StatusBadRequest, "kind must be current or target")
		return "", false
	}
	return kind, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func tierToWire(t csfassessment.TierRating) tierWire {
	return tierWire{
		ID:                 t.ID.String(),
		FrameworkVersionID: t.FrameworkVersionID.String(),
		Tier:               t.Tier,
		Rationale:          t.Rationale,
		RatedBy:            t.RatedBy,
		RatedAt:            t.RatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func profileToWire(p csfassessment.Profile) map[string]any {
	return map[string]any{
		"id":                   p.ID.String(),
		"framework_version_id": p.FrameworkVersionID.String(),
		"kind":                 p.Kind,
		"name":                 p.Name,
		"created_by":           p.CreatedBy,
	}
}

func selectionsToWire(sels []csfassessment.Selection) []selectionWire {
	out := make([]selectionWire, len(sels))
	for i, s := range sels {
		out[i] = selectionWire{
			SubcategoryCode:  s.SubcategoryCode,
			SubcategoryTitle: s.SubcategoryTitle,
			RequirementID:    s.RequirementID.String(),
			TargetOutcome:    s.TargetOutcome,
			Note:             s.Note,
		}
	}
	return out
}
