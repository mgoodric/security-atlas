# #3061 — Bring-your-own SCF release installs boot again: decisions log

detection_tier_actual: manual_review
detection_tier_target: integration

The #3055 review found the defect by reading the stock pins against `docs/SELF_HOSTING.md`. Nothing ran the bootstrap with `SCF_CATALOG` set to a release other than the sample. `TestBYOBootstrap_*` (`internal/api/soc2import/byo_release_bootstrap_integration_test.go`) is that integration-tier guard now. It plays `bootstrap.sh`'s SCF, crosswalk and stock-upload steps in the script's order, each through the package call its `atlas-cli` command makes. Its operator release is SCF 2026.3, built from `internal/api/scfseed/testdata/scf-pin.json`: all 1,591 real control IDs and titles, no descriptions (OQ #1).

A second defect surfaced in the integration tier while building the fix (D3).

## D1 — Approach A: load the sample as legacy beside the operator's release

Decided by Matt (2026-11-24, relayed by the dispatcher).

The bootstrap now:

1. imports `SCF_CATALOG`;
2. when that is not the bundled sample, loads the sample with `atlas-cli catalog import-scf --keep-current`;
3. imports the SOC 2 crosswalk and uploads the stock controls, which bind in `test-2026.3` as their pin says;
4. after the uploads, imports `SCF_CATALOG` again.

The step 4 import is a frozen no-op (#1768). `import-scf` runs `RebindControls` (#1971, #2321) and `QueueEdgeRemapProposals` (#2315) after every import, and those two move the stock controls and SOC 2 edges onto the operator's release wherever the code and title match. All 98 sample titles equal the SCF 2026.3 titles (checked against `scf-pin.json`), so on a 2026.3 install everything moves. On another release, anything whose code or title differs stays on the sample's anchor: controls are listed at `/v1/controls/needs-reanchoring`, and edges stay open in the edge-remap queue for a super-admin.

The stock bytes are not touched, so `IsStockManifest` and the #1773 D3 (in-place) and D4 (re-bind carve-out) rules keep applying.

Why the sample must not just be imported. `scfimport.Import` makes any new release_version current and demotes the previous one. Take an install bootstrapped on its own release before #3035. On its next boot it would see `test-2026.3` as new, demote the operator's release to legacy, and the CLI's re-bind would then move its controls onto the 98-row sample. Loading the sample first does not help either: on every later boot the sample is the existing release, so the operator's own re-import is the one that cannot take current back.

## D2 — `--keep-current` (`scfimport.Options.KeepCurrent`)

The flag name is the worker's call. It names what the flag protects.

- **Another release is current:** a new release is inserted with status `legacy`. Nothing is demoted, and `frameworks.latest_version_id` is not moved.
- **No release is current:** the new one becomes current as usual, so the flag never leaves an install with no current release.
- **The release is already loaded:** it keeps its status. The #1768 freeze path returns before any status write, as before.

`Report.Status` and the CLI's `status=` field report the outcome.

The bootstrap passes it only for the sample, and only when `SCF_CATALOG` is not the sample's path. The default install runs exactly the commands it ran before. The one visible change is the extra `status=current` field on the import line.

Tests:

- `TestImportKeepCurrent_LoadsANewReleaseAsLegacy` (re-runs included);
- `TestImportKeepCurrent_WithNoCurrentReleaseTheNewOneIsCurrent`;
- `TestImport_NewReleaseStillDemotesTheCurrentOne`.

## D3 — A crosswalk bound in an older release no longer retires its current-release copy

**The defect.** `TestBYOBootstrap_UpgradeFromUnpinnedInstallKeepsTheOperatorReleaseCurrent` failed on the first build: every mapped SOC 2 requirement lost their live edge on the operator's release. The mechanism:

1. An install bootstrapped before #3035 imported the SOC 2 crosswalk unpinned. Its edges, with `crosswalk_source = yaml:soc2-tsc-2017.yaml`, sit on the operator's (current) anchors.
2. The upgrade boot imports the pinned crosswalk into `test-2026.3`. The #1971 retire pass then retired every one of those edges: the crosswalk owns them and no longer lists those anchor rows.
3. The edge carry in step 4 found an edge, the retired one, already on each target anchor. `carryEdge` closes such a proposal as "already maps", so no edge came back.

Default coverage reads the current release (#2203), so the install would have lost its SOC 2 coverage.

**The fix.** In `soc2import`, `spareCurrentReleaseEdges` takes one class of edge out of the retire list. All three must hold:

- the crosswalk binds in a declared release that is not current;
- the edge's anchor is the current release's anchor for that code;
- the crosswalk lists the same requirement against the same code.

Such an edge stands for a listed mapping, one release forward. The retire pass exists for the opposite direction: an edge left on a release older than the one the crosswalk binds in (#1971). The rule cannot fire when the crosswalk binds in the current release, so every existing retire case is unchanged. The full `soc2import` and `scfimport` integration suites pass with it.

Rejected alternatives:

- **Have `carryEdge` restore a retired target edge.** The next boot's crosswalk import would retire it again, because the crosswalk still owns it. Its proposal is then decided, so nothing carries it back on the third boot.
- **Rewrite the retired edge's `crosswalk_source` to the remap.** That needs a migration to `scf_edge_remap_decide`, which refuses an approval when any edge exists for the pair. It also edits an edge's provenance after the fact.

A fresh install never hits this. Its crosswalk edges are written only in `test-2026.3`, and the carried copies carry `crosswalk_source = scf-remap:<proposal>`, which no YAML import owns.

## D4 — Rejected: approach B, re-pinning the stock content to the loaded release

Rewriting each stock manifest's `scf_release` to the operator's release at bootstrap was the other candidate in the ticket. It was rejected (dispatcher QUESTION, 2026-11-24):

- **It loses the stock rules.** `IsStockManifest` matches the sha256 of shipped bytes, so rewritten bytes are tenant manifests. That turns off the #1773 D3 in-place rule and the D4 re-bind carve-out for every stock control on that install.
- **It breaks every catalog upgrade.** `docs/SELF_HOSTING.md` makes a newer release a re-run with a new `SCF_CATALOG`. The re-pinned manifests would then change a declared `scf_release`, which supersedes (#1773 D3). All 65 stock controls would mint new versions, leaving owners, evidence, exceptions and risk links on the old rows (docs/audit-log/1908).
- **It fails on other releases.** A re-pinned crosswalk binds its codes strictly in the operator's release. A release whose codes differ from 2026.3 (2025.4, for example) fails the import outright. Approach A queues those edges for review instead.

## D5 — Stock policies seed with no links on a BYO install (not fixed here)

Measured in `TestBYOBootstrap_FreshInstallBindsStockContentOnTheOperatorRelease` on a 2026.3 install:

- `seed.Seed` with the shipped `policies/stock/` resolves the declared `test-2026.3` with no `SCFReleaseWarnings`;
- every policy links 0 controls;
- `MissingAnchors` lists all 15 linked codes.

The default install links 11 (#3033 D4).

The cause is #1772: `SQLAnchorResolver` links a code to the control bound to that code's anchor in the declared release, and step 4 moved the stock controls to the operator's release. Fixing it changes that resolver's rule: for example, also accept a control bound to the current release's anchor with the same code and title. That is a product decision on #1772 semantics, so this change does not make it. `docs/SELF_HOSTING.md` states the gap. The bootstrap does not run `seed-stock`, so the gap does not affect boot.

## D6 — The test plays the script's steps in Go, not the script itself

Phases 4 and 5 of `bootstrap.sh` need a running `atlas` server and an OAuth client. Neither the integration tier nor this machine (no Docker) can run those. The test calls, in the script's order:

- `scfimport.ImportWithOptions`, then the three post-import passes `import-scf` runs;
- `soc2import.Import`;
- `control.Store.Upload`.

The script's own new lines are three `atlas-cli catalog import-scf` invocations, and the path comparison that gates them.

Each test was observed failing before it passed:

| Mutation                                  | Tests that fail                                                     |
| ----------------------------------------- | ------------------------------------------------------------------- |
| `KeepCurrent` disabled                    | fresh install, re-run, upgrade                                      |
| Sample load dropped (the original defect) | fresh install: `unknown SCF release "test-2026.3"` at the crosswalk |
| Final re-import dropped                   | fresh install: controls left on `test-2026.3`                       |
| `spareCurrentReleaseEdges` absent         | upgrade (D3)                                                        |
| `listed[...]` check dropped (D3)          | `TestSpareCurrentRelease_DroppedMappingIsStillRetired`              |
| anchor-ID equality dropped (D3)           | `TestSpareCurrentRelease_EdgeOnAnotherReleaseAnchorIsStillRetired`  |
| freeze loop breaks before the scan (D8)   | `TestImportKeepCurrent_AnchorlessRowAheadOfTheCurrentRelease`       |
| BYO fixture title left unchanged (D10)    | `TestBYOBootstrap_DifferingAnchorsAreLeftForReview`                 |

## Review of #3063 (head 42274034)

### D7 — `--keep-current` is documented as one-way, not limited to the sample label (N1)

A release loaded with `--keep-current` stays legacy. A re-import is a frozen no-op, and nothing promotes a legacy release, so only SQL can recover from a wrong use. The review offered two options: document it, or refuse the flag for any label but the sample's.

The worker chose to document it, in the flag help and in `docs/SELF_HOSTING.md`. Refusing other labels would hard-code the sample's label into the CLI, and that label moves with each fixture re-pin (#1773 D2). The bootstrap is the only caller, and it passes only the sample's path.

### D8 — The freeze loop scans every version before deciding `otherCurrent` (N2)

The loop that finds the incoming label `break`s on an anchor-less version row. If that row sorted ahead of the current release, `otherCurrent` stayed false, and `KeepCurrent` marked the row `current` beside the real current release.

`otherCurrent` is now computed in a full pass of its own before that loop. `TestImportKeepCurrent_AnchorlessRowAheadOfTheCurrentRelease` seeds such a row and fails without the fix (`Status:current`).

### D9 — A dropped mapping leaves the carried copy live on a BYO install (N4, not fixed)

A carried edge has `crosswalk_source = scf-remap:<proposal>` (#2315), and `ownsEdge` needs an exact source match. Suppose a later bundled crosswalk drops a mapping. Its import retires the sample-anchor edge, which it wrote, but not the carried copy on the operator's release, which the remap wrote. That copy keeps counting toward default coverage.

This is the existing #2315 provenance rule. It is not new with #3061, but every BYO install now meets it on every crosswalk change. The fix would decide which source owns a carried edge, which is an ownership-model change. The dispatcher is filing it as a follow-up, and this PR does not change it.

### D10 — Differing anchors are left for review (N5)

`TestBYOBootstrap_DifferingAnchorsAreLeftForReview` boots against the 2026.3 fixture with two codes that a stock control and the SOC 2 crosswalk share (chosen in sorted order). One code's title is reworded and the other code is removed.

| Code           | Stock control                                                                                       | SOC 2 edge                                                                       |
| -------------- | --------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Reworded title | moves: stock controls re-bind on a title-only change (#2321), so it is not on the re-anchoring list | waits as an open edge-remap proposal: edges carry only on code and title (#2315) |
| Removed        | listed for re-anchoring                                                                             | waits as an open proposal                                                        |

### D11 — D5 is asserted, not just described (N6)

The fresh-install test now asserts the measured gap: every stock policy links 0 controls, and `MissingAnchors` holds all 15 linked codes. A resolver fix for D5 makes the test fail, and the fix updates it. The dispatcher is filing D5 as a follow-up that needs Matt's decision on #1772.

### D12 — Smaller items

- **N7:** `docs/SELF_HOSTING.md` tells operators to re-import their catalog after a hand-run `import-crosswalk`, so the new edges move onto their release. The other bundled crosswalks are not covered by a D3 upgrade test. They go through the same `importIntoTx` retire pass as SOC 2, which the `TestSpareCurrentRelease_*` tests exercise directly.
- **N8:** after #3055, `declaredSCFRelease` never returns an empty release, so the `release == ""` guard in `spareCurrentReleaseEdges` is removed.
- **N9:** `Report.Status` stays a `string`. It is printed as is, and `IsNewVersion` and the other `Report` fields are plain types too.
