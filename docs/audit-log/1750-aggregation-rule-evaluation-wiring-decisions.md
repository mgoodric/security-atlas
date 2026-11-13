# 1750 — Aggregation rules evaluated on risk writes — decisions log

Build-time calls made for issue #1750 (`aggrule.Store.Evaluate` had no caller
outside its package, so an active rule never fired and
`aggregation_rule_evaluations` stayed empty).

- detection_tier_actual: manual_review
- detection_tier_target: integration

(The bug surfaced in a first-run adopter setup on 2026-11-12. The slice 054
suite called the engine directly; no test drove a rule through a risk write.
`TestRiskWrite_EvaluatesActiveRules_1750` now does, and fails when the hook is
removed from `httpserver.go`.)

## Decisions made

### D1 — The engine is injected into `risk.Store`, not imported by it

`aggrule` imports `risk` (`DeriveGridCell`), so `risk` cannot import `aggrule`.
`risk.Store` takes an `AfterRiskWrite` hook, run inside the write's
transaction with the written risk's themes; `httpserver.go` sets it to
`aggrule.EvaluateOnRiskWrite`. Evaluation commits or rolls back with the
write, which is what the engine's doc comment requires. A `risk.Store` built
without the hook (tests, other callers) behaves as before.

### D2 — Which writes trigger evaluation

Risk create and theme assignment (`AssignThemes`, which also backs theme
removal). There is no REST risk update path yet (#1749); when one lands it
should call the same hook. Residual recompute, control links and the MCP
treatment applier do not change a candidate's themes, `created_at` or inherent
score, so they cannot change a rule's outcome and do not trigger it.

### D3 — A theme-less write skips the engine

The engine treats an empty trigger set as "evaluate every rule". A risk with
no themes cannot match any rule (each rule targets one theme), so the hook
returns without evaluating rather than sweeping every rule on each create.
Removing a theme evaluates only the themes the risk still carries.

### D4 — "Evaluate now" is `POST /v1/aggregation-rules/evaluate`

It runs a full sweep (`Store.Evaluate(ctx, nil)`) and returns the evaluation
rows written, one per active rule. It is a literal-segment POST, so it does not
collide with `/{id}` routes, and sits behind the same middleware chain as the
other aggregation-rules routes (`routes_mw.golden` depth 8).

## Re-evaluate

- The engine's window floor is the rule's `activated_at`: risks created before
  activation never count, by design (slice 054 "no re-fire on historical data").
  An adopter who activates a rule after importing a register will see
  `no_match` from "evaluate now" until new matching risks are written. Whether
  the evaluate-now sweep should offer a deliberate backfill is a product call,
  not made here.
