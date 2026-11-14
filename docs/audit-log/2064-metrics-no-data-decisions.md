# Issue #2064 — Metrics with no inputs read as "No data", not as a pass

The critical-findings SLA evaluator returned 1.0 when no findings existed, and
the metrics page rendered that as "100.0%", green against a target. Other
evaluators returned 0 for the same absence, which reads as a fail.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

## D1 — One empty marker, written by the evaluator

**Decision.** An evaluator whose inputs are absent returns `noData(reason)`:
`dimensions.sample = "empty"` (the key the evaluators already used) plus a
one-line `dimensions.no_data_reason`. `Value` is 0 and carries no meaning.

**Why.** The UI cannot tell "no inputs" from a real 0 or 1 by the number. The
evaluator is the only place that knows, and the dimensions column already
reaches the UI through the observations API, so no migration or wire change is
needed. Setting `Value` to 0 (not 1.0) means a consumer that ignores the marker
shows a non-pass rather than a pass.

## D2 — Which evaluators can have no inputs

| Evaluator                      | Empty when                                 |
| ------------------------------ | ------------------------------------------ |
| `critical_findings_sla`        | no findings in the 90-day window           |
| `program_effectiveness`        | no control evaluations                     |
| `evidence_freshness_pct`       | no freshness rows                          |
| `audit_readiness_score`        | no activated scope, or no freshness rows   |
| `open_risk_financial_exposure` | no open risk carries a residual score      |
| `policy_attestation_rate`      | no published policy or no acks in 365 days |
| `training_completion_rate`     | no training assigned                       |
| `vendor_risk_concentration`    | no vendors                                 |
| `exception_expiration_runway`  | never: a count of 0 expiring is an answer  |

`audit_readiness_score` used to treat missing freshness rows as a 0 freshness
factor. With no freshness rows the evidence half of the product is
unmeasured, and `evidence_freshness_pct` already calls the same condition "no
data", so the two now agree.

## D3 — The UI hides the number and the colour

The board card and the detail page check the newest observation for the
marker. When present they show "No data" with the reason, pass no value to the
threshold badge (so it is neutral "no data"), and drop empty-marked points
from the sparkline and line chart.

The board pack and its exports do not read `metric_observations`, so they are
unaffected by this marker.
