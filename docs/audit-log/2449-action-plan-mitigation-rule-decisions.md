# 2449 — Mitigate rule on action-plan tombstone and risk unlink: decisions log

Issue: mgoodric/security-atlas#2449, from the review of #2444 (#2106).
A mitigate risk keeps at least one linked control or live action plan
(`risk.ValidateTreatment`, #1746). #2444 enforced it when a control is
unlinked; `actionplan.Store.Tombstone` and `UnlinkRisk` did not, so a
mitigate risk could lose its only plan.

## Detection-tier classification

- **detection_tier_actual:** manual_review (the #2444 review).
- **detection_tier_target:** integration.

## Decisions

### D1 — the rule is mirrored, not imported

`internal/risk` imports `internal/eval`, which imports `internal/actionplan`
(`ReconcileVerification`), so `actionplan` cannot call
`risk.ValidateTreatment`. `actionplan.MitigationHeld` restates the mitigate
case in one line, and `internal/risk/treatment_mitigation_test.go` (an
external test package that imports both) checks the two agree over every
small count and every treatment. A change to the rule in one place fails
that test until the other follows.

### D2 — lock order

- **Tombstone:** the plan row `FOR UPDATE` (`GetActionPlanByIDForUpdate`,
  #2637), then each linked risk row (`LockRiskByID`) in ascending id order.
  The plan lock conflicts with the `KEY SHARE` a link insert's FK takes, so
  no risk can be linked to the plan between reading its risk set and the
  tombstone. Sorting by id gives two tombstones over overlapping risk sets
  the same order.
- **UnlinkRisk:** the risk row only, as the risk-side control unlink takes
  it. The plan is read without a lock, as before.

No path on the risk side locks an action plan row, and `LockRiskByID` is
`FOR NO KEY UPDATE`, which does not conflict with the `KEY SHARE` a link
insert takes on the risk, so neither order can cycle with an existing one.
The risk row is the last step of the #2335 order (rule advisory locks,
category share lock, risk row); neither path takes the earlier steps,
because plans and their links are not aggregation inputs.

### D3 — change first, then re-count

Each path locks its risks, makes its write, then counts the risk's control
links and live plans in the same transaction. Under READ COMMITTED each
count is a fresh snapshot that includes the transaction's own write and any
write committed by a transaction that held the risk lock before it. So of
two writes that each remove one of a risk's two plans, the second sees the
first's commit and refuses.

### D4 — 409, naming the risk; the whole tombstone or nothing

`ErrMitigationRequired` is a state conflict, so 409, as the dispatcher
asked (the ticket allowed 400 or 409). `MitigationRequiredError` names the
first risk, in id order, that would be left with neither. A refused
tombstone rolls back entirely: the plan stays live and every link stays.

### D5 — closed risks are checked too

The rule is checked whatever the risk's status. The risk side refuses any
link change on a closed risk, because its links stand as they were until it
is reopened (#2106); refusing to strip a closed mitigate risk of its last
plan is consistent with that, and less strict than refusing every change.

### D6 — existing data is not repaired

A read-only, counts-only query for mitigate risks that already have
neither is in the PR body.
