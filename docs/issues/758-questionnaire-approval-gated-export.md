# 758 — Approval-gated questionnaire export + export audit trail

**Cluster:** Questionnaires / Audit workflow
**Estimate:** S-M (1d)
**Type:** JUDGMENT (exclude-vs-block export semantics)
**Status:** `ready`

## Narrative

Filed from the OE-595 end-to-end questionnaire-answering design (2026-07-28
repo review). Slice 4 of 4; see #755 (mapping suggest-approve), #756 (batch
run), #757 (review queue UI). Independently shippable — the approval columns
it gates on landed with slice 441's migration
(`20260612000000_questionnaire_answer_ai_columns.sql`).

The slice-155 PDF export renders every answer row it finds. That predates AI
drafts: since slice 441, `questionnaire_answers` can hold rows with
`ai_assisted=true, human_approved=false` — drafts the operator has not signed
off — and the exporter would happily send them to a customer. Today the only
thing preventing a constitutional breach ("no publish without one-click human
approval") is that drafts are created one row at a time; once #756 fills a
whole questionnaire with drafts, export becomes the breach waiting to happen.
The export IS the "publish/return" moment for a questionnaire, so the gate
belongs here regardless of how the drafts got created — this slice closes the
gap for the single-row present and the batch future at once.

Design: the export hydration path classifies each answer. Manual answers
(`ai_assisted=false`) and approved AI answers export unchanged. **Unapproved
AI drafts never appear in the export output** — the row renders as unanswered,
and the export response/UI carries an explicit summary ("N drafted answers
pending approval were excluded") so the exclusion is visible, not silent. A
strict variant (block the export entirely while unapproved drafts exist) is
the JUDGMENT call to settle in the grill; the default position is
exclude-with-visible-summary, because blocking punishes the operator who
legitimately wants to send the manually-answered subset now. Every export is
audit-logged: who, when, which questionnaire, counts (approved AI / manual /
excluded drafts), so "what did we actually send" is answerable forensically.

## Threat model — LIGHT (read-side gate)

The risk this slice EXISTS to close is Elevation-of-privilege shaped: an
unapproved AI draft crossing the publish boundary inside an export nobody
gated. Secondary: Repudiation — a customer received a document and no record
says which answers were in it (closed by the export audit row + counts);
Information-disclosure is unchanged (export path already runs under the
tenant's RLS context; this slice narrows output, never widens it). The gate
must live in the shared hydration/classification step, not in one renderer,
so a future export format (Excel round-trip, portal) inherits it by
construction rather than by remembering.

## Acceptance criteria

- [ ] **AC-1.** The export hydration path classifies every answer
      (manual / approved-AI / unapproved-AI-draft) from the slice-441 columns;
      classification lives in ONE shared spot the PDF renderer consumes (and
      any future exporter must consume).
- [ ] **AC-2.** An unapproved AI draft's text appears NOWHERE in the exported
      bytes; its question renders as unanswered.
- [ ] **AC-3.** Manual answers and approved AI answers export unchanged
      (manual-first-class: both render identically).
- [ ] **AC-4.** The export response surfaces the exclusion summary (count of
      drafts pending approval); the web export affordance shows it before and
      after export.
- [ ] **AC-5.** Every export writes an audit event: actor, questionnaire,
      timestamp, counts (manual / approved-AI / excluded-draft).
- [ ] **AC-6.** Integration: a questionnaire with a manual answer, an approved
      AI answer, and an unapproved draft exports exactly the first two; the
      draft narrative is absent from the PDF bytes; the audit row records
      1/1/1.
- [ ] **AC-7.** Integration: approving the draft (slice-441 path) then
      re-exporting includes it — proving the gate keys on the approval
      columns, not on authorship.
- [ ] **AC-8.** Integration: Tenant B cannot export Tenant A's questionnaire
      (existing RLS posture re-asserted on this path).
- [ ] **AC-9.** Decisions log at
      `docs/audit-log/758-questionnaire-export-gate-decisions.md`
      (exclude-vs-block call + revisit list, detection-tier fields) +
      changelog entry.

## Constitutional invariants honored

- **AI-assist boundary (hard).** "The platform does NOT publish any
  audit-binding artifact without one-click human approval" — this slice makes
  the questionnaire export structurally incapable of it.
- **#9 — Manual evidence is first-class.** Manual and approved-AI answers are
  indistinguishable in the output.
- **#6 — Tenant isolation via RLS.** Export path re-asserted tenant-scoped.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6.4 ("approved responses compiled
  into the requested format"), §4.6.5 (boundary), §4.6.3
  (`QuestionnaireResponse.status: draft / under_review / approved / sent`).
- `CLAUDE.md` "AI-assist boundary (hard)".

## Dependencies

- **#441** (approval columns + guard on `questionnaire_answers`) — `merged`.
- **#155** (PDF export) — `merged`.
- NOT dependent on #755/#756/#757 — the gate is correct with or without batch.

## Anti-criteria (P0 — block merge)

- **P0-758-1.** Does NOT let unapproved AI-draft text reach exported bytes
  under any flag, format, or error path.
- **P0-758-2.** Does NOT silently drop drafts — the exclusion is always
  surfaced (AC-4).
- **P0-758-3.** Does NOT approve anything as a side effect of exporting.
- **P0-758-4.** Does NOT fork the classification logic per renderer (one
  shared gate — AC-1).

## Skill mix (3-5)

`grill-with-docs` · `tdd` (byte-level absence test AC-6 is load-bearing) ·
`security-review` (publish boundary) · `simplify`.

## Notes for the implementing agent

- The byte-level assertion in AC-6 matters: assert the draft narrative string
  is absent from the PDF output, not merely that the item list omitted it —
  the renderer, not the list, is what the customer receives. (PDF text
  extraction from the chromedp output is acceptable; asserting on the
  pre-render `PDFInput` AND that the renderer received the gated input is the
  fallback if extraction is brittle — record the choice.)
- Settle exclude-vs-block in the grill against canvas §4.6.3's
  response-status model; record the call and the revisit trigger (operator
  complaints either way).
- Audit event goes through `internal/audit` like the platform's other
  operator-action events.
