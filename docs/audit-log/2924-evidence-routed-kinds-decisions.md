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
- **Consequence:** live results that are recomputed, replayed or read as-of
  over those rows' dates can change from inconclusive to pass or fail (cc8_1,
  cc6_1, cc6_6, cc4_1 on default connector flags). So can draws from
  populations created after the routing cutover, and reads of periods frozen
  after it. Persisted results don't change: stored `control_evaluations` rows
  and drawn samples (`sample_evidence`) are snapshots. Periods frozen and
  populations created before the cutover are pinned (D6).
- **Rationale:** the rows are genuine tenant connector evidence. They passed
  the scope and credential checks at push and are read under RLS. Only the
  routing was wrong. (B) adds a predicate to about 12 reader queries to hide
  real evidence.

## D6 — Periods frozen and populations created before the cutover are pinned (Matt, 2026-11-21, review of PR #2927)

- **Requirement:** an audit period frozen before this change keeps its state,
  and a population created before it re-draws the same `row_count` and the
  same same-seed sample (invariant 10).
- **Cutover:** migration `20261290000000` records its apply time in a
  one-row global table, `evidence_routing_cutover.cut_at` (`now()` of the
  migration transaction). Reads compare against that recorded instant, never
  against the wall clock at read time. A missing row is an error, so a pinned
  read never silently widens.
- **Anchors:**
  - A period anchors on its `frozen_at`. The only freeze path
    (`internal/api/auditperiods`) stamps `frozen_at = now()`, so this is the
    freeze instant. An open period is unanchored and reads live.
  - A population anchors on the earlier of its `created_at` and its
    `frozen_at`. The second case is a population created after the cutover
    and attached to a period frozen before it: it stays consistent with that
    period.
- **Mechanism:** a pinned read binds only the owned kinds
  (`unbound.Kinds()`) as `unbound_kinds` instead of owned plus routed
  (`routed.KindsAsOf`). That is exactly the parameter every reader had before
  this change, so a pinned read is the pre-change read. Records bound to the
  control by UUID or bundle id are unaffected, because they match on
  `control_id`.
- **Readers pinned:**
  - the period's `ControlState`;
  - the period evidence set: its count and list, and the frozen-horizon
    citation resolver;
  - population re-draws (`DrawSample`);
  - the auditor attribute lookup (`EvidencePeriods`), which no longer
    places a control-free routed record in a pinned population's period.
  - A population created now (`CreatePopulation`) is after the cutover by
    definition, so it reads routed records.
- **Evidence:** `TestRouted2924_PeriodsAndPopulationsPinnedBeforeCutover`.
  With the pin disabled (`KindsAsOf` always returning owned plus routed) it
  fails on every pinned read:

  ```
  old period state = [a8de4e1a-… d02894d7-…], want unchanged [a8de4e1a-…]
  old period evidence-set total = 2, want 1
  old period resolves routed row as citable: ok=true err=<nil>, want false
  old population re-draw = [a8de4e1a-… d02894d7-…], want the original [a8de4e1a-…]
  old population re-draw size = 2, want row_count 1
  routed row's periods = [2c51dee7-… b8bd7710-…], want only [2c51dee7-…] (not the pinned b8bd7710-…)
  ```

  With the pin it passes, and the period frozen and population created after
  the cutover see the routed row (row_count 2, state of both records).

- **Constraint lock:** the relaxed CHECK is added `NOT VALID` and then
  validated, so the scan runs under SHARE UPDATE EXCLUSIVE, not ACCESS
  EXCLUSIVE.

## Not changed

- Connector flags for bound kinds (azure, gcp, jamf, pagerduty, grafana, hris,
  the k8s kinds other than NetworkPolicy coverage, and so on) keep their
  defaults. No stock bundle reads those kinds today. When one starts to, D2's
  test fails until the kind is categorized.
- `atlas-cli evidence push` is unchanged. It sends whatever `control_id` it is
  given.
