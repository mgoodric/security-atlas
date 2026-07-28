---
name: atlas-risk-briefing
description: Produce a ranked briefing of the security program's top open risks from security-atlas, with treatment status and residual severity. Read-only. USE WHEN risk briefing, top risks, what are my risks, risk register summary, risk posture, brief me on risks, board risk summary, risks in treatment, riskiest items.
---

# /atlas-risk-briefing — top open risks, ranked

Produce a concise, decision-ready briefing of the tenant's most significant open
risks from security-atlas, using the MCP read tools. **Read-only** — this skill
never changes data. If the briefing warrants a change, use the write tools, which
require your approval (a proposal you confirm) before anything is committed.

## Prerequisite

The [atlas-mcp server](../../docs-site/docs/mcp.md) must be connected. If the
`list_risks` tool is not available, stop and tell the operator to connect it.

## Steps

1. Call `list_risks` to get the register. If the operator asked specifically for
   risks being actively worked, pass `treatment=mitigate`; otherwise list all.
2. Rank the risks by **residual severity** (severity after controls) descending,
   then by **inherent severity**, then by age in treatment. Residual is the number
   that matters for "what could still hurt us"; lead with it.
3. For the top 5 (or the count the operator asked for), call `get_risk` on each to
   pull the linked controls and any owner. Do not call `get_risk` for the whole
   register — only the ones you will report.
4. Write the briefing:
   - One line per risk: title · residual severity · treatment · owner · a phrase on
     what's driving it (from the description / linked controls).
   - Call out any **top risk with no linked controls** or **no owner** — those are
     the gaps a CISO acts on.
   - A one-sentence bottom line: is the top of the register trending covered or
     exposed?
5. Keep it to what fits on a screen. This is a briefing, not a data dump.

## Output shape

```
Risk briefing — <tenant>, <date>

Top open risks (by residual severity):
1. <title>  · residual <n> · <treatment> · owner <name/UNASSIGNED>
   <one line on the driver + control coverage>
...

Gaps: <risks missing controls or owners, if any>
Bottom line: <one sentence>
```

## Boundaries

- Read-only. Do NOT create or modify risks in this skill. If the operator asks to
  change a treatment or owner, hand off to the write tool (`update_risk_treatment`)
  and explain that it files a proposal for their approval.
- Report only what the tools return — never invent a residual score, an owner, or a
  control link that isn't in the data.
- Do not widen the token's access; you see exactly what the operator's credential
  can see.
