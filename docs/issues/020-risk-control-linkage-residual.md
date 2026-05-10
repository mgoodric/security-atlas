# 020 — Risk → control linkage + residual risk derivation

**Cluster:** Risk register
**Estimate:** 2d
**Type:** AFK

## Narrative

Link risks to controls (many-to-many) and derive residual risk from real-time control effectiveness. `residual_score = inherent_score × (1 − weighted_control_effectiveness)`. `control_effectiveness` is a derived score combining design, operational, and coverage components (per `06-risk.md` §6.2). The operational component pulls from slice 012's rolling pass rate; coverage uses slice 017's applicability set intersected with passing cells. Residual recomputes whenever control state changes (event-driven via NATS). This makes risk dashboards trend with reality — a control with great design but a 40% evidence pass rate honestly raises residual.

## Acceptance criteria

- [ ] AC-1: `POST /v1/risks/:id/controls` links a control to a risk; appears in `linked_control_ids[]`
- [ ] AC-2: `GET /v1/risks/:id` returns `inherent_score`, `residual_score`, and effectiveness breakdown per linked control
- [ ] AC-3: `control_effectiveness` math: `weight_design × design_score + weight_operation × operational_score + weight_coverage × coverage_score`
- [ ] AC-4: Operational score derived from slice 012's 30-day rolling pass rate
- [ ] AC-5: Residual recomputes within 60 seconds of any control_state change (via NATS subscriber)
- [ ] AC-6: Integration test: control flips pass→fail, risk residual visibly increases on next query
- [ ] AC-7: Risk with no linked controls returns `residual_score = inherent_score` and a `warning: no_controls_linked` flag

## Constitutional invariants honored

- **Invariant 2 (ingestion/eval separated):** residual derived from `control_state`; never modifies evidence

## Canvas references

- `Plans/canvas/06-risk.md` §6.2 (residual risk derivation formula)
- `Plans/canvas/02-primitives.md` §2.2 (Risk-Control linkage)

## Dependencies

- #019, #012

## Anti-criteria (P0)

- Does NOT cache residual scores beyond their staleness threshold (must reflect current effectiveness)
- Does NOT permit `treatment=mitigate` without ≥1 linked control (enforced upstream in slice 019)
- Does NOT skip the no-controls-linked warning

## Skill mix (3–5)

- Go (subscriber + handlers)
- Postgres derived columns / triggers
- NATS subscribe pattern
- Risk methodology math
- Integration testing with time control
