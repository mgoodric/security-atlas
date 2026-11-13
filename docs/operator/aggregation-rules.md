# Aggregation rules: operating notes

Aggregation rules (canvas §6.6, slice 054) roll many child risks that share a
theme into one rule-generated meta-risk. The engine runs inside the
transaction of every risk write that touches a rule's `target_theme`:

| Write                                  | Rules evaluated                                 |
| -------------------------------------- | ----------------------------------------------- |
| `POST /v1/risks` (create)              | rules targeting any of the new risk's themes    |
| `POST /v1/risks/{id}/themes`           | rules targeting a theme the risk had or now has |
| `DELETE /v1/risks/{id}/themes/{theme}` | rules targeting a theme the risk had or now has |
| `DELETE /v1/risks/{id}`                | rules targeting the deleted risk's themes       |
| `POST /v1/aggregation-rules/evaluate`  | every active rule                               |

Deleting a rule-generated meta-risk does not evaluate anything. If its rule
still meets its threshold, the next matching risk write creates a new one.

## One meta-risk per activation

A rule keeps one meta-risk for as long as it stays active. Each evaluation
re-links that meta-risk to exactly the risks in the current rolling window
(the last `window_days`, never earlier than the rule's activation) and
recomputes its score. When a theme is removed, a risk is deleted, or a risk
ages out of the window, the meta-risk drops it. This also happens when the
rule falls below its threshold. The meta-risk itself is never closed or
deleted by the engine.

Ageing out is not on a timer. Nothing runs when a risk passes the end of the
window; the meta-risk drops it on the next evaluation that touches the rule's
theme (a risk write on that theme, or `POST /v1/aggregation-rules/evaluate`).
Until then the meta-risk still shows the old children and score. Run
`evaluate` to bring every meta-risk up to date at once.

When every child has gone, the meta-risk stays open with `child_count` 0 and
an empty score, which the grid would otherwise show as a quiet 1x1 risk. The
risks list and the risk page label it **Pattern no longer met**. Close or
delete it if the pattern is over; the engine will not. The evaluation that
emptied it is a `near_miss` or `no_match` ledger row that names the
meta-risk, so the change is traceable.

Deactivating a rule leaves its meta-risk as it was. Re-activating the rule
starts a new meta-risk; the old one stays as history.

Installs that ran an active rule for more than `window_days` before #1861 may
hold several meta-risks for that rule, one for each day it fired after
`window_days`. The engine now updates only the meta-risk keyed to the rule's
current activation. That is the one created in the rule's first
`window_days`, if it fired then. If it didn't, the engine creates one on the
next evaluation that meets the threshold. It leaves the per-day meta-risks
alone. Review them and delete the ones you don't need.

Installs that raced two evaluations of one rule before #1861 may also hold
two meta-risks with the same key. The engine always updates the oldest of
them (by `created_at`, then `id`), so the other one stops changing. Delete it.

## One broken active rule blocks writes on its theme

Evaluation runs inside the risk write's transaction, so an evaluation error
rolls the write back. If one active rule cannot evaluate, every write in the
table above that touches its `target_theme` fails with a 500 until the rule
is fixed or deactivated. The usual cause is a `custom_rego` severity policy
that errors or doesn't assign an integer. `POST /v1/aggregation-rules/evaluate`
fails the same way for every theme.

### Find it

The failed evaluation is rolled back, so it leaves no
`aggregation_rule_evaluations` row. The server log has the full error. The
client response does not. Search the log for `aggrule:`. The message names
the rule's id:

```text
aggrule: compute severity for rule 3f0c...: ...
```

If the log isn't available, list the active rules and look for the ones
targeting the failing theme. A `custom_rego` rule is the first suspect:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$ATLAS_URL/v1/aggregation-rules?status=active"
```

### Deactivate it

```bash
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" \
  "$ATLAS_URL/v1/aggregation-rules/<id>/deactivate"
```

Deactivation is audited (`aggregation_rule_audit_log`, event `deactivated`).
Risk writes on the theme succeed again straight away. The rule's existing
meta-risk is not changed. Fix the rule, then re-activate it (`PATCH
.../activate`). That starts a new meta-risk, and the rule does not re-fire
on risks written before the re-activation.
