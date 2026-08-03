// Package personnelsecurity serves the OE-663 HTTP API over the OE-630
// personnel-security checklist store (internal/personnelsecurity). Routes
// (registered onto the platform root router by
// internal/api/register_personnelsecurity.go):
//
//	GET  /v1/personnel-security/checklists                     list (?kind= ?status= ?overdue=true filters)
//	POST /v1/personnel-security/checklists                     create a manual checklist
//	GET  /v1/personnel-security/checklists/{id}                one checklist + its items
//	POST /v1/personnel-security/checklist-items/{id}/complete  complete an item (writes CC1 evidence)
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx). Writes go through the OE-630 store unchanged —
// completing an item is what emits the personnel_security.workflow.v1
// evidence record (invariant #9: manual evidence is first-class). The
// list read goes through the package-local ReadStore (store.go) so the
// OE-630 store's surface stays untouched (the internal/api/calendar
// precedent for API-owned reads over another slice's tables).
//
// This surface is evidence + task tracking ONLY: no route provisions or
// deprovisions access anywhere (the OE-630 boundary).
package personnelsecurity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/personnelsecurity"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the OE-663 routes over the OE-630 store plus the
// package-local list ReadStore.
type Handler struct {
	store  *personnelsecurity.Store
	reader *ReadStore
	// clock is injectable so integration tests can pin "now" for the
	// ?overdue=true filter. Production uses time.Now (UTC).
	clock func() time.Time
}

// New constructs a Handler over the OE-630 store and the package-local
// ReadStore.
func New(store *personnelsecurity.Store, reader *ReadStore) *Handler {
	return &Handler{store: store, reader: reader, clock: func() time.Time { return time.Now().UTC() }}
}

// WithClock returns a copy of the handler with an injected clock. Test
// seam only — mirrors personnelsecurity.Store.WithClock.
func (h *Handler) WithClock(clock func() time.Time) *Handler {
	copy := *h
	if clock != nil {
		copy.clock = clock
	}
	return &copy
}

// ----- wire shapes -----

type itemWire struct {
	ID               string     `json:"id"`
	ChecklistID      string     `json:"checklist_id"`
	Slug             string     `json:"slug"`
	Title            string     `json:"title"`
	Category         string     `json:"category"`
	SortOrder        int32      `json:"sort_order"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	CompletedBy      string     `json:"completed_by"`
	EvidenceRecordID *string    `json:"evidence_record_id,omitempty"`
	EvidenceURI      string     `json:"evidence_uri"`
	Notes            string     `json:"notes"`
}

type checklistWire struct {
	ID                string    `json:"id"`
	Kind              string    `json:"kind"`
	Source            string    `json:"source"`
	SourceEventID     string    `json:"source_event_id"`
	PersonExternalID  string    `json:"person_external_id"`
	PersonWorkEmail   string    `json:"person_work_email"`
	PersonDisplayName string    `json:"person_display_name"`
	ControlID         *string   `json:"control_id,omitempty"`
	DueAt             time.Time `json:"due_at"`
	Status            string    `json:"status"`
	// Items is always an array (never null) on the get-one path; the
	// list path returns checklists without items ([] on the wire) so
	// the index read stays one query.
	Items []itemWire `json:"items"`
}

type createReq struct {
	Kind              string     `json:"kind"`
	PersonExternalID  string     `json:"person_external_id"`
	PersonWorkEmail   string     `json:"person_work_email"`
	PersonDisplayName string     `json:"person_display_name"`
	ControlID         string     `json:"control_id"` // optional UUID
	DueAt             *time.Time `json:"due_at,omitempty"`
	// CreatedBy defaults to the caller's credential UserID when omitted
	// so the recorded actor is the authenticated principal, not a
	// caller-spoofable string.
	CreatedBy string `json:"created_by"`
}

type completeReq struct {
	// CompletedBy defaults to the caller's credential UserID when
	// omitted — same posture as createReq.CreatedBy.
	CompletedBy string `json:"completed_by"`
	EvidenceURI string `json:"evidence_uri"`
	Notes       string `json:"notes"`
}

// ----- handlers -----

// ListChecklists handles GET /v1/personnel-security/checklists.
//
// Filters (all optional, all compose):
//
//	?kind=onboarding|offboarding
//	?status=open|completed
//	?overdue=true    — open checklists whose due_at has passed
func (h *Handler) ListChecklists(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter, err := parseListFilter(r.URL.Query().Get("kind"), r.URL.Query().Get("status"), r.URL.Query().Get("overdue"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter.Now = h.clock()
	checklists, err := h.reader.List(ctx, filter)
	if err != nil {
		httperr.WriteInternal(w, r, "list personnel checklists", err)
		return
	}
	out := make([]checklistWire, len(checklists))
	for i, c := range checklists {
		out[i] = checklistWireFrom(c)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"checklists": out, "count": len(out)})
}

// GetChecklist handles GET /v1/personnel-security/checklists/{id} —
// one checklist with its items. A checklist belonging to another tenant
// is indistinguishable from a missing one (404) — RLS plus the
// tenant-scoped query never surface the other tenant's row.
func (h *Handler) GetChecklist(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	checklist, err := h.store.Load(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpresp.WriteError(w, http.StatusNotFound, "checklist not found")
			return
		}
		httperr.WriteInternal(w, r, "get personnel checklist", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"checklist": checklistWireFrom(checklist)})
}

// CreateChecklist handles POST /v1/personnel-security/checklists —
// manual checklist creation (source=manual). HRIS-triggered creation
// stays connector-side (store.HandleWorkerEvent); it is deliberately
// NOT exposed over HTTP.
func (h *Handler) CreateChecklist(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	in, err := manualInputFrom(req, callerID(r))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := h.store.CreateManual(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, personnelsecurity.ErrInvalidWorkflow),
			errors.Is(err, personnelsecurity.ErrPersonRequired):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			httperr.WriteInternal(w, r, "create personnel checklist", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"checklist": checklistWireFrom(created)})
}

// CompleteItem handles POST /v1/personnel-security/checklist-items/{id}/complete.
// The store writes the personnel_security.workflow.v1 evidence record
// and the completion columns in one transaction; an already-completed
// or cross-tenant item is a 404 (store ErrItemNotFound — deliberately
// not distinguishable, so the route leaks nothing across tenants).
func (h *Handler) CompleteItem(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req completeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	completedBy := strings.TrimSpace(req.CompletedBy)
	if completedBy == "" {
		completedBy = callerID(r)
	}
	item, err := h.store.CompleteItem(ctx, personnelsecurity.Completion{
		ItemID:      id,
		CompletedBy: completedBy,
		EvidenceURI: req.EvidenceURI,
		Notes:       req.Notes,
	})
	if err != nil {
		switch {
		case errors.Is(err, personnelsecurity.ErrItemNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "checklist item not found")
		case errors.Is(err, personnelsecurity.ErrCompleterRequired):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			httperr.WriteInternal(w, r, "complete personnel checklist item", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"item": itemWireFrom(item)})
}

// ----- helpers -----

func (h *Handler) tenantContext(r *http.Request) (context.Context, bool) {
	// Slice 033 pattern: the tenancy middleware already lifted the
	// credential's TenantID onto r.Context(); confirming it doubles as
	// a credential-presence check.
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

// callerID returns the authenticated credential's UserID (empty when no
// credential is in context — the upstream middleware rejects that path
// before any handler runs).
func callerID(r *http.Request) string {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		return ""
	}
	return cred.UserID
}

// parseListFilter validates the three optional query filters. A
// present-but-unknown value is a 400 rather than a silently-ignored
// filter (the slice-067 ?org_unit= posture).
func parseListFilter(kind, status, overdue string) (ListFilter, error) {
	var f ListFilter
	switch kind {
	case "":
	case string(personnelsecurity.WorkflowOnboarding), string(personnelsecurity.WorkflowOffboarding):
		f.Kind = kind
	default:
		return ListFilter{}, errors.New("kind must be onboarding or offboarding")
	}
	switch status {
	case "", "open", "completed":
		f.Status = status
	default:
		return ListFilter{}, errors.New("status must be open or completed")
	}
	switch overdue {
	case "", "false":
	case "true":
		f.OverdueOnly = true
	default:
		return ListFilter{}, errors.New("overdue must be true or false")
	}
	return f, nil
}

// manualInputFrom maps the create wire shape onto the OE-630 store
// input. Kind and person validation stay in the store (normalizeManualInput)
// so the HTTP surface cannot drift from the store's invariants; only the
// wire-level concerns (UUID parse, actor default) live here.
func manualInputFrom(req createReq, caller string) (personnelsecurity.ManualInput, error) {
	in := personnelsecurity.ManualInput{
		Kind:              personnelsecurity.WorkflowKind(req.Kind),
		PersonExternalID:  req.PersonExternalID,
		PersonWorkEmail:   req.PersonWorkEmail,
		PersonDisplayName: req.PersonDisplayName,
		CreatedBy:         strings.TrimSpace(req.CreatedBy),
	}
	if in.CreatedBy == "" {
		in.CreatedBy = caller
	}
	if req.DueAt != nil {
		in.DueAt = req.DueAt.UTC()
	}
	if strings.TrimSpace(req.ControlID) != "" {
		id, err := uuid.Parse(req.ControlID)
		if err != nil {
			return personnelsecurity.ManualInput{}, errors.New("control_id must be a UUID")
		}
		in.ControlID = id
	}
	return in, nil
}

func checklistWireFrom(c personnelsecurity.Checklist) checklistWire {
	items := make([]itemWire, len(c.Items))
	for i, item := range c.Items {
		items[i] = itemWireFrom(item)
	}
	w := checklistWire{
		ID:                c.ID.String(),
		Kind:              string(c.Kind),
		Source:            string(c.Source),
		SourceEventID:     c.SourceEventID,
		PersonExternalID:  c.PersonExternalID,
		PersonWorkEmail:   c.PersonWorkEmail,
		PersonDisplayName: c.PersonDisplayName,
		DueAt:             c.DueAt.UTC(),
		Status:            c.Status,
		Items:             items,
	}
	if c.ControlID != uuid.Nil {
		s := c.ControlID.String()
		w.ControlID = &s
	}
	return w
}

func itemWireFrom(item personnelsecurity.Item) itemWire {
	w := itemWire{
		ID:          item.ID.String(),
		ChecklistID: item.ChecklistID.String(),
		Slug:        item.Slug,
		Title:       item.Title,
		Category:    item.Category,
		SortOrder:   item.SortOrder,
		CompletedBy: item.CompletedBy,
		EvidenceURI: item.EvidenceURI,
		Notes:       item.Notes,
	}
	if !item.CompletedAt.IsZero() {
		t := item.CompletedAt.UTC()
		w.CompletedAt = &t
	}
	if item.EvidenceRecordID != uuid.Nil {
		s := item.EvidenceRecordID.String()
		w.EvidenceRecordID = &s
	}
	return w
}
