// Package frameworkscopes serves the slice-018 HTTP API for the
// FrameworkScope predicate + four-state workflow. Routes:
//
//	POST   /v1/framework-scopes                        — create a draft scope
//	GET    /v1/framework-scopes                        — list (filter by framework_version, state, as_of)
//	GET    /v1/framework-scopes/{id}                   — get one
//	PATCH  /v1/framework-scopes/{id}                   — edit predicate (triggers re-approval if review/approved)
//	PATCH  /v1/framework-scopes/{id}/submit            — draft → review
//	PATCH  /v1/framework-scopes/{id}/approve           — review → approved (approver-role gate)
//	PATCH  /v1/framework-scopes/{id}/activate          — approved → activated
//	GET    /v1/controls/{id}/effective-scope?framework_version=… — intersection compute (AC-11)
//
// Auth: the existing httpAuthMiddleware mounts the credential into context;
// each handler reads the tenant id off the credential and (for approve)
// checks IsApprover OR IsAdmin.
package frameworkscopes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles slice-018 routes. The store does the workflow heavy
// lifting; this layer only does shape validation, auth gates, and wire
// rendering.
type Handler struct {
	store      *frameworkscope.Store
	scopeStore *scope.Store
}

// New constructs a Handler. The scopeStore is used for the
// effective-scope compute (AC-11) which has to load the control's
// applicability cells before intersecting with the framework predicate.
func New(store *frameworkscope.Store, scopeStore *scope.Store) *Handler {
	return &Handler{store: store, scopeStore: scopeStore}
}

// ----- wire types -----

type createReq struct {
	FrameworkVersionID string          `json:"framework_version_id"`
	Name               string          `json:"name"`
	Predicate          json.RawMessage `json:"predicate"`
}

type approveReq struct {
	ApprovalEvidenceFileURL  string `json:"approval_evidence_file_url"`
	ApprovalEvidenceFileHash string `json:"approval_evidence_file_hash"`
}

type activateReq struct {
	EffectiveFrom string `json:"effective_from"` // RFC3339 timestamp
}

type patchReq struct {
	Predicate json.RawMessage `json:"predicate"`
}

type scopeWire struct {
	ID                       string          `json:"id"`
	FrameworkVersionID       string          `json:"framework_version_id"`
	Name                     string          `json:"name"`
	State                    string          `json:"state"`
	Predicate                json.RawMessage `json:"predicate"`
	PredicateHash            string          `json:"predicate_hash"`
	ApproverUserID           *string         `json:"approver_user_id,omitempty"`
	ApprovedAt               *time.Time      `json:"approved_at,omitempty"`
	PredicateHashAtApproval  *string         `json:"predicate_hash_at_approval,omitempty"`
	ApprovalEvidenceFileURL  *string         `json:"approval_evidence_file_url,omitempty"`
	ApprovalEvidenceFileHash *string         `json:"approval_evidence_file_hash,omitempty"`
	EffectiveFrom            *time.Time      `json:"effective_from,omitempty"`
	SupersededBy             *string         `json:"superseded_by,omitempty"`
	SupersededAt             *time.Time      `json:"superseded_at,omitempty"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at"`
}

func toWire(s frameworkscope.FrameworkScope) scopeWire {
	w := scopeWire{
		ID:                       s.ID.String(),
		FrameworkVersionID:       s.FrameworkVersionID.String(),
		Name:                     s.Name,
		State:                    s.State,
		Predicate:                json.RawMessage(s.Predicate),
		PredicateHash:            s.PredicateHash,
		PredicateHashAtApproval:  s.PredicateHashAtApproval,
		ApprovalEvidenceFileURL:  s.ApprovalEvidenceFileURL,
		ApprovalEvidenceFileHash: s.ApprovalEvidenceFileHash,
		EffectiveFrom:            s.EffectiveFrom,
		ApprovedAt:               s.ApprovedAt,
		SupersededAt:             s.SupersededAt,
		CreatedAt:                s.CreatedAt,
		UpdatedAt:                s.UpdatedAt,
	}
	if s.ApproverUserID != nil {
		v := s.ApproverUserID.String()
		w.ApproverUserID = &v
	}
	if s.SupersededBy != nil {
		v := s.SupersededBy.String()
		w.SupersededBy = &v
	}
	return w
}

// ----- handlers -----

// Create — AC-5. POST /v1/framework-scopes.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	fvID, err := uuid.Parse(req.FrameworkVersionID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version_id must be a UUID")
		return
	}
	out, err := h.store.Create(ctx, frameworkscope.CreateRequest{
		FrameworkVersionID: fvID,
		Name:               req.Name,
		Predicate:          []byte(req.Predicate),
	})
	if err != nil {
		if errors.Is(err, frameworkscope.ErrPredicateMalformed) {
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		httperr.WriteInternal(w, r, "create framework_scope", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"framework_scope": toWire(out)})
}

// Patch — AC-9. PATCH /v1/framework-scopes/{id}. Body may include predicate.
// When the row was in `review` or `approved`, the DB trigger bounces it back
// to `draft`; we surface this as `approval_invalidated: true`.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req patchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Predicate) == 0 {
		httpresp.WriteError(w, http.StatusBadRequest, "predicate is required for PATCH")
		return
	}
	out, invalidated, err := h.store.UpdatePredicate(ctx, id, []byte(req.Predicate))
	if err != nil {
		if errors.Is(err, frameworkscope.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "framework_scope not found")
			return
		}
		if errors.Is(err, frameworkscope.ErrPredicateMalformed) {
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		httperr.WriteInternal(w, r, "patch framework_scope", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"framework_scope":      toWire(out),
		"approval_invalidated": invalidated,
	})

}

// Submit — AC-6. PATCH /v1/framework-scopes/{id}/submit.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	out, err := h.store.Submit(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, frameworkscope.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "framework_scope not found")
		case errors.Is(err, frameworkscope.ErrWrongState):
			httpresp.WriteError(w, http.StatusConflict, "scope must be in `draft` to submit")
		default:
			httperr.WriteInternal(w, r, "submit framework_scope", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"framework_scope": toWire(out)})
}

// Approve — AC-7. PATCH /v1/framework-scopes/{id}/approve. Approver-role gate.
//
// Optional body carries the offline-signed evidence-file URL + hash. We
// record both verbatim; the hash is NOT cryptographically verified — that
// would be pretending to have provenance we don't. ADR-0001 §positive notes
// the in-app attestation is the load-bearing audit trail; the file is the
// auditor's own document.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "approver role required")
		return
	}
	// Slice 033: tenancy.Middleware already set app.current_tenant from
	// cred.TenantID. Confirm; bail if absent (would mean misconfig).
	ctx := r.Context()
	if _, terr := tenancy.TenantFromContext(ctx); terr != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req approveReq
	// Approve body is optional — a zero Content-Length is fine.
	if r.ContentLength > 0 {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	// Sanity-check the file fields: URL + hash come in pairs.
	if (req.ApprovalEvidenceFileURL != "") != (req.ApprovalEvidenceFileHash != "") {
		httpresp.WriteError(w, http.StatusBadRequest, "approval_evidence_file_url and approval_evidence_file_hash must be provided together")
		return
	}
	// Sha256 hex is 64 chars; reject anything else loudly so clients don't
	// pass placeholder hashes by accident. Note: we do NOT verify that the
	// hash matches an actual S3 object — that's slice-036's domain.
	if req.ApprovalEvidenceFileHash != "" && !isLikelySha256Hex(req.ApprovalEvidenceFileHash) {
		httpresp.WriteError(w, http.StatusBadRequest, "approval_evidence_file_hash must be a 64-char hex sha256")
		return
	}

	out, err := h.store.Approve(ctx, frameworkscope.ApproveRequest{
		ID:               id,
		ApproverUserID:   cred.UserID,
		EvidenceFileURL:  req.ApprovalEvidenceFileURL,
		EvidenceFileHash: req.ApprovalEvidenceFileHash,
	})
	if err != nil {
		switch {
		case errors.Is(err, frameworkscope.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "framework_scope not found")
		case errors.Is(err, frameworkscope.ErrWrongState):
			httpresp.WriteError(w, http.StatusConflict, "scope must be in `review` to approve")
		default:
			httperr.WriteInternal(w, r, "approve framework_scope", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"framework_scope": toWire(out)})
}

// Activate — AC-8. PATCH /v1/framework-scopes/{id}/activate.
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req activateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.EffectiveFrom) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "effective_from is required")
		return
	}
	t, err := time.Parse(time.RFC3339, req.EffectiveFrom)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "effective_from must be RFC3339: "+err.Error())
		return
	}
	out, err := h.store.Activate(ctx, id, t)
	if err != nil {
		switch {
		case errors.Is(err, frameworkscope.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "framework_scope not found")
		case errors.Is(err, frameworkscope.ErrWrongState):
			httpresp.WriteError(w, http.StatusConflict, "scope must be in `approved` to activate")
		case errors.Is(err, frameworkscope.ErrAnotherActivated):
			httpresp.WriteError(w, http.StatusConflict, "another scope is already activated for this framework version")
		default:
			httperr.WriteInternal(w, r, "activate framework_scope", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"framework_scope": toWire(out)})
}

// Get — single scope by id.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	out, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, frameworkscope.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "framework_scope not found")
			return
		}
		httperr.WriteInternal(w, r, "get framework_scope", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"framework_scope": toWire(out)})
}

// List — AC-10, AC-13. Supports ?framework_version=<uuid>, ?state=<state>,
// ?as_of=<RFC3339>. as_of returns the single row that was active at that
// timestamp (requires framework_version) — strictly more specific than the
// generic list, so the two modes don't compose.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var fvID *uuid.UUID
	if v := strings.TrimSpace(r.URL.Query().Get("framework_version")); v != "" {
		parsed, err := uuid.Parse(v)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "framework_version must be a UUID")
			return
		}
		fvID = &parsed
	}
	if asOf := strings.TrimSpace(r.URL.Query().Get("as_of")); asOf != "" {
		if fvID == nil {
			httpresp.WriteError(w, http.StatusBadRequest, "as_of requires framework_version")
			return
		}
		t, err := time.Parse(time.RFC3339, asOf)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "as_of must be RFC3339: "+err.Error())
			return
		}
		out, err := h.store.AsOf(ctx, *fvID, t)
		if err != nil {
			if errors.Is(err, frameworkscope.ErrNotFound) {
				httpresp.WriteJSON(w, http.StatusOK, map[string]any{"framework_scopes": []scopeWire{}})
				return
			}
			httperr.WriteInternal(w, r, "as_of framework_scope", err)
			return
		}
		httpresp.WriteJSON(w, http.StatusOK, map[string]any{"framework_scopes": []scopeWire{toWire(out)}})
		return
	}

	state := strings.TrimSpace(r.URL.Query().Get("state"))
	rows, err := h.store.List(ctx, frameworkscope.ListFilters{
		FrameworkVersionID: fvID,
		State:              state,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "list framework_scopes", err)
		return
	}
	out := make([]scopeWire, len(rows))
	for i, s := range rows {
		out[i] = toWire(s)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"framework_scopes": out})
}

// EffectiveScope — AC-11. GET /v1/controls/{id}/effective-scope.
//
// Computes effective_scope(control, framework) = control.applicability_expr
// ∩ framework_scope.predicate. Out-of-scope controls return an empty
// effective_scope; the caller's coverage compute interprets that as `n/a`,
// not `fail` (per slice 018 anti-criterion + canvas §5.5).
func (h *Handler) EffectiveScope(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	controlIDStr := chi.URLParam(r, "id")
	controlID, err := uuid.Parse(controlIDStr)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control id must be a UUID")
		return
	}
	fvStr := strings.TrimSpace(r.URL.Query().Get("framework_version"))
	if fvStr == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version query parameter is required")
		return
	}
	fvID, err := uuid.Parse(fvStr)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version must be a UUID")
		return
	}

	// Load the control's applicability set (slice-017 store).
	applicability, err := h.scopeStore.ControlApplicability(ctx, controlID)
	if err != nil {
		httperr.WriteInternal(w, r, "control applicability", err)
		return
	}
	// Load the active framework_scope. If none is activated, the control
	// is effectively out-of-scope (no audit-bound predicate).
	activated, err := h.store.Activated(ctx, fvID)
	if err != nil {
		if errors.Is(err, frameworkscope.ErrNotFound) {
			httpresp.WriteJSON(w, http.StatusOK, map[string]any{
				"control_id":            controlID.String(),
				"framework_version_id":  fvID.String(),
				"framework_scope_id":    nil,
				"effective_scope":       []map[string]any{},
				"effective_scope_count": 0,
				"in_scope":              false,
				"out_of_scope_reason":   "no activated framework_scope for this framework_version",
			})

			return
		}
		httperr.WriteInternal(w, r, "activated framework_scope", err)
		return
	}

	cells, err := frameworkscope.EffectiveScope(ctx, applicability, activated.Predicate)
	if err != nil {
		httperr.WriteInternal(w, r, "intersect", err)
		return
	}
	wireCells := make([]map[string]any, len(cells))
	for i, c := range cells {
		wireCells[i] = map[string]any{
			"id":         c.ID.String(),
			"label":      c.Label,
			"dimensions": c.Dimensions,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"control_id":            controlID.String(),
		"framework_version_id":  fvID.String(),
		"framework_scope_id":    activated.ID.String(),
		"effective_scope":       wireCells,
		"effective_scope_count": len(wireCells),
		"in_scope":              len(wireCells) > 0,
	})

}

// ----- helpers -----

func (h *Handler) tenantContext(r *http.Request) (context.Context, bool) {
	// Slice 033: tenancy.Middleware (httpserver.go) lifted cred.TenantID
	// onto r.Context() via tenancy.WithTenant. Confirm; bail if absent
	// (would mean no credential or misconfig).
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

func isLikelySha256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !ok {
			return false
		}
	}
	return true
}
