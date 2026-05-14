// Package controlstate serves the slice-012 control state evaluation HTTP
// API. Routes (appended onto the platform root router by httpserver.go):
//
//	GET /v1/controls/{id}/state          AC-1: current evaluated state
//	GET /v1/controls/{id}/effectiveness  AC-6: rolling 30-day pass rate
//
// Both endpoints are pure reads over the control_evaluations ledger — a GET
// never triggers evaluation (the engine's NATS consumer + scheduler own
// that) and never has a write side-effect. The handlers run with the tenant
// set by upstream auth middleware; the eval.Engine opens its own per-call
// transaction and applies the tenant GUC so RLS is enforced.
package controlstate

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-012 read routes over a single eval.Engine.
type Handler struct {
	engine *eval.Engine
}

// New constructs a Handler.
func New(engine *eval.Engine) *Handler { return &Handler{engine: engine} }

// ----- wire shapes -----

// stateWire is the AC-1 response shape for one (control, scope_cell).
type stateWire struct {
	ScopeCellID           *string `json:"scope_cell_id"`
	Result                string  `json:"result"`
	FreshnessStatus       string  `json:"freshness_status"`
	EvidenceCountInWindow int     `json:"evidence_count_in_window"`
	LastObservedAt        *string `json:"last_observed_at"`
	EvaluatedAt           string  `json:"evaluated_at"`
	FreshnessClass        string  `json:"freshness_class"`
	Trigger               string  `json:"trigger"`
}

func stateWireFrom(s eval.State) stateWire {
	w := stateWire{
		Result:                s.Result,
		FreshnessStatus:       s.FreshnessStatus,
		EvidenceCountInWindow: s.EvidenceCountInWindow,
		FreshnessClass:        s.FreshnessClass,
		Trigger:               s.Trigger,
		EvaluatedAt:           s.EvaluatedAt.UTC().Format(time.RFC3339Nano),
	}
	if s.ScopeCellID != nil {
		id := s.ScopeCellID.String()
		w.ScopeCellID = &id
	}
	if s.LastObservedAt != nil {
		t := s.LastObservedAt.UTC().Format(time.RFC3339Nano)
		w.LastObservedAt = &t
	}
	return w
}

// State handles GET /v1/controls/{id}/state.
//
// Query params:
//   - ?scope=<predicate>  slice-017 JSON-AST predicate; narrows the result
//     to the cells the predicate resolves to. Omit for all cells.
//   - ?as-of=<RFC3339>    point-in-time horizon; the latest evaluation row
//     at or before this instant is returned. Omit for live (latest) state.
//
// The AC-1 contract: the response carries {result, evidence_count_in_window,
// freshness_status, last_observed_at} per scope cell.
func (h *Handler) State(w http.ResponseWriter, r *http.Request) {
	ctx, ok := tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}

	scopePredicate := r.URL.Query().Get("scope")
	asOf := eval.FarFuture
	if raw := r.URL.Query().Get("as-of"); raw != "" {
		ts, perr := time.Parse(time.RFC3339, raw)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "as-of must be an RFC3339 timestamp")
			return
		}
		asOf = ts.UTC()
	}

	states, err := h.engine.ControlState(ctx, controlID, scopePredicate, asOf)
	if err != nil {
		writeStateErr(w, err)
		return
	}

	out := make([]stateWire, len(states))
	for i, s := range states {
		out[i] = stateWireFrom(s)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"control_id": controlID.String(),
		"states":     out,
		"count":      len(out),
	})
}

// effectivenessWire is the AC-6 response shape.
type effectivenessWire struct {
	ControlID   string  `json:"control_id"`
	PassRate    float64 `json:"pass_rate"`
	PassCount   int     `json:"pass_count"`
	TotalCount  int     `json:"total_count"`
	WindowStart string  `json:"window_start"`
	WindowEnd   string  `json:"window_end"`
}

// Effectiveness handles GET /v1/controls/{id}/effectiveness — AC-6's rolling
// 30-day pass rate (the canvas §6.2 operational_score that slice 020's risk
// residual derivation consumes).
func (h *Handler) Effectiveness(w http.ResponseWriter, r *http.Request) {
	ctx, ok := tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}

	eff, err := h.engine.Effectiveness(ctx, controlID)
	if err != nil {
		writeStateErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, effectivenessWire{
		ControlID:   eff.ControlID.String(),
		PassRate:    eff.PassRate,
		PassCount:   eff.PassCount,
		TotalCount:  eff.TotalCount,
		WindowStart: eff.WindowStart.UTC().Format(time.RFC3339Nano),
		WindowEnd:   eff.WindowEnd.UTC().Format(time.RFC3339Nano),
	})
}

// ----- helpers -----

// tenantContext confirms the upstream tenancy middleware lifted a tenant id
// onto the request context (slice 033). Absent it, the request is
// unauthenticated.
func tenantContext(r *http.Request) (context.Context, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

// writeStateErr maps an engine error to an HTTP status. ErrControlNotFound /
// pgx.ErrNoRows → 404; a malformed scope predicate → 400; everything else →
// 500.
func writeStateErr(w http.ResponseWriter, err error) {
	switch {
	case eval.IsNotFound(err):
		writeError(w, http.StatusNotFound, "control not found")
	case eval.IsBadScopePredicate(err):
		writeError(w, http.StatusBadRequest, "scope predicate is malformed: "+err.Error())
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
