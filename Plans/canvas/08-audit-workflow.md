**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 8. Audit Workflow

## 8.1 Auditor role

A dedicated `auditor` role with:
- Read-only access to evidence, controls, scopes, exceptions, policies.
- Sample-pull tools (random N from population, deterministic seed for reproducibility).
- Walkthrough recording (annotated screen captures + transcript stored alongside evidence).
- Their own workspace for organizing testing notes — not visible to the auditee.
- A time-window scope (auditor sees state as of `audit_period_end`, not live).

This role is **first-class**, not an afterthought. Auditors who can do their work in our tool become advocates; auditors who can't insist on Vanta or spreadsheets.

## 8.2 OSCAL SSP / POA&M export

| Artifact | Generated from |
|----------|----------------|
| SSP (`system-security-plan`) | Org profile + scope cells + applicable controls + control implementation narratives + linked policies |
| Assessment Plan | Auditor's selected sample population + planned procedures |
| Assessment Results | Sampled evidence records + auditor pass/fail/finding annotations |
| POA&M | Open findings with milestones, owners, due dates |

We commit to OSCAL JSON v1.1.x compatibility and ship an `oscal-export` CLI alongside the UI export.

## 8.3 Walkthrough and sample-pull primitives

- `Population(control, scope_predicate, time_window)` — defines what a sample is drawn from.
- `Sample(population, n, seed)` — deterministic, reproducible.
- `Walkthrough(control, narrative, attachments[])` — auditor or owner recorded explanation, hashed and signed.
- `Finding(control, severity, description, linked_evidence[])` — drives POA&M.
- `AuditPeriod(audit_id, period_start, period_end, frozen_at)` — see [§8.4 Audit-period freezing](#84-audit-period-freezing-the-snapshot-primitive).
- `AuditNote(scope: control | finding | sample, author, body, visibility)` — auditor↔auditee threaded comments inside the tool.

These primitives compose. An audit cycle is a graph of populations, samples, walkthroughs, findings, frozen periods, and notes against the control set.

## 8.4 Audit-period freezing (the snapshot primitive)

A recurring practitioner complaint about Vanta/Drata is **post-window evidence pollution** — a control is failing on the day of the auditor walkthrough but passes the next morning, and the sample population shifts under the auditor's feet. We solve this with explicit freezing.

When an `AuditPeriod` is created, the user (or auditor) calls `freeze(period_id, frozen_at)`. From that moment:
- Sample populations for that period draw only from evidence with `observed_at ≤ frozen_at`.
- Control state for the period is computed against frozen evidence; live state continues independently.
- New evidence after `frozen_at` does not retroactively change the auditor's view.
- Frozen state is hashed and signed; tampering is detectable.

The append-only evidence ledger makes this cheap — we don't need separate snapshots, we just shift the read horizon. This is one of the practical wins of the event-driven evidence architecture (see [§4 Evidence Engine](./04-evidence-engine.md)).

## 8.5 Auditor collaboration (the "Audit Hub" pattern)

Practitioners cite Drata's in-product auditor↔auditee comment thread as the single most valuable feature when migrating between tools. We replicate it as a first-class workflow:

- Auditor leaves a comment on a control / sample / finding.
- Auditee receives a notification, replies in-product, attaches additional evidence.
- Comment thread is retained as an audit artifact, exported to OSCAL `assessment-results` `observation` annotations.
- No email back-and-forth. No "I'll send you the screenshot in Drive" loop.

This is not a separate "messaging" feature — it's threaded annotations on first-class objects.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 7. Metrics](./07-metrics.md) · **Next:** [9. Architecture and Tech Stack →](./09-tech-stack.md)
