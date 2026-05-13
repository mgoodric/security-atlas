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

## 6.4 Risk hierarchy and tiered acceptance

A single flat risk register doesn't survive organizations larger than ~10 people. Three risk levels:

| Level     | Examples                                          | Acceptance authority         |
| --------- | ------------------------------------------------- | ---------------------------- |
| `team`    | AppSec, Cloud, Platform, individual product teams | Team lead / senior engineer  |
| `org`     | Department, business unit, security program       | Director / VP                |
| `company` | Enterprise / program-level                        | CISO / Exec                  |

Each Risk record has a `level` field and an `org_unit_id` field. `risk_acceptance_authority` is derived from `(level, org_unit)` — each tenant configures the role-to-level mapping in `org_units.acceptance_authorities` (jsonb).

Risks roll up two ways:

- **Manual** — a higher-level Risk explicitly references one or more child Risks (`parent_risk_id` on the child, or a `risk_aggregations` join table for M:N).
- **Automatic** — driven by aggregation rules (§6.6).

A child risk's lifecycle is independent of its parent: closing all children does not auto-close the parent; the parent represents a *pattern* that may persist beyond any individual instance.

## 6.5 Theme taxonomy

Every Risk carries one or more `themes` — categorical tags that let aggregation rules detect patterns across teams without coupling to the team taxonomy.

**Default theme set** (extensible per-tenant via `org-private:<name>`):

| Theme              | Captures                                              |
| ------------------ | ----------------------------------------------------- |
| `ownership`        | Asset/service/resource without an identified owner    |
| `tech-debt`        | Known shortcuts, MVP scaffolding, deferred-by-design  |
| `access-control`   | Identity, authn/z, privilege management               |
| `key-management`   | Secrets, certificates, rotation                       |
| `data-protection`  | Encryption, classification, residency                 |
| `availability`     | Uptime, redundancy, BCP                               |
| `monitoring`       | Detection, logging, audit trail                       |
| `supply-chain`     | Third-party deps, OSS, build pipeline                 |
| `vendor-risk`      | Direct vendor management                              |
| `human-process`    | Training, awareness, manual workflow                  |

Themes are flat (no hierarchy) — aggregation rules carry the logic, not the taxonomy. Adding hierarchy here would invite the same flat-table-crosswalk pathologies we explicitly reject for frameworks (§3).

## 6.6 Aggregation rules and roll-up math

Aggregation rules are **declarative YAML**. Each rule defines when child-level risks should generate a parent-level meta-risk:

```yaml
rule_id: ownership-cross-team-2026
target_theme: ownership
threshold:
  min_risks: 3
  min_teams: 2          # risks must span ≥ 2 distinct org_units
  window_days: 90
parent_risk:
  level: org
  title_template: "Cross-team {theme} risk pattern detected"
  severity_function: weighted_max
status: active
```

**Severity functions** (configurable per rule):

| Function       | Formula                                                          | When to use                                       |
| -------------- | ---------------------------------------------------------------- | ------------------------------------------------- |
| `max`          | severity of highest child risk                                   | Default — conservative                            |
| `weighted_max` | `max × (1 + log(child_count))`                                   | When count matters as much as severity            |
| `sum`          | sum of all child severities (capped at scale max)                | When risks compound (rare; opt-in)                |
| `custom_rego`  | OPA Rego policy returns severity                                 | Tenant-defined logic                              |

The engine re-evaluates rules on every risk write. A satisfied threshold creates one meta-risk per rule per window — duplicate detections within the window update the existing meta-risk (idempotency via `(rule_id, window_start)` key).

**Closing a child does NOT close the parent.** The parent represents a pattern that may persist; the human reviewer decides when the pattern is genuinely resolved.

## 6.7 Decision Log

A **Decision Log** entry captures non-compliance decisions: tradeoffs, deferred best practices, MVP shortcuts, anything where the rationale matters more than formal control compliance. Distinct from exceptions in scope and audit role:

| Aspect             | Exception (§6.3)                       | Decision Log                                                |
| ------------------ | -------------------------------------- | ----------------------------------------------------------- |
| Scope              | Formal bypass of a specific control    | Any operational / architectural decision                    |
| Required link      | `control_id`                           | None required (but linkable to risks/controls/exceptions)   |
| Time-bound         | Yes (≤365d, no auto-renew)             | Optional `revisit_by` (hint, not gate)                      |
| Audit relevance    | Direct (auditor reads exceptions)      | Indirect (audit narrative + trust signal)                   |
| Typical content    | "Auditor finding accepted with comp."  | "Shipping MVP; deferring SAML to v1.2"                      |

**Schema:**

| Field                                                                        | Notes                                                                                       |
| ---------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `decision_id`                                                                | Globally unique within tenant                                                               |
| `title`, `narrative`                                                         | Required; freeform                                                                          |
| `constraints[]`                                                              | Structured tags: `time-pressure`, `cost`, `dependency-blocked`, `risk-accepted`, etc.       |
| `tradeoffs`                                                                  | Freeform; what was given up                                                                 |
| `decision_maker`, `decided_at`                                               | Required                                                                                    |
| `revisit_by`                                                                 | Optional ISO date; surfaces on dashboard when due                                           |
| `linked_risks[]`, `linked_controls[]`, `linked_exceptions[]`, `scope_predicate` | M:N via join tables                                                                       |
| `status`                                                                     | `active` \| `revisited` \| `superseded` \| `expired`                                        |
| `superseded_by`                                                              | Decision ID of the replacement, if any                                                      |

Decisions appear in audit narrative export (§8) as **context**, not as compliance artifacts. An auditor reading the SSP sees: *"This gap exists because of decision DL-2026-04-12: ship MVP, defer X. Linked risk RSK-... accepted by .... Revisit: 2026-07-01."* The chain is legible.

The Decision Log + Exception + Aggregation Rules triad is the operational risk surface: exceptions handle the formal compliance dance, decisions capture the broader operational tradeoffs, and aggregation rules surface emergent patterns that no single risk-raiser saw alone.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 5. Scopes](./05-scopes.md) · **Next:** [7. Metrics and Posture →](./07-metrics.md)
