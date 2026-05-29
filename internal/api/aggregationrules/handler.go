// Package aggregationrules serves the slice 054 declarative aggregation
// rules HTTP API (canvas Plans/canvas/06-risk.md §6.6).
//
// Routes (appended onto the platform root router by httpserver.go):
//
//	POST   /v1/aggregation-rules                create a rule (status=staged)
//	GET    /v1/aggregation-rules                list the tenant's rules
//	GET    /v1/aggregation-rules/{id}           get one rule
//	PATCH  /v1/aggregation-rules/{id}/activate    HITL: staged|inactive -> active
//	PATCH  /v1/aggregation-rules/{id}/deactivate  active -> inactive
//
// POST accepts BOTH application/json and application/yaml — the Content-Type
// header selects the parser, and either way the body is decoded into the
// same aggrule.Rule struct, validated against one schema, and persisted with
// the typed columns plus the canonical JSON rule body.
//
// Rules are ALWAYS created `staged`. They do not execute while staged. The
// PATCH .../activate route is the explicit human action — the HITL gate —
// that flips a rule to `active` and makes it live; every activation /
// deactivation writes an append-only aggregation_rule_audit_log row naming
// the actor.
package aggregationrules

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// maxRuleBodyBytes bounds the POST body — a rule document is tiny; anything
// larger is almost certainly an error or abuse.
const maxRuleBodyBytes = 64 * 1024

// Handler bundles the slice 054 routes over a single aggrule.Store.
type Handler struct {
	store *aggrule.Store
}

// New constructs a Handler.
func New(store *aggrule.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type ruleWire struct {
	ID               string `json:"id"`
	RuleID           string `json:"rule_id"`
	TargetTheme      string `json:"target_theme"`
	MinRisks         int    `json:"min_risks"`
	MinTeams         int    `json:"min_teams"`
	WindowDays       int    `json:"window_days"`
	ParentLevel      string `json:"parent_level"`
	SeverityFunction string `json:"severity_function"`
	TitleTemplate    string `json:"title_template"`
	Status           string `json:"status"`
	ActivatedBy      string `json:"activated_by,omitempty"`
	ActivatedAt      string `json:"activated_at,omitempty"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

func ruleWireFrom(sr aggrule.StoredRule) ruleWire {
	w := ruleWire{
		ID:               sr.ID.String(),
		RuleID:           sr.Rule.RuleID,
		TargetTheme:      sr.Rule.TargetTheme,
		MinRisks:         sr.Rule.MinRisks,
		MinTeams:         sr.Rule.MinTeams,
		WindowDays:       sr.Rule.WindowDays,
		ParentLevel:      sr.Rule.ParentLevel,
		SeverityFunction: sr.Rule.SeverityFunction,
		TitleTemplate:    sr.Rule.TitleTemplate,
		Status:           sr.Status,
		CreatedAt:        sr.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:        sr.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if sr.ActivatedBy != nil {
		w.ActivatedBy = *sr.ActivatedBy
	}
	if sr.ActivatedAt != nil {
		w.ActivatedAt = sr.ActivatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return w
}

// Create handles POST /v1/aggregation-rules. It accepts application/json or
// application/yaml, parses into the same Rule struct, validates, and
// persists as status=staged. Field-level validation failures return 400 with
// the full list of offending fields.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	format, ok := formatFromContentType(r.Header.Get("Content-Type"))
	if !ok {
		httpresp.WriteError(w, http.StatusUnsupportedMediaType,
			"Content-Type must be application/json or application/yaml")

		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRuleBodyBytes+1))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(body) > maxRuleBodyBytes {
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge, "rule document too large")
		return
	}

	rule, err := aggrule.ParseRule(body, format)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if verr := rule.Validate(); verr != nil {
		writeValidationError(w, verr)
		return
	}

	created, err := h.store.Create(r.Context(), rule, actorFrom(r))
	if err != nil {
		var ve *aggrule.ValidationError
		switch {
		case errors.As(err, &ve):
			writeValidationError(w, err)
		case errors.Is(err, aggrule.ErrDuplicateRuleID):
			httpresp.WriteError(w, http.StatusConflict, err.Error())
		default:
			httperr.WriteInternal(w, r, "create aggregation rule", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"rule": ruleWireFrom(created)})
}

// List handles GET /v1/aggregation-rules. Optional ?status= filter.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	statusFilter := r.URL.Query().Get("status")
	if statusFilter != "" && statusFilter != "staged" && statusFilter != "active" && statusFilter != "inactive" {
		httpresp.WriteError(w, http.StatusBadRequest, "status filter must be one of staged, active, inactive")
		return
	}
	rules, err := h.store.List(r.Context(), statusFilter)
	if err != nil {
		httperr.WriteInternal(w, r, "list aggregation rules", err)
		return
	}
	out := make([]ruleWire, len(rules))
	for i, sr := range rules {
		out[i] = ruleWireFrom(sr)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"rules": out, "count": len(out)})
}

// Get handles GET /v1/aggregation-rules/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	sr, err := h.store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, aggrule.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "aggregation rule not found")
			return
		}
		httperr.WriteInternal(w, r, "get aggregation rule", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"rule": ruleWireFrom(sr)})
}

// Activate handles PATCH /v1/aggregation-rules/{id}/activate — the HITL gate.
// It flips a staged (or inactive) rule to active and writes an audit-log row.
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, true)
}

// Deactivate handles PATCH /v1/aggregation-rules/{id}/deactivate — flips an
// active rule to inactive and writes an audit-log row.
func (h *Handler) Deactivate(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, false)
}

// transition is the shared activate/deactivate path: parse id, call the
// store's lifecycle method, map sentinels to status codes.
func (h *Handler) transition(w http.ResponseWriter, r *http.Request, activate bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	actor := actorFrom(r)

	var sr aggrule.StoredRule
	if activate {
		sr, err = h.store.Activate(r.Context(), id, actor)
	} else {
		sr, err = h.store.Deactivate(r.Context(), id, actor)
	}
	if err != nil {
		switch {
		case errors.Is(err, aggrule.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "aggregation rule not found")
		case errors.Is(err, aggrule.ErrWrongState):
			httpresp.WriteError(w, http.StatusConflict, err.Error())
		default:
			op := "activate aggregation rule"
			if !activate {
				op = "deactivate aggregation rule"
			}
			httperr.WriteInternal(w, r, op, err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"rule": ruleWireFrom(sr)})
}

// ----- helpers -----

// formatFromContentType maps a request Content-Type onto an aggrule.Format.
// The media-type parameter (e.g. "; charset=utf-8") is ignored.
func formatFromContentType(ct string) (aggrule.Format, bool) {
	mediaType := strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])
	switch strings.ToLower(mediaType) {
	case "application/json", "":
		// An empty Content-Type defaults to JSON — the common API client case.
		return aggrule.FormatJSON, true
	case "application/yaml", "application/x-yaml", "text/yaml":
		return aggrule.FormatYAML, true
	default:
		return aggrule.FormatJSON, false
	}
}

// actorFrom resolves the human (or service principal) performing the action
// for the audit log. The authenticated credential's subject is the actor;
// when no credential is in context (unit-test servers) it falls back to a
// non-empty sentinel so the audit-log actor-nonempty CHECK never trips.
func actorFrom(r *http.Request) string {
	if cred, ok := authctx.CredentialFromContext(r.Context()); ok && cred.ID != "" {
		return cred.ID
	}
	return "system"
}

// writeValidationError renders a *aggrule.ValidationError as a 400 with the
// full field-level error list, so the caller sees exactly which fields are
// wrong. Falls back to a flat error for any other error type.
func writeValidationError(w http.ResponseWriter, err error) {
	var ve *aggrule.ValidationError
	if errors.As(err, &ve) {
		httpresp.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "rule validation failed",
			"fields": ve.Errors,
		})

		return
	}
	httpresp.WriteError(w, http.StatusBadRequest, err.Error())
}
