# #1773 — Pin stock content to the sample's SCF release: decisions log

detection_tier_actual: manual_review
detection_tier_target: integration

Three problems surfaced. The first two were caught in the integration tier before the PR opened; the third was caught by the PR review (D3, "Scope"). First, declaring `scf_release` on the 65 shipped bundles would have superseded every stock control on upgrade, which orphans owners and evidence. `stock-supersede-check` and `TestUpgradeFromUnpinnedStock_UpdatesInPlace` both catch it. Second, a pinned stock manifest was classified `OutcomePinned`, which switched off the remap machinery for stock controls. The scfimport stock re-bind tests caught that one.

## D1 — The pinned release is `test-2026.3`, not `2026.3` (deviation from AC #2)

The ticket's AC #2 says the sample catalog's release label is "the pinned real release". The real release the sample was regenerated from is SCF 2026.3 (`scfPinRelease` in `internal/api/scfseed/scfpin_test.go`). The label stays `test-2026.3`. The label names that real release, and the `test-` prefix marks the file as a 77-row subset whose descriptions are security-atlas's own wording, not SCF text (OQ #1).

Decided by the dispatcher on Matt's behalf (2026-11-24, option A). Matt can overrule it at review. The reasons:

- **The freeze.** #1768 freezes an imported release label. `scfimport.diffRelease` refuses a re-import of an existing label whose anchors are added, changed or missing. If bootstrap imported the subset as `2026.3`, an operator who later imports the real SCF 2026.3 workbook (about 1,400 controls, real descriptions) would get a `ReleaseConflictError` and could never load 2026.3 under its own name.
- **Matt's 2026-11-14 decision on #1717.** It allowed one label bump for the whole 2026.3 remap, and #1999 already spent it (`test-2026.1` → `test-2026.3`). A second bump would also put every upgraded install through a `test-2026.3` → `2026.3` remap queue in which every proposal is the "unchanged" class.

`TestSampleReleaseLabelNamesThePinnedSCFRelease` holds the label to `"test-" + scfPinRelease`.

## D2 — `test-2026.3` is frozen

`fixturepin_test.go` called `test-2026.3` an open working label, one that each #1717 family group re-pinned until the remap was complete. #1717 is closed and `knownMismatchCeiling` is 0, so that exception is gone. Stock content now declares this label, so changed anchor content has to ship under a new label. The stock bundles and crosswalks move to that new label in the same PR.

## D3 — A shipped stock manifest that adds `scf_release` updates in place

Every install bootstrapped before #1773 stores the unpinned stock manifests. Bootstrap re-uploads every bundle on each boot, and `control.SupersedingFields` treated any new key as superseding. So all 65 stock controls would have minted new versions, and their owners, evidence, exceptions and risk links would have stayed on the old rows (docs/audit-log/1908).

The rule, in `SupersedingFields`: when the prior manifest has no `scf_release` (absent, null or blank) **and the next manifest is a shipped stock manifest** (`scfimport.IsStockManifest` on its bytes and `bundle_id`), the next manifest's `scf_release` is ignored for the supersede decision. The upload still decides the anchor. In the in-place branch, `Store.Upload` passes the **prior** manifest's pin to `rebindAnchor`, so a newly pinned control is re-bound or flagged exactly as an unpinned one would be (#1971 D3: it moves only to a same-code, same-title row in the current release). Every later upload holds to the pin.

What still supersedes: changing a declared `scf_release`, removing one, or declaring one alongside any other superseding change, such as a different `scf_anchor_id`.

**Scope: stock only (review of #3035, head 88445ca6).** The first head applied the rule to every manifest, which broke two things:

- **#1776 remap approval.** `approveLocked` rewrites the stored manifest (`scf_anchor_id`, plus an added `scf_release`) and requires the upload to supersede. On an unpinned tenant control whose approved code equals its legacy code, such as a renamed successor that keeps its code, the rewrite differed only by the added `scf_release`. The upload went in place and the approval failed with `ErrRemapControlChanged`.
- **A tenant declaring an older release.** The control stayed on its current-release anchor while its manifest pinned another release, and `Classify` then treated it as pinned for good.

Of the two fixes the review offered, this takes the stock-only gate, not "the declared release resolves to the current anchor". The stock gate matches the only case the rule exists for, the boot re-upload of shipped bytes. It needs no database lookup inside a pure comparison that the CI tripwire also calls. And it can't misfire on an install whose stock has already moved past `test-2026.3`. A tenant bundle that adds `scf_release` supersedes onto the anchor that release names, as before #1773.

`stock-supersede-check` uses the same function, so the tripwire reports this PR as all in place. Tests:

- `TestSupersedingFields_SCFRelease` and the two new cases in `TestManifestsEqualIgnoringMetadata` cover the rule at the unit level.
- `TestUpgradeFromUnpinnedStock_UpdatesInPlace` uploads the pre-#1773 bytes of all 65 bundles, then the shipped bytes. Each control keeps its id, version and anchor. Both tests fail with the rule disabled.
- `TestRemapApprove_SameCodeOnUnpinnedTenantControlSupersedes` and `TestUpload_TenantDeclaringOlderReleaseSupersedesOntoIt` (`internal/api/scfimport`) pin the tenant side. Both fail on the first head's unscoped rule; the first fails with "did not supersede it".

A future re-pin of the stock content to a new label changes a declared release, so it supersedes under this rule. That PR needs its own path, such as treating a release change whose code resolves to a same-code, same-title anchor as in place. Building it here would be speculative.

## D4 — A pinned stock manifest is still classified as unpinned on a release import

Decided by the dispatcher on Matt's behalf (2026-11-24, option 1).

Before this PR, `scfimport.Classify` returned `OutcomePinned` for any manifest that declares `scf_release` (#1770). That outcome is neither moved nor flagged, and `RebindControls` queues no remap proposal for it. Once the stock bundles were pinned, importing a newer SCF release would have skipped all 65 stock controls. They would get no #1775 proposal, no #2321 stock title-only re-bind, and no entry on `/v1/controls/needs-reanchoring`.

The carve-out: a pinned manifest for which `IsStockManifest` returns true is classified as if unpinned. `IsStockManifest` matches the sha256 of shipped revisions only, so a tenant cannot opt in. For stock content, the pin governs only which release the codes resolve in at upload. Tenant bundles keep #1770 semantics.

The carve-out applies at release import (`Classify` and `RebindControls`), not on the upload re-bind. When `Store.Upload` sees an unchanged stock manifest, `rebindAnchor` gets that manifest's own pin and does nothing. This was left as is on purpose (review of #3035, non-blocking):

- **Upgrades that already re-bound:** an install whose stock moved to a release newer than `test-2026.3` is not moved back, because the pinned release is not current.
- **A new stock bundle:** one first shipped after such an install upgraded binds to its `test-2026.3` anchor on upload. The next SCF import's `RebindControls` moves it, or it shows on `/v1/controls/needs-reanchoring`. The upload re-bind only touches existing controls, so applying the carve-out there would not change this case.

Tests in `TestClassify_StockTitleOnlyChange`:

- the shipped, pinned manifest classifies as before;
- the pre-#1773 bytes classify as rebindable;
- a tenant pinned manifest is `OutcomePinned`;
- the stock bytes with one byte appended are `OutcomePinned`.

## D5 — Stock-upgrade integration tests use the pre-#1773 shipped bytes

Five tests model "a stock control bound in synthetic release A, then release B imported": the scfimport held-back (x2), keep-reviewed-anchor and re-bind tests, and the soc2import `TestSCFRemapUpgrade_RetiresEdgesLeftOnTheOldRelease` crosswalk. The pinned bytes resolve only in `test-2026.3`, which those tests do not load. So they use the shipped manifest (or crosswalk) without its `scf_release`. That is what every install bootstrapped before #1773 stores, and it is still on the stock hash list. Each test says so at the point of use. The re-bind test seeds the stored manifest directly and keeps the shipped bytes, which exercises D4.

`TestStockSOC2Upgrade_RewordedDescriptionKeepsOwnerAndEvidence` seeds its anchors in a legacy `test-2026.3` version (`seedSCFAnchorInRelease`), because a declared release resolves whatever its status.

## D6 — Stock policies are out of scope

`policies/stock/*.md` declare `scf_release: "2026.3"` (#1772). A fresh install does not load that release, so the policy seed warns and binds in the current release. Four of their linked codes (`IAC-22`, `CHG-04`, `IRO-01`, `TPM-04`) are not in the sample. AC #1 names control bundles and crosswalks only, so the policies are unchanged. The dispatcher is filing a follow-up.
