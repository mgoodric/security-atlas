// Package crosswalkconflict implements the slice-536a conflict-detection
// heuristics over STRM crosswalk mappings: the three families of
// contradiction a human reviewer needs surfaced before curating a
// community_draft crosswalk — competing anchors, contradictory strengths, and
// orphaned requirements.
//
// # Pure, catalog-only, read-only
//
// Every function here is pure: no DB handle, no context, no clock, no tenant
// data, no writes. That is a threat-model requirement, not a style choice.
// Slice 536's threat-model I says the conflict heuristics "must NOT fold in any
// tenant's evaluated coverage … Conflict detection runs over catalog edges
// only". Input carries a requirement inventory and a catalog edge set and
// nothing else, so there is no field through which tenant state could arrive —
// the mitigation holds by construction rather than by a caller remembering it.
//
// Detection is also strictly advisory (decisions log D5). Nothing here demotes
// a mapping, rejects one, or transitions a tier. A tier change stays a human
// act through slice 483's admin endpoint, which is what keeps the
// constitutional "no auto-approve its own mappings" boundary intact from both
// directions.
//
// # Relationship to slice 483
//
// This package adds the conflict dimension slice 483 does not have. It does NOT
// add a second approval workflow: 483's draft -> under_review -> verified state
// machine (internal/crosswalktier) is the only review lifecycle. See
// docs/audit-log/536a-crosswalk-conflict-detection-decisions.md Part 1 for the
// full scope reconciliation.
//
// # Constitutional invariant 7
//
// Edge models exactly one direction, requirement -> SCF anchor. No type in this
// package can express a requirement-to-requirement relation, and every Conflict
// names a single requirement. The graph shape invariant 7 forbids is
// unreachable from this API.
package crosswalkconflict

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// RelationshipType is the STRM relationship a mapping asserts, mirroring the
// strm_relationship_type Postgres enum (NIST IR 8477; canvas 3.2).
type RelationshipType string

const (
	// RelEqual asserts logical equivalence between the requirement and the
	// anchor. Canvas 3.2 pins this to full confidence: "(equal, 1.0)".
	RelEqual RelationshipType = "equal"
	// RelSubsetOf asserts the requirement is fully covered by the anchor.
	RelSubsetOf RelationshipType = "subset_of"
	// RelSupersetOf asserts the requirement covers more than the anchor.
	RelSupersetOf RelationshipType = "superset_of"
	// RelIntersectsWith asserts partial overlap.
	RelIntersectsWith RelationshipType = "intersects_with"
	// RelNoRelationship is a confirmed absence of overlap. Canvas 3.2: "Yes,
	// this is data; it suppresses false suggestions."
	RelNoRelationship RelationshipType = "no_relationship"
)

// Kind is the heuristic family a finding belongs to. The three kinds are the
// three the slice brief names.
type Kind string

const (
	// KindCompetingAnchors covers a requirement whose mappings claim the same
	// ground twice.
	KindCompetingAnchors Kind = "competing_anchors"
	// KindContradictoryStrength covers a strength value that contradicts its
	// own relationship type, or a sibling mapping's.
	KindContradictoryStrength Kind = "contradictory_strength"
	// KindOrphanedRequirement covers a requirement with no anchoring path into
	// the SCF spine.
	KindOrphanedRequirement Kind = "orphaned_requirement"
)

// Reason is the specific sub-shape within a Kind. The reviewer acts on the
// reason, not the kind — "two equal claims" and "two subset_of claims in one
// family" call for different edits.
type Reason string

const (
	// ReasonDuplicateEqualClaim — two or more equal edges to distinct anchors.
	ReasonDuplicateEqualClaim Reason = "duplicate_equal_claim"
	// ReasonDuplicateTotalClaimInFamily — two or more equal/subset_of edges to
	// distinct anchors inside one SCF family.
	ReasonDuplicateTotalClaimInFamily Reason = "duplicate_total_claim_in_family"

	// ReasonNoRelationshipWithStrength — a confirmed non-overlap carrying
	// positive strength.
	ReasonNoRelationshipWithStrength Reason = "no_relationship_with_strength"
	// ReasonEqualBelowFullStrength — a hedged equal.
	ReasonEqualBelowFullStrength Reason = "equal_below_full_strength"
	// ReasonIntersectsAtFullStrength — a partial overlap at full confidence.
	ReasonIntersectsAtFullStrength Reason = "intersects_at_full_strength"
	// ReasonAssertedRelationshipZeroStrength — an asserted relationship worth
	// nothing, which is what no_relationship already records.
	ReasonAssertedRelationshipZeroStrength Reason = "asserted_relationship_zero_strength"
	// ReasonSiblingStrengthDivergence — same requirement, same family, same
	// type, materially different strengths.
	ReasonSiblingStrengthDivergence Reason = "sibling_strength_divergence"

	// ReasonUnmapped — the requirement has no edges at all.
	ReasonUnmapped Reason = "unmapped"
	// ReasonZeroStrengthOnly — edges exist but every one is worth zero.
	ReasonZeroStrengthOnly Reason = "zero_strength_only"
	// ReasonExplicitlyUnmapped — every edge is a deliberate no_relationship.
	ReasonExplicitlyUnmapped Reason = "explicitly_unmapped"
)

// Severity orders the reviewer's queue. It is advisory only (decisions log D5):
// no severity causes the platform to act on a mapping by itself.
type Severity string

const (
	// SeverityLow is informational — a reviewer confirms rather than fixes.
	SeverityLow Severity = "low"
	// SeverityMedium is a probable mapping error worth an edit.
	SeverityMedium Severity = "medium"
	// SeverityHigh is a contradiction that misstates coverage today.
	SeverityHigh Severity = "high"
)

// Heuristic thresholds. Both are JUDGMENT calls reasoned from the canvas and
// from slice 482's shipped confidence bands rather than from measured
// false-positive rates; both are on the decisions log's "Revisit once in use"
// list.
const (
	// equalStrengthFloor is the lowest strength an `equal` edge may carry
	// before it reads as hedged. A tolerance rather than an exact == 1.0 test,
	// so float round-tripping through DOUBLE PRECISION cannot manufacture a
	// finding. 0.9 sits inside slice 482's `strong` band (floor 0.8), so an
	// edge that clears this check also reads as strongly covered downstream.
	equalStrengthFloor = 0.9

	// siblingStrengthSpread is the largest strength range tolerated among
	// same-family, same-type siblings. 0.4 is one slice-482 confidence band's
	// width (482 cuts at 0.5 / 0.8), so a wider spread guarantees the siblings
	// land in different operator-visible bands — the point where the
	// inconsistency stops being academic.
	siblingStrengthSpread = 0.4
)

// Requirement is one framework requirement in the inventory being reviewed.
//
// The inventory is a SEPARATE input from the edges because a requirement with
// zero edges produces zero rows in any edge query and is therefore invisible to
// an edges-only API. Omit Requirements and ReasonUnmapped silently cannot fire;
// the other heuristics are unaffected.
type Requirement struct {
	ID uuid.UUID
	// Code is the framework's own identifier (e.g. "A.5.15"). Used for stable
	// output ordering and for the human-readable Detail.
	Code string
}

// Edge is one requirement -> SCF anchor STRM mapping. This is the only edge
// shape the package models (invariant 7).
type Edge struct {
	ID              uuid.UUID
	RequirementID   uuid.UUID
	RequirementCode string
	AnchorID        uuid.UUID
	// AnchorSCFID is the anchor's catalog identifier (e.g. "IAC-22").
	AnchorSCFID string
	// AnchorFamily is the SCF family the anchor sits in (e.g. "IAC"). Several
	// heuristics are family-scoped: a requirement legitimately spanning
	// multiple SCF domains is the normal case, not a conflict.
	AnchorFamily     string
	RelationshipType RelationshipType
	Strength         float64
}

// Input is the catalog slice under review. It carries no tenant-scoped field,
// by design (threat-model I).
type Input struct {
	// Requirements is the inventory scanned for orphans. Requirements
	// referenced by an edge but absent here are still analysed — they are
	// synthesized from the edge — but a requirement with no edges must appear
	// here to be seen at all.
	Requirements []Requirement
	// Edges is the catalog edge set. Order is irrelevant; Detect sorts.
	Edges []Edge
}

// Conflict is one finding. It names exactly one requirement and zero or more of
// that requirement's edges/anchors — never a second requirement (invariant 7).
type Conflict struct {
	Kind            Kind
	Reason          Reason
	Severity        Severity
	RequirementID   uuid.UUID
	RequirementCode string
	// EdgeIDs are the edges implicated, ascending. Empty for ReasonUnmapped,
	// which is a finding about the absence of edges.
	EdgeIDs []uuid.UUID
	// AnchorSCFIDs are the implicated anchors, ascending — the reviewer-facing
	// labels for EdgeIDs.
	AnchorSCFIDs []string
	// Detail is a human-readable statement of the contradiction, for the
	// 536b review queue.
	Detail string
}

// Detect runs all three heuristic families over the input and returns the
// findings in a deterministic order (decisions log D6: requirement code, then
// kind, then reason, then edge ids). It never mutates the input and never
// writes anything.
func Detect(in Input) []Conflict {
	byRequirement := make(map[uuid.UUID][]Edge, len(in.Requirements))
	for _, e := range in.Edges {
		byRequirement[e.RequirementID] = append(byRequirement[e.RequirementID], e)
	}

	out := make([]Conflict, 0)
	for _, req := range inventory(in) {
		edges := byRequirement[req.ID]
		sortEdges(edges)
		out = append(out, detectOrphaned(req, edges)...)
		out = append(out, detectCompetingAnchors(req, edges)...)
		out = append(out, detectContradictoryStrengths(req, edges)...)
	}
	sortConflicts(out)
	return out
}

// inventory returns the requirements to analyse: the declared inventory, plus
// any requirement referenced only by an edge (synthesized from the edge so a
// caller passing edges alone still gets the edge-scoped heuristics). Sorted by
// code then id so the walk is deterministic.
func inventory(in Input) []Requirement {
	seen := make(map[uuid.UUID]Requirement, len(in.Requirements))
	for _, r := range in.Requirements {
		seen[r.ID] = r
	}
	for _, e := range in.Edges {
		if _, ok := seen[e.RequirementID]; !ok {
			seen[e.RequirementID] = Requirement{ID: e.RequirementID, Code: e.RequirementCode}
		}
	}
	out := make([]Requirement, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out
}

// detectOrphaned implements decisions-log D4. A requirement with no anchoring
// path into the SCF spine can never be covered by any evidence (canvas 3.1: all
// framework-to-framework relationships derive through SCF anchors), so slice
// 482's rollup reports it uncovered forever with no way for the operator to
// tell "not applicable" from "the crosswalk forgot it".
//
// The three reasons are deliberately distinct: `unmapped` is a loader gap a
// maintainer must act on, `zero_strength_only` hides behind a healthy-looking
// edge count, and `explicitly_unmapped` is a deliberate assertion a reviewer
// already made — surfaced low so a whole category of intentional data does not
// read as defects.
func detectOrphaned(req Requirement, edges []Edge) []Conflict {
	if len(edges) == 0 {
		return []Conflict{{
			Kind:            KindOrphanedRequirement,
			Reason:          ReasonUnmapped,
			Severity:        SeverityHigh,
			RequirementID:   req.ID,
			RequirementCode: req.Code,
			Detail: fmt.Sprintf(
				"requirement %s has no STRM edge to any SCF anchor; it can never be covered by evidence",
				req.Code),
		}}
	}

	allNoRelationship, allZeroStrength := true, true
	for _, e := range edges {
		if e.RelationshipType != RelNoRelationship {
			allNoRelationship = false
		}
		if e.Strength > 0 {
			allZeroStrength = false
		}
	}

	switch {
	case allNoRelationship:
		return []Conflict{{
			Kind:            KindOrphanedRequirement,
			Reason:          ReasonExplicitlyUnmapped,
			Severity:        SeverityLow,
			RequirementID:   req.ID,
			RequirementCode: req.Code,
			EdgeIDs:         edgeIDs(edges),
			AnchorSCFIDs:    anchorSCFIDs(edges),
			Detail: fmt.Sprintf(
				"every one of requirement %s's %d edges is no_relationship; confirm the requirement is genuinely out of scope",
				req.Code, len(edges)),
		}}
	case allZeroStrength:
		return []Conflict{{
			Kind:            KindOrphanedRequirement,
			Reason:          ReasonZeroStrengthOnly,
			Severity:        SeverityHigh,
			RequirementID:   req.ID,
			RequirementCode: req.Code,
			EdgeIDs:         edgeIDs(edges),
			AnchorSCFIDs:    anchorSCFIDs(edges),
			Detail: fmt.Sprintf(
				"requirement %s is mapped to %d anchor(s) but every edge carries strength 0; it is mapped on paper only",
				req.Code, len(edges)),
		}}
	default:
		return nil
	}
}

// detectCompetingAnchors implements decisions-log D2.
//
// Rule 1 (duplicate_equal_claim, high) is NOT family-scoped: canvas 3.1 calls
// SCF anchors semantic-equivalence-class anchors and STRM `equal` means
// logically equivalent, so a requirement cannot be equivalent to two distinct
// classes wherever they sit — if it genuinely were, the anchors should be
// merged.
//
// Rule 2 (duplicate_total_claim_in_family, medium) IS family-scoped, because a
// requirement spanning several SCF domains is the normal case: an ISO
// requirement can be subset_of an IAC anchor and subset_of a CRY anchor because
// it really has two independent obligations. Only inside one family do two
// full-coverage claims mean the mapper could not choose.
//
// There is deliberately no reverse-direction check. The graph stores only
// requirement -> anchor (invariant 7) and fw_to_scf_edges is UNIQUE
// (framework_requirement_id, scf_anchor_id) with exactly one relationship_type
// per pair (NIST IR 8477 section 4), so there is no reverse edge to disagree
// with — materializing one would push toward the shape invariant 7 forbids.
func detectCompetingAnchors(req Requirement, edges []Edge) []Conflict {
	var out []Conflict

	equalEdges := distinctByAnchor(filterByType(edges, RelEqual))
	if len(equalEdges) >= 2 {
		out = append(out, Conflict{
			Kind:            KindCompetingAnchors,
			Reason:          ReasonDuplicateEqualClaim,
			Severity:        SeverityHigh,
			RequirementID:   req.ID,
			RequirementCode: req.Code,
			EdgeIDs:         edgeIDs(equalEdges),
			AnchorSCFIDs:    anchorSCFIDs(equalEdges),
			Detail: fmt.Sprintf(
				"requirement %s asserts `equal` against %d distinct anchors (%s); logical equivalence admits exactly one",
				req.Code, len(equalEdges), strings.Join(anchorSCFIDs(equalEdges), ", ")),
		})
	}

	byFamily := make(map[string][]Edge)
	for _, e := range edges {
		if e.RelationshipType == RelEqual || e.RelationshipType == RelSubsetOf {
			byFamily[e.AnchorFamily] = append(byFamily[e.AnchorFamily], e)
		}
	}
	for _, family := range sortedKeys(byFamily) {
		group := distinctByAnchor(byFamily[family])
		if len(group) < 2 {
			continue
		}
		// Dedup (D2): a group made entirely of `equal` edges was already
		// reported by rule 1 at higher severity. Emitting both would
		// double-count one problem in the reviewer's queue.
		if allOfType(group, RelEqual) {
			continue
		}
		out = append(out, Conflict{
			Kind:            KindCompetingAnchors,
			Reason:          ReasonDuplicateTotalClaimInFamily,
			Severity:        SeverityMedium,
			RequirementID:   req.ID,
			RequirementCode: req.Code,
			EdgeIDs:         edgeIDs(group),
			AnchorSCFIDs:    anchorSCFIDs(group),
			Detail: fmt.Sprintf(
				"requirement %s claims full coverage by %d anchors in SCF family %s (%s); keep one and downgrade the rest to intersects_with",
				req.Code, len(group), family, strings.Join(anchorSCFIDs(group), ", ")),
		})
	}

	return out
}

// detectContradictoryStrengths implements decisions-log D3 — per-edge
// incoherence between a relationship type and its strength (D3a), then
// divergence between sibling edges (D3b).
//
// Canvas 3.2 states the contract the per-edge rules enforce: "A strength field
// captures auditor judgment: (equal, 1.0) is full confidence; (intersects_with,
// 0.4) flags partial coverage." The type therefore implies a strength band, and
// an edge outside its own band contradicts itself regardless of any other edge.
func detectContradictoryStrengths(req Requirement, edges []Edge) []Conflict {
	var out []Conflict

	for _, e := range edges {
		// At most one per-edge reason fires. The arms are ordered
		// most-specific-first and are mutually exclusive on purpose: an `equal`
		// edge at strength 0 is doubly wrong, but two findings for one edge is
		// queue noise, and the zero-strength arm is the more actionable of the
		// two.
		switch {
		case e.RelationshipType == RelNoRelationship && e.Strength > 0:
			out = append(out, edgeConflict(req, e,
				ReasonNoRelationshipWithStrength, SeverityHigh,
				fmt.Sprintf("edge %s -> %s is no_relationship (confirmed no overlap) yet carries strength %.2f",
					req.Code, e.AnchorSCFID, e.Strength)))
		case e.RelationshipType == RelNoRelationship:
			// Coherent: a confirmed non-overlap at zero strength.
		case e.Strength <= 0:
			out = append(out, edgeConflict(req, e,
				ReasonAssertedRelationshipZeroStrength, SeverityMedium,
				fmt.Sprintf("edge %s -> %s asserts %s at strength %.2f; a relationship worth nothing should be recorded as no_relationship",
					req.Code, e.AnchorSCFID, e.RelationshipType, e.Strength)))
		case e.RelationshipType == RelEqual && e.Strength < equalStrengthFloor:
			out = append(out, edgeConflict(req, e,
				ReasonEqualBelowFullStrength, SeverityMedium,
				fmt.Sprintf("edge %s -> %s asserts `equal` at strength %.2f (below %.2f); a hedged equivalence is probably subset_of or intersects_with",
					req.Code, e.AnchorSCFID, e.Strength, equalStrengthFloor)))
		case e.RelationshipType == RelIntersectsWith && e.Strength >= 1.0:
			out = append(out, edgeConflict(req, e,
				ReasonIntersectsAtFullStrength, SeverityMedium,
				fmt.Sprintf("edge %s -> %s asserts partial overlap (`intersects_with`) at full strength %.2f; full coverage is `equal` or `subset_of`",
					req.Code, e.AnchorSCFID, e.Strength)))
		}
	}

	// D3b — same requirement, same family, same STRM type is the same auditor
	// judgment applied twice; a wide spread means one of the two was assigned
	// carelessly even though neither is individually incoherent.
	//
	// no_relationship groups are skipped: their strengths are meaningfully zero
	// and any positive one is already owned by the per-edge arm above.
	bySibling := make(map[string][]Edge)
	for _, e := range edges {
		if e.RelationshipType == RelNoRelationship {
			continue
		}
		bySibling[e.AnchorFamily+"\x00"+string(e.RelationshipType)] = append(
			bySibling[e.AnchorFamily+"\x00"+string(e.RelationshipType)], e)
	}
	for _, key := range sortedKeys(bySibling) {
		group := bySibling[key]
		if len(group) < 2 {
			continue
		}
		lo, hi := group[0].Strength, group[0].Strength
		for _, e := range group[1:] {
			if e.Strength < lo {
				lo = e.Strength
			}
			if e.Strength > hi {
				hi = e.Strength
			}
		}
		if hi-lo <= siblingStrengthSpread {
			continue
		}
		family, relType, _ := strings.Cut(key, "\x00")
		out = append(out, Conflict{
			Kind:            KindContradictoryStrength,
			Reason:          ReasonSiblingStrengthDivergence,
			Severity:        SeverityMedium,
			RequirementID:   req.ID,
			RequirementCode: req.Code,
			EdgeIDs:         edgeIDs(group),
			AnchorSCFIDs:    anchorSCFIDs(group),
			Detail: fmt.Sprintf(
				"requirement %s's %s edges into SCF family %s span strengths %.2f-%.2f (spread %.2f > %.2f); the same judgment was applied inconsistently",
				req.Code, relType, family, lo, hi, hi-lo, siblingStrengthSpread),
		})
	}

	return out
}

func edgeConflict(req Requirement, e Edge, reason Reason, sev Severity, detail string) Conflict {
	return Conflict{
		Kind:            KindContradictoryStrength,
		Reason:          reason,
		Severity:        sev,
		RequirementID:   req.ID,
		RequirementCode: req.Code,
		EdgeIDs:         []uuid.UUID{e.ID},
		AnchorSCFIDs:    []string{e.AnchorSCFID},
		Detail:          detail,
	}
}

func filterByType(edges []Edge, t RelationshipType) []Edge {
	out := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if e.RelationshipType == t {
			out = append(out, e)
		}
	}
	return out
}

func allOfType(edges []Edge, t RelationshipType) bool {
	for _, e := range edges {
		if e.RelationshipType != t {
			return false
		}
	}
	return len(edges) > 0
}

// distinctByAnchor collapses edges landing on the same anchor. The DB's UNIQUE
// (framework_requirement_id, scf_anchor_id) already guarantees this for
// DB-sourced input; the guard keeps a hand-built or merged Input from inflating
// a "competing anchors" count with what is really one anchor.
func distinctByAnchor(edges []Edge) []Edge {
	seen := make(map[uuid.UUID]bool, len(edges))
	out := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if seen[e.AnchorID] {
			continue
		}
		seen[e.AnchorID] = true
		out = append(out, e)
	}
	return out
}

func edgeIDs(edges []Edge) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func anchorSCFIDs(edges []Edge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.AnchorSCFID)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string][]Edge) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortEdges orders a requirement's edges by anchor SCF id then edge id, so
// every heuristic walks them identically regardless of how the caller built the
// slice (Go map iteration and DB row order are both unreliable).
func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].AnchorSCFID != edges[j].AnchorSCFID {
			return edges[i].AnchorSCFID < edges[j].AnchorSCFID
		}
		return edges[i].ID.String() < edges[j].ID.String()
	})
}

// sortConflicts pins the output order (D6). Without it the same catalog would
// produce a differently-ordered review queue on every call and the 536b UI
// would reshuffle between refreshes.
func sortConflicts(cs []Conflict) {
	key := func(c Conflict) []string {
		ids := make([]string, 0, len(c.EdgeIDs))
		for _, id := range c.EdgeIDs {
			ids = append(ids, id.String())
		}
		return []string{c.RequirementCode, c.RequirementID.String(), string(c.Kind), string(c.Reason), strings.Join(ids, ",")}
	}
	sort.Slice(cs, func(i, j int) bool {
		a, b := key(cs[i]), key(cs[j])
		for n := range a {
			if a[n] != b[n] {
				return a[n] < b[n]
			}
		}
		return false
	})
}
