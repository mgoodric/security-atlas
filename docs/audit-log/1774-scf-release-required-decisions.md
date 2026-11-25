# Issue #1774 — An SCF release is required on every write path

The contract step of #1718 (release-aware SCF anchors), after the resolver
(#1770), the crosswalk and OSCAL imports (#1771), seeding and suggestions
(#1772) and the stock content pin (#1773, #3033). Every write that turns an
SCF code into an anchor now names the release it means. A missing release, a
release that is not loaded, and a code the release lacks each fail, write
nothing, and give the fix. None of them falls back to the current release.

- `detection_tier_actual`: none
- `detection_tier_target`: none

No bug surfaced during the work. The change is a contract tightening, and
each path's integration test was rewritten from asserting the fallback to
asserting the refusal.

| Write path                                    | Release comes from                                 | Without it                                             |
| --------------------------------------------- | -------------------------------------------------- | ------------------------------------------------------ |
| Control bundle upload                         | `scf_release` in `control.yaml`                    | 400, `control.ErrSCFReleaseRequired`                   |
| Crosswalk import (YAML)                       | top-level `scf_release`                            | `soc2import.ErrSCFReleaseRequired`                     |
| Crosswalk import (workbook)                   | `SCF <release>` sheet, or `--scf-release`          | unloaded sheet release refused (D3)                    |
| OSCAL component, catalog and profile import   | `--scf-release` or the `scf-release` metadata prop | `<importer>.ErrSCFReleaseRequired`                     |
| Vendor-claim mapping (`PATCH …/scf-anchor`)   | `scf_release` in the body                          | 422, `oscalcomponents.ErrSCFReleaseRequired`           |
| Mapping overlay edge (create and bulk import) | `scf_release` on the edge                          | 400 `invalid_overlay_edge`                             |
| Stock policy seed                             | `scf_release` in the policy frontmatter            | `seed.ErrSCFReleaseRequired`; unloaded release refused |
| Demo seed posture spine                       | `demoseed.DemoSCFRelease` (`test-2026.3`)          | unloaded release: every anchor synthesized (D5)        |
| Control import approval (#1784)               | the batch's recorded release (staging: D6)         | already strict (`ErrNoBatchRelease`)                   |
| SCF remap approval (#1775)                    | the proposal's target release                      | already strict                                         |

Every error wraps `scfrelease.ErrReleaseRequired`, so a caller can test for
it without knowing the path.

## D1 — An anchor UUID needs no release; an unanchored control needs none

**Decision.** `control.yaml` must declare `scf_release` when `scf_anchor_id` is
an SCF code. A UUID `scf_anchor_id` names exactly one anchor row, which belongs
to exactly one release, so it is not "resolving a code" and needs no release.
`anchor_fit: none` has no anchor to bind. `Manifest.MissingSCFRelease` holds the
rule. Upload (`bindSCFAnchor`) and `controls validate` both apply it.

**Why.** The ticket is about binding by code. Machine-written bundles
(`IsPinned` already treats a UUID ref as pinned, #1971) would break for no
gain if a UUID also needed a label.

## D2 — The check runs at upload, not in `ValidateStructural`

**Decision.** The missing-release refusal is in the upload path and in
`controls validate`, not in `Manifest.ValidateStructural`.

**Why.** `ValidateStructural` also runs when the platform re-parses manifests
it stored itself: remap re-anchoring, bundle download, the test runner. A
control stored before this change without `scf_release` must keep working
there, so only a write binds strictly. That is also the upgrade answer the
dispatcher asked for. Boot is unaffected, because bootstrap uploads only the
shipped stock bundles, crosswalks and policies, and all of them declare
`test-2026.3` (#1773, #3033). A stored tenant manifest without `scf_release`
fails only when it is re-uploaded.

## D3 — An unloaded workbook sheet release is refused; renaming is the override

**Decision.** A workbook whose `SCF <release>` sheet names a release that is
not loaded now fails like any other declared release. There is no override
flag. The error and `docs/operator/scf-crosswalk-import.md` give two
supported paths: rename the sheet to `SCF <loaded release>`, or convert the
workbook to YAML declaring `scf_release`.

A sheet name can only spell an `SCF <yyyy>.<n>` release
(`scfworkbook.SheetRelease`), so a workbook cannot be renamed onto the
bundled sample's `test-2026.3` label. YAML is the route there. The
workbook-fixture tests that relied on the fallback to reach the sample now
declare it on the loaded crosswalk, as a converted YAML file would
(`inSample` in `internal/api/soc2import`).

The common fix is the one the error names first: import the workbook's own
release, `atlas-cli catalog import-scf <same .xlsx>`, then import the
crosswalk. There is no workbook-to-YAML converter; converting means writing
the YAML by hand.

**Why.** The owner's #1774 comment asked for a choice between an override
flag and documenting the rename. An override would be a second way to
declare the release that contradicts the workbook's own statement, which
#1771 refused on purpose. The rename keeps the workbook as the single source
of the claim, and it is one edit.

## D4 — The vendor-claim mapping takes the release from the request

**Decision.** `PATCH /v1/oscal/component-claims/{id}/scf-anchor` requires
`scf_release`. `GET /v1/anchors` (the current-release list) now reports the
`scf_release` its rows are in, read from the listed rows. The claims page
sends that value with the picked code.

**Why.** The mapping binds a code directly, with no proposal in between, so
it is a write that resolved in the current release. The picker shows anchors
of exactly one release, so the release the operator picked from is known and
can be named. An unmapped claim does not record its import's release
(`releaseOf` returns nil for it), so the claim row cannot supply one.

## D5 — The demo seed pins `test-2026.3` and synthesizes when it is not loaded

**Decision.** `DemoSCFRelease` moves from `2026.3` to `test-2026.3`, the
bootstrap sample's label, matching the other stock content (#1773: "test-" +
the real release the sample was regenerated from). When that release is not
loaded, the spine adopts nothing and synthesizes every anchor, as on an
install with no catalog.

**Why.** No install loads `2026.3`, so with the fallback removed the old pin
would never adopt. On a fresh install the demo data would quietly stop
hanging off the real catalog anchors. With `test-2026.3`, a fresh install
adopts exactly what it adopted before through the fallback (7 of 8 codes;
`TPM-04` is not in the 77-row sample and is synthesized, as before).
`TestDemoSCFAnchors_MatchPinnedRelease` now checks `"test-" + pin release`.

## D6 — Two staged writes keep their read default (deliberate exclusions)

Two paths take the release that is current _when they read_, record it, and
then bind only in the recorded release. Neither binds a code in whatever
release is current at the moment of the write. Both are Matt's calls through
the dispatcher, 2026-11-24.

**1. The AI question-mapping suggestion** (option a).
`POST …/scf-mapping/ai-suggest` without `?scf_release=` reads its candidates
in the current release (`internal/qmapsuggest/service.go:72`). Both the
candidates and the model's pick are resolved inside that one release
(`ResolveAnchorInRelease(rel.Version, …)`). The proposal row records it
through `scf_anchor_uuid`, a foreign key to one anchor row of one frozen
release, and the response returns `scf_release`. Approval does no lookup: it
copies the recorded code (`internal/qmapsuggest/store.go:225`). Its only
caller, the web answer-editor panel, has no release to send.
`TestApprove_KeepsTheRecordedReleaseWhenTheCurrentReleaseChanges` suggests
`CRY-03.1`, a code only release B has, while B is current. It then makes A
current and approves. The question is mapped to `CRY-03.1`, which a lookup in
the current release (A) could not produce.

**2. Control-import batch staging.** Staging a control import
(`internal/controlimport/batch.go:248`, `store_suggest.go:63-85`) records
the release current at staging on the batch, without the operator choosing
it. The staging response returns it as `scf_release` and the review page
shows it as a badge (`web/app/(authed)/controls/imports/[id]/page.tsx:182`).
Approval (`approve.go`) binds every row in that recorded release: a typed-in
code becomes the control's `scf_release`, and an anchor UUID must belong to
the batch's release (`approve.go:370-381`). It never binds in the current
release. `TestApproveRow_BindsInTheStagedReleaseAfterTheCurrentReleaseChanges`
stages while B is current, makes A current, and approves `IAC-06`. The
control is bound to B's `IAC-06` row, not A's.

## D7 — What stays as it is

- **Read endpoints** (`/v1/anchors`, `/v1/anchors/{id}`, coverage, audited-under
  candidates) default to the current release and report which one they used,
  as #1718 user story 9 asks.
- **The SCF re-bind and remap queue** (`internal/api/scfimport/rebind.go`,
  `ListControlsOnLegacySCFAnchors`) still compares controls against the new
  current release after an SCF import. That is the reviewed upgrade mechanism
  (#1971 D3, #1775). It moves a control only when its code and title are
  unchanged, and only when the control is not pinned. Since this change, a
  pin is every code-anchored control uploaded from now on.
- **The edge and overlay remap queues** (`internal/api/scfimport/edgeremap.go:63`,
  `overlayremap.go:75`) join each legacy edge to the current release to
  propose its successor. That is the same reviewed upgrade mechanism (#2315,
  #1804): nothing moves until a person approves, and approval binds in the
  proposal's target release.
- **Search** (`internal/api/search/search.go:593`) reads anchors in the
  current release. It is a read.
- **`AnchorCoverageWarning`**, the non-fatal note after an upload, reads the
  bound anchor by UUID, and its parent code in that anchor's own release
  (`GetSCFAnchorByVersionAndSCFID`), in a read-only transaction.
- **The in-place stock upgrade still re-binds on upload.** A shipped stock
  manifest that newly declares `scf_release` over a stored one that declared
  none updates the control in place (#1773). Because the stored manifest was
  unpinned, `rebindAnchor` moves or flags it under D3 and records
  `SourceUpload`. That is the only upload path that re-binds, and
  `TestUpload_ShippedStockPinUpgradeRebindsInPlaceAndRecordsUpload` covers it.
  The byte-identical branch's call was dead (its manifest is always pinned)
  and is gone.

## D8 — OSCAL imports refuse a missing release before the bridge

**Decision.** The OSCAL catalog, profile and component imports check for a
declared release right after reading it (`scfbind.Declared`). That is before
the bridge round-trip, so a refused import writes nothing, not even the
rejection audit row the bridge-failure path writes. `scfbind.Bind` still
refuses an empty release as a backstop.

**Why.** Review of #3055: the first version found the missing release only
inside `persist`, after the bridge had validated the document. A request
missing a required field is not a validation rejection worth an audit row.
