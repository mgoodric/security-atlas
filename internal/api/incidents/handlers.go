// Package incidents serves the minimal OE-631 incident register HTTP API.
package incidents

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
	"github.com/mgoodric/security-atlas/internal/incident"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type Handler struct {
	store *incident.Store
}

func New(store *incident.Store) *Handler { return &Handler{store: store} }

type createReq struct {
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	Severity           string          `json:"severity"`
	AffectedSystemTier *string         `json:"affected_system_tier"`
	AffectedSystems    json.RawMessage `json:"affected_systems"`
	DetectedAt         *time.Time      `json:"detected_at"`
	ControlIDs         []string        `json:"control_ids"`
	RiskIDs            []string        `json:"risk_ids"`
	VendorIDs          []string        `json:"vendor_ids"`
	EvidenceIDs        []string        `json:"evidence_ids"`
}

type transitionReq struct {
	ToState string `json:"to_state"`
	Summary string `json:"summary"`
}

type closeReq struct {
	PostmortemSummary string     `json:"postmortem_summary"`
	ObservedAt        *time.Time `json:"observed_at"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
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
	ids, ok := parseLinkIDs(w, req)
	if !ok {
		return
	}
	detectedAt := time.Time{}
	if req.DetectedAt != nil {
		detectedAt = req.DetectedAt.UTC()
	}
	detail, err := h.store.Create(ctx, incident.CreateInput{
		Title:              req.Title,
		Description:        req.Description,
		OperatorSeverity:   req.Severity,
		AffectedSystemTier: req.AffectedSystemTier,
		AffectedSystems:    []byte(req.AffectedSystems),
		DetectedBy:         cred.ID,
		DetectedAt:         detectedAt,
		ControlIDs:         ids.ControlIDs,
		RiskIDs:            ids.RiskIDs,
		VendorIDs:          ids.VendorIDs,
		EvidenceIDs:        ids.EvidenceIDs,
	})
	if err != nil {
		h.writeErr(w, r, "create incident", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"incident": detailWireFrom(detail)})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	rows, err := h.store.List(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "list incidents", err)
		return
	}
	out := make([]incidentWire, len(rows))
	for i, inc := range rows {
		out[i] = incidentWireFrom(inc)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"incidents": out, "count": len(out)})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	detail, err := h.store.Get(ctx, id)
	if err != nil {
		h.writeErr(w, r, "get incident", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"incident": detailWireFrom(detail)})
}

func (h *Handler) Transition(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var req transitionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	inc, err := h.store.Transition(ctx, id, req.ToState, cred.ID, req.Summary)
	if err != nil {
		h.writeErr(w, r, "transition incident", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"incident": incidentWireFrom(inc)})
}

func (h *Handler) Close(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var req closeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	observedAt := time.Time{}
	if req.ObservedAt != nil {
		observedAt = req.ObservedAt.UTC()
	}
	detail, err := h.store.Close(ctx, id, incident.CloseInput{
		Actor:             cred.ID,
		PostmortemSummary: req.PostmortemSummary,
		ObservedAt:        observedAt,
	})
	if err != nil {
		h.writeErr(w, r, "close incident", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"incident": detailWireFrom(detail)})
}

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, incident.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "incident not found")
	case errors.Is(err, incident.ErrWrongState):
		httpresp.WriteError(w, http.StatusConflict, "incident not in expected state for this transition")
	case errors.Is(err, incident.ErrTitleRequired),
		errors.Is(err, incident.ErrActorRequired),
		errors.Is(err, incident.ErrSeverityInvalid),
		errors.Is(err, incident.ErrAffectedSystemsInvalid),
		errors.Is(err, incident.ErrPostmortemRequired),
		errors.Is(err, incident.ErrIRControlRequired):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		if strings.Contains(err.Error(), "target does not exist in tenant") {
			httpresp.WriteError(w, http.StatusBadRequest, "linked target does not exist in tenant")
			return
		}
		httperr.WriteInternal(w, r, op, err)
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

func parsePathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

type parsedLinks struct {
	ControlIDs  []uuid.UUID
	RiskIDs     []uuid.UUID
	VendorIDs   []uuid.UUID
	EvidenceIDs []uuid.UUID
}

func parseLinkIDs(w http.ResponseWriter, req createReq) (parsedLinks, bool) {
	var out parsedLinks
	var ok bool
	if out.ControlIDs, ok = parseUUIDs(w, "control_ids", req.ControlIDs); !ok {
		return out, false
	}
	if out.RiskIDs, ok = parseUUIDs(w, "risk_ids", req.RiskIDs); !ok {
		return out, false
	}
	if out.VendorIDs, ok = parseUUIDs(w, "vendor_ids", req.VendorIDs); !ok {
		return out, false
	}
	if out.EvidenceIDs, ok = parseUUIDs(w, "evidence_ids", req.EvidenceIDs); !ok {
		return out, false
	}
	return out, true
}

func parseUUIDs(w http.ResponseWriter, field string, vals []string) ([]uuid.UUID, bool) {
	out := make([]uuid.UUID, len(vals))
	for i, raw := range vals {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, field+" must contain UUIDs")
			return nil, false
		}
		out[i] = id
	}
	return out, true
}

type incidentWire struct {
	ID                 string          `json:"id"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	Status             string          `json:"status"`
	OperatorSeverity   string          `json:"operator_severity"`
	Severity           string          `json:"severity"`
	AffectedSystemTier *string         `json:"affected_system_tier,omitempty"`
	AffectedSystems    json.RawMessage `json:"affected_systems"`
	DetectedBy         string          `json:"detected_by"`
	DetectedAt         time.Time       `json:"detected_at"`
	ClosedBy           *string         `json:"closed_by,omitempty"`
	ClosedAt           *time.Time      `json:"closed_at,omitempty"`
	PostmortemSummary  *string         `json:"postmortem_summary,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type detailWire struct {
	Incident incidentWire        `json:"record"`
	Links    incident.Links      `json:"links"`
	Timeline []timelineEntryWire `json:"timeline"`
}

type timelineEntryWire struct {
	ID         string          `json:"id"`
	Action     string          `json:"action"`
	Actor      string          `json:"actor"`
	FromState  *string         `json:"from_state,omitempty"`
	ToState    string          `json:"to_state"`
	Summary    string          `json:"summary"`
	Detail     json.RawMessage `json:"detail"`
	OccurredAt time.Time       `json:"occurred_at"`
}

func detailWireFrom(d incident.Detail) detailWire {
	out := detailWire{
		Incident: incidentWireFrom(d.Incident),
		Links:    d.Links,
		Timeline: make([]timelineEntryWire, len(d.Timeline)),
	}
	for i, e := range d.Timeline {
		out.Timeline[i] = timelineEntryWire{
			ID:         e.ID.String(),
			Action:     e.Action,
			Actor:      e.Actor,
			FromState:  e.FromState,
			ToState:    e.ToState,
			Summary:    e.Summary,
			Detail:     jsonRaw(e.Detail),
			OccurredAt: e.OccurredAt,
		}
	}
	return out
}

func incidentWireFrom(inc incident.Incident) incidentWire {
	return incidentWire{
		ID:                 inc.ID.String(),
		Title:              inc.Title,
		Description:        inc.Description,
		Status:             inc.Status,
		OperatorSeverity:   inc.OperatorSeverity,
		Severity:           inc.Severity,
		AffectedSystemTier: inc.AffectedSystemTier,
		AffectedSystems:    jsonRaw(inc.AffectedSystems),
		DetectedBy:         inc.DetectedBy,
		DetectedAt:         inc.DetectedAt,
		ClosedBy:           inc.ClosedBy,
		ClosedAt:           inc.ClosedAt,
		PostmortemSummary:  inc.PostmortemSummary,
		CreatedAt:          inc.CreatedAt,
		UpdatedAt:          inc.UpdatedAt,
	}
}

func jsonRaw(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}
