# Slice 440 — Board-narrative AI v0 (decisions log)

JUDGMENT slice. This is the platform's **highest-risk AI-assist surface** — an
AI-drafted board-report section consumed by non-technical board members who take
the output at face value, so the hallucination cost is asymmetric. Claude made
the subjective build-time calls below using best-reasoned, pattern-matched
judgment, recorded them here, and shipped. The runtime **AI-assist boundary is
constitutional and untouched** — this log is a development-process artifact, not
a relaxation of that boundary. The product still never publishes a board-binding
artifact without one-click human approval, never fabricates coverage or numbers,
and never seeds Tenant B with Tenant A data.

- detection_tier_actual: integration
- detection_tier_target: integration

> Two fixture-vs-schema mismatches surfaced during the slice and were caught at
> the right tier (`target=integration, actual=integration`): the first control
> seed omitted the slice-009 NOT-NULL `bundle_id`, and the first evidence seed
> omitted the slice-004 nonempty-`control_ref` CHECK column. Both rejected with
> SQLSTATE 23502 / 23514 against real Postgres — exactly where a fixture-vs-
> schema mismatch should surface, not in production. Fixed in the seed helper;
> no production-code change. No guardrail-bypass bug surfaced.

## The seven guardrails (where each lives)

This slice implements ONE numbered section (`control_coverage_summary`) end to
end with all seven guardrails wired. The guardrails are the constitutional
boundary, not options — the follow-on slices add **more sections**, not **fewer
guardrails**.

| #   | Guardrail                  | Where enforced (file:function)                                                     | Test                                                                                                                   |
| --- | -------------------------- | ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| 1   | Hybrid input               | `prompt.go:buildPrompt` (rollup + bounded cited excerpts)                          | `boardnarrative_test.go:TestSystemPromptWiresBanList` + the integration valid path                                     |
| 2   | Per-section approval       | `service.go:Approve` + `store.go:Approve` (one section flips)                      | `integration_test.go:TestIntegration_ApprovalRequiresApprover`                                                         |
| 3   | Full prompt+response audit | `service.go:Generate` → `llm.AuditWriter.Write` (ai_generations row)               | integration valid path (audit row written)                                                                             |
| 4   | Mandatory citations        | `citations.go:validateCitations` (grounding + RLS tenant-ownership gate)           | `TestValidateCitations`, `TestIntegration_UnresolvableCitationRejected`, `TestIntegration_CrossTenantCitationRejected` |
| 5   | Numeric-claim verification | `numeric.go:verifyNumbers` (every number ∈ rollup ground truth)                    | `TestVerifyNumbers` (exhaustive), `TestIntegration_FabricatedNumberRejected`                                           |
| 6   | Section-shape enforcement  | `shape.go:enforceShape` (exact heading + 4 numbered items in order)                | `TestEnforceShape`, `TestIntegration_BadShapeRejected`                                                                 |
| 7   | Editor-mode UX / tone      | FE `web/lib/board/narrative-ai.ts:approveEnabled` + `tone.go:containsBannedPhrase` | `web/lib/board/narrative-ai.test.ts`, `TestContainsBannedPhrase`                                                       |

## How slice 498's shared guard was adopted on `board_narrative_sections`

CLAUDE.md's AI-assist boundary names board-narrative v0 as a surface that gains
the boundary columns + the shared CHECK. This slice's migration
(`20260612050000_board_narrative_ai.sql`) creates the per-section table with the
approval triple (`ai_assisted` / `human_approved` / `human_approver`) + the four
provenance columns (`prompt_version` / `model_name` / `model_version` /
`model_provider`) and adopts the slice-498 function **line-for-line in structure
with slice 441**:

```sql
ALTER TABLE board_narrative_sections
    ADD CONSTRAINT board_narrative_sections_ai_assist_invariant
        CHECK (ai_assist_human_approver_guard(
            ai_assisted, human_approved, human_approver));
```

The predicate is NOT re-authored (P0-498-4) — the shared IMMUTABLE function is
invoked. The Go mirror (`llm.EnforceApproval`) rejects a blank approver early;
the DB CHECK is authoritative. `TestIntegration_ApprovalRequiresApprover` proves
both: the service rejects a blank approver, and a raw `UPDATE … human_approved =
TRUE, human_approver = NULL` is rejected by the DB CHECK directly.

## Decisions made

### D1 — Chosen section: `control_coverage_summary`

The spec proposed the most rollup-grounded section. The coverage-summary section
is ideal for v0 because **every claim is a number drawn from the deterministic
`board.Brief`** (coverage %, freshness %, 30-day drift delta, controls-drifted-
out count, framework count) and **every supporting reference is a citable
tenant-owned control/evidence id**. That maximizes the numeric-verification and
citation guardrails' coverage — the section is almost entirely the kind of claim
the guardrails are designed to police. A narrative-heavy section (e.g. "asks of
the board") would exercise the guardrails less and is a better second slice once
the machinery is proven here.

### D2 — Rollup reuses `board.Generator.Assemble` (a new read-only method)

The deterministic ground truth REUSES the slice-031 brief data path rather than
re-deriving the numbers. `board.Generator` already assembles the `Brief` from the
freshness + drift read models, but `Generate` PERSISTS a `board_briefs` row. I
added a thin exported `Assemble(ctx, periodEnd) (Brief, error)` that runs the
same `assemble` read WITHOUT the append — the narrative surface computes a
rollup, not a frozen brief. One small, well-documented addition to `board` keeps
the two surfaces sharing the exact same numbers (so a coverage number the board
narrative states is byte-identical to the templated brief's number), which is
the whole point of grounding on the pre-computation.

### D3 — Numeric strictness: every number ∈ ground truth, magnitude-matched, decimals rejected

`verifyNumbers` parses EVERY integer token from the draft and rejects the whole
draft if any is not a rollup value. Three judgment calls inside it:

- **Strip the three non-statistic token classes before extraction:** the
  numbered-list markers (`1.`–`4.`, structure — guardrail 6's job), cited UUIDs
  (citation tokens — guardrail 4's job), and the exact period-end label. What
  remains is the section's actual numeric claims. Without this the list indices
  and the date digits would false-positive as fabricated statistics.
- **Magnitude-matched signed deltas:** a drift delta of `-3` may be written
  "down 3", "3 controls drifted out", or "-3"; all three are honest renderings
  of the same ground-truth magnitude. `AllowedNumbers` admits both the signed
  value AND its absolute value, letting the model phrase the direction in prose
  (governed by shape/tone) while pinning the magnitude to ground truth.
- **Decimals are a fabrication signal:** the rollup is all integers (rounded
  percentages + counts), so "84.5" extracts as `84` then `5`; `5` is not a
  ground-truth value, so the decimal draft rejects. The model inventing
  precision the pre-computation does not have is itself a hallucination.

The contract is all-or-nothing: ONE bad number rejects the WHOLE draft. A board
narrative with even one fabricated statistic is unacceptable because the board
cannot tell the fabricated number from the real ones.

### D4 — Citation strictness: a SINGLE unresolvable citation suppresses the whole draft

Mirrors slice 441 D2. Every cited id must be (a) in the grounding set the prompt
showed the model AND (b) resolve to a real tenant-owned control/evidence row
under RLS. A single failure suppresses the whole draft and persists nothing
(P0-440-4). A no-fabricated-coverage invariant cannot be "mostly" honored, and a
board narrative is the highest-stakes place to half-honor it.

### D5 — Tone enforcement: exact-match the unambiguous phrases; instruct + review the context-sensitive words

The slice-182 governance reference (Section 1) lists exact-match banned phrases;
Section 3 carves out words with legitimate uses ("robust", "leverage", "strong",
"improve" without a number, …). The post-generation `containsBannedPhrase` gate
exact-matches the **unambiguous** phrases (those with no Section 3 carve-out — no
false-positive risk). The context-sensitive words are enforced by the
system-prompt instruction + human review rather than a regex that would over-fire
on their legitimate forms (the governance doc itself says the regex check
excludes legitimate forms via context inspection, which is more than v0 needs).
The full ban list IS wired into the system prompt (the model is instructed to
avoid every entry); the post-generation gate is the deterministic safety net for
the phrases that can never be right.

### D6 — Editable current-state table; immutable forensics in the ai_generations ledger

"Old reports stay immutable" (CLAUDE.md) is satisfied by the append-only
slice-498 `ai_generations` ledger, which snapshots the system prompt + context +
raw draft at generation time and is never rewritten. The
`board_narrative_sections` table is the EDITABLE current-state record (the
operator edits inline and approves it — guardrail 7) with a UNIQUE
`(tenant, period_end, section)` so regeneration UPSERTs over the prior unapproved
draft. Separating editable current-state from the immutable ledger mirrors the
slice-441 `questionnaire_answers` split. The board pack that consumes the
APPROVED `final_text` is itself frozen by the slice-032 publish lifecycle.

### D7 — Cloud-routing banner: structurally false in v0, plumbed for the follow-on

`isCloudProvider` is `false` for every v0 provider (local Ollama / stub). The
`cloud_routed` field is on the wire + the FE view-model so the cloud opt-in
follow-on surfaces the visible banner without a wire-shape change. v0 routes to
local Ollama only (P0-440-5).

## Revisit once in use

- **Section coverage:** the remaining board-narrative sections (top risks, open
  findings, operational metrics, asks of the board) are explicit follow-ons —
  each its own `section_key`, reusing this guardrail machinery.
- **Cloud-LLM opt-in + visible banner** — follow-on; the field is plumbed.
- **Regenerate-with-instruction / write-from-scratch affordances** — v0 ships
  inline-edit-default + regenerate (re-POST generate); the instruction-guided
  regenerate is a follow-on (per the spec's 6A scope note).
- **Tone Section-3 carve-out regex** — if/when an operator hits a false-positive
  or a banned context-sensitive word slips through review, graduate the
  context-window regex the governance doc describes.
- **Decimal-precision rollup fields** — if a future section's ground truth is
  genuinely non-integer, `extractNumbers` + `AllowedNumbers` will need a decimal
  representation; today's all-integer assumption is correct for coverage.

## Detection-tier classification

- detection_tier_actual: integration
- detection_tier_target: integration
