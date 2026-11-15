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
release A's anchor for a code even when the demo data meant B's, and it adopted A's even
when its own release was not loaded at all. The new
`TestApply_PostureSpineAdoptsFromTheDeclaredRelease` fails on the old query
(two of three cases) and passes on the new one.

## D1 — The stock content declares SCF 2026.3

**Decision.** The five stock policies and the demo anchor slice declare
`2026.3`. Every code they name is in the pinned SCF 2026.3 workbook
(`internal/api/scfseed/testdata/scf-pin.json`, checked code by code).

**Why.** The ticket says the seed binds in "the release their seed data
declares", so the data has to declare one, and the only real release the repo
pins is 2026.3. #1773 pins the stock control bundles, crosswalks and sample
catalog label to the same release; until it lands, a database holding only
the sample catalog (labelled `test-…`) does not have 2026.3 loaded, and both
seeds behave as D2 and D3 describe.

## D2 — Policy seed: an unloaded declared release links nothing, and says so

**Decision.** When a policy's declared release is not loaded, the seed still
inserts the policy, links none of its controls, records every code as
missing, and adds a `SCFReleaseWarnings` entry naming the release and the
remedy (`atlas-cli catalog import-scf`, then re-seed). It never binds in
another release. A policy with no `scf_release` binds in the current release
with a deprecation warning, as bundles and crosswalks do (#1770, #1771).

**Why.** #1771 refuses an import whose declared release is not loaded. The
policy seed has always treated links as best-effort: a missing anchor drops
the link and the policy is flagged orphan (slice 297 AC-7). Refusing the whole
seed would stop `policy seed-stock` on every install that has not loaded
2026.3, which is a regression in an expand step. Dropping the links keeps the
seed working and keeps the binding honest.

## D3 — Demo seed: an unloaded declared release synthesizes, as before

**Decision.** The spine adopts a global anchor only when `DemoSCFRelease` is
loaded and has the code. Otherwise it synthesizes the tenant-scoped demo
anchor, which is what it already did when no catalog was loaded.

**Why.** The demo seed is self-contained by design (slice 682 AC-4); the
synthesize branch is its existing answer to "the catalog is not there". The
only change is which catalog counts. The existing demo tests run against a
database without 2026.3 and take the synthesize branch, as they did before
any catalog was loaded, and pass unchanged.

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
