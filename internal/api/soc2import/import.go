package soc2import

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
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

	// RequirementsDifferedFields names, per code in RequirementsDiffered,
	// which of "title", "body" and "points_of_focus" differed, in that order
	// (#2026).
	RequirementsDifferedFields map[string][]string

	// LegacyPointsOfFocus lists the requirement rows of the target version
	// whose code is a SOC 2 point of focus (`CC1.1-POF1`) and that the
	// crosswalk does not list, sorted. They are left from an import before
	// points of focus rolled up onto their criterion (#1731), and they still
	// count toward requirement_count and the coverage denominator; only an
	// import into a new framework version drops them (#2026).
	LegacyPointsOfFocus []string

	// LegacyPointsOfFocusEdgesRetired counts the EdgesRetired that sat on a
	// LegacyPointsOfFocus row: this import's source wrote them, and the
	// roll-up moved them to the criterion, so the retire pass (#1971) dropped
	// them. The rows they leave behind count as uncovered.
	LegacyPointsOfFocusEdgesRetired int

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

	// EdgesRetired lists the catalog edges this import retired because the
	// crosswalk no longer lists them (#1971). A retired edge keeps its row and
	// tier history but drops out of coverage. Only edges this crosswalk's
	// source wrote are retired; see staleEdges.
	EdgesRetired []CatalogEdge

	// EdgesRestored lists retired edges this crosswalk lists again; their
	// retired_at is cleared (#1971).
	EdgesRestored []CatalogEdge

	// EdgesCarriedRetired lists the edges an SCF edge remap carried from an
	// edge this import retired, which it retired with their source (#3065).
	// A carried edge's crosswalk_source is the remap ('scf-remap:<proposal>'),
	// so the retire pass never owns it directly; see settleCarriedEdges.
	EdgesCarriedRetired []CatalogEdge

	// EdgesCarriedRestored lists the carried edges this import's retire pass
	// had retired and restores now that their source is listed again (#3065).
	EdgesCarriedRestored []CatalogEdge

	// EdgesUnlisted lists live edges the crosswalk does not list but did not
	// retire, because another source wrote them (another crosswalk_source or
	// source_attribution). They are reported so an operator can see them.
	EdgesUnlisted []CatalogEdge

	// SCFRelease is the SCF release the mappings were bound in: the
	// crosswalk's declared release (#1771, required since #1774).
	SCFRelease string

	// Warnings are non-fatal notes for the operator.
	Warnings []string

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

// CatalogEdge names one catalog edge in an import report. Source is the
// edge's recorded crosswalk_source, or LegacySource when it has none.
type CatalogEdge struct {
	RequirementCode string
	SCFAnchor       string
	Attribution     string
	Tier            string
	Source          string
}

// LegacySource is how a report names an edge with no recorded
// crosswalk_source: one written before #1971 added the column, or by a seed
// that does not set it.
const LegacySource = "(none)"

// requirementCode is one (requirement row, SCF code) pair a crosswalk lists.
type requirementCode struct {
	requirementID [16]byte
	scfID         string
}

// spareCurrentReleaseEdges drops from retire every edge that is the
// current-release counterpart of a listed mapping, when the crosswalk binds
// in an older release (#3061). The bundled crosswalks bind in the sample's
// release, which an install on its own SCF release loads as legacy; an edge
// the same crosswalk wrote on the current release's anchor with the same code
// (before #1773 it bound in the current release) still stands for the listed
// mapping, and retiring it would drop the requirement from default coverage,
// which reads the current release (#2203). The retire pass exists for the
// other direction: an edge left on an older release than the one the
// crosswalk binds in (#1971).
func spareCurrentReleaseEdges(ctx context.Context, q *dbx.Queries, release string, retire []dbx.ListFwToScfEdgesForVersionForUpdateRow, listed map[requirementCode]bool) ([]dbx.ListFwToScfEdgesForVersionForUpdateRow, error) {
	// declaredSCFRelease never returns an empty release (#1774).
	if len(retire) == 0 {
		return retire, nil
	}
	bound, err := scfrelease.Lookup(ctx, q, release)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: scf_release: %w", err)
	}
	if bound.Status == string(dbx.FrameworkVersionStatusCurrent) {
		return retire, nil
	}
	current, err := scfrelease.Lookup(ctx, q, "")
	if errors.Is(err, scfrelease.ErrNoCurrentRelease) {
		return retire, nil
	}
	if err != nil {
		return nil, fmt.Errorf("crosswalk: current SCF release: %w", err)
	}
	out := retire[:0:0]
	for _, e := range retire {
		if listed[requirementCode{e.FrameworkRequirementID.Bytes, e.ScfID}] {
			anchor, err := q.GetSCFAnchorByVersionAndSCFID(ctx, dbx.GetSCFAnchorByVersionAndSCFIDParams{
				FrameworkVersionID: current.ID,
				ScfID:              e.ScfID,
			})
			switch {
			case err == nil && anchor.ID.Bytes == e.ScfAnchorID.Bytes:
				continue
			case err != nil && !errors.Is(err, pgx.ErrNoRows):
				return nil, fmt.Errorf("crosswalk: lookup scf_anchor %s in SCF release %s: %w", e.ScfID, current.Version, err)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// edgeKey identifies an edge the way the edges table does: one edge per
// (requirement row, anchor row) pair. It is keyed on row ids, not codes, so
// an edge on the same SCF code in an older SCF release (a different anchor
// row) counts as unlisted and is retired (#1971).
type edgeKey struct {
	requirementID [16]byte
	anchorID      [16]byte
}

// ownsEdge reports whether an import from source may retire an edge recorded
// under edgeSource. Each source owns the edges it wrote. An edge with no
// recorded source predates #1971, when the YAML crosswalks were the only
// importer, so a YAML import (or an in-memory crosswalk with no Source)
// owns it too; a workbook import never retires one.
func ownsEdge(source string, edgeSource *string) bool {
	if edgeSource == nil {
		return source == "" || strings.HasPrefix(source, yamlSourcePrefix)
	}
	return *edgeSource == source
}

// staleEdges splits the stored edges of one framework version that the
// crosswalk does not list into the ones to retire and the ones to leave.
//
// A crosswalk is the source of truth for the edges it writes, so an edge it
// stopped listing is retired: after an SCF remap or a new SCF release moves a
// mapping to another anchor row, the edge left on the old one would otherwise
// keep counting as coverage (#1971). An import only retires what it owns, though: an edge
// another source wrote (see ownsEdge), or one with a source_attribution the
// crosswalk never uses, is left in place and reported. fw_to_scf_edges holds
// catalog (platform) edges only; it has no tenant rows to touch.
func staleEdges(stored []dbx.ListFwToScfEdgesForVersionForUpdateRow, listed map[edgeKey]bool, source string, owned map[dbx.CrosswalkSourceAttribution]bool) (retire []dbx.ListFwToScfEdgesForVersionForUpdateRow, keep []dbx.ListFwToScfEdgesForVersionForUpdateRow) {
	for _, e := range stored {
		if listed[edgeKey{e.FrameworkRequirementID.Bytes, e.ScfAnchorID.Bytes}] {
			continue
		}
		if ownsEdge(source, e.CrosswalkSource) && owned[e.SourceAttribution] {
			retire = append(retire, e)
		} else {
			keep = append(keep, e)
		}
	}
	return retire, keep
}

func catalogEdge(e dbx.ListFwToScfEdgesForVersionForUpdateRow) CatalogEdge {
	return CatalogEdge{
		RequirementCode: e.RequirementCode,
		SCFAnchor:       e.ScfID,
		Attribution:     string(e.SourceAttribution),
		Tier:            string(e.MappingTier),
		Source:          sourceLabel(e.CrosswalkSource),
	}
}

// sourceLabel names a recorded crosswalk_source for a report or audit note.
func sourceLabel(s *string) string {
	if s == nil {
		return LegacySource
	}
	return *s
}

// RetiredBySource counts the retired edges by their recorded source, for the
// import summary.
func (r Report) RetiredBySource() map[string]int {
	out := map[string]int{}
	for _, p := range r.EdgesRetired {
		out[p.Source]++
	}
	return out
}

// retireNote and restoreNote are the tier-transition notes the importer
// writes when it retires or restores an edge. The tier does not change, so
// from_tier = to_tier; the row exists to keep the edge's history complete.
func retireNote(source string) string {
	return "retired: dropped by crosswalk " + sourceLabel(sourceParam(source))
}

func restoreNote(source string) string {
	return "restored: listed again by crosswalk " + sourceLabel(sourceParam(source))
}

// sourceParam is the crosswalk_source an import stamps on the edges it
// writes; NULL for a crosswalk with no Source.
func sourceParam(source string) *string {
	if source == "" {
		return nil
	}
	return &source
}

// ErrSCFReleaseRequired is returned for a crosswalk that declares no
// scf_release (#1774). Its codes are never bound in the current release
// instead; the import fails before anything is written. The message is the
// one-line fix.
var ErrSCFReleaseRequired = fmt.Errorf("crosswalk: %w: the crosswalk declares no scf_release; add a top-level line naming the SCF release its scf_anchor codes are from, e.g. `scf_release: \"test-2026.3\"` (for a workbook with no \"SCF <release>\" sheet, pass --scf-release)", scfrelease.ErrReleaseRequired)

// sheetReleaseNotLoaded is the error, formatted with the sheet's release
// twice, for a workbook whose "SCF <release>" sheet names a release
// that is not loaded (#1774; before it, the codes bound in the current
// release with a warning). Renaming the sheet is the supported way to
// import into another loaded release: --scf-release cannot contradict the
// sheet (#1771).
const sheetReleaseNotLoaded = "workbook sheet \"SCF %s\" names SCF release %q, which is not loaded, so nothing was written; load it with `atlas-cli catalog import-scf`, or rename the sheet to \"SCF <release>\" for a loaded release (or convert the workbook to crosswalk YAML declaring that scf_release) and import again"

// auditSystemTransition appends a tier-transition row by SystemReviewerID:
// the importer's record of a move it made (a retire, restore or re-seed), in
// the same transaction as the move.
func auditSystemTransition(ctx context.Context, q *dbx.Queries, edgeID pgtype.UUID, from, to dbx.CrosswalkMappingTier, note string) error {
	_, err := q.InsertFwToScfEdgeTierTransition(ctx, dbx.InsertFwToScfEdgeTierTransitionParams{
		EdgeID:     edgeID,
		ReviewerID: pgtype.UUID{Bytes: SystemReviewerID, Valid: true},
		FromTier:   from,
		ToTier:     to,
		Note:       note,
	})
	return err
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

	// 0. Pick the SCF release the mappings bind in before writing anything,
	//    so an unknown release fails the import cleanly (#1771).
	release, err := declaredSCFRelease(ctx, q, cw)
	if err != nil {
		return Report{}, err
	}
	bind, err := newAnchorBinder(ctx, q, release)
	if err != nil {
		return Report{}, err
	}

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
		SCFRelease:            release,
		MappingsByAttribution: map[string]int{},
	}

	// 3. Upsert requirement rows. Track ids for the mapping pass. The
	//    requirement UUID seed is namespaced by (slug, version, code) so an
	//    ISO `A.5.1` and a SOC 2 `CC5.1` derive distinct ids and never
	//    collide (AC-3 / invariant #1 — no per-framework control rows).
	reqIDByCode := make(map[string]pgtype.UUID, len(cw.Requirements))
	for _, r := range cw.Requirements {
		reqID := uuidFromString("req-" + cw.FrameworkSlug + "-" + cw.FrameworkVersion + "-" + r.Code)
		// A point of focus names SCF anchors as metadata, not edges, so no
		// edge lookup would catch a misspelt one: bind each here (#2026).
		for _, p := range r.PointsOfFocus {
			for _, a := range p.SCFAnchors {
				if _, err := bind(a); err != nil {
					return Report{}, fmt.Errorf("crosswalk: requirement %s point of focus %s: %w", r.Code, p.Code, err)
				}
			}
		}
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
			differed := differedFields(existing, r)
			if len(differed) == 0 {
				report.RequirementsUnchanged++
				continue
			}
			// A frozen version's requirements are immutable: the trigger
			// would reject this UPDATE and roll back the whole import, edges
			// included. Keep the stored title and body, import the edges, and
			// report the difference (#1730).
			if isFrozenVersionStatus(version.Status) {
				report.RequirementsDiffered = append(report.RequirementsDiffered, r.Code)
				if report.RequirementsDifferedFields == nil {
					report.RequirementsDifferedFields = map[string][]string{}
				}
				report.RequirementsDifferedFields[r.Code] = differed
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

	// 3b. Find point-of-focus requirement rows left from before the roll-up
	//     (#1731). The importer never deletes a requirement, so they stay in
	//     the denominator; report them so the operator imports into a new
	//     framework version instead (#2026).
	legacyPOF, err := legacyPointsOfFocus(ctx, q, version.ID, cw)
	if err != nil {
		return Report{}, err
	}
	report.LegacyPointsOfFocus = legacyPOF

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

	// 5. Upsert edges. Resolve scf_anchor strings to anchor IDs within the
	//    crosswalk's SCF release (#1771), or the current SCF version when it
	//    declares none (slice 006). An edge whose scf_anchor does
	//    not resolve to a real anchor is a hard, clear loader error — never
	//    a panic and never a dangling edge (AC-2 / P0-438-4 / threat-model
	//    T). Every edge is requirement -> SCF anchor; there is no
	//    requirement -> requirement path (invariant #7 / P0-438-1).
	//
	//    listed collects every (requirement row, anchor row) pair the
	//    crosswalk names, skipped rows included, for the retire pass (6).
	listed := make(map[edgeKey]bool, len(cw.Mappings))
	listedCodes := make(map[requirementCode]bool, len(cw.Mappings))
	for _, m := range cw.Mappings {
		reqID, ok := reqIDByCode[m.RequirementCode]
		if !ok {
			return Report{}, fmt.Errorf("crosswalk: mapping references unknown requirement_code %q after requirement pass", m.RequirementCode)
		}
		anchor, err := bind(m.SCFAnchor)
		if err != nil {
			return Report{}, err
		}

		attribution := m.SourceAttribution
		if attribution == "" {
			attribution = cw.SourceAttribution
		}
		relType := dbx.StrmRelationshipType(m.RelationshipType)
		srcAttr := dbx.CrosswalkSourceAttribution(attribution)

		listed[edgeKey{reqID.Bytes, anchor.ID.Bytes}] = true
		listedCodes[requirementCode{reqID.Bytes, m.SCFAnchor}] = true

		// The anchor row id is in the seed: a new SCF release mints new
		// anchor rows under the same codes, and the edge to the new row must
		// not collide with the (now retired) edge to the old one (#1971).
		edgeID := uuidFromString("edge-" + cw.FrameworkSlug + "-" + cw.FrameworkVersion + "-" + m.RequirementCode + "-" + m.SCFAnchor + "-" + uuidString(anchor.ID))

		// FOR UPDATE: the precedence check below reads, then writes. Without
		// the row lock a concurrent weaker import that passed its check
		// against the old row would overwrite a just-committed stronger edge.
		// This lookup also finds a retired edge.
		existing, lookupErr := q.GetFwToScfEdgeForUpdate(ctx, dbx.GetFwToScfEdgeForUpdateParams{
			FrameworkRequirementID: reqID,
			ScfAnchorID:            anchor.ID,
		})
		switch {
		case lookupErr == nil:
			// A retired edge the crosswalk lists again comes back (#1971).
			// Its old content no longer stands for anything, so the
			// precedence check below does not protect it: the restore
			// precedes the normal content comparison.
			restored := false
			if existing.RetiredAt.Valid {
				if err := q.RestoreFwToScfEdge(ctx, existing.ID); err != nil {
					return Report{}, fmt.Errorf("crosswalk: restore edge %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
				}
				if err := auditSystemTransition(ctx, q, existing.ID, existing.MappingTier, existing.MappingTier, restoreNote(cw.Source)); err != nil {
					return Report{}, fmt.Errorf("crosswalk: audit restore %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
				}
				report.EdgesRestored = append(report.EdgesRestored, CatalogEdge{
					RequirementCode: m.RequirementCode,
					SCFAnchor:       m.SCFAnchor,
					Attribution:     string(existing.SourceAttribution),
					Tier:            string(existing.MappingTier),
					Source:          sourceLabel(existing.CrosswalkSource),
				})
				restored = true
			}
			if !restored && attributionRank(srcAttr) < attributionRank(existing.SourceAttribution) {
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
				CrosswalkSource:   sourceParam(cw.Source),
			}); err != nil {
				return Report{}, fmt.Errorf("crosswalk: update edge %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
			}
			report.EdgesUpdated++
			if target, changed := reseedTier(existing.MappingTier, srcAttr); changed {
				if err := q.SetFwToScfEdgeTier(ctx, dbx.SetFwToScfEdgeTierParams{ID: existing.ID, MappingTier: target}); err != nil {
					return Report{}, fmt.Errorf("crosswalk: reseed tier %s->%s: %w", m.RequirementCode, m.SCFAnchor, err)
				}
				if err := auditSystemTransition(ctx, q, existing.ID, existing.MappingTier, target,
					fmt.Sprintf("crosswalk import %s@%s (%s) changed the mapping content; tier re-seeded per ADR 0018 §2", cw.FrameworkSlug, cw.FrameworkVersion, attribution)); err != nil {
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
				CrosswalkSource:        sourceParam(cw.Source),
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

	// 6. Retire the edges this crosswalk wrote and no longer lists (#1971),
	//    in the same transaction as the upsert so coverage never sees a
	//    half-applied crosswalk. A retired edge keeps its row and its
	//    append-only tier history; a transition row records the retirement.
	owned := map[dbx.CrosswalkSourceAttribution]bool{dbx.CrosswalkSourceAttribution(cw.SourceAttribution): true}
	for _, m := range cw.Mappings {
		if m.SourceAttribution != "" {
			owned[dbx.CrosswalkSourceAttribution(m.SourceAttribution)] = true
		}
	}
	stored, err := q.ListFwToScfEdgesForVersionForUpdate(ctx, version.ID)
	if err != nil {
		return Report{}, fmt.Errorf("crosswalk: list edges to retire: %w", err)
	}
	retire, keep := staleEdges(stored, listed, cw.Source, owned)
	retire, err = spareCurrentReleaseEdges(ctx, q, release, retire, listedCodes)
	if err != nil {
		return Report{}, err
	}
	for _, e := range retire {
		if err := q.RetireFwToScfEdge(ctx, e.ID); err != nil {
			return Report{}, fmt.Errorf("crosswalk: retire edge %s->%s: %w", e.RequirementCode, e.ScfID, err)
		}
		if err := auditSystemTransition(ctx, q, e.ID, e.MappingTier, e.MappingTier, retireNote(cw.Source)); err != nil {
			return Report{}, fmt.Errorf("crosswalk: audit retire %s->%s: %w", e.RequirementCode, e.ScfID, err)
		}
		report.EdgesRetired = append(report.EdgesRetired, catalogEdge(e))
		if slices.Contains(legacyPOF, e.RequirementCode) {
			report.LegacyPointsOfFocusEdgesRetired++
		}
	}

	// 7. Retire the copies an SCF edge remap carried from the edges just
	//    retired, and restore the ones whose source is listed again (#3065).
	carriedRetired, err := settleCarriedEdges(ctx, tx, q, version.ID, cw.Source, owned, listedCodes, &report)
	if err != nil {
		return Report{}, err
	}
	for _, e := range keep {
		if carriedRetired[e.ID.Bytes] {
			continue
		}
		report.EdgesUnlisted = append(report.EdgesUnlisted, catalogEdge(e))
	}

	return report, nil
}

// declaredSCFRelease returns the release a crosswalk binds in: the one it
// declares (YAML scf_release, --scf-release, or a workbook's "SCF <release>"
// sheet), strictly. None declared is ErrSCFReleaseRequired; a sheet release
// that is not loaded is scfrelease.ErrUnknownRelease with the sheet named
// (#1774). Neither falls back to the current release.
func declaredSCFRelease(ctx context.Context, q *dbx.Queries, cw *Crosswalk) (string, error) {
	if cw.SCFRelease == "" {
		return "", ErrSCFReleaseRequired
	}
	if !cw.SCFReleaseFromSheet {
		return cw.SCFRelease, nil
	}
	_, err := scfrelease.Lookup(ctx, q, cw.SCFRelease)
	switch {
	case err == nil:
		return cw.SCFRelease, nil
	case errors.Is(err, scfrelease.ErrUnknownRelease):
		return "", fmt.Errorf("crosswalk: %w: "+sheetReleaseNotLoaded, scfrelease.ErrUnknownRelease, cw.SCFRelease, cw.SCFRelease)
	default:
		return "", fmt.Errorf("crosswalk: scf_release: %w", err)
	}
}

// newAnchorBinder returns the function that turns a mapping's scf_anchor
// code into an anchor row. The release is looked up once
// (scfrelease.ErrUnknownRelease when it is not loaded) and each code binds
// within it only (scfrelease.ErrCodeNotInRelease when it is not there): a
// declared release never re-binds to the current one. release is never
// empty here; declaredSCFRelease refuses that first.
func newAnchorBinder(ctx context.Context, q *dbx.Queries, release string) (func(code string) (dbx.ScfAnchor, error), error) {
	if release == "" {
		return nil, ErrSCFReleaseRequired
	}
	rel, err := scfrelease.Lookup(ctx, q, release)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: scf_release: %w", err)
	}
	return func(code string) (dbx.ScfAnchor, error) {
		anchor, err := q.GetSCFAnchorByVersionAndSCFID(ctx, dbx.GetSCFAnchorByVersionAndSCFIDParams{
			FrameworkVersionID: rel.ID,
			ScfID:              code,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.ScfAnchor{}, fmt.Errorf("crosswalk: %w: scf_anchor %q is not in SCF release %q", scfrelease.ErrCodeNotInRelease, code, rel.Version)
		}
		if err != nil {
			return dbx.ScfAnchor{}, fmt.Errorf("crosswalk: lookup scf_anchor %s in SCF release %s: %w", code, rel.Version, err)
		}
		return anchor, nil
	}, nil
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

// differedFields names the fields of an incoming requirement that differ
// from the stored row, in a fixed order; none when the row is unchanged.
func differedFields(existing dbx.FrameworkRequirement, r Requirement) []string {
	var fields []string
	if existing.Title != r.Title {
		fields = append(fields, "title")
	}
	if existing.Body != r.Body {
		fields = append(fields, "body")
	}
	if !pointsOfFocusEqual(existing.PointsOfFocus, r.PointsOfFocus) {
		fields = append(fields, "points_of_focus")
	}
	return fields
}

// legacyPointsOfFocus lists the version's stored requirement codes that are
// point-of-focus codes the crosswalk does not list, sorted.
func legacyPointsOfFocus(ctx context.Context, q *dbx.Queries, versionID pgtype.UUID, cw *Crosswalk) ([]string, error) {
	stored, err := q.ListFrameworkRequirementsForVersion(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: list requirements: %w", err)
	}
	listed := make(map[string]struct{}, len(cw.Requirements))
	for _, r := range cw.Requirements {
		listed[r.Code] = struct{}{}
	}
	var codes []string
	for _, s := range stored {
		if _, ok := listed[s.Code]; ok {
			continue
		}
		if _, _, ok := parsePointOfFocus(s.Code); ok {
			codes = append(codes, s.Code)
		}
	}
	slices.Sort(codes)
	return codes, nil
}

// LegacyPointsOfFocusWarning renders LegacyPointsOfFocus as one operator
// warning, or "" when there are none (#2026).
func (r Report) LegacyPointsOfFocusWarning() string {
	if len(r.LegacyPointsOfFocus) == 0 {
		return ""
	}
	return fmt.Sprintf("framework version %s keeps %d point-of-focus requirement row(s) from before points of focus rolled up onto their criterion (%s); they still count toward requirement_count and coverage, and this import retired %d edge(s) on them; import into a new --framework-version to drop them",
		r.FrameworkVersion, len(r.LegacyPointsOfFocus), strings.Join(r.LegacyPointsOfFocus, ", "), r.LegacyPointsOfFocusEdgesRetired)
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
