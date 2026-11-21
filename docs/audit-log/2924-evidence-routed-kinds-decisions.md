# 2924 — Connector evidence routed to controls by evidence_kind: decisions log

Issue: https://github.com/mgoodric/security-atlas/issues/2924 (found in the
#2823 review, PR #2919). Decision by Matt, 2026-11-21: connector evidence is
routed by evidence type. ADR 0028 decision 9 records the three categories.

- detection_tier_actual: manual_review
- detection_tier_target: integration

With routing disabled (the routed list emptied), the new integration tests show
the defect:

```
TestRouted2924_OneGitHubRecordFeedsBothCC81Bundles: Process: ingest: required field missing: control_id is required
TestRouted2924_ExplicitControlIDIsProvenanceOnly: bundle id: stored (control_id=93a1d500-…, control_ref="soc2_cc8_1_change_management"), want (NULL, "soc2_cc8_1_change_management")
TestRouted2924_OneOsqueryRecordFeedsCC66AndCC41: Process: ingest: required field missing: control_id is required
```

## D1 — Reuse the #2817 control-free read rule rather than add a second one

- **Options considered:** (a) bind the owned and routed kinds together as the
  `unbound_kinds` parameter every reader already takes; (b) add a separate
  routed leg to each reader query.
- **Chosen:** (a). `routed.ControlFreeKinds()` returns owned ∪ routed.
- **Rationale:** the rule the ticket asks for (`control_id IS NULL AND kind is
routed AND kind = the query's kind`) is the #2817 rule exactly. Reusing it
  means no SQL changes and no sqlc regeneration, and every reader that #2817
  enrolled (evaluation, evidence list, summaries, freshness, period state,
  population, sample, auditor attribute lookup) picks up routed kinds at once.
  The categories stay distinct in Go: `ingest.OwnedKind` still reads
  `unbound.Kinds()` only, so routed kinds remain pushable.

## D2 — The routed list is closed and checked against the stock bundles

- **Chosen:** a hand-kept list in `internal/evidence/routed`: every connector
  kind a stock bundle reads, plus `github.repo_protection.v2` (#2925).
  `TestStockBundleKindsAreCategorized` parses every `controls/*/*/control.yaml`
  and fails on a kind that is not routed, owned or explicitly bound.
- **Rationale:** it can't be derived at run time from "kinds stock bundles
  read". A tenant bundle could then make any kind control-free by declaring
  it. Bound kinds (`manual.upload.v1`, `sast.scan_result.v1`,
  `access_review.completion.v1`, `security_awareness.training_completion.v1`)
  name the control they evidence.

## D3 — A routed record is never bound, even when the wire names a control

- **Chosen:** ingest stores a routed kind with `control_id` NULL even when the
  wire `control_id` is a UUID or a bundle id. The wire value is kept in
  `control_ref`.
- **Rationale:** the ticket says so ("any wire `control_id` is kept only as
  provenance"). A bound copy would reach only one control, the defect being
  fixed. A tenant that passes `--repo-protection-control` still pushes
  successfully, and the record reaches the named control as long as that
  control declares the kind, which it must do to evaluate the record at all
  (ADR 0028 decision 5).

## D4 — Empty `control_id` is allowed for routed kinds; the CHECK admits empty `control_ref` on control-free rows

- **Options considered:** (a) let a routed kind omit `control_id` and store
  `control_ref = ''`; (b) store a placeholder such as `kind:<evidence_kind>`;
  (c) keep `control_id` required and default connector flags to a sentinel.
- **Chosen:** (a). Migration `20261290000000_evidence_routed_kinds` relaxes
  `evidence_records_control_ref_nonempty` to
  `length(control_ref) > 0 OR control_id IS NULL`.
- **Rationale:** verify rebuilds the hashed wire record from `control_ref`
  (`RecordFromLedgerRow`), so (b) breaks hash verification.
  `TestRouted2924_OneGitHubRecordFeedsBothCC81Bundles` asserts the record
  verifies. (c) leaves a meaningless value on the wire. The CHECK doesn't
  repeat the routed list, so SQL and Go can't drift. Ingest enforces which
  kinds may omit a control.

## D5 — Existing control-free rows of a routed kind become readable (Matt, 2026-11-21)

- **Options considered:** (A) rows already stored with `control_id` NULL and a
  `scf:` `control_ref` become readable under the new rule; (B) they stay
  unreadable, which needs a marker column set at ingest and checked by every
  reader.
- **Chosen:** (A), by Matt through the dispatcher on 2026-11-21. It needs no
  code: those rows match the rule.
- **Consequence:** results that are recomputed, replayed or read as-of over
  those rows' dates, and populations drawn from now on over those dates, can
  change from inconclusive to pass or fail (cc8_1, cc6_1, cc6_6, cc4_1 on
  default connector flags). A population still draws only `observed_at <=
frozen_at`. Persisted results don't change: stored `control_evaluations`
  rows and drawn samples (`sample_evidence`) are snapshots.
- **Rationale:** the rows are genuine tenant connector evidence. They passed
  the scope and credential checks at push and are read under RLS. Only the
  routing was wrong. (B) adds a predicate to about 12 reader queries to hide
  real evidence.

## Not changed

- Connector flags for bound kinds (azure, gcp, jamf, pagerduty, grafana, hris,
  the k8s kinds other than NetworkPolicy coverage, and so on) keep their
  defaults. No stock bundle reads those kinds today. When one starts to, D2's
  test fails until the kind is categorized.
- `atlas-cli evidence push` is unchanged. It sends whatever `control_id` it is
  given.
