// Package admincrosswalkremap is the admin HTTP surface for crosswalk-edge
// remap proposals (#1777):
//
//	GET  /v1/admin/crosswalk-edge-remap-proposals?class=&status=
//	POST /v1/admin/crosswalk-edge-remap-proposals/{id}/approve      {"scf_id"?, "reason"?}
//	POST /v1/admin/crosswalk-edge-remap-proposals/{id}/reject       {"reason"?}
//	POST /v1/admin/crosswalk-edge-remap-proposals/{id}/keep-legacy  {"reason"?}
//	POST /v1/admin/crosswalk-edge-remap-proposals/approve-unchanged (no body)
//
// Every route requires a SUPER_ADMIN caller (the verified JWT's
// `atlas:super_admin` claim), the read included. fw_to_scf_edges rows are
// global catalog data shared by every tenant, so a tenant admin (including a
// per-tenant admin API key, which sets cred.IsAdmin) must not change them:
// that would be a cross-tenant write (#1757). The list is gated too: it is
// the review surface for those writes, and tenants read the resulting edges
// through coverage. policies/authz/super_admin.rego enforces the same rule
// in OPA; this handler is the second leg. On a single-tenant install the
// bootstrap admin holds super_admin (#1759).
//
// A decision is a person's: a super_admin claim always belongs to a user
// JWT, and the decider is recorded as credstore.PrincipalOf.
package admincrosswalkremap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/crosswalkremap"
)

const maxBody = 16 * 1024

// Store is the seam the handler reads and writes through; the
// crosswalkremap.Store satisfies it.
type Store interface {
	List(ctx context.Context, class, status string) ([]crosswalkremap.Proposal, error)
	Decide(ctx context.Context, in crosswalkremap.DecideInput) (crosswalkremap.Decision, error)
	BulkApproveUnchanged(ctx context.Context, decidedBy string) (crosswalkremap.BulkResult, error)
}

// Handler owns the edge remap routes.
type Handler struct {
	store Store
}

// New constructs a Handler.
func New(store Store) *Handler { return &Handler{store: store} }

// RegisterRoutes mounts the routes. The literal approve-unchanged segment is
// registered ahead of the {id}/... patterns.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/v1/admin/crosswalk-edge-remap-proposals", h.List)
	r.Post("/v1/admin/crosswalk-edge-remap-proposals/approve-unchanged", h.ApproveUnchanged)
	r.Post("/v1/admin/crosswalk-edge-remap-proposals/{id}/approve", h.Approve)
	r.Post("/v1/admin/crosswalk-edge-remap-proposals/{id}/reject", h.Reject)
	r.Post("/v1/admin/crosswalk-edge-remap-proposals/{id}/keep-legacy", h.KeepLegacy)
}

// ListResponse is the GET body.
type ListResponse struct {
	Proposals []crosswalkremap.Proposal `json:"proposals"`
}

// List handles GET /v1/admin/crosswalk-edge-remap-proposals.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireSuperAdmin(w, r); !ok {
		return
	}
	class := strings.TrimSpace(r.URL.Query().Get("class"))
	if class != "" && !slices.Contains(scfimport.RemapClasses, scfimport.RemapClass(class)) {
		httpresp.WriteError(w, http.StatusBadRequest, "class must be one of unchanged, renamed, split, reused_id, no_successor")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !slices.Contains(crosswalkremap.Statuses, status) {
		httpresp.WriteError(w, http.StatusBadRequest, "status must be one of open, approved, rejected, kept_legacy")
		return
	}
	ps, err := h.store.List(r.Context(), class, status)
	if err != nil {
		httperr.WriteInternal(w, r, "list crosswalk edge remap proposals", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, ListResponse{Proposals: ps})
}

type approveBody struct {
	SCFID  string `json:"scf_id"`
	Reason string `json:"reason"`
}

type closeBody struct {
	Reason string `json:"reason"`
}

// Approve handles POST .../{id}/approve.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	decider, ok := requireSuperAdmin(w, r)
	if !ok {
		return
	}
	var body approveBody
	if !decodeBody(w, r, &body) {
		return
	}
	h.decide(w, r, crosswalkremap.DecideInput{
		ProposalID: chi.URLParam(r, "id"), Decision: crosswalkremap.StatusApproved,
		SCFID: body.SCFID, Reason: body.Reason, DecidedBy: decider,
	})
}

// Reject handles POST .../{id}/reject.
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	h.close(w, r, crosswalkremap.StatusRejected)
}

// KeepLegacy handles POST .../{id}/keep-legacy.
func (h *Handler) KeepLegacy(w http.ResponseWriter, r *http.Request) {
	h.close(w, r, crosswalkremap.StatusKeptLegacy)
}

func (h *Handler) close(w http.ResponseWriter, r *http.Request, decision string) {
	decider, ok := requireSuperAdmin(w, r)
	if !ok {
		return
	}
	var body closeBody
	if !decodeBody(w, r, &body) {
		return
	}
	h.decide(w, r, crosswalkremap.DecideInput{
		ProposalID: chi.URLParam(r, "id"), Decision: decision, Reason: body.Reason, DecidedBy: decider,
	})
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, in crosswalkremap.DecideInput) {
	d, err := h.store.Decide(r.Context(), in)
	if err != nil {
		writeRemapError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, d)
}

// ApproveUnchanged handles POST .../approve-unchanged.
func (h *Handler) ApproveUnchanged(w http.ResponseWriter, r *http.Request) {
	decider, ok := requireSuperAdmin(w, r)
	if !ok {
		return
	}
	res, err := h.store.BulkApproveUnchanged(r.Context(), decider)
	if err != nil {
		writeRemapError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, res)
}

// requireSuperAdmin enforces the super_admin gate (#1757) and returns the
// deciding principal. A missing credential is 401; a caller whose verified
// JWT lacks the `atlas:super_admin` claim is 403, whatever its tenant role.
func requireSuperAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return "", false
	}
	if claims := jwtmw.FromContext(r.Context()); claims == nil || !claims.SuperAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "super_admin required")
		return "", false
	}
	return credstore.PrincipalOf(cred), true
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		httpresp.WriteError(w, http.StatusBadRequest, "body must be a JSON object: "+err.Error())
		return false
	}
	if dec.More() {
		httpresp.WriteError(w, http.StatusBadRequest, "body must be a single JSON object")
		return false
	}
	return true
}

func writeRemapError(w http.ResponseWriter, r *http.Request, err error) {
	msg := strings.TrimPrefix(err.Error(), "edge remap: ")
	switch {
	case errors.Is(err, crosswalkremap.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "edge remap proposal not found")
	case errors.Is(err, crosswalkremap.ErrDecided),
		errors.Is(err, crosswalkremap.ErrEdgeChanged),
		errors.Is(err, crosswalkremap.ErrEdgeExists),
		errors.Is(err, crosswalkremap.ErrBulkRunning):
		httpresp.WriteError(w, http.StatusConflict, msg)
	case errors.Is(err, crosswalkremap.ErrAnchorRequired),
		errors.Is(err, crosswalkremap.ErrAnchorNotInRelease),
		errors.Is(err, crosswalkremap.ErrReasonTooLong):
		httpresp.WriteError(w, http.StatusBadRequest, msg)
	case errors.Is(err, crosswalkremap.ErrDeciderRequired):
		httpresp.WriteError(w, http.StatusForbidden, msg)
	default:
		httperr.WriteInternal(w, r, "crosswalk edge remap decision", err)
	}
}
