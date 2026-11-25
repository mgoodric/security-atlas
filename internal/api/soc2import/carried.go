package soc2import

// Carried edges (#3065).
//
// An SCF edge remap (#1777) copies a catalog edge onto an anchor in another
// release: the carry-forward (#2315) does it on every SCF upgrade and on a
// BYO install (#3061), and a super-admin does it by approving a proposal.
// The copy records crosswalk_source 'scf-remap:<proposal id>', so ownsEdge
// never gives it to a crosswalk import, and a mapping the crosswalk later
// dropped left the copy live in default coverage while its source was
// retired.
//
// The retire pass now follows the copy back to its source through the
// approved proposal (scf_edge_remap_proposals.new_edge_id -> edge_id), up a
// chain of remaps to the first edge that is not a copy: the root. A copy is
// retired when its root is an edge this import owns, the root is retired,
// and the crosswalk lists none of the chain's codes for the requirement. It
// is restored when that stops being true, but only if this pass retired it
// (its newest tier-transition note starts with carriedRetireMarker).
//
// A live copy whose own code the crosswalk lists for its requirement takes
// the crosswalk's STRM content, in place (#3073). Without it, a remap of X
// to Y followed by a crosswalk that moves the requirement from X to Y left
// X's relationship, strength and rationale on the copy default coverage
// reads, because the carry-forward finds the pair mapped and closes the
// proposal (scfimport targetEdgeExistsSQL). A copy a person has moved
// through the tier workflow keeps its content.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// remapSourcePrefix is the crosswalk_source scf_edge_remap_decide stamps on
// the edge it creates, followed by the proposal id.
const remapSourcePrefix = "scf-remap:"

// carriedEdgesSQL lists every edge under one framework version that an edge
// remap carried from another edge, live or retired, row-locked, with that
// source edge, and whether a person (any reviewer but SystemReviewerID)
// has ever transitioned the edge. Only a source on the same requirement counts, which keeps the
// pair inside the version. It runs after the retire pass, so a source the
// pass just retired reads as retired.
const carriedEdgesSQL = `
SELECT
    e.id, e.framework_requirement_id, fr.code, a.scf_id,
    e.source_attribution::text, e.mapping_tier::text, e.crosswalk_source,
    e.retired_at IS NOT NULL,
    e.relationship_type, e.strength, e.rationale,
    EXISTS (SELECT 1 FROM fw_to_scf_edge_tier_transitions t
            WHERE t.edge_id = e.id AND t.reviewer_id <> '00000000-0000-0000-0000-000000000000'),
    s.id, sa.scf_id, s.source_attribution::text, s.crosswalk_source,
    s.retired_at IS NOT NULL
FROM fw_to_scf_edges e
JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
JOIN scf_anchors a ON a.id = e.scf_anchor_id
JOIN scf_edge_remap_proposals p
  ON p.new_edge_id = e.id AND e.crosswalk_source = '` + remapSourcePrefix + `' || p.id::text
JOIN fw_to_scf_edges s
  ON s.id = p.edge_id AND s.framework_requirement_id = e.framework_requirement_id
JOIN scf_anchors sa ON sa.id = s.scf_anchor_id
WHERE fr.framework_version_id = $1
ORDER BY fr.code, a.scf_id
FOR UPDATE OF e`

// latestTransitionNoteSQL reads the note of an edge's newest tier-transition
// row, or none.
const latestTransitionNoteSQL = `
SELECT note FROM fw_to_scf_edge_tier_transitions
WHERE edge_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1`

// carriedEdge is one row of carriedEdgesSQL.
type carriedEdge struct {
	id, requirementID      [16]byte
	requirementCode, scfID string
	attribution, tier      string
	source                 *string
	retired                bool
	content                strmContent
	humanMoved             bool
	sourceID               [16]byte
	sourceSCFID            string
	sourceAttribution      string
	sourceCrosswalkSource  *string
	sourceRetired          bool
}

// carriedRetireMarker is the machine-readable start of the tier-transition
// note this pass writes when it retires a carried edge: "carried-retire:"
// and the import's source, Go-quoted. Stable format: the restore matches on
// this prefix alone (carriedRetiredBy), so changing it strands every copy
// already retired under the old one. The quoting closes the source, so
// "yaml:a" never matches a note written for "yaml:ab". Pinned by
// TestCarriedNotes.
func carriedRetireMarker(source string) string {
	return "carried-retire:" + strconv.Quote(sourceLabel(sourceParam(source)))
}

// carriedRetireNote is carriedRetireMarker followed by human text, which is
// free to change.
func carriedRetireNote(source string) string {
	return carriedRetireMarker(source) + " retired: carried from an edge dropped by crosswalk " + sourceLabel(sourceParam(source))
}

// carriedRetiredBy reports whether note is a carried-retire note this
// import's source wrote.
func carriedRetiredBy(note, source string) bool {
	return strings.HasPrefix(note, carriedRetireMarker(source))
}

// carriedSyncMarker is the machine-readable start of the tier-transition
// note written when a crosswalk's content replaces a carried edge's (#3073):
// "carried-sync:" and the import's source, Go-quoted, as carriedRetireMarker.
func carriedSyncMarker(source string) string {
	return "carried-sync:" + strconv.Quote(sourceLabel(sourceParam(source)))
}

// carriedSyncNote records the content the sync replaced, so the copy's
// history keeps it: the row is updated in place.
func carriedSyncNote(source string, was strmContent) string {
	return fmt.Sprintf("%s content set from crosswalk %s; was %s %s %s %q",
		carriedSyncMarker(source), sourceLabel(sourceParam(source)),
		was.relType, strconv.FormatFloat(was.strength, 'f', -1, 64), was.attribution, was.rationale)
}

// carriedRestoreNote is the tier-transition note for a carried edge restored
// with its source.
func carriedRestoreNote(source string) string {
	return "restored: carried from an edge listed again by crosswalk " + sourceLabel(sourceParam(source))
}

// carriedDecision is what the retire pass does with one carried edge.
type carriedDecision int

const (
	carriedLeave carriedDecision = iota
	carriedRetire
	carriedRestore
)

// carriedSync is what the content sync does with one live carried edge.
type carriedSync int

const (
	carriedSyncNone carriedSync = iota
	carriedSyncApply
	carriedSyncHeldBack
)

// decideCarriedSync decides whether a live carried edge takes the crosswalk
// row listed for its own code (#3073). It needs a row that differs from the
// edge's content. A person's move holds it back, as does a stored
// attribution that outranks the row's: the same precedence rule an owned
// edge gets.
func decideCarriedSync(e carriedEdge, row strmContent, rowListed bool) carriedSync {
	if !rowListed || row == e.content {
		return carriedSyncNone
	}
	if e.humanMoved || attributionRank(row.attribution) < attributionRank(e.content.attribution) {
		return carriedSyncHeldBack
	}
	return carriedSyncApply
}

// decideCarried decides each carried edge from the chain back to its root.
// owns reports whether this import owns a root edge (source and
// attribution); listed reports whether the crosswalk lists a code for a
// requirement. The walk stops at an edge it has seen, so a cycle (which
// scf_edge_remap_decide cannot make, but the rows do not forbid) ends with
// no root and leaves the edge alone.
func decideCarried(edges []carriedEdge, owns func(source *string, attribution string) bool, listed func(requirementID [16]byte, scfID string) bool) map[[16]byte]carriedDecision {
	byID := make(map[[16]byte]carriedEdge, len(edges))
	for _, e := range edges {
		byID[e.id] = e
	}
	out := make(map[[16]byte]carriedDecision, len(edges))
	for _, e := range edges {
		cur, codes, rooted := carriedRoot(e, byID)
		if !rooted || !owns(cur.sourceCrosswalkSource, cur.sourceAttribution) {
			out[e.id] = carriedLeave
			continue
		}
		anyListed := false
		for _, c := range codes {
			if listed(e.requirementID, c) {
				anyListed = true
				break
			}
		}
		dropped := cur.sourceRetired && !anyListed
		switch {
		case dropped && !e.retired:
			out[e.id] = carriedRetire
		case !dropped && e.retired:
			out[e.id] = carriedRestore
		default:
			out[e.id] = carriedLeave
		}
	}
	return out
}

// carriedRoot walks e up its chain of remaps. It returns the last copy in
// the chain (whose source is the root), every SCF code on the chain, and
// whether it reached a root; a cycle reaches none.
func carriedRoot(e carriedEdge, byID map[[16]byte]carriedEdge) (last carriedEdge, codes []string, rooted bool) {
	codes = []string{e.scfID}
	seen := map[[16]byte]bool{e.id: true}
	cur := e
	for {
		codes = append(codes, cur.sourceSCFID)
		parent, isCopy := byID[cur.sourceID]
		if !isCopy {
			return cur, codes, true
		}
		if seen[parent.id] {
			return cur, codes, false
		}
		seen[parent.id] = true
		cur = parent
	}
}

// carriedOwned reports, per carried edge, whether this import owns its root.
func carriedOwned(edges []carriedEdge, owns func(source *string, attribution string) bool) map[[16]byte]bool {
	byID := make(map[[16]byte]carriedEdge, len(edges))
	for _, e := range edges {
		byID[e.id] = e
	}
	out := make(map[[16]byte]bool, len(edges))
	for _, e := range edges {
		last, _, rooted := carriedRoot(e, byID)
		out[e.id] = rooted && owns(last.sourceCrosswalkSource, last.sourceAttribution)
	}
	return out
}

func listCarriedEdges(ctx context.Context, tx pgx.Tx, versionID pgtype.UUID) ([]carriedEdge, error) {
	rows, err := tx.Query(ctx, carriedEdgesSQL, versionID)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: list carried edges: %w", err)
	}
	defer rows.Close()
	var out []carriedEdge
	for rows.Next() {
		var e carriedEdge
		if err := rows.Scan(&e.id, &e.requirementID, &e.requirementCode, &e.scfID,
			&e.attribution, &e.tier, &e.source, &e.retired,
			&e.content.relType, &e.content.strength, &e.content.rationale, &e.humanMoved,
			&e.sourceID, &e.sourceSCFID, &e.sourceAttribution, &e.sourceCrosswalkSource,
			&e.sourceRetired); err != nil {
			return nil, fmt.Errorf("crosswalk: scan carried edge: %w", err)
		}
		e.content.attribution = dbx.CrosswalkSourceAttribution(e.attribution)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("crosswalk: list carried edges: %w", err)
	}
	return out, nil
}

// settleCarriedEdges retires the carried edges whose root this import just
// dropped and restores the ones it retired whose root is listed again
// (#3065). It runs after the retire pass, in the same transaction, and
// returns the ids of the edges it retired. A copy left live whose own code
// is listed then takes the crosswalk's content (#3073).
func settleCarriedEdges(ctx context.Context, tx pgx.Tx, q *dbx.Queries, versionID pgtype.UUID, source string, owned map[dbx.CrosswalkSourceAttribution]bool, listed map[requirementCode]bool, content map[requirementCode]strmContent, report *Report) (map[[16]byte]bool, error) {
	edges, err := listCarriedEdges(ctx, tx, versionID)
	if err != nil {
		return nil, err
	}
	retired := map[[16]byte]bool{}
	if len(edges) == 0 {
		return retired, nil
	}
	owns := func(s *string, attribution string) bool {
		return ownsEdge(source, s) && owned[dbx.CrosswalkSourceAttribution(attribution)]
	}
	rootOwned := carriedOwned(edges, owns)
	decisions := decideCarried(edges, owns,
		func(requirementID [16]byte, scfID string) bool {
			return listed[requirementCode{requirementID, scfID}]
		})
	for _, e := range edges {
		id := pgtype.UUID{Bytes: e.id, Valid: true}
		tier := dbx.CrosswalkMappingTier(e.tier)
		edge := CatalogEdge{
			RequirementCode: e.requirementCode,
			SCFAnchor:       e.scfID,
			Attribution:     e.attribution,
			Tier:            e.tier,
			Source:          sourceLabel(e.source),
		}
		switch decisions[e.id] {
		case carriedRetire:
			if err := q.RetireFwToScfEdge(ctx, id); err != nil {
				return nil, fmt.Errorf("crosswalk: retire carried edge %s->%s: %w", e.requirementCode, e.scfID, err)
			}
			if err := auditSystemTransition(ctx, q, id, tier, tier, carriedRetireNote(source)); err != nil {
				return nil, fmt.Errorf("crosswalk: audit retire carried %s->%s: %w", e.requirementCode, e.scfID, err)
			}
			report.EdgesCarriedRetired = append(report.EdgesCarriedRetired, edge)
			retired[e.id] = true
		case carriedRestore:
			var note string
			if err := tx.QueryRow(ctx, latestTransitionNoteSQL, id).Scan(&note); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("crosswalk: read retire note of carried %s->%s: %w", e.requirementCode, e.scfID, err)
			}
			if !carriedRetiredBy(note, source) {
				continue
			}
			if err := q.RestoreFwToScfEdge(ctx, id); err != nil {
				return nil, fmt.Errorf("crosswalk: restore carried edge %s->%s: %w", e.requirementCode, e.scfID, err)
			}
			if err := auditSystemTransition(ctx, q, id, tier, tier, carriedRestoreNote(source)); err != nil {
				return nil, fmt.Errorf("crosswalk: audit restore carried %s->%s: %w", e.requirementCode, e.scfID, err)
			}
			report.EdgesCarriedRestored = append(report.EdgesCarriedRestored, edge)
			e.retired = false
		case carriedLeave:
		}
		if e.retired || !rootOwned[e.id] {
			continue
		}
		row, ok := content[requirementCode{e.requirementID, e.scfID}]
		switch decideCarriedSync(e, row, ok) {
		case carriedSyncNone:
		case carriedSyncHeldBack:
			report.EdgesCarriedHeldBack = append(report.EdgesCarriedHeldBack, edge)
		case carriedSyncApply:
			if err := syncCarriedEdge(ctx, q, e, row, source); err != nil {
				return nil, err
			}
			edge.Attribution = string(row.attribution)
			if target, changed := reseedTier(tier, row.attribution); changed {
				edge.Tier = string(target)
			}
			report.EdgesCarriedSynced = append(report.EdgesCarriedSynced, edge)
		}
	}
	return retired, nil
}

// syncCarriedEdge writes row's content onto a carried edge in place. The
// edge keeps its crosswalk_source, so it stays linked to its remap and under
// the carried retire pass. The tier re-seeds per ADR 0018 §2, as for an
// owned edge whose content changed, and one system transition records the
// sync with the content it replaced.
func syncCarriedEdge(ctx context.Context, q *dbx.Queries, e carriedEdge, row strmContent, source string) error {
	id := pgtype.UUID{Bytes: e.id, Valid: true}
	if _, err := q.UpdateFwToScfEdge(ctx, dbx.UpdateFwToScfEdgeParams{
		ID:                id,
		RelationshipType:  row.relType,
		Strength:          row.strength,
		SourceAttribution: row.attribution,
		Rationale:         row.rationale,
		CrosswalkSource:   e.source,
	}); err != nil {
		return fmt.Errorf("crosswalk: sync carried edge %s->%s: %w", e.requirementCode, e.scfID, err)
	}
	from := dbx.CrosswalkMappingTier(e.tier)
	to, changed := reseedTier(from, row.attribution)
	if changed {
		if err := q.SetFwToScfEdgeTier(ctx, dbx.SetFwToScfEdgeTierParams{ID: id, MappingTier: to}); err != nil {
			return fmt.Errorf("crosswalk: reseed tier of carried %s->%s: %w", e.requirementCode, e.scfID, err)
		}
	}
	if err := auditSystemTransition(ctx, q, id, from, to, carriedSyncNote(source, e.content)); err != nil {
		return fmt.Errorf("crosswalk: audit sync carried %s->%s: %w", e.requirementCode, e.scfID, err)
	}
	return nil
}

// strmContent is the STRM content of one crosswalk row.
type strmContent struct {
	relType     dbx.StrmRelationshipType
	strength    float64
	attribution dbx.CrosswalkSourceAttribution
	rationale   string
}
