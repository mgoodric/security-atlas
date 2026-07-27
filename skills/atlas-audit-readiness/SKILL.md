---
name: atlas-audit-readiness
description: Produce an audit-readiness snapshot for a security-atlas audit period — control coverage, evidence gaps, expiring exceptions, and open remediation. Read-only. USE WHEN audit readiness, are we ready for the audit, audit prep, audit gap check, readiness snapshot, pre-audit review, what's outstanding before the audit, SOC 2 readiness.
---

# /atlas-audit-readiness — readiness snapshot for the current audit period

Give the operator a single, honest picture of how ready they are for an audit:
what's covered, what's missing, and what will expire or is still open. **Read-only.**

## Prerequisite

The [atlas-mcp server](../../docs-site/docs/mcp.md) must be connected. This skill
uses `list_audit_periods`, `list_controls`, `list_evidence`, `list_exceptions`, and
`list_action_plans`. If any is unavailable, note it and report on what you can.

## Steps

1. Call `list_audit_periods`. Identify the relevant period — the operator's named
   one, else the open (not-yet-frozen) period, else the nearest upcoming. Note
   whether it is **frozen** (a frozen period samples from a fixed snapshot).
2. Call `list_controls` for the in-scope controls.
3. Call `list_evidence` and correlate to find controls with **missing or stale**
   evidence (same logic as the freshness sweep). These are the direct audit gaps.
4. Call `list_exceptions`. Flag any exception that **expires during or before the
   audit period** — an exception lapsing mid-audit is a finding waiting to happen.
5. Call `list_action_plans`. Flag any remediation still **open** (not closed) whose
   subject overlaps an in-scope control.
6. Assemble the snapshot and end with a plain readiness call: green (no material
   gaps), yellow (gaps with time to fix), or red (gaps that will surface in the
   audit as written).

## Output shape

```
Audit readiness — <period name> (<frozen? / open>), <tenant>, <date>

Coverage: <X of Y in-scope controls have current evidence>
Evidence gaps: <count> (list the worst)
Exceptions expiring in-window: <count> (list them)
Open remediation touching scope: <count> (list them)

Readiness: <GREEN / YELLOW / RED> — <one sentence why>
```

## Boundaries

- Read-only. Do NOT freeze the period, push evidence, or change control state. Those
  are deliberate operator actions; the write tools file proposals you approve.
- Be honest about the readiness color — a green light on a program with evidence
  gaps is worse than no snapshot. Under-claiming readiness is safer than over-claiming.
- If the in-scope set for the period isn't clear from the data, say so rather than
  guessing which controls the auditor will sample.
- Report only what the tools return.
