# Issue #3073 — A crosswalk's STRM content wins on a carried edge it lists by code

Follow-up from the #3072 review. Take a remap that renamed X to Y on
requirement R and carried an edge onto Y's anchor
(`crosswalk_source = 'scf-remap:<proposal>'`). If a crosswalk then moved R
from X to Y, the copy stayed live, which is right for coverage, but it kept
X's relationship, strength and rationale. The carry-forward could not
replace it, because `targetEdgeExistsSQL` (`scfimport/edgeremap.go`) finds
the pair already mapped and closes the new edge's proposal as `kept_legacy`.
Default coverage read stale STRM content.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

Found in review. `TestCarriedEdges_CrosswalkContentWinsOnTheCarriedCopy` and
`TestCarriedEdges_HumanReviewedCopyKeepsItsContent` fail without the fix and
pass with it.

## D1 — Update the copy in place, not supersede it

`fw_to_scf_edges` allows one row per (requirement, anchor): the
`scf_edge_remap_decide` EXISTS check and the table's unique constraint both
enforce it. An owned edge cannot sit beside the copy on Y's anchor. To
supersede the copy, the import would have to delete it or move it to another
anchor, and either loses the history the ticket says to keep. Updating in
place keeps the row id, every earlier tier transition, and the decision
record that links the copy to its remap. The new transition row holds the
content that was replaced (D4).

## D2 — The copy keeps its `crosswalk_source`

The sync writes content only. `crosswalk_source` stays
`scf-remap:<proposal>`, so `carriedEdgesSQL` still finds the copy and the
#3065 retire pass still governs it. If the crosswalk later drops Y and X, the
copy is retired with its root, and it is restored when either code is listed
again. If the sync took ownership instead (`crosswalk_source = <import
source>`), the copy would lose its link to the remap. It would also become an
edge on an anchor outside the crosswalk's bound release. Whether such an edge
survives the next retire pass depends on `spareCurrentReleaseEdges` and which
release is current. That is a second ownership path to reason about.

## D3 — When the sync applies

All of these must hold for a carried edge:

- it is live after the retire/restore pass;
- this import owns its root (the ownership test of #3065 D3, applied through
  the same chain walk, `carriedRoot`);
- the crosswalk lists the copy's own SCF code for its requirement. A code
  elsewhere on the chain does not count: the content on that row belongs to a
  different anchor;
- the listed row's content (relationship, strength, attribution, rationale)
  differs from the copy's.

A crosswalk that binds in the copy's own release never reaches this pass. The
main upsert loop finds the copy by (requirement, anchor) and updates it as it
would any edge, which already sets `crosswalk_source` to the import. Only a
crosswalk bound in another release, the BYO and upgrade shape, needs the
sync.

## D4 — The audit row

Each sync appends one transition by `SystemReviewerID` through
`auditSystemTransition`. Its note starts with the stable marker
`carried-sync:"<source>"` and records the replaced content
(`was <relationship> <strength> <attribution> "<rationale>"`), so the
history keeps what the row no longer holds. The tier re-seeds per ADR 0018 §2
(`reseedTier`), the same rule the main loop applies when it changes an owned
edge's content. `from_tier`/`to_tier` show the move, and a rejected copy is
never reopened. A sync note never matches `carriedRetiredBy`
(`TestCarriedSyncNote`), so the restore check is unaffected.

## D5 — "A human edited it": any transition by a person

No API edits an edge's STRM content. Content columns change only through
importers and `scf_edge_remap_decide`. A person acts on an edge only through
the tier workflow (`internal/crosswalktier`), and every such move writes a
transition with the reviewer's id. A carried edge counts as human-edited when
any transition row on it has a `reviewer_id` other than `SystemReviewerID`
(the nil uuid). The importer, the retire pass and this sync all write as
`SystemReviewerID`. Such a copy keeps its content and is listed in
`Report.EdgesCarriedHeldBack`. Only the copy's own history counts. A move a
person made on the root vouched for the root's content, which the crosswalk
has since replaced.

The same report lists a copy whose stored attribution outranks the
crosswalk row's (`attributionRank`), the precedence rule the main loop
applies to an owned edge.

## D6 — Effect on existing installs

The sync changes only installs whose state the ticket describes. On those, a
live copy's relationship and strength, which coverage reads, change to the
crosswalk's on the next import. Its tier changes only if the crosswalk row's
attribution seeds a different tier. A default install with no SCF upgrade has
no carried edges. On an upgraded or BYO install where every copy came from
the same-code carry-forward, the copy's content already equals the row the
crosswalk lists for that code, so nothing syncs.
