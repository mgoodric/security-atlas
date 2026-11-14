package soc2import

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Report summarizes one import run. Used by the CLI to print a one-line
// summary and by tests to assert idempotency (second-run = all Unchanged).
type Report struct {
	FrameworkSlug      string
	FrameworkVersion   string
	FrameworkID        string
	FrameworkVersionID string
	IsNewVersion       bool

	RequirementsCreated   int
	RequirementsUpdated   int
	RequirementsUnchanged int

	// RequirementsDiffered lists the codes of existing requirements whose
	// incoming title or body differed from the stored one in a frozen
	// version. The stored text is kept: a frozen version is never updated,
	// it ships a new version instead (#1730).
	RequirementsDiffered []string

	EdgesCreated   int
	EdgesUpdated   int
	EdgesUnchanged int

	// EdgesSkipped lists the (requirement, anchor) pairs whose incoming row
	// was dropped because the existing edge carries a higher-precedence
	// source_attribution (see attributionRank). The stored edge is left
	// untouched, provenance and mapping_tier included.
	EdgesSkipped []SkippedEdge

	// EdgesUpgradedOntoRejected lists edges whose provenance this import
	// raised while their mapping_tier is 'rejected'. A rejection is a human
	// judgment the importer never overrides, so these stay hidden from
	// coverage until a reviewer reopens them (rejected -> draft, #1849).
	EdgesUpgradedOntoRejected []UpgradedEdge

	// EdgesTierReseeded lists edges whose content this import changed and
	// whose mapping_tier it therefore re-seeded per ADR 0018 §2 (#1849): a
	// tier vouches for the content a reviewer saw, so changed content restarts
	// at the tier its new source would seed at. Each re-seed also appends an
	// audit row by SystemReviewerID.
	EdgesTierReseeded []ReseededEdge

	// MappingsByAttribution tallies edges by their source_attribution
	// label. Exposed for the HITL audit summary so the orchestrator
	// can read "X community-draft mappings ingested, 0 scf_official"
	// without a second query.
	MappingsByAttribution map[string]int
}

// SkippedEdge names one crosswalk row the importer refused to apply because
// it would downgrade an existing edge's provenance.
type SkippedEdge struct {
	RequirementCode     string
	SCFAnchor           string
	ExistingAttribution string
	IncomingAttribution string
}

// UpgradedEdge names one existing edge whose provenance an import raised.
type UpgradedEdge struct {
	RequirementCode     string
	SCFAnchor           string
	PreviousAttribution string
	IncomingAttribution string
}

// ReseededEdge names one existing edge whose tier an import re-seeded.
type ReseededEdge struct {
	RequirementCode string
	SCFAnchor       string
	PreviousTier    string
	NewTier         string
}

// SystemReviewerID is the reviewer_id on a tier-transition audit row written
// by the importer rather than a person. fw_to_scf_edge_tier_transitions has a
// NOT NULL uuid reviewer column, and no atlas user has the nil uuid, so the
// row reads unambiguously as a system move; its note names the import.
var SystemReviewerID = uuid.Nil

// seedTier is the tier ADR 0018 §2 seeds an edge from this source at: an
// scf_official mapping is trusted on arrival, every other source starts at
// draft.
func seedTier(a dbx.CrosswalkSourceAttribution) dbx.CrosswalkMappingTier {
	if a == dbx.CrosswalkSourceAttributionScfOfficial {
		return dbx.CrosswalkMappingTierVerified
	}
	return dbx.CrosswalkMappingTierDraft
}

// reseedTier decides the tier of an existing edge whose content an import
// just changed (#1849). The content a reviewer vetted is gone, so the edge
// restarts at the seed tier of its new source. A rejected edge is the one
// exception: rejection is a human judgment, and only a reviewer reopens it.
// Returns the target tier and whether it differs from the current one.
func reseedTier(current dbx.CrosswalkMappingTier, incoming dbx.CrosswalkSourceAttribution) (dbx.CrosswalkMappingTier, bool) {
	if current == dbx.CrosswalkMappingTierRejected {
		return current, false
	}
	target := seedTier(incoming)
	return target, target != current
}

// attributionRank orders crosswalk provenance: scf_official > community_draft
// > org_internal. Edges are global catalog data, so a weaker source must
// never overwrite a stronger one for every tenant at once.
func attributionRank(a dbx.CrosswalkSourceAttribution) int {
	switch a {
	case dbx.CrosswalkSourceAttributionScfOfficial:
		return 3
	case dbx.CrosswalkSourceAttributionCommunityDraft:
		return 2
	case dbx.CrosswalkSourceAttributionOrgInternal:
		return 1
	}
	return 0
}

// pgxBeginner is the minimum surface Import needs from the DB handle.
// Both *pgxpool.Pool and *pgx.Conn satisfy it.
type pgxBeginner interface {
	BeginTx(ctx context.Context, txOpts pgx.TxOptions) (pgx.Tx, error)
}

// Import runs the crosswalk against the database in a single transaction.
// Returns a Report on success; rolls back on any error. The DB handle must
// have INSERT/UPDATE rights on frameworks, framework_versions,
// framework_requirements, and fw_to_scf_edges (atlas_migrate role).
func Import(ctx context.Context, db pgxBeginner, cw *Crosswalk) (Report, error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, fmt.Errorf("crosswalk: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	report, err := importIntoTx(ctx, tx, cw)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("crosswalk: commit: %w", err)
	}
	return report, nil
}

func importIntoTx(ctx context.Context, tx pgx.Tx, cw *Crosswalk) (Report, error) {
	q := dbx.New(tx)

	// 1. Upsert the framework row. Deterministic UUID derived from the
	//    framework slug so re-imports never duplicate.
	frameworkID := uuidFromString("framework-" + cw.FrameworkSlug)
	framework, err := q.UpsertFramework(ctx, dbx.UpsertFrameworkParams{
		ID:          frameworkID,
		Name:        cw.FrameworkName,
		Slug:        cw.FrameworkSlug,
		Issuer:      cw.FrameworkIssuer,
		Description: "",
	})
	if err != nil {
		return Report{}, fmt.Errorf("crosswalk: upsert framework: %w", err)
	}

	// 2. Decide if this is a new framework_version. Same demotion logic as
	//    slice 006's SCF importer — only one row per framework can be
	//    status='current' at a time.
	versions, err := q.ListFrameworkVersionsBySlug(ctx, cw.FrameworkSlug)
	if err != nil {
		return Report{}, fmt.Errorf("crosswalk: list versions: %w", err)
	}
	isNew := true
	for _, v := range versions {
		if v.Version == cw.FrameworkVersion {
			isNew = false
			break
		}
	}
	if isNew {
		if err := q.DemoteCurrentFrameworkVersions(ctx, framework.ID); err != nil {
			return Report{}, fmt.Errorf("crosswalk: demote current: %w", err)
		}
	}

	versionID := uuidFromString("fv-" + cw.FrameworkSlug + "-" + cw.FrameworkVersion)
	version, err := q.UpsertFrameworkVersion(ctx, dbx.UpsertFrameworkVersionParams{
		ID:            versionID,
		FrameworkID:   framework.ID,
		Version:       cw.FrameworkVersion,
		EffectiveFrom: parseDate(cw.ReleaseDate),
		EffectiveTo:   pgtype.Date{},
		Status:        dbx.FrameworkVersionStatusCurrent,
	})
	if err != nil {
		return Report{}, fmt.Errorf("crosswalk: upsert framework_version: %w", err)
	}

	if err := q.SetLatestVersion(ctx, dbx.SetLatestVersionParams{
		ID:              framework.ID,
		LatestVersionID: pgtype.UUID{Bytes: version.ID.Bytes, Valid: true},
	}); err != nil {
		return Report{}, fmt.Errorf("crosswalk: set latest version: %w", err)
	}

	report := Report{
		FrameworkSlug:         cw.FrameworkSlug,
		FrameworkVersion:      cw.FrameworkVersion,
		FrameworkID:           uuidString(framework.ID),
		FrameworkVersionID:    uuidString(version.ID),
		IsNewVersion:          isNew,
		MappingsByAttribution: map[string]int{},
	}

	// 3. Upsert requirement rows. Track ids for the mapping pass. The
	//    requirement UUID seed is namespaced by (slug, version, code) so an
	//    ISO `A.5.1` and a SOC 2 `CC5.1` derive distinct ids and never
	//    collide (AC-3 / invariant #1 — no per-framework control rows).
	reqIDByCode := make(map[string]pgtype.UUID, len(cw.Requirements))
	for _, r := range cw.Requirements {
		reqID := uuidFromString("req-" + cw.FrameworkSlug + "-" + cw.FrameworkVersion + "-" + r.Code)
		pofJSON, err := marshalPointsOfFocus(r.PointsOfFocus)
		if err != nil {
			return Report{}, fmt.Errorf("crosswalk: encode points of focus for %s: %w", r.Code, err)
		}

		existing, lookupErr := q.GetFrameworkRequirementByVersionAndCode(ctx, dbx.GetFrameworkRequirementByVersionAndCodeParams{
			FrameworkVersionID: version.ID,
			Code:               r.Code,
		})
		switch {
		case lookupErr == nil:
			reqIDByCode[r.Code] = existing.ID
			if existing.Title == r.Title && existing.Body == r.Body && pointsOfFocusEqual(existing.PointsOfFocus, r.PointsOfFocus) {
				report.RequirementsUnchanged++
				continue
			}
			// A frozen version's requirements are immutable: the trigger
			// would reject this UPDATE and roll back the whole import, edges
			// included. Keep the stored title and body, import the edges, and
			// report the difference (#1730).
			if isFrozenVersionStatus(version.Status) {
				report.RequirementsDiffered = append(report.RequirementsDiffered, r.Code)
				continue
			}
			if _, err := q.UpsertFrameworkRequirement(ctx, dbx.UpsertFrameworkRequirementParams{
				ID:                 existing.ID,
				FrameworkVersionID: version.ID,
				Code:               r.Code,
				Title:              r.Title,
				Body:               r.Body,
				PointsOfFocus:      pofJSON,
			}); err != nil {
				return Report{}, fmt.Errorf("crosswalk: update requirement %s: %w", r.Code, err)
			}
			report.RequirementsUpdated++
		case errors.Is(lookupErr, pgx.ErrNoRows):
			row, err := q.UpsertFrameworkRequirement(ctx, dbx.UpsertFrameworkRequirementParams{
				ID:                 reqID,
				FrameworkVersionID: version.ID,
				Code:               r.Code,
				Title:              r.Title,
				Body:               r.Body,
				PointsOfFocus:      pofJSON,
			})
			if err != nil {
				return Report{}, fmt.Errorf("crosswalk: insert requirement %s: %w", r.Code, err)
			}
			reqIDByCode[r.Code] = row.ID
			report.RequirementsCreated++
		default:
			return Report{}, fmt.Errorf("crosswalk: lookup requirement %s: %w", r.Code, lookupErr)
		}
	}

	// 4. Keep framework_versions.requirement_count in sync.
	count, err := q.CountFrameworkRequirementsForVersion(ctx, version.ID)
	if err != nil {
		return Report{}, fmt.Errorf("crosswalk: count requirements: %w", err)
	}
	if err := q.UpdateFrameworkVersionRequirementCount(ctx, dbx.UpdateFrameworkVersionRequirementCountParams{
		ID:               version.ID,
		RequirementCount: int32(count),
	}); err != nil {
		return Report{}, fmt.Errorf("crosswalk: update requirement_count: %w", err)
	}

	// 5. Upsert edges. Resolve scf_anchor strings to anchor IDs against
	//    the current SCF version (slice 006). An edge whose scf_anchor does
	//    not resolve to a real anchor is a hard, clear loader error — never
	//    a panic and never a dangling edge (AC-2 / P0-438-4 / threat-model
	//    T). Every edge is requirement -> SCF anchor; there is no
	//    requirement -> requirement path (invariant #7 / P0-438-1).
	for _, m := range cw.Mappings {
		reqID, ok := reqIDByCode[m.RequirementCode]
		if !ok {
			return Report{}, fmt.Errorf("crosswalk: mapping references unknown requirement_code %q after requirement pass", m.RequirementCode)
		}
		anchor, err := q.GetSCFAnchorBySCFID(ctx, m.SCFAnchor)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Report{}, fmt.Errorf("crosswalk: scf_anchor %q not found — import the SCF catalog first (slice 006)", m.SCFAnchor)
			}
			return Report{}, fmt.Errorf("crosswalk: lookup scf_anchor %s: %w", m.SCFAnchor, err)
		}

		attribution := m.SourceAttribution
		if attribution == "" {
			attribution = cw.SourceAttribution
		}
		relType := dbx.StrmRelationshipType(m.RelationshipType)
		srcAttr := dbx.CrosswalkSourceAttribution(attribution)

		edgeID := uuidFromString("edge-" + cw.FrameworkSlug + "-" + cw.FrameworkVersion + "-" + m.RequirementCode + "-" + m.SCFAnchor)

		// FOR UPDATE: the precedence check below reads, then writes. Without
		// the row lock a concurrent weaker import that passed its check
		// against the old row would overwrite a just-committed stronger edge.
		existing, lookupErr := q.GetFwToScfEdgeForUpdate(ctx, dbx.GetFwToScfEdgeForUpdateParams{
			FrameworkRequirementID: reqID,
			ScfAnchorID:            anchor.ID,
		})
		switch {
		case lookupErr == nil:
			if attributionRank(srcAttr) < attributionRank(existing.SourceAttribution) {
				report.EdgesSkipped = append(report.EdgesSkipped, SkippedEdge{
					RequirementCode:     m.RequirementCode,
					SCFAnchor:           m.SCFAnchor,
					ExistingAttribution: string(existing.SourceAttribution),
					IncomingAttribution: attribution,
				})
				continue
			}
			report.MappingsByAttribution[attribution]++
			if edgeContentEqual(existing, relType, m.Strength, srcAttr, m.Rationale) {
				report.EdgesUnchanged++
				continue
			}
			if _, err := q.UpdateFwToScfEdge(ctx, dbx.UpdateFwToScfEdgeParams{
				ID:                existing.ID,
				RelationshipType:  relType,
				Strength:          m.Strength,
				SourceAttribution: srcAttr,
				Rationale:         m.Rationale,
			}); err != nil {
				return Report{}, fmt.Errorf("crosswalk: update edge %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
			}
			report.EdgesUpdated++
			if target, changed := reseedTier(existing.MappingTier, srcAttr); changed {
				if err := q.SetFwToScfEdgeTier(ctx, dbx.SetFwToScfEdgeTierParams{ID: existing.ID, MappingTier: target}); err != nil {
					return Report{}, fmt.Errorf("crosswalk: reseed tier %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
				}
				if _, err := q.InsertFwToScfEdgeTierTransition(ctx, dbx.InsertFwToScfEdgeTierTransitionParams{
					EdgeID:     existing.ID,
					ReviewerID: pgtype.UUID{Bytes: SystemReviewerID, Valid: true},
					FromTier:   existing.MappingTier,
					ToTier:     target,
					Note:       fmt.Sprintf("crosswalk import %s@%s (%s) changed the mapping content; tier re-seeded per ADR 0018 §2", cw.FrameworkSlug, cw.FrameworkVersion, attribution),
				}); err != nil {
					return Report{}, fmt.Errorf("crosswalk: audit tier reseed %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
				}
				report.EdgesTierReseeded = append(report.EdgesTierReseeded, ReseededEdge{
					RequirementCode: m.RequirementCode,
					SCFAnchor:       m.SCFAnchor,
					PreviousTier:    string(existing.MappingTier),
					NewTier:         string(target),
				})
			}
			if existing.MappingTier == dbx.CrosswalkMappingTierRejected && srcAttr != existing.SourceAttribution {
				report.EdgesUpgradedOntoRejected = append(report.EdgesUpgradedOntoRejected, UpgradedEdge{
					RequirementCode:     m.RequirementCode,
					SCFAnchor:           m.SCFAnchor,
					PreviousAttribution: string(existing.SourceAttribution),
					IncomingAttribution: attribution,
				})
			}
		case errors.Is(lookupErr, pgx.ErrNoRows):
			report.MappingsByAttribution[attribution]++
			if _, err := q.InsertFwToScfEdge(ctx, dbx.InsertFwToScfEdgeParams{
				ID:                     edgeID,
				FrameworkRequirementID: reqID,
				ScfAnchorID:            anchor.ID,
				RelationshipType:       relType,
				Strength:               m.Strength,
				SourceAttribution:      srcAttr,
				Rationale:              m.Rationale,
			}); err != nil {
				return Report{}, fmt.Errorf("crosswalk: insert edge %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
			}
			// The column defaults to draft; an official edge seeds at verified
			// (ADR 0018 §2), as the tier migration did for existing rows. A seed
			// is not a transition, so no audit row.
			if tier := seedTier(srcAttr); tier != dbx.CrosswalkMappingTierDraft {
				if err := q.SetFwToScfEdgeTier(ctx, dbx.SetFwToScfEdgeTierParams{ID: edgeID, MappingTier: tier}); err != nil {
					return Report{}, fmt.Errorf("crosswalk: seed tier %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
				}
			}
			report.EdgesCreated++
		default:
			return Report{}, fmt.Errorf("crosswalk: lookup edge %s->%s: %w", m.RequirementCode, m.SCFAnchor, lookupErr)
		}
	}

	return report, nil
}

// marshalPointsOfFocus encodes a requirement's points of focus for the
// framework_requirements.points_of_focus JSONB column; none encodes as [].
func marshalPointsOfFocus(pofs []PointOfFocus) ([]byte, error) {
	if len(pofs) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(pofs)
}

// pointsOfFocusEqual compares a stored points_of_focus column with incoming
// points of focus. It decodes rather than comparing bytes because Postgres
// re-renders JSONB; an undecodable stored value counts as different.
func pointsOfFocusEqual(stored []byte, incoming []PointOfFocus) bool {
	var got []PointOfFocus
	if len(stored) > 0 {
		if err := json.Unmarshal(stored, &got); err != nil {
			return false
		}
	}
	if len(got) != len(incoming) {
		return false
	}
	for i := range got {
		if got[i].Code != incoming[i].Code || !slices.Equal(got[i].SCFAnchors, incoming[i].SCFAnchors) {
			return false
		}
	}
	return true
}

// isFrozenVersionStatus mirrors enforce_framework_requirement_immutability:
// a version in one of these states rejects UPDATEs of its requirements.
func isFrozenVersionStatus(s dbx.FrameworkVersionStatus) bool {
	switch s {
	case dbx.FrameworkVersionStatusCurrent, dbx.FrameworkVersionStatusLegacy, dbx.FrameworkVersionStatusWithdrawn:
		return true
	}
	return false
}

func edgeContentEqual(row dbx.FwToScfEdge, relType dbx.StrmRelationshipType, strength float64, srcAttr dbx.CrosswalkSourceAttribution, rationale string) bool {
	if row.RelationshipType != relType {
		return false
	}
	if row.Strength != strength {
		return false
	}
	if row.SourceAttribution != srcAttr {
		return false
	}
	if row.Rationale != rationale {
		return false
	}
	return true
}

func uuidFromString(seed string) pgtype.UUID {
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("security-atlas/soc2import/"+seed))
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func parseDate(s string) pgtype.Date {
	if s == "" {
		return pgtype.Date{}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}
