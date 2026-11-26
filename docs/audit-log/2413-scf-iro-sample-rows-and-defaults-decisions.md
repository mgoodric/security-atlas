# #2413 — SCF IRO sample rows and incident defaults moved by meaning: decisions log

detection_tier_actual: unit
detection_tier_target: unit

Follow-up to #1980 (PR #2408) D3, which kept the weak `IRO-16` fits because the better anchors were not in the sample. The expected failures were:

- `TestSampleFixtureContentIsPinnedToItsRelease` flagged the new rows, so the open `test-2026.3` label's hash was re-pinned.
- `TestAnchorExistsInCatalog_RejectsDanglingAnchor` used `IRO-02` as its real-but-absent example. `IRO-02` is now present, so it moved to the present list and `IRO-04` (left the sample in #1980) took its place as the absent example.

No stock control's `scf_anchor_id` changed, so nothing is superseded on boot.

## D1 — Four sample rows

| ID       | 2026.3 title (verbatim from the pin)        |
| -------- | ------------------------------------------- |
| `IRO-02` | Incident Response Operations                |
| `IRO-06` | Incident Handling                           |
| `IRO-13` | Chain of Custody & Forensics                |
| `IRO-14` | Root Cause Analysis (RCA) & Lessons Learned |

Descriptions are our own words. `IRO-13`, optional in the ticket, was added because the ISO A.5.28 rationale (D4) needs it. Rows sit in code order inside the IRO block, so no `control_family` question arises.

## D2 — Defaults moved by meaning

| Default                                                                          | Old          | New          |
| -------------------------------------------------------------------------------- | ------------ | ------------ |
| Datadog `--siem-signal-control`, `--firing-control`; Grafana `--firing-control`  | `scf:IRO-16` | `scf:IRO-06` |
| `monitoring.alert_firing` (with `MON-02`), `datadog.siem_signal` (with `THR-02`) | `IRO-16`     | `IRO-06`     |
| PagerDuty `--postmortem-control`                                                 | `scf:IRO-13` | `scf:IRO-14` |
| `incident.postmortem`, `pagerduty.postmortem_summary`                            | `IRO-16`     | `IRO-14`     |
| `pagerduty.response_metrics` (with `MON-02`)                                     | `IRO-16`     | `IRO-02`     |
| `pagerduty.incident_summary`                                                     | `IRO-16`     | `IRO-02`     |

The PagerDuty `--postmortem-control` default `scf:IRO-13` was wrong in 2026.3 (Chain of Custody & Forensics), so it is a fix, not a re-choice.

**Low-confidence picks, decided by Matt on review:** both kinds stay on `IRO-02`, as built.

- `pagerduty.response_metrics` → `IRO-02` rather than `IRO-06`. MTTA/MTTR aggregates describe the response program, not one incident's handling, and `IRO-02` matches the `--metrics-control` default the ticket already called a fit.
- `pagerduty.incident_summary` → `IRO-02`. The ticket did not name this kind, but its description said `IRO-16` was used only because `IRO-02` was absent, which became false once `IRO-02` was added. It follows the `--incident-control` default `scf:IRO-02`. `IRO-06` Incident Handling is the alternative.

The `--incident-control` and `--metrics-control` defaults (`scf:IRO-02`) were already right and are unchanged.

## D3 — Comments and READMEs

`registry.go`, the kind descriptions, `metrics.go` and the three connector READMEs no longer say "IRO-02 is absent from the bundled SCF fixture" and use the 2026.3 titles (`IRO-02` is Incident Response Operations, not Incident Handling).

## D4 — ISO rationale nits re-anchor two rows

| Requirement                    | Old anchor             | New anchor             | Relationship                                             |
| ------------------------------ | ---------------------- | ---------------------- | -------------------------------------------------------- |
| A.5.27 Learning from incidents | `IRO-08` subset_of 0.7 | `IRO-14` equal 0.8     | the 2026.3 control is the lessons-learned control        |
| A.5.28 Collection of evidence  | `IRO-08` subset_of 0.6 | `IRO-13` subset_of 0.8 | evidence collection is one part of custody and forensics |

A.5.27 became `equal` at 0.8, one notch under the A.5.24 ↔ `IRO-08` equal at 0.9. A.5.28 was first built as `equal` 0.8; on review Matt set it to `subset_of` 0.8, because `IRO-13` also covers forensic analysis, which A.5.28 does not ask for. A.5.27 → `IRO-14` `equal` 0.8 was kept as built. A.5.28's second row (`MON-04`) is unchanged. On upgrade, the crosswalk edge retirement from #1999 retires the two `IRO-08` edges.
