# Issue #1772 — Seeding and suggestion features resolve anchors in an explicit SCF release

Part of #1718 (release-aware SCF anchors), after the resolver (#1770) and the
crosswalk and OSCAL component imports (#1771). Three features that matched
anchors by raw `scf_id` now resolve each code through `internal/scfrelease` in
one named release:

| Feature                      | Where the release comes from                                        |
| ---------------------------- | ------------------------------------------------------------------- |
| Stock policy seed            | `scf_release` in each `policies/stock/*.md` frontmatter (`2026.3`)  |
| Demo seed posture spine      | `demoseed.DemoSCFRelease` (`2026.3`)                                |
| Question-mapping suggestions | `?scf_release=` on the suggest route; the current release otherwise |

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

The bug the work exposed: the demo seed adopted "the oldest global anchor with
this code" across every loaded release. With two releases loaded it bound
release A's anchor for a code even when the demo data meant B's, and even
when B was the current release. The new
`TestApply_PostureSpineAdoptsFromTheDeclaredRelease` fails on the old query
(two of three cases) and passes on the new one.

A second bug was caught in review (`detection_tier_actual`: manual_review,
`detection_tier_target`: integration): the first version linked nothing when
a declared release was not loaded. On a default install (only the sample
catalog, `test-2026.3`, current) the stock policies declaring `2026.3` then
linked zero controls where `main` linked nine. D2 and D3 are the fix.

## D1 — The stock content declares SCF 2026.3

**Decision.** The five stock policies and the demo anchor slice declare
`2026.3`. Every code they name is in the pinned SCF 2026.3 workbook
(`internal/api/scfseed/testdata/scf-pin.json`, checked code by code).

**Why.** The ticket says the seed binds in "the release their seed data
declares", so the data has to declare one, and the only real release the repo
pins is 2026.3. #1773 pins the stock control bundles, crosswalks and sample
catalog label to the same release. It is blocked by #1717, so until it lands a
default install does not have 2026.3 loaded, and both seeds fall back as D2
and D3 describe.

## D2 — Policy seed: an unloaded declared release falls back to current, warned

**Decision.** When a policy's declared release is not loaded, its codes bind
in the current release, and `SCFReleaseWarnings` names the declared release,
the release used and the remedy (`atlas-cli catalog import-scf`, then
re-seed). A policy with no `scf_release` binds in the current release with a
deprecation warning, as bundles and crosswalks do (#1770, #1771). With no SCF
release current at all, the seed still inserts the policies, links none of
their controls, records every code as missing, and warns.

**Why.** This is #1771 D5's rule for a workbook sheet release that is not
loaded: in the expand step nothing that works today may stop working. The
stock policies are the platform's own content, and until #1773 lands their
declared release is not on a default install; refusing, or linking nothing,
would regress `policy seed-stock` there. The warning makes the fallback
visible, and #1774, the contract step, makes the declared release strict.
The no-release case keeps the slice 297 best-effort contract: a missing
anchor drops the link and the policy is flagged orphan.

## D3 — Demo seed: an unloaded declared release adopts from current

**Decision.** The spine adopts from `DemoSCFRelease` when it is loaded,
else from the current release, and every code resolves in that one release.
A code the chosen release lacks, or every code when no SCF release is
current, gets the tenant-scoped synthesized anchor.

**Why.** It is the same fallback as D2, for the same reason: a default install
adopts the sample catalog's anchors today, and the demo must keep doing so
until #1773 lands. Synthesis stays the answer to "no catalog", which is what
the demo seed was built for (slice 682 AC-4). The existing demo tests pass
unchanged.

## D4 — Policy links go through the anchor, not `controls.scf_id`

**Decision.** `SQLAnchorResolver` resolves each code to an anchor in the
release, then finds the control whose `scf_anchor_id` is that anchor. It no
longer matches the free-text `controls.scf_id`.

**Why.** `controls.scf_id` is the bare code and carries no release, so two
controls bound to A's and B's IAC-06 both match it. Every control a bundle
upload writes has both columns set together (`internal/control/store.go`), so
matching on the anchor loses nothing a real install has. The integration tests
that seeded controls with only `scf_id` now bind them to fixture anchors.

## D5 — Suggestions: one release per call, named in the output

**Decision.** `Suggest` looks up the release once (`SuggestParams.SCFRelease`,
empty for current). Candidates are retrieved in it, the model's pick is
resolved in it through `scfrelease.Resolve`, and `Proposal.scf_release` names
it. An unknown release is `scfrelease.ErrUnknownRelease`, a 404 on the route
(the anchor reads' mapping, #1770). With no release named and no SCF release
loaded, the suggestion is manual-only, as it was with an empty catalog.

**Why.** The proposal row already stores `scf_anchor_uuid`, which pins it to
one release, so no migration is needed. The approved mapping is still written
to `questionnaire_questions.scf_anchor_id` as a code; giving questions a
release column is not in this ticket, and the remap work (#1775) is where a
question's release is read.

## Remaining bare-code anchor lookups

Every production path that still turns an SCF code into an anchor without a
named release, and why it stays:

| Path                                                                              | Why it stays                                                                                                      |
| --------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `internal/control/store.go` `bindSCFAnchor`, `release == ""`                      | Bundle fallback with deprecation warning (#1770); #1774 removes it.                                               |
| `internal/api/soc2import/import.go` `newAnchorBinder`, `release == ""`            | Crosswalk fallback with deprecation warning (#1771); #1774 removes it.                                            |
| `internal/oscal/componentimport` with no declared release                         | OSCAL component fallback with deprecation warning (#1771); #1774 removes it.                                      |
| `internal/api/oscalcomponents/store.go` `MapScfAnchor` (`GetSCFAnchorBySCFID`)    | OSCAL claim re-map validates in the current release and records it (#1771 D3); the remap queue (#1775) owns it.   |
| `internal/oscal/catalogimport`, `internal/oscal/profileimport` `loadSCFAnchorIDs` | Current-release code set for OSCAL catalog/profile imports; not one of this ticket's features. Flagged in the PR. |
| `internal/api/scfseed` (`GetSCFAnchorBySCFID`)                                    | Integration-test seeding helper; its completeness probe reads the current release on purpose.                     |
| `internal/api/search/search.go` `searchAnchors`                                   | Keyword search shows the current release's anchors; it returns rows, it binds nothing.                            |
| `fw_to_scf_overlay_edges.sql` `anchor_code` filter                                | A list filter; each returned edge carries its anchor's release.                                                   |
