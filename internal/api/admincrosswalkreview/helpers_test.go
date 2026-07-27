package admincrosswalkreview

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/crosswalkconflict"
	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Pure-Go unit suite for the review-queue assembly and its query-param bounds
// (the slice-353 Q-2 convention). buildQueue is deliberately free of
// *http.Request and context so the filter semantics — the part most easily got
// wrong — are exercisable with no Postgres. The handlers' authz + transactional
// paths are covered by the integration suite.

func pgID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func req(id uuid.UUID, code, title string) dbx.FrameworkRequirement {
	return dbx.FrameworkRequirement{ID: pgID(id), Code: code, Title: title}
}

type edgeOpt func(*dbx.ListFwToScfEdgesForRequirementIDsRow)

func withTier(t dbx.CrosswalkMappingTier) edgeOpt {
	return func(r *dbx.ListFwToScfEdgesForRequirementIDsRow) { r.MappingTier = t }
}

func edge(
	id, reqID, anchorID uuid.UUID,
	reqCode, scfID, family string,
	relType dbx.StrmRelationshipType,
	strength float64,
	opts ...edgeOpt,
) dbx.ListFwToScfEdgesForRequirementIDsRow {
	row := dbx.ListFwToScfEdgesForRequirementIDsRow{
		ID:                     pgID(id),
		FrameworkRequirementID: pgID(reqID),
		RequirementCode:        reqCode,
		ScfAnchorID:            pgID(anchorID),
		RelationshipType:       relType,
		Strength:               strength,
		SourceAttribution:      dbx.CrosswalkSourceAttributionCommunityDraft,
		MappingTier:            dbx.CrosswalkMappingTierDraft,
		ScfID:                  scfID,
		Family:                 family,
		AnchorTitle:            scfID + " anchor",
		UpdatedAt:              pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true},
	}
	for _, o := range opts {
		o(&row)
	}
	return row
}

func TestBuildQueue_GroupsEdgesUnderTheirRequirement(t *testing.T) {
	t.Parallel()
	version := uuid.New()
	r1, r2 := uuid.New(), uuid.New()
	e1, e2, e3 := uuid.New(), uuid.New(), uuid.New()

	got := buildQueue(version,
		[]dbx.FrameworkRequirement{req(r1, "A.5.15", "Access control"), req(r2, "A.8.24", "Cryptography")},
		[]dbx.ListFwToScfEdgesForRequirementIDsRow{
			edge(e1, r1, uuid.New(), "A.5.15", "IAC-22", "IAC", dbx.StrmRelationshipTypeSubsetOf, 0.8),
			edge(e2, r1, uuid.New(), "A.5.15", "IAC-07", "IAC", dbx.StrmRelationshipTypeIntersectsWith, 0.5),
			edge(e3, r2, uuid.New(), "A.8.24", "CRY-03", "CRY", dbx.StrmRelationshipTypeEqual, 1.0),
		},
		queueFilter{})

	if got.FrameworkVersionID != version.String() {
		t.Fatalf("framework_version_id = %q", got.FrameworkVersionID)
	}
	if len(got.Requirements) != 2 {
		t.Fatalf("requirements = %d; want 2", len(got.Requirements))
	}
	if len(got.Requirements[0].Edges) != 2 || len(got.Requirements[1].Edges) != 1 {
		t.Fatalf("edge grouping = %d/%d; want 2/1",
			len(got.Requirements[0].Edges), len(got.Requirements[1].Edges))
	}
	if got.Requirements[0].Code != "A.5.15" || got.Requirements[0].Title != "Access control" {
		t.Fatalf("requirement 0 = %+v", got.Requirements[0])
	}
	// The wire shape must carry BOTH orthogonal axes (ADR 0018): provenance and
	// trust. Collapsing them is the obsolete model slice 536a corrected.
	first := got.Requirements[0].Edges[0]
	if first.SourceAttribution == "" || first.MappingTier == "" {
		t.Fatalf("edge dropped an axis: %+v", first)
	}
}

func TestBuildQueue_SurfacesConflictsFrom536aModule(t *testing.T) {
	t.Parallel()
	version := uuid.New()
	rid := uuid.New()

	// Two `equal` claims to distinct anchors — 536a D2 rule 1, high severity.
	got := buildQueue(version,
		[]dbx.FrameworkRequirement{req(rid, "A.5.15", "Access control")},
		[]dbx.ListFwToScfEdgesForRequirementIDsRow{
			edge(uuid.New(), rid, uuid.New(), "A.5.15", "IAC-22", "IAC", dbx.StrmRelationshipTypeEqual, 1.0),
			edge(uuid.New(), rid, uuid.New(), "A.5.15", "IAC-07", "IAC", dbx.StrmRelationshipTypeEqual, 1.0),
		},
		queueFilter{})

	if got.ConflictCount == 0 {
		t.Fatal("duplicate equal claims produced no conflict")
	}
	var seen bool
	for _, c := range got.Requirements[0].Conflicts {
		if c.Reason == string(crosswalkconflict.ReasonDuplicateEqualClaim) {
			seen = true
			if c.Severity != string(crosswalkconflict.SeverityHigh) {
				t.Errorf("severity = %q; want high", c.Severity)
			}
			if len(c.EdgeIDs) != 2 || len(c.AnchorSCFIDs) != 2 {
				t.Errorf("finding did not name both edges/anchors: %+v", c)
			}
			if c.Detail == "" {
				t.Error("finding carried no operator-readable detail")
			}
		}
	}
	if !seen {
		t.Fatalf("duplicate_equal_claim missing from %+v", got.Requirements[0].Conflicts)
	}
	if got.ConflictCount != len(got.Requirements[0].Conflicts) {
		t.Fatalf("ConflictCount = %d; want %d", got.ConflictCount, len(got.Requirements[0].Conflicts))
	}
}

func TestBuildQueue_UnmappedRequirementIsDetectable(t *testing.T) {
	t.Parallel()
	// 536a D4: a requirement with zero edges is invisible to an edges-only
	// view, so the queue must pass the requirement inventory into Detect. This
	// is the assertion that the wiring does.
	rid := uuid.New()
	got := buildQueue(uuid.New(),
		[]dbx.FrameworkRequirement{req(rid, "A.9.99", "Orphan")},
		nil,
		queueFilter{})

	if len(got.Requirements) != 1 {
		t.Fatalf("requirements = %d", len(got.Requirements))
	}
	var unmapped bool
	for _, c := range got.Requirements[0].Conflicts {
		if c.Reason == string(crosswalkconflict.ReasonUnmapped) {
			unmapped = true
		}
	}
	if !unmapped {
		t.Fatalf("unmapped finding missing: %+v", got.Requirements[0].Conflicts)
	}
}

func TestBuildQueue_ConflictsOnlyFilter(t *testing.T) {
	t.Parallel()
	version := uuid.New()
	clean, dirty := uuid.New(), uuid.New()

	rows := []dbx.FrameworkRequirement{
		req(clean, "A.1.01", "Clean"),
		req(dirty, "A.2.02", "Dirty"),
	}
	edges := []dbx.ListFwToScfEdgesForRequirementIDsRow{
		edge(uuid.New(), clean, uuid.New(), "A.1.01", "IAC-22", "IAC", dbx.StrmRelationshipTypeSubsetOf, 0.8),
		edge(uuid.New(), dirty, uuid.New(), "A.2.02", "CRY-03", "CRY", dbx.StrmRelationshipTypeNoRelationship, 0.7),
	}

	all := buildQueue(version, rows, edges, queueFilter{})
	if len(all.Requirements) != 2 {
		t.Fatalf("unfiltered requirements = %d; want 2", len(all.Requirements))
	}

	filtered := buildQueue(version, rows, edges, queueFilter{conflictsOnly: true})
	if len(filtered.Requirements) != 1 || filtered.Requirements[0].Code != "A.2.02" {
		t.Fatalf("conflicts_only kept %+v", filtered.Requirements)
	}
	// Filtering must not change what a finding SAYS — it narrows the view, not
	// the heuristics' input.
	if len(filtered.Requirements[0].Conflicts) != len(all.Requirements[1].Conflicts) {
		t.Fatal("conflicts_only altered the findings for a requirement it kept")
	}
}

func TestBuildQueue_TierFilter(t *testing.T) {
	t.Parallel()
	version := uuid.New()
	draftReq, verifiedReq := uuid.New(), uuid.New()

	rows := []dbx.FrameworkRequirement{
		req(draftReq, "A.1.01", "Draft mapping"),
		req(verifiedReq, "A.2.02", "Verified mapping"),
	}
	edges := []dbx.ListFwToScfEdgesForRequirementIDsRow{
		edge(uuid.New(), draftReq, uuid.New(), "A.1.01", "IAC-22", "IAC",
			dbx.StrmRelationshipTypeSubsetOf, 0.8, withTier(dbx.CrosswalkMappingTierDraft)),
		edge(uuid.New(), verifiedReq, uuid.New(), "A.2.02", "CRY-03", "CRY",
			dbx.StrmRelationshipTypeSubsetOf, 0.8, withTier(dbx.CrosswalkMappingTierVerified)),
	}

	got := buildQueue(version, rows, edges, queueFilter{tier: crosswalktier.TierDraft})
	if len(got.Requirements) != 1 || got.Requirements[0].Code != "A.1.01" {
		t.Fatalf("tier=draft kept %+v", got.Requirements)
	}

	got = buildQueue(version, rows, edges, queueFilter{tier: crosswalktier.TierVerified})
	if len(got.Requirements) != 1 || got.Requirements[0].Code != "A.2.02" {
		t.Fatalf("tier=verified kept %+v", got.Requirements)
	}

	// An unmapped requirement has no edges and therefore no tier — a tier
	// filter must exclude it rather than crash or keep it.
	orphan := uuid.New()
	got = buildQueue(version,
		append(rows, req(orphan, "A.9.99", "Orphan")),
		edges,
		queueFilter{tier: crosswalktier.TierDraft})
	for _, r := range got.Requirements {
		if r.Code == "A.9.99" {
			t.Fatal("tier filter kept an edgeless requirement")
		}
	}
}

func TestBuildQueue_EmptySlicesNeverNil(t *testing.T) {
	t.Parallel()
	// The BFF and the UI both index into these arrays; a JSON `null` where an
	// empty array belongs is the classic "cannot read length of null" break.
	got := buildQueue(uuid.New(), nil, nil, queueFilter{})
	if got.Requirements == nil {
		t.Fatal("Requirements serialised as nil")
	}

	rid := uuid.New()
	got = buildQueue(uuid.New(),
		[]dbx.FrameworkRequirement{req(rid, "A.1.01", "Clean")},
		[]dbx.ListFwToScfEdgesForRequirementIDsRow{
			edge(uuid.New(), rid, uuid.New(), "A.1.01", "IAC-22", "IAC", dbx.StrmRelationshipTypeSubsetOf, 0.8),
		},
		queueFilter{})
	if got.Requirements[0].Conflicts == nil {
		t.Fatal("Conflicts serialised as nil for a clean requirement")
	}
	if got.Requirements[0].Edges == nil {
		t.Fatal("Edges serialised as nil")
	}
}

// TestBuildQueue_NoConflictNamesTwoRequirements is the invariant-#7 pin at the
// wire boundary: the queue may never render a finding that relates one
// requirement to another. crosswalkconflict holds this by construction; this
// asserts the flattening into ConflictWire did not reintroduce it by hanging a
// finding off the wrong requirement.
func TestBuildQueue_NoConflictNamesTwoRequirements(t *testing.T) {
	t.Parallel()
	r1, r2 := uuid.New(), uuid.New()
	e1, e2 := uuid.New(), uuid.New()

	got := buildQueue(uuid.New(),
		[]dbx.FrameworkRequirement{req(r1, "A.1.01", "One"), req(r2, "A.2.02", "Two")},
		[]dbx.ListFwToScfEdgesForRequirementIDsRow{
			edge(e1, r1, uuid.New(), "A.1.01", "IAC-22", "IAC", dbx.StrmRelationshipTypeEqual, 1.0),
			edge(e2, r2, uuid.New(), "A.2.02", "IAC-07", "IAC", dbx.StrmRelationshipTypeEqual, 1.0),
		},
		queueFilter{})

	ownEdges := map[string]map[string]bool{}
	for _, r := range got.Requirements {
		ownEdges[r.ID] = map[string]bool{}
		for _, e := range r.Edges {
			ownEdges[r.ID][e.ID] = true
		}
	}
	for _, r := range got.Requirements {
		for _, c := range r.Conflicts {
			for _, id := range c.EdgeIDs {
				if !ownEdges[r.ID][id] {
					t.Fatalf("conflict on requirement %s names edge %s belonging to another requirement",
						r.Code, id)
				}
			}
		}
	}
}

func TestParseBoundedInt32(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want int32
	}{
		{"absent falls back to default", "", defaultLimit},
		// A mistyped page size must not 400 the operator out of a browse
		// surface — it falls back rather than erroring.
		{"garbage falls back to default", "fifty", defaultLimit},
		{"in range passes through", "25", 25},
		{"zero clamps up to 1", "0", 1},
		{"negative clamps up to 1", "-5", 1},
		{"over cap clamps to max", "10000", maxLimit},
		{"at cap passes through", "200", maxLimit},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseBoundedInt32(tc.raw, defaultLimit, maxLimit); got != tc.want {
				t.Fatalf("parseBoundedInt32(%q) = %d; want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseOffset(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want int32
	}{
		{"", 0},
		{"garbage", 0},
		// A negative OFFSET is a Postgres error, so it is bounded here rather
		// than passed through.
		{"-1", 0},
		{"100", 100},
		{"99999999999", 1 << 30},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			if got := parseOffset(tc.raw); got != tc.want {
				t.Fatalf("parseOffset(%q) = %d; want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestHasTier(t *testing.T) {
	t.Parallel()
	edges := []EdgeWire{
		{MappingTier: string(crosswalktier.TierDraft)},
		{MappingTier: string(crosswalktier.TierVerified)},
	}
	if !hasTier(edges, crosswalktier.TierVerified) {
		t.Error("verified should match")
	}
	if hasTier(edges, crosswalktier.TierRejected) {
		t.Error("rejected should not match")
	}
	if hasTier(nil, crosswalktier.TierDraft) {
		t.Error("an edgeless requirement should match no tier")
	}
}
