# #1973 — SCF 2026.3 remap, AST + BCD family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The third #1717 family group. It follows #1971 and #1972 (`1972-scf-aaa-mon-remap-decisions.md`): the sample stays on the open working label `test-2026.3`, the remap rows go into `data/scfremap/remaps.json`, and the boot-time re-bind and crosswalk edge retirement from #1999 do the upgrade work. No migration. The only failure seen was the expected one: `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, and the open label's hash was updated.

## D1 — The remap

| Sample ID | 2026.3 ID | 2026.3 title (verbatim)                               | Basis in `remaps.json` |
| --------- | --------- | ----------------------------------------------------- | ---------------------- |
| `AST-03`  | `AST-04`  | Asset Inventories                                     | concept                |
| `AST-09`  | `AST-22`  | Secure Disposal, Destruction or Re-Use of Equipment   | legacy                 |
| `BCD-02`  | `BCD-04`  | Business Continuity & Disaster Recovery (BC/DR) Plans | concept                |
| `BCD-09`  | `BCD-24`  | Data Backups                                          | concept                |
| `BCD-11`  | `BCD-32`  | Testing for Reliability & Integrity                   | concept                |

All five come straight from D5 in `1717-scf-pin-ratchet-decisions.md`; none is a `low` pick, so none needed Matt's confirmation. `AST-01` already matched. No target is a source of another row, so the permutation has no intermediate collisions. The five targets were not in the sample before this change.

## D2 — Substitution scope

Same as #1972 D3: one bounded Perl lookup-table pass, so `AST-03` never matches `AST-03.1` or `AST-030`. Left as written: existing CHANGELOG entries, `docs/audit-log/`, `docs/issues/`, `scf-pin.json`, and the places that use these codes as synthetic data for the SCF-release diff fixtures (`internal/scfrelease/testdata`, the `scf_release_integration_test.go` files, `cmd_controls_validate_test.go`, and `mappingoverlays/integration_test.go`). There, `AST-04` and `AST-09` are deliberately a reused code and a renumbered code between two fake releases, and have nothing to do with the bundled sample.

Hand edits on top:

- **History kept.** The HIPAA 164.308(a)(7)(ii)(E) rationale says the edge was re-pointed "was BCD-02 @ 0.60". It now reads "was BCD-02 @ 0.60; BCD-04 since the SCF 2026.3 remap".
- **Prose that quoted old titles** now quotes the 2026.3 titles in crosswalk rationales and in the Jamf and Intune READMEs ("Business Continuity Plan", "Backups", "Backup Testing", "Asset Disposal", "Asset Inventory").
- **`BCD-11` meant "Data Backups" in three places, not "Backup Testing".** The demo seed's `demoSCFAnchors` row, the demo questionnaire item that mirrors it, and the `rerank_test.go` candidate all pair `BCD-11` with the title "Data Backups", which is the legacy SCF meaning of that code. These follow the title, so they move to `BCD-24` Data Backups instead of `BCD-32`. That keeps their ID and title a real 2026.3 pair.

## D3 — Not changed: the ISO A.5.9 edge

ISO A.5.9 Inventory of information and other associated assets was anchored on `AST-09` Asset Disposal because the sample had nothing closer. It now sits on `AST-22`. The sample now carries `AST-04` Asset Inventories, which is the better anchor, but moving the edge would change the mapping and not just the code. That is a crosswalk change, outside a permutation ticket. Left for #2301; the rationale now says the edge covers the asset lifecycle and doesn't claim `AST-22` is the nearest anchor.

## D4 — Upgrade effect

Five stock SOC 2 controls change their anchor code, so on the next boot they are superseded, not re-bound: `soc2_a1_2_backup_processes` (`BCD-09`→`BCD-24`), `soc2_a1_3_backup_testing` (`BCD-11`→`BCD-32`), `soc2_c1_2_confidential_disposal` and `soc2_cc6_5_asset_disposal` (`AST-09`→`AST-22`), and `soc2_cc9_1_business_continuity` (`BCD-02`→`BCD-04`). A tenant control on any of the five old anchors in `test-2026.1` is flagged and not re-bound, because `RebindControls` moves only same code and same title, and none of the five old codes is in the `test-2026.3` sample. The control's note names the new code from `remaps.json`. The `endpoint.software_inventory` evidence kind's default anchors change from `VPM-04`, `AST-03` to `VPM-04`, `AST-04`.
