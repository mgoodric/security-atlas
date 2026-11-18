# #2426 — Stock SOC 2 Privacy control kit (P1–P8): decisions log

detection_tier_actual: manual_review
detection_tier_target: unit

#2422 (#1753) added the 18 Privacy criteria to `data/crosswalks/soc2-tsc-2017.yaml` with no stock control anchored to their PRI/IRO rows, so `go run ./cmd/scripts/coverage-check` fell to 44 / 61 (72%), below its own 80% gate. Nothing ran that script in CI, so the drop was caught by review of the merged PR rather than by a check. That gap is why the target tier is `unit` (a CI gate) and the actual is `manual_review`. This change adds 13 bundles under `controls/soc2/` and wires the script into CI. Coverage is now 61 / 61.

## D1 — One bundle per SCF anchor, not one per criterion

There are 17 uncovered criteria (P4.3 was already reached through `soc2_cc6_5_asset_disposal` on AST-22). Where two criteria share their primary anchor in the crosswalk, one bundle on that anchor covers both, as an auditor would test one control for both:

| Bundle                              | Anchor   | Criteria covered |
| ----------------------------------- | -------- | ---------------- |
| `soc2_p1_1_privacy_notice`          | PRI-21   | P1.1             |
| `soc2_p2_1_consent_records`         | PRI-24   | P2.1             |
| `soc2_p3_1_collection_minimization` | PRI-31   | P3.1             |
| `soc2_p3_2_explicit_consent`        | PRI-27   | P3.2 (and P2.1)  |
| `soc2_p4_1_use_limitation`          | PRI-29   | P4.1             |
| `soc2_p4_2_retention_schedule`      | PRI-30   | P4.2, P4.3       |
| `soc2_p5_1_data_subject_requests`   | PRI-39   | P5.1, P5.2       |
| `soc2_p6_1_third_party_disclosure`  | PRI-16   | P6.1             |
| `soc2_p6_2_disclosure_log`          | PRI-17.1 | P6.2, P6.7       |
| `soc2_p6_3_privacy_breach_handling` | IRO-22   | P6.3, P6.6       |
| `soc2_p6_4_vendor_privacy_terms`    | PRI-14   | P6.4, P6.5       |
| `soc2_p7_1_data_quality`            | PRI-35   | P7.1             |
| `soc2_p8_1_privacy_monitoring`      | PRI-11   | P8.1             |

P2.1 and P3.2 get separate bundles even though PRI-27 reaches both: P2.1's direct equivalent is PRI-24 (consent records, including withdrawal), and P3.2 is the narrower opt-in-before-collection case. P8.1 anchors to PRI-11 (the compliance-monitoring half, 0.6) rather than PRI-37 (0.5). The review it asks the owner to upload also lists the inquiries and complaints, so one bundle covers both halves of the criterion.

`control_family` follows the anchor's SCF family (#2405): `PRI` everywhere except the breach bundle, which is `IRO`.

## D2 — Licensing

Each `description` cites its criterion id (`TSC P6.4`) and states the control in our own words. Nothing is quoted or closely paraphrased from the AICPA criteria or points of focus (`data/crosswalks/README.md`, `controls/soc2/README.md`).

## D3 — Which bundles carry evidence queries

The kit's AC-2 gate wants at least half the bundles to carry an `evidence_queries[]` entry. That was 26 / 50. 13 manual-only bundles would have taken it to 26 / 63 and failed. Privacy controls are mostly owner attestations, so automation was added only where an evidence kind already in the registry carries a real signal:

- `jira.ticket_evidence.v1` for data-subject requests (`privacy-dsr` tickets) and privacy breaches (`privacy-incident` tickets). These follow the same ticket-tag pattern as `soc2_cc1_4_competence_training`. Both are `automated`.
- `slack.retention_settings.v1` for retention, alongside a quarterly retention-schedule upload (`semi_automated`).
- `manual.upload.v1` freshness queries for the privacy notice review, disclosure log, DPA register and annual privacy program review (`semi_automated`, the `soc2_cc6_5_asset_disposal` pattern).

The other six are `manual_attested`. The result is 33 / 63 (52%). No new evidence kind or schema was added.

## D4 — Owner roles and freshness

The owner is `ciso` (the solo security leader also runs privacy) except for the bundles that describe product behaviour: collection, opt-in and data quality go to `product-engineering`. Attestations are annual; logs and registers that change often are quarterly; the ticket-backed queries are weekly.

## D5 — Coverage gate in CI

`coverage-check` runs as a step in the `Go · lint` job and as `just lint-soc2-coverage` (part of `just lint`). AC-1's exact bundle count moved from 50 to 63. That job runs only when the `code` path filter matches. `controls/**` and `data/crosswalks/**` are not in it, so a PR that changes only bundles or the crosswalk still skips the gate. Widening the tier filters is a separate CI change and was left out of this ticket.
