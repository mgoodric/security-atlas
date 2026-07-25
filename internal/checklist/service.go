package checklist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/llm"
)

// ControlReader loads the in-scope control set for the caller's tenant under
// RLS (AC-2). The production implementation is *Store; tests supply a fake so
// the suppression + cross-tenant branches are exercised without a live Postgres
// (the integration tier uses the real *Store).
type ControlReader interface {
	InScopeControls(ctx context.Context) ([]ControlInput, error)
}

// SectionStore persists a role section + its items, and approves a section.
// Production is *Store; the interface keeps the Service testable.
type SectionStore interface {
	// PersistSection writes one role section + its items in a single tenant tx
	// and returns the section id. For the unassigned bucket, aiAssisted is
	// false + provenance is empty. items is non-empty for an AI section.
	PersistSection(ctx context.Context, generationID uuid.UUID, role Role, aiAssisted bool, prov Provenance, items []Item) (string, error)
	// Approve flips human_approved=TRUE + records the approver on an AI-assisted,
	// unapproved section.
	Approve(ctx context.Context, sectionID uuid.UUID, approver string) (ApprovedSection, error)
}

// AuditSink records the slice-498 ai_generations audit row for one section's
// generation (R-mitigation). Production is *llm.AuditWriter (whose Write
// signature this mirrors exactly, so it satisfies the seam directly); the
// Service holds it behind a narrow seam so a test can assert the audit write
// without a DB.
type AuditSink interface {
	Write(ctx context.Context, g llm.Generation) (dbx.AiGeneration, error)
}

// Service is the checklist generation orchestrator. It loads the tenant's
// in-scope controls, splits them DETERMINISTICALLY by role, runs one bounded
// local-Ollama generation per AI role, validates every citation against
// tenant-owned rows, persists each section as an UNAPPROVED draft, writes the
// audit record, and approves a section on a separate operator action. The
// constitutional invariants (deterministic split, no fabricated coverage, no
// cross-tenant bleed, one-click approval, local-only) are enforced here + at
// the DB layer.
type Service struct {
	reader   ControlReader
	client   llm.Client
	resolver ControlResolver
	store    SectionStore
	audit    AuditSink
}

// NewService wires the control reader, the local-inference client (Ollama in
// production, Stub in CI), the citation resolver, the section store, and the
// audit sink. In production the DB seams are the same *Store and audit is
// *llm.AuditWriter.
func NewService(reader ControlReader, client llm.Client, resolver ControlResolver, store SectionStore, audit AuditSink) *Service {
	return &Service{reader: reader, client: client, resolver: resolver, store: store, audit: audit}
}

// Provenance is the model metadata persisted onto a section.
type Provenance struct {
	PromptVersion string
	ModelName     string
	ModelVersion  string
	ModelProvider string
}

// Generate produces (and persists) a role-sectioned, cited DRAFT checklist for
// the caller's tenant. The flow:
//
//  1. Load the in-scope active controls (RLS-scoped, AC-2). Reject an over-cap
//     set (AC-3, P0-471-7) and an empty set (nothing to ground).
//  2. Split each control DETERMINISTICALLY into a role (AC-1) — never an LLM
//     guess.
//  3. For each AI role with controls, run ONE bounded local-Ollama generation,
//     parse the task lines, and validate EVERY citation against the control's
//     grounding set + tenant-owned rows. A single failure suppresses the whole
//     section (the strict JUDGMENT call) — nothing is persisted for it.
//  4. Persist each valid section as an UNAPPROVED draft + write the audit row.
//  5. Emit the unassigned bucket honestly (a non-AI section listing the
//     controls that matched no role).
//
// Approval is a SEPARATE per-section operator action (ApproveSection). The only
// error Generate returns is a genuine infra/over-cap failure; a suppressed
// section is conveyed on the Section, not via error.
func (s *Service) Generate(ctx context.Context) (Checklist, error) {
	controls, err := s.reader.InScopeControls(ctx)
	if err != nil {
		return Checklist{}, err
	}
	if len(controls) == 0 {
		return Checklist{}, ErrNoControls
	}
	if len(controls) > MaxControls {
		return Checklist{}, fmt.Errorf("%w: %d > %d", ErrTooManyControls, len(controls), MaxControls)
	}

	// Step 2: deterministic split. Each control already carries its assigned
	// Role from the store read (the store calls AssignRole); group by role.
	byRole := map[Role][]ControlInput{}
	for _, c := range controls {
		r := c.Role
		if !ValidRole(r) {
			r = RoleUnassigned
		}
		byRole[r] = append(byRole[r], c)
	}

	generationID := uuid.New()
	out := Checklist{GenerationID: generationID.String()}

	// Step 3+4: one section per AI role, in stable order.
	for _, role := range AIRoles {
		roleControls := byRole[role]
		if len(roleControls) == 0 {
			continue
		}
		section := s.generateSection(ctx, generationID, role, roleControls)
		out.Sections = append(out.Sections, section)
		if section.CloudRouted {
			out.CloudRouted = true
		}
	}

	// Step 5: the unassigned bucket — surfaced honestly, never dropped (AC-1).
	if unassigned := byRole[RoleUnassigned]; len(unassigned) > 0 {
		section, perr := s.persistUnassigned(ctx, generationID, unassigned)
		if perr != nil {
			return Checklist{}, perr
		}
		out.Sections = append(out.Sections, section)
	}

	return out, nil
}

// generateSection runs one role's bounded generation, validates citations, and
// persists the section on success. On any citation failure or backend outage it
// returns a suppressed section and persists nothing (P0-471-2/P0-471-4).
func (s *Service) generateSection(ctx context.Context, generationID uuid.UUID, role Role, controls []ControlInput) Section {
	section := Section{Role: role, AIAssisted: true}

	sysPrompt := buildSystemPrompt(role)
	promptBody := buildPrompt(role, controls)
	res, err := s.client.Generate(ctx, llm.GenerateRequest{
		Surface:       llm.SurfaceChecklist,
		PromptVersion: promptVersion,
		SystemPrompt:  sysPrompt + "\n\n" + promptBody,
		Context:       sectionContext(role, controls),
		MaxTokens:     MaxSectionTokens,
		Timeout:       GenerationTimeout,
	})
	if err != nil {
		section.Suppressed = true
		section.Reason = ReasonGenerationUnavailable
		return section
	}
	section.ModelName = res.ModelName
	section.ModelVersion = res.ModelVersion
	section.ModelProvider = res.ModelProvider
	section.CloudRouted = isCloudProvider(res.ModelProvider)

	items, ok, reason := s.validateSection(ctx, res.Text, controls)
	if !ok {
		section.Suppressed = true
		section.Reason = reason
		return section
	}
	if len(items) == 0 {
		section.Suppressed = true
		section.Reason = ReasonNoTasks
		return section
	}

	prov := Provenance{
		PromptVersion: promptVersion,
		ModelName:     res.ModelName,
		ModelVersion:  res.ModelVersion,
		ModelProvider: res.ModelProvider,
	}
	sectionID, perr := s.store.PersistSection(ctx, generationID, role, true, prov, items)
	if perr != nil {
		// A genuine persistence failure degrades to a suppressed section rather
		// than failing the whole generation; the other sections still land.
		section.Suppressed = true
		section.Reason = ReasonGenerationUnavailable
		return section
	}
	section.SectionID = sectionID
	for i := range items {
		items[i].ItemID = "" // item ids are assigned at persist; not surfaced pre-read
	}
	section.Items = items

	// R-mitigation: write the append-only audit record (best-effort — a failed
	// audit write does not unwind the persisted draft, but is surfaced via the
	// returned section staying valid; the ledger write is logged by the writer).
	if s.audit != nil {
		_, _ = s.audit.Write(ctx, llm.Generation{
			Surface:        llm.SurfaceChecklist,
			PromptVersion:  promptVersion,
			ModelName:      res.ModelName,
			ModelVersion:   res.ModelVersion,
			ModelProvider:  res.ModelProvider,
			SystemPrompt:   sysPrompt + "\n\n" + promptBody,
			ContextInputs:  sectionContext(role, controls),
			RawDraft:       res.Text,
			SurfaceSubject: sectionID,
		})
	}
	return section
}

// validateSection parses the model's task lines and validates each one's
// citations against the matching control's grounding set. A line is attributed
// to a control by the FIRST control id it cites (which must be one of this
// section's controls). The STRICT contract: a single unresolved/out-of-grounding
// citation fails the whole section (returns ok=false). On success, returns the
// validated items, capped at MaxTasksPerControl per control.
func (s *Service) validateSection(ctx context.Context, draft string, controls []ControlInput) ([]Item, bool, string) {
	// Build the per-control grounding sets keyed by control id.
	allowedByControl := make(map[uuid.UUID]allowedRefs, len(controls))
	inputByControl := make(map[uuid.UUID]ControlInput, len(controls))
	for _, c := range controls {
		a, err := buildAllowed(c)
		if err != nil {
			continue
		}
		allowedByControl[a.controlID] = a
		inputByControl[a.controlID] = c
	}

	perControlCount := make(map[uuid.UUID]int, len(controls))
	var items []Item

	for _, line := range parseTaskLines(draft) {
		cited := parseCitedUUIDs(line)
		if len(cited) == 0 {
			// A task line with no citation at all fails the section (the model
			// was told every line must begin with the control id).
			return nil, false, ReasonNoCitations
		}
		// Attribute the line to the first cited id that is one of this section's
		// controls. A line whose first control id is NOT in this section is a
		// fabrication/cross-section leak.
		owner := cited[0]
		allowed, ok := allowedByControl[owner]
		if !ok {
			return nil, false, ReasonUnresolvedCitation
		}
		// Cap per-control task count (D-mitigation, P0-471-7): silently drop
		// over-cap lines for a control rather than failing.
		if perControlCount[owner] >= MaxTasksPerControl {
			continue
		}
		cites, valid, reason, err := validateItemCitations(ctx, s.resolver, line, allowed)
		if err != nil || !valid {
			if reason == "" {
				reason = ReasonUnresolvedCitation
			}
			return nil, false, reason
		}
		in := inputByControl[owner]
		items = append(items, Item{
			ControlID:   owner.String(),
			Task:        line,
			Citations:   cites,
			NoEvidence:  !in.HasEvidence,
			ControlText: in.Title,
		})
		perControlCount[owner]++
	}
	return items, true, ""
}

// persistUnassigned writes the unassigned bucket: a non-AI section listing the
// controls that matched no role, each as one item whose "task" names the gap.
// It carries the control citation (always tenant-grounded) so the operator can
// click through, but is ai_assisted=FALSE and never approvable.
func (s *Service) persistUnassigned(ctx context.Context, generationID uuid.UUID, controls []ControlInput) (Section, error) {
	section := Section{Role: RoleUnassigned, AIAssisted: false}
	items := make([]Item, 0, len(controls))
	for _, c := range controls {
		cid, err := uuid.Parse(c.ID)
		if err != nil {
			continue
		}
		items = append(items, Item{
			ControlID:   c.ID,
			Task:        "Assign an owning team for control " + oneLine(c.Title) + " (no role could be derived from its owner_role).",
			Citations:   []Citation{{Kind: KindControl, ID: cid.String(), Ref: cid.String()}},
			NoEvidence:  !c.HasEvidence,
			ControlText: c.Title,
		})
	}
	if len(items) == 0 {
		return section, nil
	}
	sectionID, err := s.store.PersistSection(ctx, generationID, RoleUnassigned, false, Provenance{}, items)
	if err != nil {
		return Section{}, err
	}
	section.SectionID = sectionID
	section.Items = items
	return section, nil
}

// ApproveSection is the one-click human approval of one role section (AC-10). It
// records the approver and flips human_approved=TRUE. There is NO auto-approve
// path; this is the ONLY way a checklist section becomes approved. The
// blank-approver guard fires here (the Go mirror of the DB CHECK, P0-471-6) so a
// confused caller gets ErrApproverRequired rather than a raw 23514.
func (s *Service) ApproveSection(ctx context.Context, sectionID uuid.UUID, approver string) (ApprovedSection, error) {
	if strings.TrimSpace(approver) == "" {
		return ApprovedSection{}, ErrApproverRequired
	}
	if err := llm.EnforceApproval(llm.ApprovalState{
		AIAssisted:    true,
		HumanApproved: true,
		HumanApprover: approver,
	}); err != nil {
		return ApprovedSection{}, ErrApproverRequired
	}
	approved, err := s.store.Approve(ctx, sectionID, approver)
	if err != nil {
		return ApprovedSection{}, err
	}
	return approved, nil
}

// sectionContext builds the structured context map recorded on the
// ai_generations audit row (R-mitigation: which controls backed the section is
// reconstructable). The model consumes only the system prompt; this map is the
// forensic record.
func sectionContext(role Role, controls []ControlInput) map[string]any {
	ids := make([]string, 0, len(controls))
	for _, c := range controls {
		ids = append(ids, c.ID)
	}
	return map[string]any{
		"role":        string(role),
		"control_ids": ids,
	}
}

// isCloudProvider reports whether the resolved provider is a cloud LLM (triggers
// the visible routing banner). v0 only serves local Ollama or the stub, so this
// is structurally false; the helper exists so the cloud opt-in follow-on
// surfaces the banner without a wire change.
func isCloudProvider(provider string) bool {
	switch strings.ToLower(provider) {
	case "", "ollama", "ollama-local", "local", "stub":
		return false
	default:
		return true
	}
}

// marshalCitations serializes an item's citations to JSONB for persistence.
func marshalCitations(cites []Citation) ([]byte, error) {
	if len(cites) == 0 {
		return nil, fmt.Errorf("checklist: refusing to persist an item with no citations")
	}
	return json.Marshal(cites)
}
