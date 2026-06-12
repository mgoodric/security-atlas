// Package checklist serves the slice-471 role-scoped control-implementation
// checklist generator v0 HTTP surface:
//
//	POST /v1/controls/checklist:generate        — generate a cited DRAFT
//	GET  /v1/controls/checklist/{generationID}  — read a generation's sections
//	POST /v1/controls/checklist/sections/{id}:approve — one-click per-section
//	GET  /v1/controls/checklist/{generationID}/export.md — markdown (approved only)
//
// The checklist is a cited, NON-BINDING DRAFT. The which-control -> which-role
// split is DETERMINISTIC (internal/checklist), never LLM-guessed. Every item is
// cited to a real tenant-owned control/SCF-anchor/policy id, validated before
// the operator sees the draft; a single unresolvable citation suppresses that
// role section. Nothing is exported / marked authoritative without one-click
// per-section human approval (P0-471-1). Generation + approval are role-gated to
// the same grc_engineer/admin role as the other control-write surfaces (no new
// ingress; threat-model S/E).
package checklist

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/checklist"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// generator is the handler's service seam — exactly the methods the routes
// need. The production *checklist.Service satisfies it; tests inject a fake.
type generator interface {
	Generate(ctx context.Context) (checklist.Checklist, error)
	ApproveSection(ctx context.Context, sectionID uuid.UUID, approver string) (checklist.ApprovedSection, error)
}

// loader reads back a persisted generation's sections (for the review view +
// markdown export). Production is *checklist.Store.
type loader interface {
	LoadGeneration(ctx context.Context, generationID uuid.UUID) ([]checklist.Section, error)
}

// Handler serves the checklist routes over the service + store seams.
type Handler struct {
	svc   generator
	store loader
}

// New constructs a Handler over the checklist service + store.
func New(svc *checklist.Service, store *checklist.Store) *Handler {
	return &Handler{svc: svc, store: store}
}

// newHandlerWith constructs a Handler over arbitrary seams. For tests only.
func newHandlerWith(svc generator, store loader) *Handler {
	return &Handler{svc: svc, store: store}
}

// RegisterRoutes attaches the checklist routes. Per the parallel-batch
// convention routes are appended (chi rejects two Mounts at "/").
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/controls/checklist:generate", h.Generate)
	r.Get("/v1/controls/checklist/{generationID}", h.LoadGeneration)
	r.Post("/v1/controls/checklist/sections/{id}:approve", h.ApproveSection)
	r.Get("/v1/controls/checklist/{generationID}/export.md", h.ExportMarkdown)
}

// tenantCred resolves the tenant context + the authenticated credential.
func (h *Handler) tenantCred(r *http.Request) (context.Context, credstore.Credential, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	c, found := authctx.CredentialFromContext(r.Context())
	if !found || c.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), c, true
}

// ===== wire shapes =====

type citationWire struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Ref  string `json:"ref,omitempty"`
}

type itemWire struct {
	ControlID    string         `json:"control_id"`
	ControlTitle string         `json:"control_title,omitempty"`
	Task         string         `json:"task"`
	NoEvidence   bool           `json:"no_evidence"`
	Citations    []citationWire `json:"citations"`
}

type sectionWire struct {
	SectionID     string     `json:"section_id,omitempty"`
	Role          string     `json:"role"`
	AIAssisted    bool       `json:"ai_assisted"`
	HumanApproved bool       `json:"human_approved"`
	HumanApprover string     `json:"human_approver,omitempty"`
	Suppressed    bool       `json:"suppressed"`
	Reason        string     `json:"reason,omitempty"`
	ModelName     string     `json:"model_name,omitempty"`
	ModelVersion  string     `json:"model_version,omitempty"`
	ModelProvider string     `json:"model_provider,omitempty"`
	CloudRouted   bool       `json:"cloud_routed"`
	Items         []itemWire `json:"items"`
}

type checklistWire struct {
	GenerationID string        `json:"generation_id"`
	Sections     []sectionWire `json:"sections"`
	CloudRouted  bool          `json:"cloud_routed"`
	// Binding is ALWAYS false: an honest non-binding disclosure (label honesty,
	// AC-12). Disclosure is the human-readable notice.
	Binding    bool   `json:"binding"`
	Disclosure string `json:"disclosure"`
}

const draftDisclosure = "AI-assisted draft — review before use. Not an audit artifact until each section is approved."

// Generate handles POST /v1/controls/checklist:generate. It generates a cited,
// role-sectioned DRAFT for the caller's tenant. Role-gated to grc_engineer/admin
// (the control-write role). A suppressed section is returned with its reason,
// not as an error.
func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCred(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "grc_engineer role required to generate a checklist")
		return
	}
	if h.svc == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "checklist generation is not enabled on this deployment")
		return
	}
	cl, err := h.svc.Generate(ctx)
	if err != nil {
		switch {
		case errors.Is(err, checklist.ErrNoControls):
			httpresp.WriteError(w, http.StatusUnprocessableEntity, "no in-scope controls to build a checklist from")
		case errors.Is(err, checklist.ErrTooManyControls):
			httpresp.WriteError(w, http.StatusUnprocessableEntity, "too many in-scope controls for one generation; narrow the scope")
		default:
			httperr.WriteInternal(w, r, "checklist", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, checklistWireFrom(cl))
}

// LoadGeneration handles GET /v1/controls/checklist/{generationID}. Read-only
// review view of a persisted generation's sections + items. Role-gated to
// control-read.
func (h *Handler) LoadGeneration(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCred(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin && len(cred.OwnerRoles) == 0 {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant checklist-read access")
		return
	}
	genID, err := uuid.Parse(chi.URLParam(r, "generationID"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "generation id must be a uuid")
		return
	}
	sections, err := h.store.LoadGeneration(ctx, genID)
	if err != nil {
		httperr.WriteInternal(w, r, "checklist", err)
		return
	}
	out := checklistWire{
		GenerationID: genID.String(),
		Sections:     make([]sectionWire, 0, len(sections)),
		Binding:      false,
		Disclosure:   draftDisclosure,
	}
	for _, s := range sections {
		out.Sections = append(out.Sections, sectionWireFrom(s))
	}
	httpresp.WriteJSON(w, http.StatusOK, out)
}

// ApproveSection handles POST /v1/controls/checklist/sections/{id}:approve. The
// one-click per-section approval (AC-10). Role-gated to grc_engineer/admin. The
// approver is the authenticated credential — NEVER client-supplied.
func (h *Handler) ApproveSection(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCred(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "grc_engineer role required to approve a checklist section")
		return
	}
	if h.svc == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "checklist generation is not enabled on this deployment")
		return
	}
	sectionID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "section id must be a uuid")
		return
	}
	approved, err := h.svc.ApproveSection(ctx, sectionID, cred.ID)
	if err != nil {
		switch {
		case errors.Is(err, checklist.ErrApproverRequired):
			httpresp.WriteError(w, http.StatusBadRequest, "approver is required")
		case errors.Is(err, checklist.ErrSectionNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "approvable checklist section not found")
		default:
			httperr.WriteInternal(w, r, "checklist", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"section_id":     approved.SectionID,
		"role":           string(approved.Role),
		"human_approved": approved.HumanApproved,
		"human_approver": approved.HumanApprover,
	})
}

// ExportMarkdown handles GET /v1/controls/checklist/{generationID}/export.md. It
// renders ONLY the APPROVED sections as markdown (AC-11, P0-471-1): an
// unapproved/draft section is never exported. If no section is approved the
// export is a 422 with a clear message — a draft checklist cannot be exported.
func (h *Handler) ExportMarkdown(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCred(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin && len(cred.OwnerRoles) == 0 {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant checklist-read access")
		return
	}
	genID, err := uuid.Parse(chi.URLParam(r, "generationID"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "generation id must be a uuid")
		return
	}
	sections, err := h.store.LoadGeneration(ctx, genID)
	if err != nil {
		httperr.WriteInternal(w, r, "checklist", err)
		return
	}
	md, approvedCount := renderMarkdown(genID.String(), sections)
	if approvedCount == 0 {
		// A draft checklist cannot be exported (the human-approval gate).
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "no approved sections to export; approve at least one section first")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(md))
}

// renderMarkdown builds the markdown export from the APPROVED sections only.
// Returns the markdown + the number of approved sections rendered (0 => caller
// must refuse the export). The unapproved + suppressed + unassigned sections are
// omitted from the export (only approved AI sections are authoritative).
func renderMarkdown(generationID string, sections []checklist.Section) (string, int) {
	var b strings.Builder
	b.WriteString("# Role-scoped control-implementation checklist\n\n")
	b.WriteString("_Generation " + generationID + " — approved sections only._\n")

	approved := 0
	for _, s := range sections {
		if !s.HumanApproved || !s.AIAssisted {
			continue
		}
		approved++
		b.WriteString("\n## " + titleForRole(s.Role) + "\n\n")
		if s.HumanApprover != "" {
			b.WriteString("_Approved by " + s.HumanApprover + "._\n\n")
		}
		for _, it := range s.Items {
			marker := ""
			if it.NoEvidence {
				marker = " _(no evidence yet)_"
			}
			b.WriteString("- [ ] " + it.Task + marker + "\n")
		}
	}
	return b.String(), approved
}

func titleForRole(r checklist.Role) string {
	switch r {
	case checklist.RoleInfra:
		return "Infrastructure team"
	case checklist.RoleEngineering:
		return "Engineering team"
	case checklist.RoleSecurity:
		return "Security team"
	case checklist.RoleUnassigned:
		return "Unassigned"
	default:
		return string(r)
	}
}

// ===== wire converters =====

func checklistWireFrom(cl checklist.Checklist) checklistWire {
	out := checklistWire{
		GenerationID: cl.GenerationID,
		Sections:     make([]sectionWire, 0, len(cl.Sections)),
		CloudRouted:  cl.CloudRouted,
		Binding:      false,
		Disclosure:   draftDisclosure,
	}
	for _, s := range cl.Sections {
		out.Sections = append(out.Sections, sectionWireFrom(s))
	}
	return out
}

func sectionWireFrom(s checklist.Section) sectionWire {
	items := make([]itemWire, 0, len(s.Items))
	for _, it := range s.Items {
		cites := make([]citationWire, 0, len(it.Citations))
		for _, c := range it.Citations {
			cites = append(cites, citationWire{Kind: string(c.Kind), ID: c.ID, Ref: c.Ref})
		}
		items = append(items, itemWire{
			ControlID:    it.ControlID,
			ControlTitle: it.ControlText,
			Task:         it.Task,
			NoEvidence:   it.NoEvidence,
			Citations:    cites,
		})
	}
	return sectionWire{
		SectionID:     s.SectionID,
		Role:          string(s.Role),
		AIAssisted:    s.AIAssisted,
		HumanApproved: s.HumanApproved,
		HumanApprover: s.HumanApprover,
		Suppressed:    s.Suppressed,
		Reason:        s.Reason,
		ModelName:     s.ModelName,
		ModelVersion:  s.ModelVersion,
		ModelProvider: s.ModelProvider,
		CloudRouted:   s.CloudRouted,
		Items:         items,
	}
}
