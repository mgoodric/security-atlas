# 032 — Quarterly board pack with templated narrative + investment-vs-coverage entry

**Cluster:** Board reporting
**Estimate:** 2.5d
**Type:** AFK

## Narrative

Extend slice 031's monthly brief into the full quarterly board pack per the mockup at `Plans/mockups/board-pack.html`. Sections: posture summary with auto-templated narrative, top risks aging table, control coverage trend (line chart), open findings burndown, operational metrics (phishing pass rate, P1 patch median, incident count, vendor reviews on time), investment-vs-coverage section (user manually enters Q spend; system computes coverage delta + implied cost per coverage point), asks of the board (user-authored). Output is PDF + Markdown for paste-into-deck. Approval workflow: user reviews section-by-section, approves each narrative, then publishes the pack as a frozen artifact. The slice delivers value because the binary v1 success test ("user runs the SOC 2 audit + generates the board pack from this tool") completes.

## Acceptance criteria

- [ ] AC-1: `POST /v1/board-packs` with `period_end` generates a draft quarterly pack with all sections
- [ ] AC-2: Each templated narrative section is editable; user can override the templated text before approval
- [ ] AC-3: Investment-vs-coverage section accepts user input ($ spend breakdown) and computes coverage delta over the period
- [ ] AC-4: Asks of the board is a freeform editable section — no AI generation
- [ ] AC-5: Approval workflow: per-section approve → publish overall → frozen artifact
- [ ] AC-6: Output formats: PDF + Markdown; both rendered to match the `Plans/mockups/board-pack.html` visual reference
- [ ] AC-7: Once published, the pack is immutable; future quarter regenerates a new pack
- [ ] AC-8: The pack pulls finding/POA&M data from slice 030's audit-export pipeline

## Constitutional invariants honored

- **AI-assist boundary:** narrative templated; explicit per-section human approval required before publish
- **Invariant 10 (audit-period freezing — analog):** published pack is frozen; no in-place mutation
- **Anti-pattern rejected (AI-generated audit responses):** the board pack is treated with the same discipline

## Canvas references

- `Plans/canvas/07-metrics.md` §7.5 (board pack content table)
- `Plans/mockups/board-pack.html` (visual reference)

## Dependencies

- #031, #030

## Anti-criteria (P0)

- Does NOT publish without per-section human approval
- Does NOT generate AI narrative in v1 (templated only)
- Does NOT mutate a published pack

## Skill mix (3–5)

- Go templating + section orchestration
- PDF rendering with chart embedding (line chart, bar chart from slice 016 data)
- Approval workflow state machine
- Next.js review/approve UI per section
- Markdown + PDF parity
