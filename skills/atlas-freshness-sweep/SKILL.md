---
name: atlas-freshness-sweep
description: Find security-atlas controls whose evidence is stale or missing, so the team can refresh it before an audit. Read-only. USE WHEN stale evidence, evidence freshness, which controls are stale, evidence gaps, controls without evidence, freshness sweep, what needs refreshing, evidence coverage check.
---

# /atlas-freshness-sweep — controls with stale or missing evidence

Surface the controls whose supporting evidence is stale or absent, so the operator
can refresh it before it bites them in an audit. **Read-only.**

## Prerequisite

The [atlas-mcp server](../../docs-site/docs/mcp.md) must be connected with the
`list_controls` and `list_evidence` tools available.

## Steps

1. Call `list_controls` to get the tenant's active controls.
2. Call `list_evidence` to get the recent evidence ledger window. (Evidence rows
   carry an `observed_at` timestamp and the control/kind they attach to; they never
   include the raw payload — you don't need it here.)
3. Correlate: for each control, find its most recent evidence.
   - **Missing** — a control with no evidence at all.
   - **Stale** — a control whose newest evidence is older than the operator's
     freshness expectation (ask if they didn't say; a common default is 90 days).
4. Report, worst first:
   - Controls with **no evidence** (highest priority — an auditor sees an unsupported
     control).
   - Controls with **stale evidence**, sorted oldest-first, with the age.
5. If the platform already exposes a freshness verdict per control in the control
   rows, prefer that over recomputing from timestamps — say which you used.

## Output shape

```
Freshness sweep — <tenant>, <date> (threshold: <N> days)

No evidence (<count>):
- <control> (<family>)
...

Stale evidence (<count>):
- <control> — last seen <date> (<age> ago)
...

Fresh: <count> controls are within threshold.
```

## Boundaries

- Read-only. Do NOT push evidence or change control state here. If the operator
  wants to record fresh evidence, hand off to `push_evidence` — which files a
  proposal for their approval, not an unattended write.
- If the freshness threshold is ambiguous, ask once rather than guessing silently;
  the answer changes which controls appear.
- Report only what the tools return; do not assume a control is covered because it
  "should" be.
