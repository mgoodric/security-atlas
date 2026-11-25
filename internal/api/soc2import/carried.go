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
// source edge. Only a source on the same requirement counts, which keeps the
// pair inside the version. It runs after the retire pass, so a source the
// pass just retired reads as retired.
const carriedEdgesSQL = `
SELECT
    e.id, e.framework_requirement_id, fr.code, a.scf_id,
    e.source_attribution::text, e.mapping_tier::text, e.crosswalk_source,
    e.retired_at IS NOT NULL,
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
		codes := []string{e.scfID}
		seen := map[[16]byte]bool{e.id: true}
		cur := e
		rooted := false
		for {
			codes = append(codes, cur.sourceSCFID)
			parent, isCopy := byID[cur.sourceID]
			if !isCopy {
				rooted = true
				break
			}
			if seen[parent.id] {
				break
			}
			seen[parent.id] = true
			cur = parent
		}
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
			&e.sourceID, &e.sourceSCFID, &e.sourceAttribution, &e.sourceCrosswalkSource,
			&e.sourceRetired); err != nil {
			return nil, fmt.Errorf("crosswalk: scan carried edge: %w", err)
		}
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
// returns the ids of the edges it retired.
func settleCarriedEdges(ctx context.Context, tx pgx.Tx, q *dbx.Queries, versionID pgtype.UUID, source string, owned map[dbx.CrosswalkSourceAttribution]bool, listed map[requirementCode]bool, report *Report) (map[[16]byte]bool, error) {
	edges, err := listCarriedEdges(ctx, tx, versionID)
	if err != nil {
		return nil, err
	}
	retired := map[[16]byte]bool{}
	if len(edges) == 0 {
		return retired, nil
	}
	decisions := decideCarried(edges,
		func(s *string, attribution string) bool {
			return ownsEdge(source, s) && owned[dbx.CrosswalkSourceAttribution(attribution)]
		},
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
		case carriedLeave:
		}
	}
	return retired, nil
}
