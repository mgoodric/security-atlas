# #1981 — SCF 2026.3 remap, PRI + SEA + TDA + VPM family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The eleventh #1717 family group. It uses the same mechanics as #1980 (`1980-scf-iro-mnt-rsk-remap-decisions.md`). The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and the crosswalk edge retirement from #1999 do the upgrade work. No migration. The failures seen were all expected:

- `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label's hash was updated.
- `TestStockManifestHashes_CoverControlsDir` flagged the five re-anchored stock controls, so `scripts/gen-stock-control-hashes.sh` appended their hashes (5 additions, no deletions).

## D1 — The remap

| Sample ID | 2026.3 ID | 2026.3 title (verbatim)                      | Basis in `remaps.json` |
| --------- | --------- | -------------------------------------------- | ---------------------- |
| `PRI-01`  | `PRI-02`  | Data Privacy Program                         | legacy                 |
| `PRI-04`  | `PRI-39`  | Data Subject Rights Management               | concept                |
| `SEA-01`  | `SEA-05`  | Secure Engineering Principles                | legacy                 |
| `SEA-05`  | `TDA-05`  | Secure Software Development Practices (SSDP) | concept                |
| `TDA-01`  | `TDA-02`  | Technology Development & Acquisition         | legacy                 |
| `VPM-04`  | `VPM-06`  | Vulnerability Remediation Process            | concept                |

All rows come from D5 in `1717-scf-pin-ratchet-decisions.md`, and none is a low-confidence pick there. `VPM-01` already matched and is untouched. Titles are verbatim from `internal/api/scfseed/testdata/scf-pin.json`. Descriptions stay in our own words and are unchanged, because each still describes its 2026.3 title.

`SEA-05`→`TDA-05` is the one cross-family move. The sample row's `family` changes from `SEA` to `TDA` and the row moves into the TDA block after `TDA-02`. The three stock controls on it (`soc2_pi1_2_input_completeness`, `soc2_pi1_3_processing_validity`, `soc2_pi1_4_output_delivery`) keep `control_family: SEA`. Whether `control_family` tracks the anchor's family is the open #2405 question, so this PR does not decide it; #2405 now has three more cases.

## D2 — One substitution, one chain

Sources and targets overlap only in `SEA-05`: `SEA-01`→`SEA-05` and `SEA-05`→`TDA-05`. All six moves were one Perl `s///g` pass that maps each match through a lookup table, never one `sed` per ID, with the bounded pattern `(?<![A-Za-z0-9.-])(PRI-0[14]|SEA-0[15]|TDA-01|VPM-04)(?![0-9]|\.[0-9])`. So an old `SEA-01` lands on `SEA-05` and stays there, and an old `SEA-05` lands on `TDA-05`; nothing moves twice. The lookahead leaves dotted children alone. None of the six codes has a dotted child cited outside `scf-pin.json`.

**Each reference landed exactly once.** A check script compared each of the 35 substituted files against `HEAD`. It took every SCF-shaped token in the old file, applied the table, and required the result to equal the token sequence in the new file, in order. All 35 matched. The sample and the pin test were then hand-edited (below).

**Target codes checked before the change:** `PRI-02`, `PRI-39`, `TDA-02`, `TDA-05` and `VPM-06` appeared nowhere outside `scf-pin.json` and the history dirs, except `fixtures/readme-demo/anchors-list.json`. That file is a synthetic README demo payload (it also carries `CM-04`, which is not an SCF family) with a mocked `VPM-06` "Vulnerability Scanning Cadence" row. It is not a sample reference and was left. `SEA-05` was a source itself, handled by the simultaneous pass.

**Out of the pass:**

- `migrations/sql/20260511000004_evidence_ledger.sql` and its `.down.sql`: applied migrations, and `scf:VPM-04` there is only a comment example. Same as #1971.
- by convention, the existing CHANGELOG entries and `changelog.d/` fragments, `docs/audit-log/`, `docs/issues/`, `Plans/_archive/` and `scf-pin.json`.
- `internal/api/scfseed/scfpin_test.go`, edited by hand: its `knownMismatch` keys are the old codes.

**In the pass, as free-form `control_ref` examples:** `scf:VPM-04` appears in evidence-ingest, eval, streambuf, canonjson, auth and API tests and comments as an example of an SDK control ref that names no bundle, plus `Plans/EVIDENCE_SDK.md`. It never resolves against the sample. It moved to `scf:VPM-06` anyway, so that the example still names the remediation concept and agrees with the Jamf and Intune defaults.

**Hand edits after the pass:**

- the sample: the `PRI-02`, `PRI-39` and `TDA-05` titles, and the `TDA-05` family and position (D1);
- quoted titles in crosswalk rationales: "Privacy Governance" → "Data Privacy Program", "Data Subject Rights" → "Data Subject Rights Management", and "Secure Software Development" → "Secure Software Development Practices (SSDP)" (ISO 27001, NIST CSF, PCI DSS, SOC 2). "Secure Engineering Principles", "Technology Development & Acquisition" and "Vulnerability Remediation Process" did not change;
- `knownMismatch`: the six entries removed, and `knownMismatchCeiling` lowered from 15 to 9.

## D3 — Connector and kind defaults keep their concept

| Default                                       | Old                | New                | Concept kept                         |
| --------------------------------------------- | ------------------ | ------------------ | ------------------------------------ |
| Jamf and Intune `--software-control`          | `scf:VPM-04`       | `scf:VPM-06`       | vulnerability remediation            |
| `endpoint.software_inventory` default anchors | `VPM-04`, `AST-04` | `VPM-06`, `AST-04` | vulnerability remediation, inventory |
| `sast.scan_result` default anchors            | `VPM-04`, `TDA-01` | `VPM-06`, `TDA-02` | remediation, technology development  |

The permutation and the meaning agree on every row, so nothing was re-chosen. No weak fit was found. An explicit old flag value `scf:VPM-04` now names Vulnerability Exploitation Analysis in 2026.3; the fragment says to change it.

## D4 — Upgrade effect

Five stock SOC 2 controls change anchor code, so on the next boot each is superseded, not re-bound:

- `soc2_cc5_2_general_tech_controls` (`SEA-01`→`SEA-05`);
- `soc2_cc7_1_vulnerability_remediation` (`VPM-04`→`VPM-06`);
- `soc2_pi1_2_input_completeness`, `soc2_pi1_3_processing_validity`, `soc2_pi1_4_output_delivery` (`SEA-05`→`TDA-05`).

Owners, evidence, exceptions and risk links stay with the old version. No stock policy cites any of the six codes.

**`SEA-05` is re-used with another meaning.** In `test-2026.1` it is Secure Software Development; in `test-2026.3` it is Secure Engineering Principles. `RebindControls` moves only same code and same title, so a tenant control on the old `SEA-05` is not re-bound onto the new holder. It is flagged, with the `remaps.json` note naming `TDA-05`. A tenant control on any of the other five old codes is flagged the same way.
