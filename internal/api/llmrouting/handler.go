// Package llmrouting serves the slice-499 tenant-admin API for the per-tenant
// cloud-LLM opt-in routing config. Routes (registered onto the platform root
// router by internal/api/register_questionnaire.go):
//
//	GET    /v1/admin/llm-routing   read the tenant's routing config (masked)
//	PUT    /v1/admin/llm-routing   set/replace provider + (encrypted) key
//	DELETE /v1/admin/llm-routing   clear config -> revert to local-ollama
//
// All routes are TENANT-ADMIN gated (P0-499 threat-model S): a non-admin
// credential is 403. The provider is a CLOSED ENUM (no operator URL, P0-499-3).
// The provider API key is WRITE-ONLY: it is accepted on PUT, encrypted, and
// NEVER returned (GET/PUT responses carry only a "has_api_key" boolean +
// "<redacted>" mask) and never logged (P0-499-4 / AC-3 / AC-11).
//
// The handler changes only WHERE a tenant's drafts are generated; it does NOT
// touch the human-approval gate (P0-499-7) — that lives on the surface records.
package llmrouting

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/llm/cloud"
)

// maxBody bounds the request body so a malicious client cannot stream an
// unbounded payload into the key field.
const maxBody = 1 << 16 // 64 KiB

// Handler bundles the routing-config routes over a single cloud.Store.
type Handler struct {
	store *cloud.Store
}

// New constructs a Handler.
func New(store *cloud.Store) *Handler { return &Handler{store: store} }

// RegisterRoutes mounts the routing-config routes on the platform root router.
func (h *Handler) RegisterRoutes(root chi.Router) {
	root.Get("/v1/admin/llm-routing", h.Get)
	root.Put("/v1/admin/llm-routing", h.Put)
	root.Delete("/v1/admin/llm-routing", h.Delete)
}

// ----- wire shapes -----

// putReq is the PUT body. provider is the closed enum; api_key is WRITE-ONLY
// (accepted here, never echoed back). For local-ollama, api_key must be empty.
type putReq struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
}

// configResp is the masked, API-safe routing config. It NEVER carries the key
// plaintext or ciphertext — only the provider, whether a key is configured, and
// a redaction placeholder.
type configResp struct {
	Provider  string `json:"provider"`
	IsCloud   bool   `json:"is_cloud"`
	HasAPIKey bool   `json:"has_api_key"`
	APIKey    string `json:"api_key,omitempty"`
}

func toResp(mc cloud.MaskedConfig) configResp {
	return configResp{
		Provider:  mc.Provider.String(),
		IsCloud:   mc.IsCloud,
		HasAPIKey: mc.HasAPIKey,
		APIKey:    mc.APIKeyMasked,
	}
}

// Get handles GET /v1/admin/llm-routing — returns the tenant's masked config.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !requireTenantAdmin(w, r) {
		return
	}
	mc, err := h.store.Get(r.Context())
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "failed to read routing config")
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, toResp(mc))
}

// Put handles PUT /v1/admin/llm-routing — set/replace the provider + key.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	if !requireTenantAdmin(w, r) {
		return
	}
	var req putReq
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	provider, ok := cloud.ParseProvider(req.Provider)
	if !ok {
		// Closed enum (P0-499-3): an unknown / free-text provider is rejected
		// before it reaches the DB. No URL is ever accepted.
		httpresp.WriteError(w, http.StatusBadRequest, "provider must be one of: local-ollama, anthropic, openai, bedrock")
		return
	}
	mc, err := h.store.Set(r.Context(), provider, cloud.Secret(req.APIKey))
	if err != nil {
		writeSetError(w, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, toResp(mc))
}

// Delete handles DELETE /v1/admin/llm-routing — clear -> revert to local.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !requireTenantAdmin(w, r) {
		return
	}
	if _, err := h.store.Clear(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "failed to clear routing config")
		return
	}
	// Idempotent: clearing an already-default tenant is a 200 with the default.
	httpresp.WriteJSON(w, http.StatusOK, toResp(cloud.MaskedConfig{
		Provider: cloud.ProviderLocalOllama,
	}))
}

// writeSetError maps a Store.Set error to a clean HTTP status. The key bytes
// are never echoed (the errors are about shape, not content).
func writeSetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, cloud.ErrCloudKeyRequired):
		httpresp.WriteError(w, http.StatusBadRequest, "a cloud provider requires an api_key")
	case errors.Is(err, cloud.ErrLocalProviderNoKey):
		httpresp.WriteError(w, http.StatusBadRequest, "local-ollama provider takes no api_key")
	case errors.Is(err, cloud.ErrCrypterUnconfigured):
		// The deployment has no cloud master key, so it cannot protect a stored
		// key. 409: the request is well-formed but the deployment cannot honor
		// it. The operator must configure ATLAS_LLM_CLOUD_KEY(_FILE) first.
		httpresp.WriteError(w, http.StatusConflict, "cloud routing is not enabled on this deployment (no cloud key configured)")
	default:
		httpresp.WriteError(w, http.StatusInternalServerError, "failed to set routing config")
	}
}

// requireTenantAdmin enforces the tenant-admin gate (P0-499 threat-model S): a
// missing credential is 401; a credential that is neither super_admin nor holds
// the per-tenant "admin" role is 403. Reading/switching the provider + key is a
// privileged action a lower-privilege user must not perform.
func requireTenantAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	if cred.IsAdmin || hasTenantAdminRole(cred.OwnerRoles) {
		return true
	}
	httpresp.WriteError(w, http.StatusForbidden, "tenant-admin role required")
	return false
}

// hasTenantAdminRole reports whether the per-tenant role list grants the
// admin role (the slice-056 RoleAdmin string).
func hasTenantAdminRole(roles []string) bool {
	for _, role := range roles {
		if role == string(authz.RoleAdmin) {
			return true
		}
	}
	return false
}
