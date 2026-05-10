# 031 — Monthly board brief (templated, no LLM)

**Cluster:** Board reporting
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Generate the monthly single-page board brief: posture summary per framework, drift in the last 30 days, top-3 risks aging. Output is a pinned snapshot — the board reads what posture *was* at the report date, even if live state has changed afterward. Narrative is templated (Jinja-style) over real metrics — no LLM in v1 (per gate resolution). Output as PDF + Markdown for paste-into-deck. The slice delivers value because the user has a board-ready single-pager at the end of each month without leaving the tool.

## Acceptance criteria

- [ ] AC-1: `POST /v1/board-briefs` with `period_end=YYYY-MM-DD` generates a brief pinned to that snapshot
- [ ] AC-2: Brief includes: framework posture (each), recent drift count, top-3 risks aging
- [ ] AC-3: Narrative templated — example: "We are in audit-ready state for SOC 2 ({coverage_pct}%, {trend_arrow} {delta} pts)."
- [ ] AC-4: Output formats: PDF + Markdown; both downloadable via `GET /v1/board-briefs/:id/pdf` and `.md`
- [ ] AC-5: Pinned snapshot: re-fetching after live changes returns the original content (snapshot is immutable)
- [ ] AC-6: Solo-operator can generate without any LLM dependency present

## Constitutional invariants honored

- **AI-assist boundary:** v1 narrative is templated only — no LLM. No auto-publish risk.
- **Invariant 10 (audit-period freezing — analog):** brief is a snapshot, immutable after generation

## Canvas references

- `Plans/canvas/07-metrics.md` §7.5 (board reporting; deck cadence — monthly briefs)
- `Plans/mockups/board-pack.html` (visual reference)

## Dependencies

- #012, #016, #020

## Anti-criteria (P0)

- Does NOT include LLM-generated narrative in v1
- Does NOT auto-publish without explicit user generation
- Does NOT permit edit of a pinned snapshot — new brief = new snapshot

## Skill mix (3–5)

- Jinja-style Go templating (text/template + structured input)
- PDF rendering (chromedp or wkhtmltopdf)
- Markdown export
- Snapshot/pin pattern in Postgres
- Date-bounded metric queries
