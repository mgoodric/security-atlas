# 2817 — Policy-acknowledgement evidence reaches the controls that query it: decisions log

Issue: https://github.com/mgoodric/security-atlas/issues/2817 (parent #1860). PR #2869.

- detection_tier_actual: integration
- detection_tier_target: integration

The ticket's reading of the code held. `TestAcknowledge2817_ReachesStockAckControls`
(`internal/api/policyacks/stock_controls_2817_integration_test.go`) loads the
three stock bundles from `controls/soc2/`, acknowledges a policy through
`POST /v1/policies/{id}/acknowledge`, and evaluates each control. Before the fix
all three came out `inconclusive` with 0 records in window:

```
soc2_cc1_5_accountability_acknowledgment after ack: got (inconclusive, 0 in window), want (pass, 1)
soc2_cc5_3_policies_procedures after ack: got (inconclusive, 0 in window), want (pass, 1)
soc2_cc7_4_incident_response after ack: got (inconclusive, 0 in window), want (pass, 1)
```

The cause: the ack handler pushes `control_id = "policy:<id>:v<version_id>"`.
Ingest resolves a control id only when it is a UUID or a bundle id (#2100), so
the row keeps `control_id NULL` and only `control_ref`. Every ledger reader
matched `control_id = <uuid> OR control_ref = <uuid string>`, which never
matches.

## D1 — Link by evidence kind at read time, not through the policy's linked controls

- **Options considered:**
  (a) through the policy's `linked_control_ids`: a control sees the acks of the
  policies that link it;
  (b) through the control's bundle id: push the ack once per querying control,
  with that control's bundle id;
  (c) by kind at read time: a control whose evidence queries declare
  `evidence_kind: policy.acknowledgment.v1` also reads the tenant's unbound
  records of that kind.
- **Chosen:** (c).
- **Why:** (a) does not reach cc1_5 at all. cc1_5 is anchored on HRS-02, and
  none of the five stock policies lists HRS-02 in `linked_control_ids`
  (they list IAC/CHG/CFG/GOV/RSK/IRO/TPM codes). cc1_5 also asks a
  tenant-wide question ("every active user has acknowledged all five stock
  policies"), not one about the policies that happen to link it. (b) writes
  N ledger rows per acknowledgement, and a control uploaded after the ack would
  never see it, since evidence is append-only and not re-linked. (c) uses the
  declaration the bundle already makes, needs no ledger change, and keeps
  point-in-time replay intact because every reader is still a pure read under
  its existing `observed_at` bounds.

## D2 — One rule, owned by `internal/evidence/unbound`, applied by every reader

- `unbound.Kinds()` is the closed list: `policy.acknowledgment.v1` only. Every
  other kind keeps today's behaviour: an unbound record is a free-form
  `control_ref` and reaches only the control that names it. Widening every
  declared kind would let any connector's record reach every control that
  queries its kind, a much larger change than this ticket.
- The rule is the same SQL leg in each reader, with `Kinds()` bound as
  `unbound_kinds`:
  `control_id IS NULL AND evidence_kind = ANY(unbound_kinds) AND <control>.evidence_queries @> [{"evidence_kind": evidence_kind}]`.
  The declaration check is in SQL rather than Go, so the joined readers
  (freshness, portfolio) and the single-control readers apply it identically,
  and no caller has to load a control's queries first. A record of a listed
  kind that IS bound to a control reaches only that control.
- First review (PR #2869): applying the rule only in the engine left these three
  controls PASSING while their evidence list and period sample showed nothing.
  An auditor couldn't trace the verdict to evidence. Every reader that resolves
  a control's evidence now applies it:

  | Reader                                                                                   | Surface                                                   |
  | ---------------------------------------------------------------------------------------- | --------------------------------------------------------- |
  | `ListEvidenceForControlAsOf`                                                             | evaluation engine                                         |
  | `ListEvidenceForControlPaged`, `CountEvidenceForControlWindow`                           | `GET /v1/evidence?control_id=` list and total             |
  | `ListEvidenceRecordsByControl`, `CountEvidenceRecordsByControl` (+ `BeforeHorizon` pair) | evidence summaries (live, period, portfolio), gap-explain |
  | `ListActiveControlsForPortfolio`                                                         | portfolio ranking counts                                  |
  | `ListControlsWithLatestEvidence`                                                         | freshness read model                                      |
  | `ListEvidenceForPeriodControl`                                                           | period control state                                      |
  | `CountPopulationEvidence`, `ListPopulationEvidenceIDs`                                   | population row count and sample draw                      |
  | `EvidencePeriods` via new `ListControlIDsDeclaringEvidenceKind`                          | authz hydrator: periods a record belongs to               |

  The hydrator change keeps auditor access in step with the population. Before
  it, an unbound ack in a period's population mapped to no control, and so to
  no period, and a period-scoped auditor was denied a record in their own
  sample. It now maps to every control declaring its kind. Any other unbound
  record still maps to none and is still denied.

- Deliberately not widened: `ExistsAttestationAfter` (attestation kind only),
  `checklist.sql` (`control_id` only, and not a control-evidence reader the
  review named), and the freeze-hash ingredient query (see D3).
- sqlc resolves a placeholder's type by column name across the subquery's
  tables too, so `tenant_id`/`id` compared to a parameter in the outer query
  became "ambiguous". The outer references in those queries are qualified
  `evidence_records.` and the subquery correlates on
  `evidence_records.tenant_id`. Generated field names are unchanged.

## D3 — Frozen audit periods stay stable (ADR 0003, invariant #10)

- **The freeze hash does not move.** ADR 0003's evidence ingredient is
  `ListEvidenceIDsForPeriodHash`: every tenant record with
  `observed_at <= frozen_at`, not a per-control set. An unbound ack was in
  that set before this change and still is. Its hash, the query and
  `computeFreezeHash` are untouched by this PR. This change only alters which
  control a record is attributed to, and the hash commits to the set of
  records, not to attribution.
- **Drawn samples do not move.** A sample's members are stored in
  `sample_evidence` at draw time and read back from there, not re-drawn, so a
  sample drawn before this change keeps exactly its members.
- **What does change for an already-frozen period:** views recomputed from the
  ledger under the frozen horizon. These are `ControlState`, a new draw from an
  existing population, and the population's live re-resolution. For cc1_5,
  cc5_3 and cc7_4 they now include unbound acks with `observed_at <= frozen_at`.
  No post-freeze record enters, because every widened reader keeps its
  `observed_at <= frozen_at` bound. So invariant #10 holds: the frozen universe
  is the same set of records, now correctly attributed. A population created
  before this change keeps its stored `row_count`, which can understate what a
  new draw resolves. That stored count is the pre-fix (wrong) attribution, not
  tampering.

## Verification

- `TestAcknowledge2817_ReachesStockAckControls` asserts, for each of the
  three controls and against an unrelated control:
  - evaluation is pass with 1 record;
  - the ack is on `GET /v1/evidence?control_id=` with total 1;
  - the freshness `evidence_count` is 1;
  - the population `row_count` is 1;
  - the frozen period's control state lists the ack;
  - the drawn sample is exactly the ack;
  - `EvidencePeriods(ack)` is the period.
- Each assertion was observed failing first. With `nil` kinds passed by the
  control-detail, period and population readers, the list, row-count,
  control-state and draw assertions failed. With only the hydrator reverted,
  the `EvidencePeriods` assertion failed.
- The evidence-summary, portfolio and gap-explain readers carry the same leg
  but are not asserted by this test.

## Not changed (filed or noted elsewhere)

- Push trust: any push credential can send an unbound `policy.acknowledgment.v1`
  record that counts for all three controls. Filed separately by the dispatcher.
- Overclaim: one acknowledgement now passes cc1_5, whose description says
  "every active user, all stock policies". Noted on #2820, with completeness
  pending #2834.
