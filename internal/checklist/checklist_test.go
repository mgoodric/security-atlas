package checklist

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/llm"
)

// ----- fakes -----

// fakeResolver answers ResolveControl/ResolvePolicy from in-memory id sets. A
// cross-tenant id is simply absent (the unit analogue of RLS invisibility).
type fakeResolver struct {
	controls map[uuid.UUID]bool
	policies map[uuid.UUID]bool
}

func (f fakeResolver) ResolveControl(_ context.Context, id uuid.UUID) (bool, error) {
	return f.controls[id], nil
}
func (f fakeResolver) ResolvePolicy(_ context.Context, id uuid.UUID) (bool, error) {
	return f.policies[id], nil
}

// fakeReader returns a fixed in-scope control set.
type fakeReader struct{ controls []ControlInput }

func (f fakeReader) InScopeControls(_ context.Context) ([]ControlInput, error) {
	return f.controls, nil
}

// recordingStore captures persisted sections + items for assertions and assigns
// deterministic section ids.
type recordingStore struct {
	sections []recordedSection
	approved map[uuid.UUID]string
}

type recordedSection struct {
	id         uuid.UUID
	role       Role
	aiAssisted bool
	prov       Provenance
	items      []Item
}

func (s *recordingStore) PersistSection(_ context.Context, _ uuid.UUID, role Role, ai bool, prov Provenance, items []Item) (string, error) {
	id := uuid.New()
	s.sections = append(s.sections, recordedSection{id: id, role: role, aiAssisted: ai, prov: prov, items: items})
	return id.String(), nil
}

func (s *recordingStore) Approve(_ context.Context, sectionID uuid.UUID, approver string) (ApprovedSection, error) {
	for _, sec := range s.sections {
		if sec.id == sectionID {
			if !sec.aiAssisted {
				return ApprovedSection{}, ErrSectionNotFound
			}
			if s.approved == nil {
				s.approved = map[uuid.UUID]string{}
			}
			s.approved[sectionID] = approver
			return ApprovedSection{SectionID: sectionID.String(), Role: sec.role, HumanApproved: true, HumanApprover: approver}, nil
		}
	}
	return ApprovedSection{}, ErrSectionNotFound
}

// nopAudit satisfies AuditSink without a DB.
type nopAudit struct{ writes int }

func (a *nopAudit) Write(_ context.Context, _ llm.Generation) (dbx.AiGeneration, error) {
	a.writes++
	return dbx.AiGeneration{}, nil
}

// newStubSvc wires a Service over a StubClient that returns draft verbatim.
func newStubSvc(reader ControlReader, resolver ControlResolver, store SectionStore, audit AuditSink, draft string) *Service {
	stub := llm.NewStubClient()
	stub.Result = llm.GenerateResult{Text: draft, ModelName: "stub", ModelVersion: "0", ModelProvider: "stub"}
	return NewService(reader, stub, resolver, store, audit)
}

func ctrl(id, role, scf string, hasEv bool, policies ...string) ControlInput {
	return ControlInput{ID: id, Title: "Title " + id[:8], Description: "desc", Role: Role(role), SCFID: scf, PolicyIDs: policies, HasEvidence: hasEv}
}

// ----- citation validation -----

func TestValidateItemCitations_ControlOnly(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}}
	allowed, _ := buildAllowed(ctrl(cid.String(), "infra", "", true))
	cites, ok, reason, err := validateItemCitations(context.Background(), res, "Enable MFA ("+cid.String()+").", allowed)
	if err != nil || !ok {
		t.Fatalf("want valid, got ok=%v reason=%q err=%v", ok, reason, err)
	}
	if len(cites) != 1 || cites[0].Kind != KindControl || cites[0].ID != cid.String() {
		t.Fatalf("unexpected citations: %+v", cites)
	}
}

func TestValidateItemCitations_NoCitationRejected(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}}
	allowed, _ := buildAllowed(ctrl(cid.String(), "infra", "", true))
	_, ok, reason, _ := validateItemCitations(context.Background(), res, "Enable MFA everywhere.", allowed)
	if ok || reason != ReasonNoCitations {
		t.Fatalf("want no-citations rejection, got ok=%v reason=%q", ok, reason)
	}
}

func TestValidateItemCitations_FabricatedUUIDRejected(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	fab := uuid.New()
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}}
	allowed, _ := buildAllowed(ctrl(cid.String(), "infra", "", true))
	// Cites the control AND a fabricated id outside the grounding set.
	task := "Enable MFA (" + cid.String() + ") per (" + fab.String() + ")."
	_, ok, reason, _ := validateItemCitations(context.Background(), res, task, allowed)
	if ok || reason != ReasonUnresolvedCitation {
		t.Fatalf("want unresolved rejection, got ok=%v reason=%q", ok, reason)
	}
}

func TestValidateItemCitations_PolicyInGrounding(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	pid := uuid.New()
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}, policies: map[uuid.UUID]bool{pid: true}}
	allowed, _ := buildAllowed(ctrl(cid.String(), "security", "", true, pid.String()))
	task := "Document the policy (" + cid.String() + ") (" + pid.String() + ")."
	cites, ok, _, err := validateItemCitations(context.Background(), res, task, allowed)
	if err != nil || !ok {
		t.Fatalf("want valid, got ok=%v err=%v", ok, err)
	}
	var sawPolicy bool
	for _, c := range cites {
		if c.Kind == KindPolicy && c.ID == pid.String() {
			sawPolicy = true
		}
	}
	if !sawPolicy {
		t.Fatalf("policy citation not captured: %+v", cites)
	}
}

func TestValidateItemCitations_SCFAnchorMatched(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}}
	allowed, _ := buildAllowed(ctrl(cid.String(), "security", "IAC-06", true))
	task := "Implement IAC-06 access control (" + cid.String() + ")."
	cites, ok, _, _ := validateItemCitations(context.Background(), res, task, allowed)
	if !ok {
		t.Fatal("want valid SCF citation")
	}
	var sawSCF bool
	for _, c := range cites {
		if c.Kind == KindSCFAnchor && c.ID == "IAC-06" {
			sawSCF = true
		}
	}
	if !sawSCF {
		t.Fatalf("scf anchor citation not captured: %+v", cites)
	}
}

func TestValidateItemCitations_WrongSCFRejected(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}}
	allowed, _ := buildAllowed(ctrl(cid.String(), "security", "IAC-06", true))
	// Cites a DIFFERENT anchor than the control's.
	task := "Implement GOV-01 (" + cid.String() + ")."
	_, ok, reason, _ := validateItemCitations(context.Background(), res, task, allowed)
	if ok || reason != ReasonUnresolvedCitation {
		t.Fatalf("want unresolved (wrong scf), got ok=%v reason=%q", ok, reason)
	}
}

// ----- prompt parsing -----

func TestParseTaskLines(t *testing.T) {
	t.Parallel()
	in := "- Enable MFA (id-a).\n* Rotate keys (id-b).\n1. Patch hosts (id-c).\n\n  Audit logs (id-d).  "
	got := parseTaskLines(in)
	want := []string{"Enable MFA (id-a).", "Rotate keys (id-b).", "Patch hosts (id-c).", "Audit logs (id-d)."}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildPrompt_NoEvidenceMarker(t *testing.T) {
	t.Parallel()
	cid := uuid.New().String()
	p := buildPrompt(RoleInfra, []ControlInput{ctrl(cid, "infra", "IAC-06", false)})
	if !strings.Contains(p, "NO EVIDENCE YET") {
		t.Error("prompt must mark a control with no evidence")
	}
	if !strings.Contains(p, cid) {
		t.Error("prompt must include the control id for citation")
	}
	if !strings.Contains(p, "IAC-06") {
		t.Error("prompt must include the scf anchor")
	}
}

// ----- service-level: suppression, no-fabrication, approval, unassigned -----

func TestGenerate_OverCapRejected(t *testing.T) {
	t.Parallel()
	var controls []ControlInput
	for i := 0; i < MaxControls+1; i++ {
		controls = append(controls, ctrl(uuid.New().String(), "infra", "", true))
	}
	svc := newStubSvc(fakeReader{controls}, fakeResolver{}, &recordingStore{}, &nopAudit{}, "")
	_, err := svc.Generate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "too many controls") {
		t.Fatalf("want ErrTooManyControls, got %v", err)
	}
}

func TestGenerate_NoControls(t *testing.T) {
	t.Parallel()
	svc := newStubSvc(fakeReader{nil}, fakeResolver{}, &recordingStore{}, &nopAudit{}, "")
	if _, err := svc.Generate(context.Background()); err != ErrNoControls {
		t.Fatalf("want ErrNoControls, got %v", err)
	}
}

func TestGenerate_ValidDraftPersistsUnapproved(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	c := ctrl(cid.String(), "infra", "", true)
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}}
	store := &recordingStore{}
	audit := &nopAudit{}
	draft := "Enable MFA on all infra accounts (" + cid.String() + ")."
	svc := newStubSvc(fakeReader{[]ControlInput{c}}, res, store, audit, draft)

	out, err := svc.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Sections) != 1 || out.Sections[0].Role != RoleInfra {
		t.Fatalf("want 1 infra section, got %+v", out.Sections)
	}
	sec := out.Sections[0]
	if sec.Suppressed || !sec.AIAssisted || sec.HumanApproved {
		t.Fatalf("section must be an unapproved AI draft: %+v", sec)
	}
	if len(sec.Items) != 1 || sec.Items[0].NoEvidence {
		t.Fatalf("expected 1 evidence-backed item: %+v", sec.Items)
	}
	if audit.writes != 1 {
		t.Errorf("expected 1 audit write, got %d", audit.writes)
	}
	if len(store.sections) != 1 || store.sections[0].aiAssisted != true {
		t.Fatalf("section not persisted as ai_assisted")
	}
}

func TestGenerate_FabricatedCitationSuppressesSection(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	fab := uuid.New() // never resolvable
	c := ctrl(cid.String(), "security", "", true)
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}}
	store := &recordingStore{}
	// The model cites the control AND a fabricated id.
	draft := "Review access (" + cid.String() + ") see (" + fab.String() + ")."
	svc := newStubSvc(fakeReader{[]ControlInput{c}}, res, store, &nopAudit{}, draft)

	out, err := svc.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Sections) != 1 || !out.Sections[0].Suppressed {
		t.Fatalf("fabricated citation must suppress the section: %+v", out.Sections)
	}
	if out.Sections[0].Reason != ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", out.Sections[0].Reason, ReasonUnresolvedCitation)
	}
	if len(store.sections) != 0 {
		t.Fatal("a suppressed section must persist nothing (P0-471-2/P0-471-4)")
	}
}

func TestGenerate_BackendUnavailableSuppresses(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	c := ctrl(cid.String(), "infra", "", true)
	stub := llm.NewStubClient()
	stub.Err = llm.ErrBackend
	store := &recordingStore{}
	svc := NewService(fakeReader{[]ControlInput{c}}, stub, fakeResolver{controls: map[uuid.UUID]bool{cid: true}}, store, &nopAudit{})

	out, err := svc.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !out.Sections[0].Suppressed || out.Sections[0].Reason != ReasonGenerationUnavailable {
		t.Fatalf("backend outage must suppress with generation_unavailable: %+v", out.Sections[0])
	}
	if len(store.sections) != 0 {
		t.Fatal("nothing persisted on backend outage")
	}
}

func TestGenerate_NoEvidenceControlFlagged(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	c := ctrl(cid.String(), "infra", "", false) // NO evidence
	res := fakeResolver{controls: map[uuid.UUID]bool{cid: true}}
	draft := "Establish and capture evidence for backups (" + cid.String() + ")."
	svc := newStubSvc(fakeReader{[]ControlInput{c}}, res, &recordingStore{}, &nopAudit{}, draft)
	out, _ := svc.Generate(context.Background())
	if !out.Sections[0].Items[0].NoEvidence {
		t.Fatal("a control with no evidence must yield a no_evidence item (AC-6)")
	}
}

func TestGenerate_UnassignedBucketSurfaced(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	c := ctrl(cid.String(), "unassigned", "", true)
	store := &recordingStore{}
	svc := newStubSvc(fakeReader{[]ControlInput{c}}, fakeResolver{controls: map[uuid.UUID]bool{cid: true}}, store, &nopAudit{}, "")
	out, err := svc.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Sections) != 1 || out.Sections[0].Role != RoleUnassigned {
		t.Fatalf("unassigned bucket must be surfaced: %+v", out.Sections)
	}
	if out.Sections[0].AIAssisted {
		t.Fatal("unassigned bucket must be ai_assisted=false")
	}
	// And it is non-approvable.
	if len(store.sections) == 1 {
		secID := store.sections[0].id
		if _, err := svc.ApproveSection(context.Background(), secID, "operator"); err != ErrSectionNotFound {
			t.Fatalf("unassigned bucket must not be approvable, got %v", err)
		}
	}
}

func TestApproveSection_BlankApproverRejected(t *testing.T) {
	t.Parallel()
	svc := NewService(fakeReader{}, llm.NewStubClient(), fakeResolver{}, &recordingStore{}, &nopAudit{})
	if _, err := svc.ApproveSection(context.Background(), uuid.New(), "  "); err != ErrApproverRequired {
		t.Fatalf("blank approver must be rejected (P0-471-6), got %v", err)
	}
}

func TestApproveSection_RecordsApprover(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	c := ctrl(cid.String(), "infra", "", true)
	store := &recordingStore{}
	draft := "Enable MFA (" + cid.String() + ")."
	svc := newStubSvc(fakeReader{[]ControlInput{c}}, fakeResolver{controls: map[uuid.UUID]bool{cid: true}}, store, &nopAudit{}, draft)
	out, _ := svc.Generate(context.Background())
	secID := uuid.MustParse(out.Sections[0].SectionID)
	approved, err := svc.ApproveSection(context.Background(), secID, "key_grc_engineer")
	if err != nil {
		t.Fatalf("ApproveSection: %v", err)
	}
	if !approved.HumanApproved || approved.HumanApprover != "key_grc_engineer" {
		t.Fatalf("approval did not record approver: %+v", approved)
	}
}

func TestGenerate_MultiRoleSplit(t *testing.T) {
	t.Parallel()
	infraID, engID, secID := uuid.New(), uuid.New(), uuid.New()
	controls := []ControlInput{
		ctrl(infraID.String(), "infra", "", true),
		ctrl(engID.String(), "engineering", "", true),
		ctrl(secID.String(), "security", "", true),
	}
	res := fakeResolver{controls: map[uuid.UUID]bool{infraID: true, engID: true, secID: true}}
	// One stub draft can't cite all three controls correctly per-section, so use
	// a reader that returns each control and a draft citing each section's own.
	// Simplest: stub returns a draft citing whichever control is first in each
	// section. Because each section has exactly one control, a draft that cites
	// that control resolves. We use a dynamic stub.
	store := &recordingStore{}
	svc := NewService(fakeReader{controls}, perControlStub{}, res, store, &nopAudit{})
	out, err := svc.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Sections) != 3 {
		t.Fatalf("want 3 sections, got %d: %+v", len(out.Sections), out.Sections)
	}
	roles := map[Role]bool{}
	for _, s := range out.Sections {
		roles[s.Role] = true
		if s.Suppressed {
			t.Fatalf("section %s suppressed unexpectedly: %s", s.Role, s.Reason)
		}
	}
	for _, want := range AIRoles {
		if !roles[want] {
			t.Errorf("missing section for role %q", want)
		}
	}
}

// perControlStub returns a draft that cites the FIRST control id it finds in the
// system prompt, so each single-control section gets a resolvable draft.
type perControlStub struct{}

func (perControlStub) Generate(_ context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	if err := reqValidate(req); err != nil {
		return llm.GenerateResult{}, err
	}
	id := uuidPattern.FindString(req.SystemPrompt)
	return llm.GenerateResult{Text: "Implement this control (" + id + ").", ModelName: "stub", ModelVersion: "0", ModelProvider: "stub"}, nil
}

// reqValidate re-runs the shared request caps the StubClient would enforce, so
// the dynamic stub rejects identically.
func reqValidate(req llm.GenerateRequest) error {
	stub := llm.NewStubClient()
	_, err := stub.Generate(context.Background(), req)
	return err
}
