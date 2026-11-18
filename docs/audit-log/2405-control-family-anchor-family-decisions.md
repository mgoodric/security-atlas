# #2405 — Stock `control_family` follows the anchor's SCF family: decisions log

detection_tier_actual: manual_review
detection_tier_target: unit

Follow-ups from the review of #2398 (#1979, the GOV and HRS remap). #1979 was the first #1717 group to move a code across families (`HRS-04`→`SAT-04`). The sample row took family `SAT`, but `soc2_cc1_4_competence_training` kept `control_family: HRS`. Nothing checked the two against each other, so the review caught it and no test did. That is why the target tier is `unit`.

## D1 — `control_family` tracks the anchor's family

The dispatcher relayed this decision on 2026-11-18. A stock control's `control_family` equals the SCF family of its `scf_anchor_id`, which is the code's prefix (`SAT-04` is `SAT`). The label shows where a control is filed, in the controls list, the MCP `controls` tool and the OSCAL statement label, so it should not disagree with the anchor's family.

`TestStockControlFamilyMatchesAnchorPrefix` (`internal/control/control_family_test.go`) parses every `control.yaml` under `controls/` and fails on a mismatch. A bundle that may differ needs an entry in `controlFamilyExceptions`, with a comment giving the reason. The list starts empty. The test also fails when an entry is no longer needed, so stale entries cannot pile up.

It skips a bundle with no anchor (`anchor_fit: none`) and a bundle that omits `control_family`. In both cases the store picks the family itself: it takes the anchor row's family, or `UNKNOWN` when there is no anchor.

## D2 — Four stock controls corrected, none allow-listed

The new test failed on four bundles:

| Bundle                           | Anchor   | Was   | Now   | Moved by       |
| -------------------------------- | -------- | ----- | ----- | -------------- |
| `soc2_cc1_4_competence_training` | `SAT-04` | `HRS` | `SAT` | #1979 (HRS-04) |
| `soc2_pi1_2_input_completeness`  | `TDA-05` | `SEA` | `TDA` | #1981 (SEA-05) |
| `soc2_pi1_3_processing_validity` | `TDA-05` | `SEA` | `TDA` | #1981 (SEA-05) |
| `soc2_pi1_4_output_delivery`     | `TDA-05` | `SEA` | `TDA` | #1981 (SEA-05) |

The three PI controls were the same drift from #1981 (`SEA-05`→`TDA-05`), and its review missed them too. None of the four has a reason to keep its old family, so all were corrected and none was allow-listed. `scripts/gen-stock-control-hashes.sh` added their four new manifest hashes.

## D3 — Upgrade effect

`control_family` is not one of the keys an upload can change in place (`inPlaceManifestKeys`: description, external refs, anchor-fit note). Changing it therefore supersedes the control. All four controls already changed anchor in #1979 or #1981, and neither change has been released yet (`changelog.d/1979.changed.md` and `changelog.d/1981.changed.md`). An install upgrading from the last release supersedes each of them once, as those fragments already say. Only an install that booted between #1979/#1981 and this change supersedes them a second time.

## D4 — Personnel Termination wording

The 2026.3 title of `HRS-12` is "Personnel Termination". The legacy title was "Personnel Termination Process". "The Personnel Termination (HRS-12)" read like a process with its noun cut off. Three crosswalk rationales now name the control by title and code:

- ISO A.5.11: "a step in Personnel Termination (HRS-12)";
- ISO A.6.5: "maps to SCF Personnel Termination (HRS-12)";
- HIPAA §164.308(a)(3)(ii)(C): "map directly to SCF Personnel Termination (HRS-12)".

## D5 — Historical "was" text in the HIPAA crosswalk

This note was added to D2 of `1979-scf-gov-hrs-remap-decisions.md`, next to the other quotes of history that pass handled.
