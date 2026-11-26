// Package mcpwriteproposals serves the slice-173 HTTP API for the MCP
// write-proposals approval flow. Routes (registered onto the platform
// root router by internal/api/httpserver.go):
//
//	POST   /v1/mcp/write-proposals              create draft (called by MCP write tools)
//	GET    /v1/mcp/write-proposals              list (operator approval queue)
//	GET    /v1/mcp/write-proposals/{id}         get one
//	POST   /v1/mcp/write-proposals/{id}/confirm operator approves; applier runs
//	POST   /v1/mcp/write-proposals/{id}/reject  operator rejects
//
// AuthZ:
//   - POST /v1/mcp/write-proposals — admin or grc_engineer, as OPA admits
//     (#3031). The MCP server proxies the caller's bearer, so the
//     operator's own role decides.
//   - confirm / reject — REQUIRE a tenant admin (authz.IsTenantAdmin, the
//     role set OPA admits on these two routes, #2428; grc_engineer files
//     but does not decide, #3031). Mirrors slice-021 exception approval
//     (AC-3 segregation gate). The proposer may confirm its own proposal:
//     the AI is the proposer, the bearer's holder the confirmer
//     (docs/audit-log/3031-grc-engineer-mcp-proposals-decisions.md D3).
//   - list / get — any authenticated bearer; RLS scopes to caller's tenant.
//
// Wire shapes mirror the store layer 1:1; payload_json equivalents
// (tool_input) ARE included in responses because they describe the
// proposed write (operator needs them to decide approve/reject). Slice
// 138 / slice 145 payload exclusions DO NOT apply here — tool_input is
// the bounded shape of each write tool's documented schema, not
// arbitrary evidence content (P0-A7).

package mcpwriteproposals

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
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-173 routes over a single writeproposals.Store.
type Handler struct {
	store *writeproposals.Store
}

// New constructs a Handler.
func New(store *writeproposals.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type createReq struct {
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	AIModelName    string          `json:"ai_model_name"`
	AIModelVersion string          `json:"ai_model_version"`
}

type rejectReq struct {
	Reason string `json:"reason"`
}

type proposalWire struct {
	ID             string          `json:"id"`
	TenantID       string          `json:"tenant_id"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	State          string          `json:"state"`
	AIAssisted     bool            `json:"ai_assisted"`
	AIModelName    string          `json:"ai_model_name"`
	AIModelVersion string          `json:"ai_model_version"`
	HumanApproved  bool            `json:"human_approved"`
	HumanApprover  *string         `json:"human_approver,omitempty"`
	AppliedAt      *time.Time      `json:"applied_at,omitempty"`
	AppliedSubject *string         `json:"applied_subject,omitempty"`
	RejectedAt     *time.Time      `json:"rejected_at,omitempty"`
	RejectReason   *string         `json:"reject_reason,omitempty"`
	CreatedBy      string          `json:"created_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func proposalWireFrom(p writeproposals.Proposal) proposalWire {
	out := proposalWire{
		ID:             p.ID.String(),
		TenantID:       p.TenantID.String(),
		ToolName:       p.ToolName,
		ToolInput:      p.ToolInput,
		State:          p.State,
		AIAssisted:     p.AIAssisted,
		AIModelName:    p.AIModelName,
		AIModelVersion: p.AIModelVersion,
		HumanApproved:  p.HumanApproved,
		HumanApprover:  p.HumanApprover,
		AppliedAt:      p.AppliedAt,
		AppliedSubject: p.AppliedSubject,
		RejectedAt:     p.RejectedAt,
		RejectReason:   p.RejectReason,
		CreatedBy:      p.CreatedBy,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}
	if len(out.ToolInput) == 0 {
		out.ToolInput = json.RawMessage("{}")
	}
	return out
}

// CreateProposal handles POST /v1/mcp/write-proposals.
//
// The MCP write tools dispatch through this endpoint. The bearer token's
// credential id is captured as `created_by` so the audit trail attributes
// the proposal to the operator who supplied the token to the MCP server.
func (h *Handler) CreateProposal(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !writeproposals.AllowedTools[req.ToolName] {
		httpresp.WriteError(w, http.StatusBadRequest, "unknown tool_name")
		return
	}
	if req.AIModelName == "" || req.AIModelVersion == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "ai_model_name and ai_model_version are required")
		return
	}
	created, err := h.store.Create(ctx, writeproposals.CreateInput{
		ToolName:       req.ToolName,
		ToolInput:      req.ToolInput,
		AIModelName:    req.AIModelName,
		AIModelVersion: req.AIModelVersion,
		CreatedBy:      cred.ID,
	})
	if err != nil {
		h.writeCreateErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"proposal": proposalWireFrom(created)})
}

// ListProposals handles GET /v1/mcp/write-proposals.
func (h *Handler) ListProposals(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter := writeproposals.ListFilter{
		State:    strings.TrimSpace(r.URL.Query().Get("state")),
		ToolName: strings.TrimSpace(r.URL.Query().Get("tool_name")),
	}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		httperr.WriteInternal(w, r, "list proposals", err)
		return
	}
	out := make([]proposalWire, len(rows))
	for i, p := range rows {
		out[i] = proposalWireFrom(p)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"proposals": out, "count": len(out)})
}

// GetProposal handles GET /v1/mcp/write-proposals/{id}.
func (h *Handler) GetProposal(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	p, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, writeproposals.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "proposal not found")
			return
		}
		httperr.WriteInternal(w, r, "get proposal", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"proposal": proposalWireFrom(p)})
}

// ConfirmProposal handles POST /v1/mcp/write-proposals/{id}/confirm.
func (h *Handler) ConfirmProposal(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !authz.IsTenantAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	// The confirming person is handed to the applier, so a confirmed
	// acceptance approval runs the approval rule against them (#2105). The
	// principal is the actor the applier records, as REST records it
	// (#2371).
	confirmed, err := h.store.ConfirmAs(ctx, id, cred.ID, writeproposals.Confirmer{
		Approver: approval.Approver{
			UserID:       credstore.MinterUserID(cred),
			CredentialID: cred.ID,
		},
		Principal: credstore.PrincipalOf(cred),
	})
	if err != nil {
		h.writeTransitionErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"proposal": proposalWireFrom(confirmed)})
}

// RejectProposal handles POST /v1/mcp/write-proposals/{id}/reject.
func (h *Handler) RejectProposal(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !authz.IsTenantAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req rejectReq
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	rejected, err := h.store.Reject(ctx, id, req.Reason)
	if err != nil {
		h.writeTransitionErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"proposal": proposalWireFrom(rejected)})
}

// ----- helpers -----

func (h *Handler) writeCreateErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, writeproposals.ErrUnknownTool),
		errors.Is(err, writeproposals.ErrInvalidInput):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, writeproposals.ErrPendingCapExceeded):
		httpresp.WriteError(w, http.StatusTooManyRequests, "pending proposal cap exceeded; approve or reject existing proposals before filing more")
	default:
		// A create_risk score refusal (#2502) takes the status and code
		// it takes on confirm, which are POST /v1/risks' statuses.
		if status, code, ok := scoreErrStatus(err); ok {
			httpresp.WriteJSON(w, status, transitionErrBody{Error: err.Error(), Code: code})
			return
		}
		httperr.WriteInternal(w, r, "create proposal", err)
	}
}

func (h *Handler) writeTransitionErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, writeproposals.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "proposal not found")
	case errors.Is(err, writeproposals.ErrWrongState):
		httpresp.WriteError(w, http.StatusConflict, "proposal not in ai_proposed state")
	case errors.Is(err, writeproposals.ErrUnknownTool):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, writeproposals.ErrModuleOff):
		// #2175: the proposal stays pending until its module is back on.
		httpresp.WriteJSON(w, http.StatusConflict, transitionErrBody{
			Error: "The module this proposal writes into is switched off. It stays pending; switch the module on in Admin > Features to apply it.",
			Code:  "module_switched_off",
		})
	case errors.Is(err, featureflag.ErrStoreUnavailable):
		// #2575: the module switch could not be read. The proposal stays
		// pending rather than apply into a module that may be off.
		httperr.WriteCodedStatus(w, r, http.StatusServiceUnavailable, "module switch", err, "module_state_unavailable")
	default:
		if status, code, ok := applyErrStatus(err); ok {
			if status >= http.StatusInternalServerError {
				// CWE-209: a 5xx body never carries the wrapped error.
				httpresp.WriteJSON(w, status, transitionErrBody{Error: "evidence ingest unavailable", Code: code})
				return
			}
			httpresp.WriteJSON(w, status, transitionErrBody{Error: err.Error(), Code: code})
			return
		}
		httperr.WriteInternal(w, r, "transition", err)
	}
}

// transitionErrBody is the error shape for a confirm refused by the
// applier. Code is stable; clients route on it, not on Error.
type transitionErrBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// applyErrStatus maps an applier refusal on confirm to a status and a
// stable code. The evidence appliers return ingest's sentinels (#1751), so
// the codes match the REST push surface (internal/api/evidence/http.go);
// an unknown kind is a 400 here because the proposal's input is at fault,
// not a precondition the caller can fix by registering a schema first.
// The risk appliers' treatment-validation refusal carries the code
// POST /v1/risks uses (internal/api/risks/handlers.go, #1844). The
// hierarchy-link appliers' refusals take the statuses
// POST /v1/risks/{id}/children returns (#1748). The treatment applier's
// transition refusals take the statuses the #2105 transition routes return.
func applyErrStatus(err error) (int, string, bool) {
	if status, code, ok := scoreErrStatus(err); ok {
		return status, code, true
	}
	switch {
	case risk.IsTreatmentValidation(err):
		return http.StatusBadRequest, "treatment_validation_failed", true
	case risk.IsTransitionInput(err):
		return http.StatusBadRequest, "invalid_input", true
	case errors.Is(err, risk.ErrTransitionState):
		return http.StatusConflict, "transition_not_allowed", true
	case risk.IsApprovalRefusal(err):
		return http.StatusForbidden, "approval_refused", true
	case errors.Is(err, risk.ErrAggregationCycle):
		return http.StatusConflict, "hierarchy_cycle", true
	case risk.IsChildLinkValidation(err):
		return http.StatusBadRequest, "hierarchy_validation_failed", true
	case errors.Is(err, risk.ErrNotFound), errors.Is(err, risk.ErrChildrenNotFound):
		return http.StatusNotFound, "risk_not_found", true
	case errors.Is(err, risk.ErrLinkNotFound):
		return http.StatusNotFound, "link_not_found", true
	case errors.Is(err, ingest.ErrObservedAtSkew):
		return http.StatusBadRequest, "observed_at_skew", true
	case errors.Is(err, ingest.ErrValidation):
		return http.StatusBadRequest, "schema_validation_failed", true
	case errors.Is(err, ingest.ErrUnknownKind):
		return http.StatusBadRequest, "unknown_evidence_kind", true
	case errors.Is(err, ingest.ErrMissingField):
		return http.StatusBadRequest, "missing_field", true
	case errors.Is(err, ingest.ErrOversized):
		return http.StatusRequestEntityTooLarge, "oversized_payload", true
	case errors.Is(err, ingest.ErrScopeViolation):
		return http.StatusForbidden, "scope_violation", true
	case errors.Is(err, ingest.ErrIdempotencyMismatch):
		return http.StatusConflict, "idempotency_mismatch", true
	case errors.Is(err, ingest.ErrConcurrentFreeze):
		// #3060: Process retried and a freeze refused every attempt;
		// nothing was written, and a retry succeeds.
		return http.StatusConflict, httpresp.CodeConcurrentFreeze, true
	case errors.Is(err, writeproposals.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_input", true
	case errors.Is(err, writeproposals.ErrIngestUnavailable):
		return http.StatusServiceUnavailable, "ingest_unavailable", true
	}
	return 0, "", false
}

// scoreErrStatus maps a create_risk score refusal to the status
// POST /v1/risks returns for it (internal/api/risks/handlers.go): 422 for a
// score field that is not a number in range, 400 for an unknown
// methodology or a score that fails its methodology's schema (#2502).
func scoreErrStatus(err error) (int, string, bool) {
	switch {
	case errors.Is(err, risk.ErrScoreNotNumeric):
		return http.StatusUnprocessableEntity, "score_validation_failed", true
	case errors.Is(err, risk.ErrInvalidMethodology),
		errors.Is(err, risk.ErrInherentScoreInvalid):
		return http.StatusBadRequest, "score_validation_failed", true
	}
	return 0, "", false
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
