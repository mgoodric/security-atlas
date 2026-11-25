# Issue #3065 — A crosswalk import retires the edges an SCF remap carried from the edges it drops

Follow-up to #3061 D9 (N4 in the #3063 review). An SCF edge remap copies a
catalog edge onto an anchor in another release and stamps the copy
`crosswalk_source = 'scf-remap:<proposal id>'`. `ownsEdge` needs an exact
source match, so when a later crosswalk import dropped a mapping it retired
the edge it wrote and left the carried copy live, counting toward default
coverage.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

Found in review, not by a test. `TestCarriedEdges_BYODroppedMappingRetiresTheCarriedCopy`
and `TestCarriedEdges_UpgradeDroppedMappingRetiresTheCarriedCopy` fail without
the fix and pass with it.

## D1 — Retire, not flag

**Decided by the dispatcher (2026-11-24) on the recommendation:** retire the
carried copy. This matches the import's non-destructive retire from #1971: the
row and its tier history stay, a transition row records the move, and a
re-list restores the copy. No flag-for-review state exists on
`fw_to_scf_edges`, and a flag would leave the copy in default coverage until
someone acts on it, which is the bug.

## D2 — The link is the approved proposal; no migration

`scf_edge_remap_proposals` already records the pair. `new_edge_id` is the
carried edge, `edge_id` its source, and the carried edge's `crosswalk_source`
names the proposal. The join requires all three, and it requires the source
to be on the same requirement as the copy, so the pair never leaves one
framework version.

The remap tables are not in `sqlc.yaml`'s schema list, so the query is raw SQL
in `soc2import/carried.go`, as `scfimport/edgeremap.go` reads the same tables.
Adding them to the sqlc schema would have regenerated `dbx` models for
unrelated tables.

## D3 — Ownership is the root's, and the chain's codes protect the copy

A carried edge is never owned directly (D9 of #3061 is right that that would
be an ownership-model change). Instead the import follows the copy back
through the proposals to the first edge that is not a copy: the root. A copy
is retired when all of these hold:

- this import owns the root (`ownsEdge` and the attribution set, the same test
  the retire pass applies);
- the root is retired after this import's retire pass;
- the crosswalk lists none of the chain's SCF codes for that requirement (the
  root's code, each intermediate copy's, and the copy's own).

The third condition keeps a copy whose root was retired for the #1971 reason,
an edge left on an older release than the one the crosswalk now binds in,
while the mapping still stands. It also keeps a renamed carry (`IAC-01` →
`IAC-21`) when the crosswalk lists either code.

The walk stops at an edge it has already seen. `scf_edge_remap_decide` cannot
make a cycle, but nothing in the rows forbids one, so a cycle has no root and
its edges are left alone (`TestDecideCarried`).

## D4 — A restore undoes only the import's own retirement

A retired copy is restored when the conditions in D3 stop holding, but only
if its newest tier-transition note starts with the carried-retire marker for
this import's source: `carried-retire:"<source>"`, the source Go-quoted so
`yaml:a` never matches a note written for `yaml:ab`, then free human text.
The restore matches on the marker alone, so the human wording can change;
the marker is a stable format, pinned by `TestCarriedNotes`. (From the
review of #3072: matching the whole free-text note was brittle.) A copy
retired any other way stays retired
(`TestCarriedEdges_RestoreLeavesAnEdgeRetiredOtherwise`). Today the importer
is the only code path that retires an edge; the note check keeps that true
for any path added later.

## D5 — Scope: every crosswalk import, BYO and upgraded installs alike

The pass is in `importIntoTx`, so it covers every bundled crosswalk (SOC 2,
ISO 27001, NIST CSF, HIPAA, PCI DSS, through `converge-crosswalks`) and the
workbook importer.

It is not BYO-only. A default install that has imported a real SCF release
upgrade also has carried edges from the #2315 carry-forward, and on it a
dropped mapping now retires the carried copy too
(`TestCarriedEdges_UpgradeDroppedMappingRetiresTheCarriedCopy`). That is the
same rule applied to the same state. A default install that has never had an
SCF upgrade has no carried edges, and its imports are unchanged
(`TestBYOBootstrap_DefaultInstallIsUnchanged` still passes).

## D6 — Reported separately

`Report.EdgesCarriedRetired` and `Report.EdgesCarriedRestored` list these
edges apart from `EdgesRetired`/`EdgesRestored`, so the existing summary
counts and the `RetiredBySource` tally keep their meaning. A carried copy
retired here is dropped from `EdgesUnlisted`. `atlas-cli catalog
import-crosswalk` and `converge-crosswalks` print one line per carried edge;
their existing summary lines are unchanged.
