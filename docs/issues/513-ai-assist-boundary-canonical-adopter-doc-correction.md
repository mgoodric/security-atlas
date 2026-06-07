# 513 — Correct the AI-assist-boundary canonical-adopter reference (QuestionnaireAnswer → mcp_write_proposals)

**Cluster:** Docs
**Estimate:** S (0.5d)
**Type:** AFK (documentation accuracy correction; no runtime behavior change)
**Status:** `ready`

> Filed 2026-06-07 from a spec-vs-reality finding surfaced while building slice
> 498 (the shared `internal/llm` foundation). Recorded in
> `docs/audit-log/498-llm-foundation-decisions.md` D5; the orchestrator files it
> here per the parallel-batch reconcile rule (engineer-surfaced finding it
> declined to file inline).

## Narrative

**Why (the inaccuracy).** `CLAUDE.md` ("AI-assist boundary (hard)") and
`Plans/canvas/04-evidence-engine.md` §4.6.5 both state the schema-level
`ai_assisted=true ⇒ human_approver` enforcement using **`QuestionnaireAnswer`**
as the canonical example that already carries the `ai_assisted` / `ai_model` /
`human_approved` / `human_approver` columns. Slice 498's `database-designer`
pass established that **those columns do not exist on `questionnaire_answers` on
`main`** — the real, shipped adopter of that column set + the approval-guard
pattern is **`mcp_write_proposals`** (slice 173). Slice 498 therefore templated
its reusable `ai_assist_human_approver_guard` IMMUTABLE-function + CHECK from the
**real** adopter (`mcp_write_proposals`), left it unchanged, and deliberately did
**not** retrofit `questionnaire_answers` (that retrofit belongs to slice 440, the
questionnaire AI-assist v0 surface — front-running it here would have been
out-of-scope scope-creep).

The code is correct; the **documentation** names an adopter that does not match
the schema. A future contributor (especially the slice-440 implementer) reading
the constitution would look for columns on `questionnaire_answers` that aren't
there.

**What (the deliverable).** A documentation correction, no code:

1. Update `CLAUDE.md` "AI-assist boundary (hard)" — replace the
   `QuestionnaireAnswer`-already-has-the-columns assertion with the accurate
   statement: the canonical shipped adopter is `mcp_write_proposals` (slice 173);
   the shared reusable guard lives in slice 498's `internal/llm` +
   `migrations/sql/20260607000000_ai_generations.sql`
   (`ai_assist_human_approver_guard`); `questionnaire_answers` gains the columns
   when slice 440 ships.
2. Update `Plans/canvas/04-evidence-engine.md` §4.6.5 to match.
3. Cross-reference slice 498's decisions-log D5 + the reusable-guard template so
   slices 440 / 441 / 471 adopt it identically.

**Scope discipline.** Docs only. Does NOT add the columns to
`questionnaire_answers` (that is slice 440's job — the questionnaire AI-assist
surface). Does NOT change the `internal/llm` guard or the `mcp_write_proposals`
schema. Does NOT alter the constitutional boundary itself (the boundary is
unchanged and correct — only the illustrative example is being corrected).

## Threat model

STRIDE — verdict **N/A (documentation-only)**. This slice changes prose in
`CLAUDE.md` + a canvas section; it ships no code, no migration, no endpoint, no
schema change, and touches no tenant data, auth boundary, or input path. The one
adjacent consideration: the edit must not _weaken_ the documented AI-assist
boundary — it strengthens accuracy by pointing at the real enforcement surface.

- **Tampering/Elevation (the only relevant axis):** an inaccurate constitution
  could lead a future implementer to mis-wire the `ai_assisted ↔ human_approver`
  guard (looking for it on the wrong table). _Mitigation:_ this correction points
  every adopter at the real, shipped guard (`ai_assist_human_approver_guard`,
  slice 498) so the boundary is wired identically and correctly.
- **S / R / I / D:** not applicable — no runtime surface.

## Acceptance criteria

- [ ] **AC-1.** `CLAUDE.md` "AI-assist boundary (hard)" no longer asserts
      `QuestionnaireAnswer` already carries the `ai_assisted`/`human_approver`
      columns; it names `mcp_write_proposals` (slice 173) as the shipped adopter
      and slice 498's reusable guard as the shared mechanism.
- [ ] **AC-2.** `Plans/canvas/04-evidence-engine.md` §4.6.5 matches AC-1.
- [ ] **AC-3.** Both reference slice 498's decisions-log D5 + the
      `ai_assist_human_approver_guard` template, and note `questionnaire_answers`
      gains the columns at slice 440.
- [ ] **AC-4.** No code / schema / migration change; the diff is docs-only.

## Constitutional invariants honored

- **AI-assist boundary (hard).** Unchanged in substance — this corrects the
  illustrative adopter so the documented boundary matches the shipped
  enforcement surface (slice 498's DB-layer guard).

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6.5 — the section being corrected.
- `CLAUDE.md` "AI-assist boundary (hard)" — the section being corrected.

## Dependencies

- **#498 (merged)** — established the real adopter + the reusable guard; this
  slice aligns the docs with it.
- **Relates to #440** (questionnaire AI-assist v0) — the slice that will actually
  add the `ai_assisted`/`human_approver` columns to `questionnaire_answers`; this
  doc correction notes that future event rather than performing it.
- No unmerged technical dependency → `ready`.

## Anti-criteria (P0 — block merge)

- **P0-513-1.** Does NOT add columns to `questionnaire_answers` (slice 440 scope).
- **P0-513-2.** Does NOT weaken or change the constitutional AI-assist boundary —
  only corrects the example.
- **P0-513-3.** Does NOT touch `internal/llm`, `mcp_write_proposals`, or any
  migration — docs-only.

## Skill mix (3-5)

- `grill-with-docs` — confirm the real adopter set (grep `human_approver` across
  migrations + `internal/`) before editing the constitution.
- `simplify` — keep the correction tight; do not rewrite the whole boundary
  section.

## Notes for the implementing agent

- The finding is from slice 498 decisions-log D5. Before editing, re-verify on
  `main`: `grep -rn "human_approver" migrations/sql/` (expect `mcp_write_proposals`
  - slice 498's `ai_generations` guard function, NOT `questionnaire_answers`).
- This edits `CLAUDE.md`, a meta/constitutional file — keep the change surgical
  and factual; do not restructure the boundary section.
- **Registration note (slice-382):** `_STATUS.md` row registered by the
  orchestrator on a `chore/status` branch, not this branch.
