# Slice 513 — decisions log

**Slice:** 513 — Correct the AI-assist-boundary canonical-adopter reference
(`QuestionnaireAnswer` → `mcp_write_proposals`)
**Type:** AFK (documentation accuracy correction; no runtime behavior change)
**Branch:** `docs/513-ai-adopter-correction`

## Summary

Docs-only factual correction. Two constitutional/design references asserted the
schema-level `ai_assisted=true ⇒ human_approver` enforcement using
`QuestionnaireAnswer` as the canonical adopter that already carries the
`ai_assisted` / `human_approved` / `human_approver` columns. On `main` those
columns do **not** exist on `questionnaire_answers`; the real shipped adopter is
`mcp_write_proposals` (slice 173), and slice 498 extracted the predicate into the
shared reusable `ai_assist_human_approver_guard` IMMUTABLE function + CHECK
template. This slice aligns the docs with the shipped reality.

## Finding re-verified on `main` before editing (grill-with-docs)

- `grep -rn "human_approver" migrations/sql/` → columns + CHECK live on
  `mcp_write_proposals` (`migrations/sql/20260520030000_mcp_write_proposals.sql`,
  inline `mcp_wp_ai_assist_invariant` CHECK) and the reusable guard lives in
  `migrations/sql/20260607000000_ai_generations.sql` (slice 498's
  `ai_assist_human_approver_guard` function). **No** `human_approver` /
  `ai_assisted` columns on `questionnaire_answers`.
- Guard function `ai_assist_human_approver_guard` also present in
  `internal/llm/enforce.go` + `internal/llm/client.go` (slice 498).
- Origin of the finding: `docs/audit-log/498-llm-foundation-decisions.md` D5.

## What changed

| AC   | File                                 | Change                                                                                                                                                                                                                                              |
| ---- | ------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| AC-1 | `CLAUDE.md`                          | "AI-assist boundary (hard)" — extended the "Schema-level enforcement" sentence to name `mcp_write_proposals` (slice 173) as the shipped adopter, slice 498's reusable `ai_assist_human_approver_guard`, and the slice-440 future retrofit.          |
| AC-2 | `Plans/canvas/04-evidence-engine.md` | §4.6.5 — corrected the present-tense "enforced at the schema level: `QuestionnaireAnswer.ai_assisted=true`" assertion to the same accurate adopter set; clarified the §4.6 entity table's `QuestionnaireAnswer` columns are the **designed** shape. |
| AC-3 | both                                 | Both now reference slice 498's decisions-log D5 + the reusable guard, and note `questionnaire_answers` gains the columns at slice 440.                                                                                                              |
| AC-4 | —                                    | No code / schema / migration change. `git diff --stat` shows only `.md` files.                                                                                                                                                                      |

## Decisions

- **D1 — left the §4.6 `QuestionnaireAnswer` entity table (line 132) intact.**
  That table is the _design model_ (the entity's designed column shape, which
  slice 440 will implement), not a present-tense claim about shipped schema. The
  inaccuracy was the §4.6.5 "_is enforced at the schema level_" present-tense
  assertion, which I corrected and annotated so the table reads as design intent.
  Editing the entity table would have over-reached the surgical scope.
- **D2 — kept the constitutional boundary text otherwise untouched** (P0-513-2).
  Only the illustrative adopter example was corrected; the allowed/not-allowed
  lists, the boundary substance, and every other invariant are byte-unchanged.

## Anti-criteria honored

- **P0-513-1** — did NOT add columns to `questionnaire_answers` (slice 440 scope).
- **P0-513-2** — did NOT weaken or change the constitutional AI-assist boundary;
  only the example is corrected.
- **P0-513-3** — did NOT touch `internal/llm`, `mcp_write_proposals`, or any
  migration. Docs-only.

## Detection-tier classification

- `detection_tier_actual`: `manual_review` (slice 498 db-designer pass surfaced
  the spec-vs-schema drift while reading the migrations).
- `detection_tier_target`: `manual_review` (a doc-accuracy drift between a design
  example and the shipped schema has no automated tier; review is the right
  surface).
