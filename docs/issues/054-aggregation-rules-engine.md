# 054 — Declarative aggregation rules engine

**Cluster:** Risk register
**Estimate:** 3d
**Type:** HITL

## Narrative

The automatic rollup pattern from canvas §6.6. A declarative YAML rule defines when child-level risks should generate a parent-level meta-risk (e.g., "≥ 3 `ownership` risks across ≥ 2 teams in the last 90 days → org-level pattern risk"). The engine re-evaluates rules on every risk write and auto-creates meta-risks when thresholds are met.

The meaningful design choices:

- **Window-based idempotency** — a rule firing within its window updates the existing meta-risk rather than creating a duplicate. Key = `(rule_id, window_start)`.
- **OPA Rego support** for custom severity functions — tenants who want logic beyond `max | weighted_max | sum` write Rego (canvas §6.6).
- **No auto-close of parents** when children close. The pattern persists even after individual instances resolve.
- **HITL gate** because rule-driven auto-creation of high-severity meta-risks at the `org` or `company` level can spam executives if mis-tuned. New rules require a human reviewer marking the rule `status: active` (default `staged`).

## Acceptance criteria

- [ ] AC-1: `POST /v1/aggregation_rules` accepts YAML (or JSON-equivalent) per the canvas §6.6 schema. Validated against a JSON Schema; bad rules return 400 with field-level errors.
- [ ] AC-2: Rules ship with `status: staged` by default. `PATCH /v1/aggregation_rules/{id}` flips status to `active` after human review.
- [ ] AC-3: The engine evaluates every `active` rule on every `risks` INSERT / UPDATE / DELETE. Evaluation runs inside the same Postgres transaction (idempotent: re-running on the same data produces the same result).
- [ ] AC-4: When a rule's threshold (`min_risks`, `min_teams`, `window_days`) is satisfied, the engine creates ONE meta-risk per `(rule_id, window_start)` key. Subsequent matches within the window update the existing meta-risk (add new child links; recompute severity).
- [ ] AC-5: Severity functions integrated: `max` (default), `weighted_max`, `sum`, `custom_rego` (Rego policy bundled with the rule). All four covered by unit tests.
- [ ] AC-6: Closing a child risk does NOT close the parent meta-risk. The meta-risk's severity recomputes (drops the contributor); parent's lifecycle is independent.
- [ ] AC-7: Integration test (E2E): define a rule (`ownership-cross-team`, threshold 3 risks / 2 teams / 90d), create 2 ownership risks across 1 team (no meta-risk), create a 3rd across a 2nd team (meta-risk auto-appears at `org` level with all 3 as children), resolve the 3rd (parent severity drops, parent stays `open`).
- [ ] AC-8: Rule audit log — every rule firing (and every threshold-near-miss) writes a row to `aggregation_rule_evaluations` (`rule_id`, `evaluated_at`, `outcome: fired | near_miss | no_match`, `risk_count`, `team_count`). Auditor visibility.
- [ ] AC-9: Engine performance: rule evaluation completes within 200ms p95 for a tenant with 500 active risks and 10 active rules. Indexes on `(tenant_id, theme, observed_at)` and `(tenant_id, org_unit_id)` validated.
- [ ] AC-10: Rule deactivation (`status: inactive`) stops new firings but preserves historical meta-risks. Re-activating does not retroactively re-fire on old data — only on writes after re-activation.

## Constitutional invariants honored

- **Invariant 6** (tenant isolation) — rules and their evaluations are tenant-scoped; cross-tenant rule data never touches another tenant's risks
- **AI-assist boundary** — custom Rego policies are run in a sandbox; no LLM authorship of rules without human review (which is what the `staged` → `active` transition enforces)
- **Invariant 9** (manual is first-class) — manually-aggregated risks (slice 053) coexist with rule-driven ones; they live in the same `risk_aggregations` table with `rule_id` distinguishing the source

## Canvas references

- `Plans/canvas/06-risk.md §6.6` — Aggregation rules and roll-up math (rule schema, severity functions, idempotency, parent lifecycle)

## Dependencies

- **053** (theme tagging + manual aggregation) — depends on `risk_aggregations` table being live and severity functions being unit-tested in 053
- (implicit) **052** schema is transitively required

## Anti-criteria (P0)

- Do NOT auto-activate new rules — `staged` → `active` is HITL.
- Do NOT auto-close parent risks when all children close.
- Do NOT fire the same rule more than once per `(rule_id, window_start)` window.
- Do NOT expose tenant data to OPA Rego policies from other tenants — Rego sandbox must be tenant-isolated.
- Do NOT generate meta-risks that re-aggregate into themselves (cycle detection required in the rule definition layer).
- Do NOT re-fire on historical data when a rule is re-activated — only forward-looking.

## Skill mix (3–5)

- `engineering-advanced-skills:rag-architect` (rule-as-config pattern; YAML → Rego compilation if needed)
- `tdd` (engine evaluation is logic-heavy — TDD against the threshold + window + severity logic)
- `security-review` (Rego sandbox; cross-tenant denial; cycle prevention)
- `engineering-advanced-skills:performance-profiler` (AC-9 p95 latency requires query plan review on aggregation-heavy queries)
- `engineering-advanced-skills:observability-designer` (rule evaluation telemetry: count, duration, fire-rate per rule)

## Notes for the implementing session

- Use a single `aggregation_rule_evaluations` write per rule per evaluation cycle, even if the outcome is `no_match` — this audit trail is what lets auditors trust that the engine isn't silently missing patterns.
- Window math: `window_start` should snap to a stable boundary (e.g., truncated to the hour or day) so concurrent writes within a single window converge on the same meta-risk row rather than racing.
- For OPA Rego custom severity functions, bundle the policy bytes alongside the rule definition rather than fetching at evaluation time — fewer moving parts in the hot path.
- If a rule's threshold relaxes (e.g., `min_teams: 2` → `1`) via PATCH, the engine should NOT retroactively fire on data from before the change. Document this in `aggregation_rule_evaluations` and surface it in the rule-edit UI.
