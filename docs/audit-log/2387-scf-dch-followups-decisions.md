# 2387 — SCF DCH remap follow-ups: decisions log

Follow-ups from the review of #2382 (#1977, the SCF DCH-family remap), part of #1717.

detection_tier_actual: manual_review
detection_tier_target: manual_review

No bug surfaced in code. The retention default and the stale titles are judgement calls about meaning. The #2382 reviewer caught them, and no test can tell "Data Retention" from "Media & Data Retention" without a title-quoting guard. That guard would also flag rationales that paraphrase an anchor on purpose.

## D1 — Slack retention default goes to `DCH-27` alone

#1977 applied the remap as a substitution, so the Slack retention record's default `control_id` became `scf:DCH-04` Data & Asset Classification. That was its D4, which left the re-choice out of scope. A workspace's message- and file-retention durations are retention evidence, so the default is now `scf:DCH-27` Media & Data Retention.

`x-default-scf-anchors` is a flat list. The registry has no notion of a primary or secondary anchor, and `DCH-04` in that list only claimed a classification relevance the record does not carry. The list is now `["DCH-27"]`, as the dispatcher's brief allowed.

The schema stays at 1.0.0. `ImportPlatformSchemas` skips a kind and version already in the database, so an existing install keeps its stored anchors. #1977 changed the same file in place on the same terms. Changing the version for a default-anchor edit would leave old 1.0.0 records on a version with no producer. The upgrade note says so.

## D2 — Stale `DCH-27` titles

The 2026.3 title of `DCH-27` is "Media & Data Retention". Four rationales were updated:

| File                                 | Row            | Was                               |
| ------------------------------------ | -------------- | --------------------------------- |
| `data/crosswalks/iso27001-2022.yaml` | A.5.33 records | "Data Retention (DCH-27)"         |
| `data/crosswalks/iso27001-2022.yaml` | A.8.10 delete  | "Data Retention (DCH-27)"         |
| `data/crosswalks/soc2-tsc-2017.yaml` | C1.2           | "Data Retention defines..."       |
| `data/crosswalks/pci-dss-4.0.yaml`   | 9.4.1          | "SCF Media & Physical Storage..." |

The SOC 2 PI1.5 rationale ("overlaps data retention") and the HIPAA §164.316(b)(2)(i) rationale ("data retention") use the concept in lower case, not as a title, so they were left alone. The other anchors in the family were also checked against their 2026.3 titles. Every rationale citing `DCH-04` says "Data & Asset Classification" or paraphrases it, and the `DCH-16` and `MON-27` rows already carry their 2026.3 titles. `internal/demoseed/fixtures.go` keeps "Data Classification & Handling", because there it is the SCF family name, not a control title.

## D3 — `soc2_pi1_5_storage_integrity` anchor

Not changed in this PR. Whether to keep `DCH-27` or move to `MON-27` is an SCF anchor pick, so it went to Matt through the dispatcher. The recommendation is to keep `DCH-27`: the control attests retention per data class and protective integrity mechanisms, while FIM only detects changes.
