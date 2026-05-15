# Board reporting — monthly brief and quarterly pack

<!-- TODO(slice-057): board pack preview screenshot from
     docs/images/board-pack-preview.png once slice 057 merges. -->

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - What the monthly brief and quarterly pack contain
    - How to generate each one
    - How the AI-drafted narrative is bounded by the AI-assist boundary
<!-- prettier-ignore-end -->

Board reporting is **first-class** in security-atlas — the second half
of the v1 binary success test is that the solo security leader files
their next quarterly board pack from this tool. Two artifacts ship:

| Artifact           | Cadence       | Audience                                  |
| ------------------ | ------------- | ----------------------------------------- |
| **Monthly brief**  | every month   | the CEO and the security leader's manager |
| **Quarterly pack** | every quarter | the full board                            |

## Monthly brief

A one-page status. Generates in seconds from live evidence and control
state — no manual data entry required.

```sh
just atlas-cli board-brief generate \
  --tenant <tenant-id> \
  --month 2026-04
```

What it contains:

- **Coverage** — % of applicable controls passing, by framework, with
  the trailing-30-day delta
- **Open findings** — count by severity, oldest age
- **Top risks** — the top 5 from the risk register (residual rank)
- **Calendar** — exceptions expiring, policy re-acks due, audit
  milestones in the next 30 days
- **Narrative** — three short paragraphs drafted from the metrics, ready
  for human edit

## Quarterly pack

A 6-12 page board-ready document. Same data, deeper cuts:

- **Coverage** — by framework, by quarter, with quarter-over-quarter
  trend
- **Investment vs coverage** — manual entry; what was spent vs what
  shifted (this is the only manually-entered section in v1)
- **Risk register** — top 10 risks, residual rank, ownership, status
- **Vendor program** — vendor count, criticality distribution, reviews
  completed
- **Policy program** — published policies, acknowledgment rates,
  pending re-acks
- **Audit posture** — open AuditPeriods, frozen periods this quarter,
  open findings, POA&M status
- **Narrative** — five auto-drafted sections, every one human-approved
  before the pack is exported

```sh
just atlas-cli board-pack generate \
  --tenant <tenant-id> \
  --quarter 2026Q1 \
  --out ./board-pack-2026q1.pdf
```

## How the AI-drafted narrative is bounded

This is the most valuable feature for solo operators **and** the highest
risk feature if it hallucinates. security-atlas's
[AI-assist boundary](https://github.com/mgoodric/security-atlas/blob/main/CLAUDE.md#ai-assist-boundary-hard)
constrains it strictly:

- The narrative is **drafted**, never published. Every section requires
  one-click human approval before the pack exports.
- The draft cites the metrics it relied on. A reader can click any
  number and see the underlying evidence record.
- The model name, model version, and the diff between AI draft and
  final approved text are written to the audit log on every approval.
- The default inference backend is **local Ollama** (no data leaves the
  deployment). Cloud LLMs (Anthropic / OpenAI / Bedrock) are opt-in
  per-tenant, with a visible banner in the UI when the tenant is routed
  there.
- AI **never** fabricates control coverage that has no evidence
  backing. If coverage for a framework is 73%, the narrative says 73%.

Schema-level enforcement: a section with `ai_assisted=true` cannot have
`human_approved=true` without `human_approver` set. The constraint is in
the database, not the application.

## What is deliberately deferred

- Per-section style memory across packs (e.g., "always lead with
  vendor risk")
- Per-tenant brand kit beyond a logo and a colour
- Live-link board packs (the export is a deterministic, signed PDF;
  a portal is phase 3, not v1)

## Next steps

- [Intro →](index.md) — back to the top
- For a deeper architectural read: [`Plans/canvas/07-metrics.md`](https://github.com/mgoodric/security-atlas/blob/main/Plans/canvas/07-metrics.md)

---

## Was this helpful?

Tell us in [GitHub Discussions](https://github.com/mgoodric/security-atlas/discussions).
