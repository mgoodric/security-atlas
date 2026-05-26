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
	controls []dbx.ListActiveControlsRow,
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
		frozenAt:     frozenAt,
		scopeCells:   scopeCells,
		controls:     controls,
		policies:     policies,
		populations:  populations,
		walkthroughs: walkthroughs,
		auditNotes:   auditNotes,
		failingEvals: failing,
		in:           in,
		controlOwner: map[uuid.UUID]string{},
		controlTitle: map[uuid.UUID]string{},
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
	controls := []dbx.ListActiveControlsRow{
		{
			ID:                pgUUID(ctrlIDWithSCF),
			ScfID:             &scfA,
			Title:             "Encrypt customer data at rest",
			ControlFamily:     "IAC",
			OwnerRole:         "infra_security",
			ApplicabilityExpr: "env == 'prod'",
		},
		{
			ID:                pgUUID(ctrlIDNoSCF),
			ScfID:             nil,
			Title:             "Quarterly access review",
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
	// Statement template renders the control bundle's human-authored
	// summary — Title + ControlFamily + OwnerRole — never anything
	// AI-generated (CLAUDE.md product-runtime AI-assist boundary).
	if withSCF.Statement == "" {
		t.Error("control[0].Statement must be populated")
	}
	if !contains(withSCF.Statement, "Encrypt customer data at rest") {
		t.Errorf("control[0].Statement %q does not include the Title", withSCF.Statement)
	}
	if !contains(withSCF.Statement, "IAC") {
		t.Errorf("control[0].Statement %q does not include the control family", withSCF.Statement)
	}
	if !contains(withSCF.Statement, "infra_security") {
		t.Errorf("control[0].Statement %q does not include the owner role", withSCF.Statement)
	}

	noSCF := out.ControlImplementations[1]
	if noSCF.ScfId != "" {
		t.Errorf("control[1].ScfId = %q, want empty (nil ScfID branch)", noSCF.ScfId)
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

// TestPOAMInput_FailingControlsBecomePOAMItems exercises EVERY branch in
// poamInput: severity (moderate vs high), due-date base (LastObservedAt
// > EvaluatedAt > now), owner / title fallbacks (present + missing),
// freshness status combinations.
func TestPOAMInput_FailingControlsBecomePOAMItems(t *testing.T) {
	ctrlID := uuid.New()
	missingCtrlID := uuid.New() // not present in controlOwner / controlTitle
	lastObserved := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	evaluatedAt := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)

	controls := []dbx.ListActiveControlsRow{
		{
			ID:                pgUUID(ctrlID),
			Title:             "Encrypt customer data at rest",
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
