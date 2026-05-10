# 043 — Board pack preview/export view

**Cluster:** Frontend views
**Estimate:** 2d
**Type:** AFK

## Narrative

Build the board pack preview view per `Plans/mockups/board-pack.html`. User opens a draft pack (from slice 032), reviews each section, edits/approves the templated narratives one-by-one, fills in the investment-vs-coverage section, drafts the "asks of the board" section, then publishes. Published pack is frozen and downloadable as PDF + Markdown. Visual design matches the mockup. The slice delivers value because the binary v1 success test ("generate next board pack from this tool") completes from a user's POV — they have a polished review/approve/export experience.

## Acceptance criteria

- [ ] AC-1: `/board-pack/:id` route renders the full pack matching the mockup
- [ ] AC-2: Each templated narrative section is editable inline; "AI-drafted" badge replaced with "Templated v1" (no LLM in v1)
- [ ] AC-3: Section approval status visible per section; approve buttons enforce role check
- [ ] AC-4: Investment-vs-coverage table accepts manual entry; coverage delta computed
- [ ] AC-5: "Asks of the board" section is freeform editable; no AI generation
- [ ] AC-6: Publish button enabled only when all sections approved; locks the pack and renders PDF/Markdown
- [ ] AC-7: Published pack remains read-only; future-quarter regeneration creates a new pack id

## Constitutional invariants honored

- **AI-assist boundary:** templated only in v1; user explicitly approves before publish
- **Anti-pattern rejected (AI-generated audit responses):** consistent with §4.6.5 questionnaire boundary

## Canvas references

- `Plans/mockups/board-pack.html`
- `Plans/canvas/07-metrics.md` §7.5

## Dependencies

- #005, #032

## Anti-criteria (P0)

- Does NOT show LLM-drafted content in v1
- Does NOT permit publish without all sections approved
- Does NOT mutate a published pack

## Skill mix (3–5)

- Next.js + shadcn/ui
- Section-by-section approval state
- PDF/Markdown export embed
- Role-aware approval gates
- Visual design (port from mockup)
