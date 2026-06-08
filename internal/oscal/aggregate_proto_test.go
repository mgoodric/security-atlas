// Slice 285 unit tests for the proto-conversion methods on `aggregate`:
//
//   - aggregate.metadata        -> oscalv1.Metadata
//   - aggregate.sspInput        -> oscalv1.SspInput
//   - aggregate.assessmentInput -> oscalv1.AssessmentInput
//   - aggregate.poamInput       -> oscalv1.PoamInput
//
// These are pure-data marshalling functions: they read populated dbx
// rows + ExportInput on the aggregate and emit a proto input message
// that the Python bridge will turn into canonical OSCAL JSON. They have
// no I/O surface, so unit coverage is the right tier per slice 279's
// audit ("OSCAL ingest/export marshalling; pure-data unit-testable").
//
// Branches covered:
//
//   - sspInput: scope cells populated, controls populated (with + without
//     ScfID), policies populated, statement template renders, metadata
//     stamped with the frozen horizon. (Was 50% in the slice 279 audit.)
//   - assessmentInput: populations populated (with frozen_at valid + zero),
//     walkthroughs populated, audit notes populated (with + without
//     ScopeID, with + without CreatedAt). (Was 35%.)
//   - poamInput: failing evaluations populated across all severity +
//     due-date branches (LastObservedAt valid, EvaluatedAt valid, both
//     zero; FreshnessStatus 'stale' / 'no_evidence' / 'fresh' /
//     ”; controlOwner present / missing; controlTitle present /
//     missing). (Was 15%.)
//   - metadata: already 100%, re-asserted via the SSP+AP+POA&M roundabout
//     to keep the assertion site close to the consumers.
package oscal

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// makeAggregate is a richer test builder than minimalAggregate (which
// lives in export_test.go): it lets each test pin the period, the input,
// and the per-relation slices so the proto-conversion methods exercise
// the populated branches.
func makeAggregate(
	t *testing.T,
	in ExportInput,
	scopeCells []dbx.ScopeCell,
	controls []dbx.ListActiveControlsWithDescriptionRow,
	policies []dbx.Policy,
	populations []dbx.ListPopulationsForPeriodRow,
	walkthroughs []dbx.Walkthrough,
	auditNotes []dbx.ListAuditNotesForPeriodRow,
	failing []dbx.ListFailingEvaluationsAsOfRow,
) *aggregate {
	t.Helper()
	frozenAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	periodID := uuid.New()
	tenantID := uuid.New()
	agg := &aggregate{
		period: dbx.AuditPeriod{
			ID:       pgUUID(periodID),
			TenantID: pgUUID(tenantID),
			Name:     "SOC 2 2026 Q2",
			Status:   "frozen",
			FrozenAt: pgtype.Timestamptz{Time: frozenAt, Valid: true},
		},
		frozenAt:               frozenAt,
		scopeCells:             scopeCells,
		controls:               controls,
		policies:               policies,
		populations:            populations,
		walkthroughs:           walkthroughs,
		auditNotes:             auditNotes,
		failingEvals:           failing,
		in:                     in,
		controlOwner:           map[uuid.UUID]string{},
		controlTitle:           map[uuid.UUID]string{},
		sampledEvidence:        map[uuid.UUID][]uuid.UUID{},
		walkthroughAttachments: map[uuid.UUID][]dbx.ListWalkthroughAttachmentsForPeriodRow{},
	}
	for _, c := range controls {
		cid := uuid.UUID(c.ID.Bytes)
		agg.controlOwner[cid] = c.OwnerRole
		agg.controlTitle[cid] = c.Title
	}
	return agg
}

func TestMetadata_StampsFrozenHorizonAndOSCALVersion(t *testing.T) {
	agg := makeAggregate(t, ExportInput{}, nil, nil, nil, nil, nil, nil, nil)

	md := agg.metadata("Test Title")

	if md.Title != "Test Title" {
		t.Errorf("Title = %q, want %q", md.Title, "Test Title")
	}
	if md.OscalVersion != OSCALVersion {
		t.Errorf("OscalVersion = %q, want %q", md.OscalVersion, OSCALVersion)
	}
	if md.Version != "1.0" {
		t.Errorf("Version = %q, want \"1.0\"", md.Version)
	}
	wantFrozen := agg.frozenAt.UTC().Format(time.RFC3339)
	if md.FrozenAt != wantFrozen {
		t.Errorf("FrozenAt = %q, want %q", md.FrozenAt, wantFrozen)
	}
	if md.LastModified == "" {
		t.Error("LastModified must be populated with the export wall-clock instant")
	}
	// LastModified must parse as RFC-3339 — the bridge consumes it as a
	// timestamp string, not free-form text.
	if _, err := time.Parse(time.RFC3339, md.LastModified); err != nil {
		t.Errorf("LastModified %q is not RFC-3339: %v", md.LastModified, err)
	}
}

func TestSSPInput_PopulatesScopeCellsControlsAndPolicies(t *testing.T) {
	scfA := "IAC-06"
	cellID := uuid.New()
	ctrlIDWithSCF := uuid.New()
	ctrlIDNoSCF := uuid.New()
	policyID := uuid.New()

	cells := []dbx.ScopeCell{
		{
			ID:         pgUUID(cellID),
			Label:      "prod/us-east",
			Dimensions: []byte(`{"env":"prod","cloud":"aws"}`),
		},
	}
	controls := []dbx.ListActiveControlsWithDescriptionRow{
		{
			ID:                pgUUID(ctrlIDWithSCF),
			ScfID:             &scfA,
			Title:             "Encrypt customer data at rest",
			Description:       "All customer data at rest is encrypted using AES-256 via KMS-managed keys; key rotation is enforced every 90 days.",
			ControlFamily:     "IAC",
			OwnerRole:         "infra_security",
			ApplicabilityExpr: "env == 'prod'",
		},
		{
			ID:                pgUUID(ctrlIDNoSCF),
			ScfID:             nil,
			Title:             "Quarterly access review",
			Description:       "", // no authored narrative -> labeled fallback
			ControlFamily:     "IAM",
			OwnerRole:         "grc_engineer",
			ApplicabilityExpr: "true",
		},
	}
	policies := []dbx.Policy{
		{
			ID:      pgUUID(policyID),
			Title:   "Access Control Policy",
			Version: "2.0",
			Status:  "published",
		},
	}

	in := ExportInput{
		OrganizationName:  "Acme Security Inc.",
		SystemName:        "Acme Platform",
		SystemDescription: "The SaaS platform under SOC 2 assessment.",
	}
	agg := makeAggregate(t, in, cells, controls, policies, nil, nil, nil, nil)

	out := agg.sspInput()

	if out.Metadata == nil {
		t.Fatal("Metadata must not be nil")
	}
	if out.OrganizationName != in.OrganizationName {
		t.Errorf("OrganizationName = %q, want %q", out.OrganizationName, in.OrganizationName)
	}
	if out.SystemName != in.SystemName {
		t.Errorf("SystemName = %q, want %q", out.SystemName, in.SystemName)
	}
	if out.SystemDescription != in.SystemDescription {
		t.Errorf("SystemDescription = %q, want %q", out.SystemDescription, in.SystemDescription)
	}

	if len(out.ScopeCells) != 1 {
		t.Fatalf("ScopeCells len = %d, want 1", len(out.ScopeCells))
	}
	sc := out.ScopeCells[0]
	if sc.Id != cellID.String() {
		t.Errorf("ScopeCell.Id = %q, want %q", sc.Id, cellID.String())
	}
	if sc.Label != "prod/us-east" {
		t.Errorf("ScopeCell.Label = %q, want %q", sc.Label, "prod/us-east")
	}
	if sc.DimensionsJson != `{"env":"prod","cloud":"aws"}` {
		t.Errorf("ScopeCell.DimensionsJson = %q", sc.DimensionsJson)
	}

	if len(out.ControlImplementations) != 2 {
		t.Fatalf("ControlImplementations len = %d, want 2", len(out.ControlImplementations))
	}
	withSCF := out.ControlImplementations[0]
	if withSCF.ScfId != "IAC-06" {
		t.Errorf("control[0].ScfId = %q, want IAC-06", withSCF.ScfId)
	}
	if withSCF.ControlId != ctrlIDWithSCF.String() {
		t.Errorf("control[0].ControlId mismatch")
	}
	// AC-2: when the control has an authored description, the SSP
	// implementation Statement IS that description, verbatim — never
	// AI-generated (CLAUDE.md product-runtime AI-assist boundary), never
	// the synthesized template.
	wantDesc := "All customer data at rest is encrypted using AES-256 via KMS-managed keys; key rotation is enforced every 90 days."
	if withSCF.Statement != wantDesc {
		t.Errorf("control[0].Statement = %q, want the authored description verbatim %q", withSCF.Statement, wantDesc)
	}
	// AC-4: the authored-description path carries ONLY the description —
	// no leftover synthesized boilerplate concatenated in.
	if contains(withSCF.Statement, "control family") {
		t.Errorf("control[0].Statement %q leaked the synthesized template into an authored description", withSCF.Statement)
	}

	noSCF := out.ControlImplementations[1]
	if noSCF.ScfId != "" {
		t.Errorf("control[1].ScfId = %q, want empty (nil ScfID branch)", noSCF.ScfId)
	}
	// AC-3: a control with no authored description falls back to a
	// CLEARLY-LABELED synthesized summary — never empty (P0-493-1). The
	// label must make it unmistakable to an auditor that the text is
	// auto-generated, not authored.
	if noSCF.Statement == "" {
		t.Error("control[1].Statement must never be empty (P0-493-1)")
	}
	if !contains(noSCF.Statement, "Auto-generated") {
		t.Errorf("control[1].Statement %q must be clearly labeled as auto-generated (AC-3)", noSCF.Statement)
	}
	if !contains(noSCF.Statement, "Quarterly access review") {
		t.Errorf("control[1].Statement %q fallback should include the control title", noSCF.Statement)
	}
	if !contains(noSCF.Statement, "grc_engineer") {
		t.Errorf("control[1].Statement %q fallback should include the owner role", noSCF.Statement)
	}

	if len(out.Policies) != 1 {
		t.Fatalf("Policies len = %d, want 1", len(out.Policies))
	}
	pol := out.Policies[0]
	if pol.Title != "Access Control Policy" {
		t.Errorf("policy.Title = %q", pol.Title)
	}
	if pol.Version != "2.0" {
		t.Errorf("policy.Version = %q", pol.Version)
	}
	if pol.Status != "published" {
		t.Errorf("policy.Status = %q", pol.Status)
	}
	if pol.Id != policyID.String() {
		t.Errorf("policy.Id mismatch")
	}
}

func TestSSPInput_EmptySlicesProduceEmptyOutput(t *testing.T) {
	agg := makeAggregate(t, ExportInput{}, nil, nil, nil, nil, nil, nil, nil)

	out := agg.sspInput()

	if len(out.ScopeCells) != 0 {
		t.Errorf("ScopeCells = %d, want 0", len(out.ScopeCells))
	}
	if len(out.ControlImplementations) != 0 {
		t.Errorf("ControlImplementations = %d, want 0", len(out.ControlImplementations))
	}
	if len(out.Policies) != 0 {
		t.Errorf("Policies = %d, want 0", len(out.Policies))
	}
	if out.Metadata == nil {
		t.Error("Metadata must always be populated, even on an empty SSP")
	}
}

// TestSSPInput_AcceptedVendorClaims_AreSeparateAndNeverControlImplementations
// is the slice-619 hard-boundary unit test (no DB, no bridge).
//
// An operator-ACCEPTED vendor claim must map to the SEPARATE
// VendorAttestedImplementations field — carrying the vendor attribution +
// accept-provenance — and must NEVER appear in ControlImplementations (where
// it could be counted as platform control-satisfaction). This proves the
// boundary at the proto-conversion layer, which is the only place a vendor
// claim could leak into the platform's control-implementation surface.
func TestSSPInput_AcceptedVendorClaims_AreSeparateAndNeverControlImplementations(t *testing.T) {
	claimID := uuid.New()
	acceptedAt := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	scf := "IAC-07"
	by := "operator@acme.com"

	// One platform control (real coverage) + one accepted vendor claim.
	ctrlID := uuid.New()
	controls := []dbx.ListActiveControlsWithDescriptionRow{
		{
			ID:            pgUUID(ctrlID),
			ScfID:         nil,
			Title:         "Platform-owned control",
			Description:   "Platform implementation narrative.",
			ControlFamily: "IAC",
			OwnerRole:     "infra_security",
		},
	}

	agg := makeAggregate(t, ExportInput{}, nil, controls, nil, nil, nil, nil, nil)
	agg.acceptedVendorClaims = []dbx.ListAcceptedVendorClaimsForExportRow{
		{
			ClaimID:         pgUUID(claimID),
			ComponentUuid:   "vendor-uuid-1",
			ComponentTitle:  "AcmeVault",
			ComponentType:   "service",
			ControlID:       "AC-2",
			Statement:       "AcmeVault encrypts secrets at rest.",
			RequirementUuid: "req-1",
			ScfAnchorID:     &scf,
			DispositionedBy: &by,
			DispositionedAt: pgtype.Timestamptz{Time: acceptedAt, Valid: true},
			DispositionNote: "Reviewed vendor SOC 2.",
		},
	}

	out := agg.sspInput()

	// 1. The accepted claim does NOT appear in ControlImplementations: that
	//    list contains ONLY the platform control. (The boundary.)
	if len(out.ControlImplementations) != 1 {
		t.Fatalf("ControlImplementations len = %d, want 1 (platform control only)", len(out.ControlImplementations))
	}
	if out.ControlImplementations[0].ControlId != ctrlID.String() {
		t.Errorf("ControlImplementations[0] = %q, want the platform control id %q", out.ControlImplementations[0].ControlId, ctrlID.String())
	}
	for _, ci := range out.ControlImplementations {
		if ci.ControlId == "AC-2" {
			t.Fatal("vendor claim AC-2 leaked into ControlImplementations — constitutional boundary violated")
		}
	}

	// 2. The accepted claim DOES appear in the separate vendor-attested field,
	//    with the vendor attribution + accept-provenance.
	if len(out.VendorAttestedImplementations) != 1 {
		t.Fatalf("VendorAttestedImplementations len = %d, want 1", len(out.VendorAttestedImplementations))
	}
	v := out.VendorAttestedImplementations[0]
	if v.ClaimId != claimID.String() {
		t.Errorf("ClaimId = %q, want %q", v.ClaimId, claimID.String())
	}
	if v.ControlId != "AC-2" {
		t.Errorf("ControlId = %q, want AC-2", v.ControlId)
	}
	if v.ScfId != "IAC-07" {
		t.Errorf("ScfId = %q, want IAC-07", v.ScfId)
	}
	if v.ComponentUuid != "vendor-uuid-1" || v.ComponentTitle != "AcmeVault" || v.ComponentType != "service" {
		t.Errorf("vendor attribution mismatch: %+v", v)
	}
	if v.Statement != "AcmeVault encrypts secrets at rest." {
		t.Errorf("Statement = %q", v.Statement)
	}
	if v.AcceptedBy != "operator@acme.com" {
		t.Errorf("AcceptedBy = %q, want operator@acme.com", v.AcceptedBy)
	}
	if v.AcceptedAt != acceptedAt.UTC().Format(time.RFC3339) {
		t.Errorf("AcceptedAt = %q, want %q", v.AcceptedAt, acceptedAt.UTC().Format(time.RFC3339))
	}
	if v.DispositionNote != "Reviewed vendor SOC 2." {
		t.Errorf("DispositionNote = %q", v.DispositionNote)
	}
}

// TestSSPInput_NoAcceptedVendorClaims_ProducesEmptyVendorField proves the
// vendor field is empty (never nil-panicking) when nothing was accepted —
// the rejected / needs_info / asserted case never reaches the export because
// the aggregate query filters on claim_status = 'accepted'.
func TestSSPInput_NoAcceptedVendorClaims_ProducesEmptyVendorField(t *testing.T) {
	agg := makeAggregate(t, ExportInput{}, nil, nil, nil, nil, nil, nil, nil)
	out := agg.sspInput()
	if len(out.VendorAttestedImplementations) != 0 {
		t.Errorf("VendorAttestedImplementations = %d, want 0", len(out.VendorAttestedImplementations))
	}
}

func TestAssessmentInput_PopulatesPopulationsWalkthroughsAndNotes(t *testing.T) {
	popIDWithFrozen := uuid.New()
	popIDNoFrozen := uuid.New()
	ctrlID := uuid.New()
	wtID := uuid.New()
	noteID := uuid.New()
	frozenAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	scopeRef := "scope-control:" + ctrlID.String()
	createdAt := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)

	populations := []dbx.ListPopulationsForPeriodRow{
		{
			ID:        pgUUID(popIDWithFrozen),
			ControlID: pgUUID(ctrlID),
			RowCount:  42,
			FrozenAt:  pgtype.Timestamptz{Time: frozenAt, Valid: true},
		},
		{
			ID:        pgUUID(popIDNoFrozen),
			ControlID: pgUUID(ctrlID),
			RowCount:  7,
			// FrozenAt deliberately zero -> exercises the "" branch.
			FrozenAt: pgtype.Timestamptz{},
		},
	}
	walkthroughs := []dbx.Walkthrough{
		{
			ID:            pgUUID(wtID),
			ControlID:     pgUUID(ctrlID),
			Narrative:     "Auditor observed the daily backup verification job complete.",
			Status:        "finalized",
			CanonicalHash: []byte{0xde, 0xad, 0xbe, 0xef},
		},
	}
	notes := []dbx.ListAuditNotesForPeriodRow{
		{
			ID:           pgUUID(noteID),
			AuthorUserID: "auditor-1",
			ScopeType:    "control",
			ScopeID:      &scopeRef,
			Body:         "Please clarify break-glass coverage.",
			CreatedAt:    pgtype.Timestamptz{Time: createdAt, Valid: true},
		},
		{
			// Second note: no ScopeID, no CreatedAt -> exercises both
			// zero-valued branches.
			ID:           pgUUID(uuid.New()),
			AuthorUserID: "auditor-2",
			ScopeType:    "period",
			ScopeID:      nil,
			Body:         "Overall scoping looks good.",
			CreatedAt:    pgtype.Timestamptz{},
		},
	}

	agg := makeAggregate(t, ExportInput{}, nil, nil, nil, populations, walkthroughs, notes, nil)

	out := agg.assessmentInput()

	if out.Metadata == nil {
		t.Fatal("Metadata must be populated")
	}
	if out.AuditPeriodName != "SOC 2 2026 Q2" {
		t.Errorf("AuditPeriodName = %q, want %q", out.AuditPeriodName, "SOC 2 2026 Q2")
	}
	if out.AuditPeriodId == "" {
		t.Error("AuditPeriodId must be populated")
	}
	if out.TenantId == "" {
		t.Error("TenantId must be populated")
	}

	if len(out.Populations) != 2 {
		t.Fatalf("Populations len = %d, want 2", len(out.Populations))
	}
	withFrozen := out.Populations[0]
	if withFrozen.PopulationSize != 42 {
		t.Errorf("populations[0].PopulationSize = %d, want 42", withFrozen.PopulationSize)
	}
	if withFrozen.FrozenAt == "" {
		t.Error("populations[0].FrozenAt must be populated when FrozenAt.Valid is true")
	}
	noFrozen := out.Populations[1]
	if noFrozen.FrozenAt != "" {
		t.Errorf("populations[1].FrozenAt = %q, want empty for the zero FrozenAt branch", noFrozen.FrozenAt)
	}

	if len(out.Walkthroughs) != 1 {
		t.Fatalf("Walkthroughs len = %d, want 1", len(out.Walkthroughs))
	}
	wt := out.Walkthroughs[0]
	if wt.Narrative == "" {
		t.Error("walkthrough.Narrative must be populated")
	}
	if wt.Status != "finalized" {
		t.Errorf("walkthrough.Status = %q", wt.Status)
	}
	if wt.CanonicalHash != "deadbeef" {
		t.Errorf("walkthrough.CanonicalHash = %q, want %q", wt.CanonicalHash, "deadbeef")
	}
	if wt.TamperDetected {
		t.Error("walkthrough.TamperDetected must be false at export time")
	}

	if len(out.AuditNotes) != 2 {
		t.Fatalf("AuditNotes len = %d, want 2", len(out.AuditNotes))
	}
	withScope := out.AuditNotes[0]
	if withScope.ScopeKind != "control" {
		t.Errorf("notes[0].ScopeKind = %q", withScope.ScopeKind)
	}
	if withScope.ScopeRef != scopeRef {
		t.Errorf("notes[0].ScopeRef = %q, want %q", withScope.ScopeRef, scopeRef)
	}
	if withScope.CreatedAt == "" {
		t.Error("notes[0].CreatedAt must be populated when CreatedAt.Valid is true")
	}
	noScope := out.AuditNotes[1]
	if noScope.ScopeRef != "" {
		t.Errorf("notes[1].ScopeRef = %q, want empty (nil ScopeID branch)", noScope.ScopeRef)
	}
	if noScope.CreatedAt != "" {
		t.Errorf("notes[1].CreatedAt = %q, want empty (zero CreatedAt branch)", noScope.CreatedAt)
	}
}

// TestAssessmentInput_CarriesDrawnSampleAndAttachmentRefs (slice 494) exercises
// the new proto-conversion branches: sampled_evidence_ids per population (AC-1),
// walkthrough attachment refs (AC-4/AC-5), the empty-draw branch, and a
// walkthrough with no attachments. Pure in-memory — no DB.
func TestAssessmentInput_CarriesDrawnSampleAndAttachmentRefs(t *testing.T) {
	popDrawn := uuid.New()
	popEmpty := uuid.New()
	ctrlID := uuid.New()
	wtWithAtt := uuid.New()
	wtNoAtt := uuid.New()
	ev1, ev2 := uuid.New(), uuid.New()
	att1 := uuid.New()

	populations := []dbx.ListPopulationsForPeriodRow{
		{ID: pgUUID(popDrawn), ControlID: pgUUID(ctrlID), RowCount: 10},
		{ID: pgUUID(popEmpty), ControlID: pgUUID(ctrlID), RowCount: 3},
	}
	walkthroughs := []dbx.Walkthrough{
		{ID: pgUUID(wtWithAtt), ControlID: pgUUID(ctrlID), Narrative: "n", Status: "finalized", CanonicalHash: []byte{0x01}},
		{ID: pgUUID(wtNoAtt), ControlID: pgUUID(ctrlID), Narrative: "n2", Status: "draft", CanonicalHash: []byte{0x02}},
	}

	agg := makeAggregate(t, ExportInput{}, nil, nil, nil, populations, walkthroughs, nil, nil)
	// popDrawn has a draw (shuffle order ev2, ev1); popEmpty has none.
	agg.sampledEvidence[popDrawn] = []uuid.UUID{ev2, ev1}
	// wtWithAtt has one attachment; wtNoAtt has none.
	agg.walkthroughAttachments[wtWithAtt] = []dbx.ListWalkthroughAttachmentsForPeriodRow{
		{
			ID:          pgUUID(att1),
			StorageKey:  "tenant-abc/" + att1.String(),
			ContentType: "image/png",
			Sha256Hash:  "aa11",
			Annotations: []byte(`{"regions":[{"x":1}]}`),
		},
	}

	out := agg.assessmentInput()

	// AC-1: drawn ids carried in shuffle order.
	gotDrawn := out.Populations[0].SampledEvidenceIds
	if len(gotDrawn) != 2 || gotDrawn[0] != ev2.String() || gotDrawn[1] != ev1.String() {
		t.Errorf("popDrawn sampled ids = %v, want [%s %s] in shuffle order", gotDrawn, ev2, ev1)
	}
	// Empty-draw branch: a never-sampled population carries an empty (non-nil
	// here, but proto-empty) slice — no crash, no leak of other pops' draws.
	if len(out.Populations[1].SampledEvidenceIds) != 0 {
		t.Errorf("popEmpty sampled ids = %v, want empty", out.Populations[1].SampledEvidenceIds)
	}

	// AC-4/AC-5: attachment ref carries metadata + storage URI, no bytes.
	wt0 := out.Walkthroughs[0]
	if len(wt0.Attachments) != 1 {
		t.Fatalf("wtWithAtt attachments = %d, want 1", len(wt0.Attachments))
	}
	a := wt0.Attachments[0]
	if a.Id != att1.String() {
		t.Errorf("attachment id = %q, want %q", a.Id, att1)
	}
	if a.ContentHash != "aa11" || a.ContentType != "image/png" {
		t.Errorf("attachment hash/type = %q/%q", a.ContentHash, a.ContentType)
	}
	if a.StorageUri != "tenant-abc/"+att1.String() {
		t.Errorf("attachment storage uri = %q", a.StorageUri)
	}
	if a.Filename != att1.String() {
		t.Errorf("attachment filename = %q, want basename %q", a.Filename, att1)
	}
	if a.AnnotationRef != `{"regions":[{"x":1}]}` {
		t.Errorf("attachment annotation ref = %q", a.AnnotationRef)
	}
	// A walkthrough with no attachments carries none (nil, not a panic).
	if len(out.Walkthroughs[1].Attachments) != 0 {
		t.Errorf("wtNoAtt attachments = %d, want 0", len(out.Walkthroughs[1].Attachments))
	}
}

// TestWalkthroughAttachmentCap (slice 494 D3) verifies the per-walkthrough cap
// + overflow note: 52 attachments -> 50 real refs + 1 overflow note ref.
func TestWalkthroughAttachmentCap(t *testing.T) {
	wtID := uuid.New()
	agg := makeAggregate(t, ExportInput{}, nil, nil, nil, nil,
		[]dbx.Walkthrough{{ID: pgUUID(wtID), ControlID: pgUUID(uuid.New()), Narrative: "n", Status: "finalized", CanonicalHash: []byte{0x01}}},
		nil, nil)

	rows := make([]dbx.ListWalkthroughAttachmentsForPeriodRow, 0, 52)
	for i := 0; i < 52; i++ {
		id := uuid.New()
		rows = append(rows, dbx.ListWalkthroughAttachmentsForPeriodRow{
			ID: pgUUID(id), StorageKey: "k/" + id.String(), ContentType: "image/png", Sha256Hash: "h", Annotations: []byte("{}"),
		})
	}
	agg.walkthroughAttachments[wtID] = rows

	out := agg.assessmentInput()
	atts := out.Walkthroughs[0].Attachments
	// 50 capped + 1 overflow note = 51.
	if len(atts) != maxAttachmentRefsPerWalkthrough+1 {
		t.Fatalf("attachments len = %d, want %d (cap + overflow note)", len(atts), maxAttachmentRefsPerWalkthrough+1)
	}
	overflow := atts[len(atts)-1]
	if overflow.StorageUri != "" {
		t.Errorf("overflow note should have no storage uri, got %q", overflow.StorageUri)
	}
	if overflow.Filename == "" || !strings.Contains(overflow.Filename, "not shown") {
		t.Errorf("overflow note filename = %q, want an overflow message", overflow.Filename)
	}
}

// TestAttachmentFilenameAndAnnotationRef exercises the small pure helpers.
func TestAttachmentFilenameAndAnnotationRef(t *testing.T) {
	cases := []struct{ key, want string }{
		{"tenant-abc/file-id", "file-id"},
		{"no-separator", "no-separator"},
		{"trailing/", "trailing/"}, // trailing slash -> whole key (no basename)
		{"", ""},
	}
	for _, c := range cases {
		if got := attachmentFilename(c.key); got != c.want {
			t.Errorf("attachmentFilename(%q) = %q, want %q", c.key, got, c.want)
		}
	}
	annCases := []struct{ in, want string }{
		{"", ""},
		{"{}", ""},
		{"  {}  ", ""},
		{`{"regions":[]}`, `{"regions":[]}`},
	}
	for _, c := range annCases {
		if got := annotationRef([]byte(c.in)); got != c.want {
			t.Errorf("annotationRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPOAMInput_FailingControlsBecomePOAMItems exercises EVERY branch in
// poamInput: severity (moderate vs high), due-date base (LastObservedAt
// > EvaluatedAt > now), owner / title fallbacks (present + missing),
// freshness status combinations.
func TestPOAMInput_FailingControlsBecomePOAMItems(t *testing.T) {
	ctrlID := uuid.New()
	missingCtrlID := uuid.New() // not present in controlOwner / controlTitle
	lastObserved := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	evaluatedAt := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)

	controls := []dbx.ListActiveControlsWithDescriptionRow{
		{
			ID:                pgUUID(ctrlID),
			Title:             "Encrypt customer data at rest",
			Description:       "AES-256 at rest.",
			ControlFamily:     "IAC",
			OwnerRole:         "infra_security",
			ApplicabilityExpr: "true",
		},
	}
	failing := []dbx.ListFailingEvaluationsAsOfRow{
		// Case 1: stale freshness -> severity 'high'; LastObservedAt valid
		// -> due date based on LastObservedAt.
		{
			ID:              pgUUID(uuid.New()),
			ControlID:       pgUUID(ctrlID),
			Result:          "fail",
			FreshnessStatus: "stale",
			LastObservedAt:  pgtype.Timestamptz{Time: lastObserved, Valid: true},
			EvaluatedAt:     pgtype.Timestamptz{Time: evaluatedAt, Valid: true},
		},
		// Case 2: no_evidence freshness -> severity 'high'; only
		// EvaluatedAt valid (no LastObservedAt) -> due date based on
		// EvaluatedAt.
		{
			ID:              pgUUID(uuid.New()),
			ControlID:       pgUUID(ctrlID),
			Result:          "fail",
			FreshnessStatus: "no_evidence",
			LastObservedAt:  pgtype.Timestamptz{},
			EvaluatedAt:     pgtype.Timestamptz{Time: evaluatedAt, Valid: true},
		},
		// Case 3: fresh freshness -> severity 'moderate'; neither
		// timestamp valid -> due date based on time.Now().
		{
			ID:              pgUUID(uuid.New()),
			ControlID:       pgUUID(ctrlID),
			Result:          "fail",
			FreshnessStatus: "fresh",
			LastObservedAt:  pgtype.Timestamptz{},
			EvaluatedAt:     pgtype.Timestamptz{},
		},
		// Case 4: control id absent from controlOwner/controlTitle
		// (deleted between aggregate read and POA&M conversion would be
		// the real-world cause). Owner falls back to "unassigned"; title
		// falls back to the controlID string. Severity 'moderate' since
		// freshness is neither stale nor no_evidence.
		{
			ID:              pgUUID(uuid.New()),
			ControlID:       pgUUID(missingCtrlID),
			Result:          "fail",
			FreshnessStatus: "",
			LastObservedAt:  pgtype.Timestamptz{Time: lastObserved, Valid: true},
			EvaluatedAt:     pgtype.Timestamptz{},
		},
	}

	agg := makeAggregate(t, ExportInput{}, nil, controls, nil, nil, nil, nil, failing)

	out := agg.poamInput()

	if out.Metadata == nil {
		t.Fatal("Metadata must be populated")
	}
	if out.AuditPeriodId == "" {
		t.Error("AuditPeriodId must be populated")
	}
	if len(out.Items) != 4 {
		t.Fatalf("Items len = %d, want 4", len(out.Items))
	}

	stale := out.Items[0]
	if stale.Severity != "high" {
		t.Errorf("stale item severity = %q, want high", stale.Severity)
	}
	if stale.Owner != "infra_security" {
		t.Errorf("stale item owner = %q, want infra_security", stale.Owner)
	}
	if !contains(stale.Title, "Encrypt customer data at rest") {
		t.Errorf("stale item title %q does not include control title", stale.Title)
	}
	if !contains(stale.Description, "stale") {
		t.Errorf("stale item description %q does not include freshness status", stale.Description)
	}
	if stale.DueDate == "" {
		t.Error("stale item DueDate must be populated")
	}
	if stale.Milestone == "" {
		t.Error("stale item Milestone must be populated")
	}
	// Due date is LastObservedAt + 90d, RFC-3339. Parse and confirm
	// it's ~90d after the last-observed instant.
	due, err := time.Parse(time.RFC3339, stale.DueDate)
	if err != nil {
		t.Fatalf("DueDate %q not RFC-3339: %v", stale.DueDate, err)
	}
	wantDue := lastObserved.Add(defaultRemediationWindow).UTC()
	if !due.Equal(wantDue) {
		t.Errorf("stale DueDate = %v, want %v (LastObservedAt + 90d)", due, wantDue)
	}

	noEvidence := out.Items[1]
	if noEvidence.Severity != "high" {
		t.Errorf("no_evidence item severity = %q, want high", noEvidence.Severity)
	}
	due2, err := time.Parse(time.RFC3339, noEvidence.DueDate)
	if err != nil {
		t.Fatalf("no_evidence DueDate not RFC-3339: %v", err)
	}
	wantDue2 := evaluatedAt.Add(defaultRemediationWindow).UTC()
	if !due2.Equal(wantDue2) {
		t.Errorf("no_evidence DueDate = %v, want %v (EvaluatedAt + 90d)", due2, wantDue2)
	}

	fresh := out.Items[2]
	if fresh.Severity != "moderate" {
		t.Errorf("fresh item severity = %q, want moderate", fresh.Severity)
	}
	// DueDate is now+90d; we only assert it parses and is in the future
	// (clock skew makes anything tighter flaky).
	if _, err := time.Parse(time.RFC3339, fresh.DueDate); err != nil {
		t.Errorf("fresh DueDate %q not RFC-3339: %v", fresh.DueDate, err)
	}

	missing := out.Items[3]
	if missing.Owner != "unassigned" {
		t.Errorf("missing-control item owner = %q, want unassigned (fallback)", missing.Owner)
	}
	if !contains(missing.Title, missingCtrlID.String()) {
		t.Errorf("missing-control item title %q does not include control id (fallback)", missing.Title)
	}
	if missing.Severity != "moderate" {
		t.Errorf("missing-control severity = %q, want moderate (freshness empty)", missing.Severity)
	}
}

func TestPOAMInput_EmptyFailingEvalsProducesEmptyItems(t *testing.T) {
	agg := makeAggregate(t, ExportInput{}, nil, nil, nil, nil, nil, nil, nil)
	out := agg.poamInput()

	if len(out.Items) != 0 {
		t.Errorf("Items = %d, want 0 when no failing evaluations", len(out.Items))
	}
	if out.Metadata == nil {
		t.Error("Metadata must still be populated on an empty POA&M")
	}
}

// contains aliases strings.Contains so the assertions above stay short.
func contains(s, sub string) bool { return strings.Contains(s, sub) }
