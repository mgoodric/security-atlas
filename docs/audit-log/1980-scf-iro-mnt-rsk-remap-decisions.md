# #1980 — SCF 2026.3 remap, IRO + MNT + RSK family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The tenth #1717 family group. It uses the same mechanics as #1979 (`1979-scf-gov-hrs-remap-decisions.md`). The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and the crosswalk edge retirement from #1999 do the upgrade work. No migration. The failures seen were all expected:

- `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label's hash was updated.
- `TestStockManifestHashes_CoverControlsDir` flagged the seven re-anchored stock controls, so `scripts/gen-stock-control-hashes.sh` appended their hashes (7 additions, no deletions).

## D1 — The remap

| Sample ID | 2026.3 ID | 2026.3 title (verbatim)        | Basis in `remaps.json` |
| --------- | --------- | ------------------------------ | ---------------------- |
| `IRO-04`  | `IRO-08`  | Incident Response Plan (IRP)   | legacy                 |
| `IRO-09`  | `IRO-16`  | Incident Stakeholder Reporting | concept                |
| `MNT-02`  | `MNT-03`  | Controlled Maintenance         | legacy                 |
| `RSK-01`  | `RSK-02`  | Risk Management Program        | legacy                 |
| `RSK-04`  | `RSK-13`  | Risk Assessment                | legacy                 |

All rows come from D5 in `1717-scf-pin-ratchet-decisions.md`, and none of them is a low-confidence pick there. `RSK-08` already matched and is untouched. Titles are verbatim from `internal/api/scfseed/testdata/scf-pin.json`. Descriptions stay in our own words and are unchanged, because each still describes its 2026.3 title. Every move stays within its family and every new code sorts in place, so the row order is unchanged and no `control_family` question (#2405) arises.

## D2 — One substitution, no chains

None of the five targets is itself a source, so there is no chain. The moves were still applied as one Perl `s///g` pass through a lookup table, with the bounded pattern `(?<![A-Za-z0-9.-])(IRO-0[49]|MNT-02|RSK-0[14])(?![0-9]|\.[0-9])`. The lookahead leaves any dotted child alone. The only one in the repo is `IRO-09.1` in `scf-pin.json`, which the pass never touched.

**Each reference landed exactly once.** A check script compared each of the 43 substituted files against `HEAD`. It took every SCF-shaped token in the old file, applied the table, and required the result to equal the token sequence in the new file, in order. All 43 matched. The sample was then hand-edited for titles only.

**Target codes checked before the change:** `IRO-08`, `IRO-16`, `MNT-03`, `RSK-02` and `RSK-13` appeared nowhere outside `scf-pin.json`, `docs/issues/`, `docs/audit-log/`, `CHANGELOG.md` and `Plans/_archive/`. Nothing needed moving first.

**Out of the pass, as synthetic:**

- `internal/control/external_refs_test.go`: a parse-only `control.yaml` (`scf_anchor_id: RSK-04`) that never resolves against the sample.
- `web/.claude-design/components/table/index.html`: a static design-system mock row (`SCF:RSK-04`).
- by convention, the existing CHANGELOG entries and `changelog.d/` fragments, `docs/audit-log/`, `docs/issues/`, `Plans/_archive/` and `scf-pin.json`.

**Hand edits after the pass:**

- the two IRO sample titles;
- quoted titles: crosswalk rationales, evidence-kind descriptions, `schemaregistry/registry.go` comments and the PagerDuty README now say "Incident Response Plan (IRP)" and "Incident Stakeholder Reporting". "Risk Management Program", "Risk Assessment" and "Controlled Maintenance" did not change. The stock policy named Incident Response Plan, and its `GLOSSARY.md` row, keep their own title;
- `knownMismatch`: the five entries removed, and `knownMismatchCeiling` lowered from 20 to 15;
- the demo seed (D4).

## D3 — Connector and kind defaults keep their concept

| Default                                                                                                                                                             | Old          | New          | Concept kept           |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------ | ------------ | ---------------------- |
| Datadog `--siem-signal-control`, `--firing-control`; Grafana `--firing-control`                                                                                     | `scf:IRO-09` | `scf:IRO-16` | incident reporting     |
| `datadog.siem_signal`, `monitoring.alert_firing`, `incident.postmortem`, `pagerduty.incident_summary`, `pagerduty.postmortem_summary`, `pagerduty.response_metrics` | `IRO-09`     | `IRO-16`     | incident reporting     |
| PagerDuty `--oncall-control`, `pagerduty.oncall_coverage`                                                                                                           | `scf:IRO-04` | `scf:IRO-08` | incident response plan |

The permutation and the meaning agree on every row, so nothing was re-chosen. **The `IRO-09` defaults are weak fits, and they predate this group.** Each kind's description says it was put on `IRO-09` as "the closest present incident anchor" because its real candidate (`IRO-02` incident handling, `IRO-13` root-cause analysis, `IRO-07` response team in the legacy numbering) was missing from the sample. In 2026.3 the concept lands on Incident Stakeholder Reporting, which fits incident summaries and response metrics loosely and alert firings and SIEM signals poorly. Like #1979 D4, this PR keeps the concept rather than re-choose it. A better anchor (2026.3 `IRO-06` Incident Handling, `IRO-14` Root Cause Analysis (RCA) & Lessons Learned) needs that row in the sample first, which is a separate decision.

The PagerDuty README's own defaults `--postmortem-control scf:IRO-13` and `--metrics-control scf:IRO-02` are not in this group's codes and were left alone. In 2026.3 `IRO-13` is Chain of Custody & Forensics, so that default is worth a look in its own group.

## D4 — Demo seed moves by meaning

`internal/demoseed/writers.go` synthesizes a demo spine against the real SCF 2026.3 release (`DemoSCFRelease`), not the sample. Its entry `{"IRO-04", "Incident Response", "Incident Handling"}` was labelled Incident Handling, which is `IRO-04` Incident Classification & Prioritization in 2026.3. It moves to `IRO-06`, whose 2026.3 title is exactly Incident Handling. This follows #1976, which moved the demo's `CRY-05` entry by meaning in the same way.

## D5 — Upgrade effect

Seven stock SOC 2 controls change anchor code, so on the next boot each is superseded, not re-bound:

- `soc2_cc2_3_external_communication` (`IRO-09`→`IRO-16`);
- `soc2_cc3_1_risk_objectives`, `soc2_cc5_1_control_selection` (`RSK-01`→`RSK-02`);
- `soc2_cc3_2_risk_assessment`, `soc2_cc3_3_fraud_risk` (`RSK-04`→`RSK-13`);
- `soc2_cc7_4_incident_response`, `soc2_cc7_5_incident_recovery` (`IRO-04`→`IRO-08`).

Owners, evidence, exceptions and risk links stay with the old version. No stock control cites `MNT-02`; that code moves only in the HIPAA crosswalk and its test. A tenant control on any of the five moved codes in `test-2026.1` is flagged with the note from `remaps.json`. No code in this group is re-used with another meaning in the sample, so no tenant control is re-bound onto an unrelated holder.

The stock Incident Response Plan now links `IRO-08`, `IRO-01`, `IRO-02`, and the Information Security Policy links `GOV-02`, `GOV-05`, `RSK-02`. Stock policies are seeded only by `atlas-cli policies seed`, so a tenant that already has a policy keeps the links it was seeded with.
