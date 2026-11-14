// #1763: GET /v1/me/tenancy — the install's tenant count.
//
// ADR 0023 decision 2: tenant count, not a mode flag, drives the
// experience. The web app reads this to decide whether to show tenant
// chrome (switcher, Tenants page, tenant pickers). It is platform
// metadata, not tenant data: it names no tenant, only how many exist.
//
// Unlike GET /v1/me/tenants, which lists the caller's memberships from
// the JWT claim, this counts every tenant on the install, so a member of
// one tenant on a multi-tenant install still sees multi_tenant=true.
package me

import (
	"context"
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// TenantCounter is the slice of *platform.Status the handler needs:
// the count of real tenants, the all-zeros row excluded.
type TenantCounter interface {
	TenantCount(ctx context.Context) (int, error)
}

// TenancyHandler serves GET /v1/me/tenancy.
type TenancyHandler struct {
	counter TenantCounter
}

// NewTenancy constructs a TenancyHandler. counter MAY be nil (a unit
// server without a database); the handler then returns 503.
func NewTenancy(counter TenantCounter) *TenancyHandler {
	return &TenancyHandler{counter: counter}
}

// tenancyResponse is the response body. MultiTenant is TenantCount >= 2,
// the threshold at which tenant chrome appears.
type tenancyResponse struct {
	TenantCount int  `json:"tenant_count"`
	MultiTenant bool `json:"multi_tenant"`
}

// Get handles GET /v1/me/tenancy for any authenticated caller.
func (h *TenancyHandler) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := authctx.CredentialFromContext(r.Context()); !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	if h.counter == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "tenancy unavailable: install state not configured")
		return
	}
	n, err := h.counter.TenantCount(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "count tenants", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, tenancyResponse{TenantCount: n, MultiTenant: n > 1})
}
