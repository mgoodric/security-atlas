package controlimports

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/controlimport"
)

// maxSettingsBody bounds the PUT body.
const maxSettingsBody = 4 * 1024

// SettingsStore reads and writes a tenant's control import settings
// (controlimport.Store in production).
type SettingsStore interface {
	GetSettings(ctx context.Context) (controlimport.Settings, error)
	SetSettings(ctx context.Context, in controlimport.Settings, updatedBy string, actor uuid.UUID, principal string) (controlimport.Settings, error)
}

// WithSettings serves GET and PUT /v1/controls/imports/settings (#2079).
func (h *Handler) WithSettings(s SettingsStore) *Handler {
	h.settings = s
	return h
}

type settingsPut struct {
	LLMRerankLocal *bool `json:"llm_rerank_local"`
}

// GetSettings returns the tenant's control import settings.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if !requireTenantAdmin(w, r) {
		return
	}
	s, err := h.settings.GetSettings(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "controlimports", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, s)
}

// PutSettings replaces the tenant's control import settings. Turning
// llm_rerank_local on lets a tenant not routed to a cloud LLM have its import
// candidates reranked on its local Ollama. Each change is audited (#2224).
func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	if !requireTenantAdmin(w, r) {
		return
	}
	var body settingsPut
	dec := json.NewDecoder(io.LimitReader(r.Body, maxSettingsBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.LLMRerankLocal == nil {
		httpresp.WriteError(w, http.StatusBadRequest, `body must be {"llm_rerank_local": true|false}`)
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	// The audit row names the user; a caller that is not one (a bootstrap
	// key) is recorded as uuid.Nil, with cred.ID still in updated_by. The
	// row's actor_principal names the credential either way (#2329).
	actor, _ := credstore.CallerUserID(cred)
	s, err := h.settings.SetSettings(r.Context(), controlimport.Settings{LLMRerankLocal: *body.LLMRerankLocal}, cred.ID, actor, credstore.PrincipalOf(cred))
	if err != nil {
		httperr.WriteInternal(w, r, "controlimports", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, s)
}

// requireTenantAdmin: whether an import may call an LLM is the tenant
// administrator's decision, as the LLM routing config is.
func requireTenantAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	if !authz.IsTenantAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "tenant-admin role required")
		return false
	}
	return true
}
