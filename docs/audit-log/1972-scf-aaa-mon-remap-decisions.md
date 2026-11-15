# #1972 — SCF 2026.3 remap, AAA + MON family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The second #1717 family group. It follows the #1971 pattern (`1971-scf-iac-remap-decisions.md`): the sample stays on the open working label `test-2026.3` (D2 there), the remap rows go into `data/scfremap/remaps.json`, and the boot-time re-bind and crosswalk edge retirement from #1999 do the upgrade work. No migration. The bugs found were caught by unit tests: a lowercase `mon-01` in `rerank_test.go` that the case-sensitive substitution missed, and `TestAnchorExistsInCatalog_RejectsDanglingAnchor`, which used `MON-02` as its example of a real-but-absent anchor.

## D1 — The remap, and Matt's confirmation of AAA-01

| Sample ID | 2026.3 ID  | 2026.3 title (verbatim)                                               | Basis in `remaps.json` |
| --------- | ---------- | --------------------------------------------------------------------- | ---------------------- |
| `AAA-01`  | `MON-04`   | System Generated Event Logs & Security-Relevant Telemetry             | concept (D5: low)      |
| `AAA-10`  | `MON-13`   | Event Log & Security-Relevant Telemetry Retention                     | concept                |
| `AAA-12`  | `MON-05.1` | Centralized Event Log & Security-Relevant Telemetry Review & Analysis | concept                |
| `MON-01`  | `MON-02`   | Continuous Monitoring                                                 | legacy                 |
| `MON-08`  | `MON-16`   | Anomalous Behavior                                                    | concept                |

**AAA-01 → MON-04, confirmed by Matt (2026-11-14, relayed by the dispatcher).** The candidates were `MON-04` System Generated Event Logs & Security-Relevant Telemetry, `MON-03` Event Log & Security-Relevant Telemetry Monitoring, `MON-01` Continuous Monitoring Policy and `MON-09` Content of Event Logs. Most edges on AAA-01 are about producing audit records: ISO A.8.15 Logging is its `equal` edge, and it also carries clock sync, HIPAA Audit Controls and PCI audit logs. The review and analysis side is already `AAA-12` → `MON-05.1`. The cost: stock control `soc2_cc7_3_security_event_evaluation` now anchors on `MON-04`, which fits "evaluate security events" less exactly than `MON-03` would.

`remaps.json` accepts only `legacy` or `concept`. A D5 `low` pick was also chosen by meaning, so it is recorded as `concept`.

## D2 — The AAA rows join the MON family

SCF has no AAA family. The three rows take `family: MON` and move into the sample's MON block, and the two stock controls with `control_family: AAA` (`soc2_cc4_2_deficiency_communication`, `soc2_cc7_3_security_event_evaluation`) take `MON`. The docs that listed AAA as an example SCF family (canvas §2, the docs-site controls page, the control-bundle spec) drop it.

## D3 — Substitution scope

Same as #1971 D1: one Perl lookup-table pass per file, bounded so that `MON-01` does not match `MON-01.1` or `MON-010`. Left as written: existing CHANGELOG entries, `docs/audit-log/`, `docs/issues/`, `migrations/sql/`, `Plans/_archive/`, `Plans/prompts/`, `scf-pin.json`, and the `AAA-01` input in `scfpin_test.go`'s helper test, which is arbitrary data and not a sample reference.

Two hand edits on top:

- **Historical mention kept.** The ISO A.5.7 rationale says the edge was "upgraded from the slice-438 placeholder MON-08". That names the old ID as a matter of history, so it now reads "MON-08 (MON-16 since the SCF 2026.3 remap)".
- **Prose that quoted old titles** now quotes the 2026.3 titles. This covers crosswalk rationales ("SCF Auditing & Accountability", "Audit Log Review", "Anomalous Behavior Detection", "Event Log Retention") and the `pagerduty.response_metrics` description. That description said `MON-02` was absent from the sample, and now it is present.

The `dangling_MON-02` case in `default_anchor_catalog_test.go` becomes `dangling_MON-01`: that code is real in SCF 2026.3 (Continuous Monitoring Policy) but is no longer in the sample.

## D4 — Upgrade effect

Five stock SOC 2 controls change their anchor code. On the next boot they are superseded rather than re-bound, as with #1971. The CHANGELOG lists them. A tenant control on any of the five old anchors in `test-2026.1` is flagged and not re-bound, because none of the five codes is in the `test-2026.3` sample. Even if a later group added `MON-01` or `MON-08`, the title rule in #1971 D3 would still hold such a control back: in 2026.3 those codes are Continuous Monitoring Policy and Correlate Monitoring Information. The control's note names the new code from `remaps.json`.
