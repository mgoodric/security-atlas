# 2817 — Policy-acknowledgement evidence reaches the controls that query it: decisions log

Issue: https://github.com/mgoodric/security-atlas/issues/2817 (parent #1860).

- detection_tier_actual: integration
- detection_tier_target: integration

The ticket's reading of the code held. `TestAcknowledge2817_ReachesStockAckControls`
(`internal/api/policyacks/stock_controls_2817_integration_test.go`) loads the
three stock bundles from `controls/soc2/`, acknowledges a policy through
`POST /v1/policies/{id}/acknowledge`, and evaluates each control. Before the fix
all three came out `inconclusive` with 0 records in window:

```
stock_controls_2817_integration_test.go:158: soc2_cc1_5_accountability_acknowledgment after ack: got (inconclusive, 0 in window), want (pass, 1)
stock_controls_2817_integration_test.go:158: soc2_cc5_3_policies_procedures after ack: got (inconclusive, 0 in window), want (pass, 1)
stock_controls_2817_integration_test.go:158: soc2_cc7_4_incident_response after ack: got (inconclusive, 0 in window), want (pass, 1)
```

The cause: the ack handler pushes `control_id = "policy:<id>:v<version_id>"`.
Ingest resolves a control id only when it is a UUID or a bundle id (#2100), so
the row keeps `control_id NULL` and only `control_ref`. The engine reads
`control_id = <uuid> OR control_ref = <uuid string>`, which never matches.

## D1 — Link by evidence kind at evaluation time, not through the policy's linked controls

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
  point-in-time replay intact because it is still a pure read bounded by
  `observed_at <= as_of`.

## D2 — The widening is limited to a closed list of kinds, and to unbound records

- `eval.UnboundEvidenceKinds` holds only `policy.acknowledgment.v1`. Every other
  kind keeps today's behaviour: an unbound record is a free-form `control_ref`
  and reaches only the control that names it. Widening every declared kind
  would let any connector's record reach every control that queries its kind,
  which is a much larger change than this ticket and was not asked for.
- The extra branch requires `control_id IS NULL`. An ack-kind record that is
  bound to a control (a UUID or bundle id was pushed) reaches only that control.
- The query (`ListEvidenceForControlAsOf`) moved to named `sqlc.arg` parameters
  because sqlc rejects a mix of positional and named ones; the generated
  struct keeps its existing field names and gains `UnboundKinds`.
- `idx_evidence_records_tenant_kind_observed` covers the new branch.

## Not changed (follow-ups worth filing if wanted)

- Other ledger readers still match by `control_id` / `control_ref` only: the
  control-detail evidence list, audit-period sample populations
  (`ListEvidenceForPeriodControl`), and the evidence counts on control pages.
  For cc1_5/cc5_3/cc7_4 those surfaces will show a passing evaluation with no
  listed evidence. That is a display/sampling gap, outside this ticket's
  evaluation criteria.
- Anyone holding a push credential can push a `policy.acknowledgment.v1` record
  with an unresolvable control id, and it will count for these three controls.
  That is the same trust a push credential already has for any control's
  evidence; restricting the kind to the ack handler's ingestion path would be a
  separate change.
