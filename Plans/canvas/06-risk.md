**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 6. Risk Register Linkage

## 6.1 Treatment statuses

| Treatment  | Meaning                           | Rules                                                                           |
| ---------- | --------------------------------- | ------------------------------------------------------------------------------- |
| `accept`   | Risk acknowledged, no action.     | Requires named accepter, accepted_until date, exec sign-off if above tolerance. |
| `mitigate` | Treated by linked controls.       | Must have ≥1 linked control.                                                    |
| `transfer` | Insurance, contract, third party. | Must reference instrument (policy #, SOW).                                      |
| `avoid`    | Activity stopped / not entered.   | Status-only, no controls expected.                                              |

## 6.2 Residual risk derivation

Residual = inherent × (1 − control_effectiveness). `control_effectiveness` is a derived score per linked control:

```
control_effectiveness = (
    weight_design       * design_score        // human-set, 0..1
  + weight_operation    * operational_score   // derived from evidence pass rate over rolling window
  + weight_coverage     * coverage_score      // applicability set ∩ scope where control passed
)
```

This makes residual risk _honest_: a control with great design and 40% evidence pass rate over the last 30 days drops effectiveness, raising residual. Risk dashboards trend with reality, not paper.

## 6.3 Exception / waiver workflow

Exceptions are **always scoped and time-bounded**:

| Field                         | Notes                                                          |
| ----------------------------- | -------------------------------------------------------------- |
| `control_id`                  | Required.                                                      |
| `scope_cell_predicate`        | What scope cells the exception applies to.                     |
| `justification`               | Required, freeform.                                            |
| `compensating_controls[]`     | What we're doing instead.                                      |
| `requested_by`, `approved_by` | Roles enforced.                                                |
| `expires_at`                  | Required, max 365 days. Auto-renewal forbidden.                |
| `status`                      | `requested` \| `approved` \| `denied` \| `active` \| `expired` |

Expired exceptions revert the control to evaluating normally. The expiration calendar is a first-class dashboard.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 5. Scopes](./05-scopes.md) · **Next:** [7. Metrics and Posture →](./07-metrics.md)
