# #3033 — Pin the stock policies to the sample's SCF release: decisions log

detection_tier_actual: manual_review
detection_tier_target: unit

The defect was found by the #1773 worker reading the stock policies (#1773 D6). There was no guard to catch it: nothing checked that a stock policy's `scf_release` is loaded on a fresh install or that its codes exist there. `TestStockPoliciesPinTheSampleRelease` (`internal/api/scfseed/stockpin_test.go`) is that unit-tier guard now.

## D1 — The four missing codes are remapped, not added to the sample

The ticket allowed either remapping each code to one the sample has, or adding the row to the sample under the frozen label with an updated content hash. Adding a row is not safe:

- `scfimport.diffRelease` (`internal/api/scfimport/catalog.go`) refuses a re-import of a loaded label whose anchors are **added**, changed or missing (#1768).
- The docker bootstrap re-imports `migrations/fixtures/scf-sample.json` on every boot.
- So an install that already loaded `test-2026.3` with 77 rows would get a `ReleaseConflictError` on its next boot. Updating `fixtureContentByRelease` would only silence the guard that exists to catch exactly this (`fixturepin_test.go`, #1971). #1773 D2 records the same rule: changed anchor content ships under a new label.

Each replacement is a sample row whose SCF 2026.3 title matches a section the policy body already has. Each policy still declares three codes. How many of them link a control depends on which controls are bound (D4).

| Policy            | Was      | SCF 2026.3 title (was)                       | Now      | SCF 2026.3 title (now)                                     | Body section it covers                                                       |
| ----------------- | -------- | -------------------------------------------- | -------- | ---------------------------------------------------------- | ---------------------------------------------------------------------------- |
| Access Control    | `IAC-22` | User Responsibilities for Account Management | `IAC-12` | Periodic Review of Individual & Service Account Privileges | "Access review" (quarterly / annual reviews)                                 |
| Change Management | `CHG-04` | Prohibition Of Changes                       | `CHG-03` | Configuration Change Control                               | "Configuration change"; review before deploy                                 |
| Incident Response | `IRO-01` | Incident Response Policy                     | `IRO-14` | Root Cause Analysis (RCA) & Lessons Learned                | "Post-incident review"                                                       |
| Vendor Management | `TPM-04` | Supply Chain Risk Management (SCRM)          | `TPM-14` | Third-Party Contract Requirements                          | contractual data-handling terms in "Vendor classification" and "Offboarding" |

`IRO-01` (a policy-level control) would have been the closest fit for the plan as a whole. `IRO-08` (Incident Response Plan) is already linked and carries that meaning. `IRO-14` adds coverage the plan states and no other link names.

**Decided by Matt (2026-11-24, review of #3042): keep `IRO-14`.** He considered `IRO-16` (Incident Stakeholder Reporting), which matches the plan's "Notification" section, and declined it. Neither code has a bound stock control (D4), so the choice does not change what a fresh install links today.

Titles are from `internal/api/scfseed/testdata/scf-pin.json` (SCF 2026.3, IDs and titles only).

## D2 — No in-place rule is needed for policies

The ticket and the dispatcher asked whether re-pinning supersedes or republishes existing policies, as adding `scf_release` would have superseded stock controls before #1773 D3. It does not:

- A `policies` row stores no `scf_release`. The release only steers which anchors the codes resolve in when `seed.Seed` inserts the row.
- `seed.Seed` is called only by `atlas-cli policy seed-stock`, and it only INSERTs drafts with no predecessor. The bootstrap does not call it, and no upgrade path reads `policies/stock/`.

So there is no stock-policy equivalent of `IsStockManifest` to extend. `TestSeed_UpgradeFromPreRepinStockKeepsPolicyHistory` seeds the pre-#3033 files, publishes a 1.0.0 and a 1.1.0 of one policy, then re-imports the sample and re-runs the seed with the shipped files. Every prior row keeps its id, version, status, predecessor and links. The version chain is unchanged, and the re-seed writes only fresh drafts.

That test holds by construction today. It has not been observed failing, because no code path exists that would make it fail. It guards against one being added.

Re-running `seed-stock` on an already-seeded tenant still inserts five more drafts. That is existing behaviour and out of scope here.

## D3 — The fresh-install seed needs no fallback

#1774 removes the current-release fallback from every write path, the policy seed included. `TestSeed_FreshInstallBindsStockPoliciesInTheSampleRelease` imports the sample, binds a test control to each linked code in `test-2026.3`, and seeds the shipped files. It asserts:

- no `SCFReleaseWarnings`;
- no `MissingAnchors`;
- three links per policy.

Against the pre-#3033 files it fails with five fallback warnings and the four missing codes.

## D4 — Links without a bound stock control still drop (review of #3042)

The seed links a code only when a control is bound to its anchor. Four declared codes have no bound control among the 65 shipped stock bundles (`controls/soc2/*/control.yaml`): `CFG-04`, `IRO-02`, `IRO-14` and `TPM-14`. On a fresh install with only stock controls, those links drop and are reported in `MissingAnchors`.

| Policy               | Linked before #3033 | Linked after |
| -------------------- | ------------------- | ------------ |
| Information Security | 3                   | 3            |
| Access Control       | 2                   | 3            |
| Change Management    | 1                   | 2            |
| Incident Response    | 1                   | 1            |
| Vendor Management    | 2                   | 2            |

Both columns use the same 65 bundles. No policy links fewer controls than before. Adding stock control bundles for those four codes is a separate change, and this PR does not make it.
