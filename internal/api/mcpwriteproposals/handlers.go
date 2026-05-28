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
//   - POST /v1/mcp/write-proposals — any authenticated bearer (the MCP
//     server proxies the caller's bearer; gating happens at the MCP
//     tool layer + the operator's own role).
//   - confirm / reject — REQUIRE IsApprover or IsAdmin. Mirrors slice-021
//     exception approval (AC-3 segregation gate).
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
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
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
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !writeproposals.AllowedTools[req.ToolName] {
		writeError(w, http.StatusBadRequest, "unknown tool_name")
		return
	}
	if req.AIModelName == "" || req.AIModelVersion == "" {
		writeError(w, http.StatusBadRequest, "ai_model_name and ai_model_version are required")
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
	writeJSON(w, http.StatusCreated, map[string]any{"proposal": proposalWireFrom(created)})
}

// ListProposals handles GET /v1/mcp/write-proposals.
func (h *Handler) ListProposals(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter := writeproposals.ListFilter{
		State:    strings.TrimSpace(r.URL.Query().Get("state")),
		ToolName: strings.TrimSpace(r.URL.Query().Get("tool_name")),
	}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		writeServerErr(w, r, "list proposals", err)
		return
	}
	out := make([]proposalWire, len(rows))
	for i, p := range rows {
		out[i] = proposalWireFrom(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": out, "count": len(out)})
}

// GetProposal handles GET /v1/mcp/write-proposals/{id}.
func (h *Handler) GetProposal(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	p, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, writeproposals.ErrNotFound) {
			writeError(w, http.StatusNotFound, "proposal not found")
			return
		}
		writeServerErr(w, r, "get proposal", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposal": proposalWireFrom(p)})
}

// ConfirmProposal handles POST /v1/mcp/write-proposals/{id}/confirm.
func (h *Handler) ConfirmProposal(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "approver role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	confirmed, err := h.store.Confirm(ctx, id, cred.ID)
	if err != nil {
		h.writeTransitionErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposal": proposalWireFrom(confirmed)})
}

// RejectProposal handles POST /v1/mcp/write-proposals/{id}/reject.
func (h *Handler) RejectProposal(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "approver role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
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
	writeJSON(w, http.StatusOK, map[string]any{"proposal": proposalWireFrom(rejected)})
}

// ----- helpers -----

func (h *Handler) writeCreateErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, writeproposals.ErrUnknownTool),
		errors.Is(err, writeproposals.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, writeproposals.ErrPendingCapExceeded):
		writeError(w, http.StatusTooManyRequests, "pending proposal cap exceeded; approve or reject existing proposals before filing more")
	default:
		writeServerErr(w, r, "create proposal", err)
	}
}

func (h *Handler) writeTransitionErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, writeproposals.ErrNotFound):
		writeError(w, http.StatusNotFound, "proposal not found")
	case errors.Is(err, writeproposals.ErrWrongState):
		writeError(w, http.StatusConflict, "proposal not in ai_proposed state")
	case errors.Is(err, writeproposals.ErrUnknownTool):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeServerErr(w, r, "transition", err)
	}
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

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	httperr.WriteInternal(w, r, op, err)
}
