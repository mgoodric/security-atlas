package oscalcomponents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// componentStore is the seam the handler reads + writes through. The
// production *Store satisfies it; tests inject a stub to drive the wire shape
// + error branches with no Postgres pool.
type componentStore interface {
	ListDefinitions(ctx context.Context) ([]dbx.ImportedCatalog, error)
	GetDefinitionWithClaims(ctx context.Context, defID uuid.UUID) (DefinitionWithClaims, error)
	Disposition(ctx context.Context, claimID uuid.UUID, toStatus, actor, note string) (dbx.ImportedComponentClaim, error)
}

// Handler wires the OSCAL component-claim read + disposition routes over a
// Store.
type Handler struct {
	store componentStore
}

// New constructs a Handler over the application pgx pool's Store. The
// production call site (httpserver.go) passes NewStore(pool).
func New(store *Store) *Handler { return &Handler{store: store} }

// newHandlerWithStore constructs a Handler over an arbitrary store seam.
// Unexported — it exists only for the unit tests, which inject a stub so the
// wire shapes + branches record without a Postgres pool.
func newHandlerWithStore(s componentStore) *Handler { return &Handler{store: s} }

// ===== wire shapes =====

// definitionSummaryWire is one imported component-definition in the list.
type definitionSummaryWire struct {
	ID           string `json:"id"`
	SourceLabel  string `json:"source_label"`
	CatalogTitle string `json:"catalog_title"`
	OSCALVersion string `json:"oscal_version"`
	SourceSHA256 string `json:"source_sha256"`
	ClaimCount   int    `json:"claim_count"`
	ImportedBy   string `json:"imported_by"`
	ImportedAt   string `json:"imported_at"`
}

// claimWire is one vendor-attributed claim. is_vendor_claim is ALWAYS true —
// the field is surfaced explicitly so a consumer can never mistake a claim for
// platform-verified evidence (P0-512-1 / the slice-589 read-API boundary).
type claimWire struct {
	ID                  string  `json:"id"`
	ImportedComponentID string  `json:"imported_component_id"`
	ComponentUUID       string  `json:"component_uuid"`
	ComponentTitle      string  `json:"component_title"`
	ComponentType       string  `json:"component_type"`
	ControlID           string  `json:"control_id"`
	Statement           string  `json:"statement"`
	RequirementUUID     string  `json:"requirement_uuid"`
	ScfAnchorID         *string `json:"scf_anchor_id"`
	Unmapped            bool    `json:"unmapped"`
	IsVendorClaim       bool    `json:"is_vendor_claim"`
	ClaimStatus         string  `json:"claim_status"`
	DispositionedBy     *string `json:"dispositioned_by,omitempty"`
	DispositionedAt     *string `json:"dispositioned_at,omitempty"`
	DispositionNote     string  `json:"disposition_note"`
}

// definitionDetailWire is the GET /{id} response: the provenance row + its
// flattened vendor-claim list.
type definitionDetailWire struct {
	ID           string      `json:"id"`
	SourceLabel  string      `json:"source_label"`
	CatalogTitle string      `json:"catalog_title"`
	OSCALVersion string      `json:"oscal_version"`
	SourceSHA256 string      `json:"source_sha256"`
	ImportedBy   string      `json:"imported_by"`
	ImportedAt   string      `json:"imported_at"`
	Claims       []claimWire `json:"claims"`
}

// dispositionReq is the optional body for an accept/reject/needs-info action.
type dispositionReq struct {
	Note string `json:"note"`
}

// ListDefinitions handles GET /v1/oscal/component-definitions — a tenant's
// imported component-definitions, most recent first. Read-gated +
// tenant-scoped via RLS.
func (h *Handler) ListDefinitions(w http.ResponseWriter, r *http.Request) {
	if !requireOscalRead(w, r) {
		return
	}
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	rows, err := h.store.ListDefinitions(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "oscalcomponents", err)
		return
	}
	out := make([]definitionSummaryWire, len(rows))
	for i, d := range rows {
		out[i] = definitionSummaryWire{
			ID:           uuidString(d.ID),
			SourceLabel:  d.SourceLabel,
			CatalogTitle: d.CatalogTitle,
			OSCALVersion: d.OscalVersion,
			SourceSHA256: d.SourceSha256,
			ClaimCount:   int(d.ControlCount),
			ImportedBy:   d.ImportedBy,
			ImportedAt:   tsString(d.ImportedAt),
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"component_definitions": out, "count": len(out)})
}

// GetDefinition handles GET /v1/oscal/component-definitions/{id} — one
// import's components + their vendor claims (with the SCF-anchor mapping +
// the unmapped flag). Read-gated + tenant-scoped; a cross-tenant / unknown /
// wrong-kind id returns 404.
func (h *Handler) GetDefinition(w http.ResponseWriter, r *http.Request) {
	if !requireOscalRead(w, r) {
		return
	}
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	defID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "component-definition id must be a uuid")
		return
	}
	res, err := h.store.GetDefinitionWithClaims(ctx, defID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpresp.WriteError(w, http.StatusNotFound, "imported component-definition not found")
			return
		}
		httperr.WriteInternal(w, r, "oscalcomponents", err)
		return
	}
	claims := make([]claimWire, len(res.Claims))
	for i, c := range res.Claims {
		claims[i] = claimWire{
			ID:                  uuidString(c.ClaimID),
			ImportedComponentID: uuidString(c.ImportedComponentID),
			ComponentUUID:       c.ComponentUuid,
			ComponentTitle:      c.ComponentTitle,
			ComponentType:       c.ComponentType,
			ControlID:           c.ControlID,
			Statement:           c.Statement,
			RequirementUUID:     c.RequirementUuid,
			ScfAnchorID:         c.ScfAnchorID,
			Unmapped:            c.ScfAnchorID == nil,
			IsVendorClaim:       c.IsVendorClaim,
			ClaimStatus:         c.ClaimStatus,
			DispositionedBy:     c.DispositionedBy,
			DispositionedAt:     tsPtr(c.DispositionedAt),
			DispositionNote:     c.DispositionNote,
		}
	}
	d := res.Definition
	httpresp.WriteJSON(w, http.StatusOK, definitionDetailWire{
		ID:           uuidString(d.ID),
		SourceLabel:  d.SourceLabel,
		CatalogTitle: d.CatalogTitle,
		OSCALVersion: d.OscalVersion,
		SourceSHA256: d.SourceSha256,
		ImportedBy:   d.ImportedBy,
		ImportedAt:   tsString(d.ImportedAt),
		Claims:       claims,
	})
}

// Accept handles POST /v1/oscal/component-claims/{id}:accept.
func (h *Handler) Accept(w http.ResponseWriter, r *http.Request) {
	h.disposition(w, r, "accepted")
}

// Reject handles POST /v1/oscal/component-claims/{id}:reject.
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	h.disposition(w, r, "rejected")
}

// NeedsInfo handles POST /v1/oscal/component-claims/{id}:needs-info.
func (h *Handler) NeedsInfo(w http.ResponseWriter, r *http.Request) {
	h.disposition(w, r, "needs_info")
}

// disposition is the shared accept/reject/needs-info body. It is gated on
// IsApprover (grc_engineer) | IsAdmin — disposition is a write that credits or
// declines a vendor assertion, so a bare read role is not enough. Accepting a
// claim does NOT auto-satisfy a control: the store writes only the claim's
// disposition metadata + an append-only audit row (invariant #2 / P0-512-1).
func (h *Handler) disposition(w http.ResponseWriter, r *http.Request, toStatus string) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "grc_engineer (approver) role required to disposition a vendor claim")
		return
	}
	claimID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "component-claim id must be a uuid")
		return
	}
	var req dispositionReq
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req) // note is optional
	}
	updated, err := h.store.Disposition(ctx, claimID, toStatus, cred.ID, req.Note)
	if err != nil {
		if errors.Is(err, ErrClaimNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "component claim not found")
			return
		}
		httperr.WriteInternal(w, r, "oscalcomponents", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"id":               uuidString(updated.ID),
		"control_id":       updated.ControlID,
		"is_vendor_claim":  updated.IsVendorClaim,
		"claim_status":     updated.ClaimStatus,
		"dispositioned_by": updated.DispositionedBy,
		"dispositioned_at": tsPtr(updated.DispositionedAt),
		"disposition_note": updated.DispositionNote,
	})
}

// ===== authz (defense-in-depth) =====

// requireOscalRead is the handler-level read gate, the defense-in-depth twin
// of the OPA middleware (mirrors oscalprovenance.requireOscalRead). The read
// set is admin + grc_engineer (IsApprover) + control_owner (OwnerRoles).
func requireOscalRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasOscalRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant oscal-read access")
		return false
	}
	return true
}

func hasOscalRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// ===== helpers =====

func tenantContext(r *http.Request) (context.Context, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

func (h *Handler) tenantCredContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func tsString(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}

// tsPtr renders a nullable timestamp as a *string (RFC3339 UTC), or nil when
// NULL — so an un-dispositioned claim's dispositioned_at omits cleanly.
func tsPtr(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.UTC().Format(time.RFC3339)
	return &s
}
