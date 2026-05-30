package schemaregistry

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// HTTPHandler exposes the slice-014 routes:
//
//	GET  /v1/schemas                   — list global + caller's tenant-private kinds (AC-1)
//	GET  /v1/schemas/{kind}/{semver}   — return one JSON Schema (AC-2)
//	POST /v1/schemas                   — admin-only register tenant-private kind (AC-4 / AC-5)
//
// Auth is enforced by the existing httpAuthMiddleware in api/httpserver.go;
// every handler additionally consults the credential in context to apply
// admin or tenant-scope rules.
type HTTPHandler struct {
	svc          *Service
	defaultLimit int32
	maxLimit     int32
}

// NewHTTPHandler constructs the handler.
func NewHTTPHandler(svc *Service) *HTTPHandler {
	return &HTTPHandler{svc: svc, defaultLimit: 100, maxLimit: 500}
}

// Routes returns the chi router with the slice-014 endpoints mounted.
// Kept for callers that want a self-contained router (e.g., dedicated
// tests). The production wiring in api/httpserver.go attaches each
// handler directly so they coexist with the anchors handlers.
func (h *HTTPHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/v1/schemas", h.ListHTTP)
	r.Get("/v1/schemas/{kind}/{semver}", h.GetHTTP)
	r.Post("/v1/schemas", h.RegisterHTTP)
	return r
}

// ListHTTP — AC-1: global + tenant-private kinds for the caller's tenant.
func (h *HTTPHandler) ListHTTP(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	limit, offset := h.pagination(r)
	rows, err := h.svc.List(r.Context(), cred.TenantID, limit, offset)
	if err != nil {
		httperr.WriteInternal(w, r, "list", err)
		return
	}
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = wireListItem(r)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"schemas": out})
}

// GetHTTP — AC-2: return the full JSON Schema body for (kind, semver).
func (h *HTTPHandler) GetHTTP(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	kind := chi.URLParam(r, "kind")
	semver := chi.URLParam(r, "semver")
	if kind == "" || semver == "" {
		httpresp.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "kind and semver are required"})
		return
	}
	row, err := h.svc.Get(r.Context(), cred.TenantID, kind, semver)
	if err != nil {
		if errors.Is(err, ErrUnknownKind) {
			httpresp.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "schema not found"})
			return
		}
		httperr.WriteInternal(w, r, "get", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"schema": wireFullItem(row)})
}

// registerRequest is the wire shape for POST /v1/schemas.
type registerRequest struct {
	Kind              string          `json:"kind"`
	Semver            string          `json:"semver"`
	Owner             string          `json:"owner"`
	Schema            json.RawMessage `json:"schema"`
	DefaultSCFAnchors []string        `json:"default_scf_anchors"`
}

// RegisterHTTP — AC-4 + AC-5: admin-only, tenant-scoped registration with
// semver enforcement.
func (h *HTTPHandler) RegisterHTTP(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if !cred.IsAdmin {
		httpresp.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "admin credential required"})
		return
	}
	var body registerRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		httpresp.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "decode: " + err.Error()})
		return
	}
	if len(body.Schema) == 0 {
		httpresp.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "schema body is required"})
		return
	}
	out, err := h.svc.Register(r.Context(), RegisterRequest{
		TenantID:          cred.TenantID,
		Kind:              body.Kind,
		Semver:            body.Semver,
		SchemaJSON:        body.Schema,
		Owner:             body.Owner,
		IsAdmin:           cred.IsAdmin,
		HasCredential:     true,
		CreatedBy:         cred.ID,
		DefaultSCFAnchors: body.DefaultSCFAnchors,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrAnonymous):
			httpresp.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrUnauthorized):
			httpresp.WriteJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrEmptyOwner):
			httpresp.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrSemverConflict):
			httpresp.WriteJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			// Treat catch-all input failures (parseable semver, JSON Schema,
			// uuid, missing kind) as 400 — they reflect bad client input.
			httpresp.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}
	// Drop the tenant cache so the newly registered kind is visible on
	// the very next push validation.
	h.svc.InvalidateTenant(cred.TenantID)

	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"schema": wireFullItem(out)})
}

func (h *HTTPHandler) pagination(r *http.Request) (int32, int32) {
	limit := h.defaultLimit
	offset := int32(0)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = int32(n)
			if limit > h.maxLimit {
				limit = h.maxLimit
			}
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = int32(n)
		}
	}
	return limit, offset
}

// wireListItem omits the schema body to keep the list endpoint compact;
// callers GET /v1/schemas/{kind}/{semver} for the body.
func wireListItem(r RegisteredSchema) map[string]any {
	return map[string]any{
		"id":                  r.ID,
		"kind":                r.Kind,
		"semver":              r.Semver,
		"owner":               r.Owner,
		"default_scf_anchors": defaultAnchors(r.DefaultSCFAnchors),
		"tenant_id":           r.TenantID,
		"scope":               scopeLabel(r.TenantID),
		"created_by":          r.CreatedBy,
	}
}

func wireFullItem(r RegisteredSchema) map[string]any {
	return map[string]any{
		"id":                  r.ID,
		"kind":                r.Kind,
		"semver":              r.Semver,
		"owner":               r.Owner,
		"default_scf_anchors": defaultAnchors(r.DefaultSCFAnchors),
		"tenant_id":           r.TenantID,
		"scope":               scopeLabel(r.TenantID),
		"created_by":          r.CreatedBy,
		"schema":              json.RawMessage(r.SchemaJSON),
	}
}

func scopeLabel(tenantID string) string {
	if tenantID == "" {
		return "global"
	}
	return "tenant"
}

func defaultAnchors(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}
